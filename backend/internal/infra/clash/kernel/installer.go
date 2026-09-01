package kernel

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultMihomoRepo = "MetaCubeX/mihomo"
	GitHubReleasesAPI = "https://api.github.com/repos"
)

var installerHTTPClient = &http.Client{
	Timeout: 3 * time.Minute,
}

type InstalledKernelBinary struct {
	KernelType      string    `json:"kernel_type"`
	InstallPath     string    `json:"install_path"`
	BinarySource    string    `json:"binary_source"`
	DetectedVersion string    `json:"detected_version"`
	FileName        string    `json:"file_name"`
	ReleaseTag      string    `json:"release_tag,omitempty"`
	InstalledAt     time.Time `json:"installed_at"`
}

type githubReleaseResponse struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func InspectMihomoBinary(ctx context.Context, installPath string) (*InstalledKernelBinary, error) {
	resolvedPath, err := LocateBinary(installPath)
	if err != nil {
		return nil, err
	}

	detectedVersion, err := detectMihomoVersion(ctx, resolvedPath)
	if err != nil {
		return nil, err
	}

	return &InstalledKernelBinary{
		KernelType:      "mihomo",
		InstallPath:     resolvedPath,
		BinarySource:    "existing",
		DetectedVersion: detectedVersion,
		FileName:        filepath.Base(resolvedPath),
		InstalledAt:     time.Now(),
	}, nil
}

func InstallUploadedMihomoBinary(ctx context.Context, fileName string, installPath string, reader io.Reader) (*InstalledKernelBinary, error) {
	resolvedPath := ResolveBinaryPath(installPath)
	tempPath, err := writeTempFile(filepath.Dir(resolvedPath), "mihomo-upload-", fileName, reader)
	if err != nil {
		return nil, err
	}
	return installPreparedBinary(ctx, tempPath, resolvedPath, strings.TrimSpace(fileName), "upload", "")
}

func DownloadAndInstallMihomoBinary(ctx context.Context, installPath string) (*InstalledKernelBinary, error) {
	resolvedPath := ResolveBinaryPath(installPath)
	release, err := fetchLatestRelease(ctx)
	if err != nil {
		return nil, err
	}
	asset, err := selectAsset(release.Assets)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 Mihomo 下载请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "Foam-Clash-Control")

	resp, err := installerHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载 Mihomo 二进制失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 Mihomo 二进制失败: %s", resp.Status)
	}

	var reader io.Reader = resp.Body
	if strings.HasSuffix(strings.ToLower(asset.Name), ".gz") {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("解压 Mihomo 发行包失败: %w", err)
		}
		defer func() { _ = gzipReader.Close() }()
		reader = gzipReader
	}

	tempPath, err := writeTempFile(filepath.Dir(resolvedPath), "mihomo-download-", asset.Name, reader)
	if err != nil {
		return nil, err
	}
	return installPreparedBinary(ctx, tempPath, resolvedPath, asset.Name, "download", release.TagName)
}

func fetchLatestRelease(ctx context.Context) (*githubReleaseResponse, error) {
	url := fmt.Sprintf("%s/%s/releases/latest", GitHubReleasesAPI, DefaultMihomoRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 GitHub Release 查询请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Foam-Clash-Control")

	resp, err := installerHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("查询 Mihomo 官方版本失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("查询 Mihomo 官方版本失败: %s", resp.Status)
	}

	var release githubReleaseResponse
	if err = json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析 Mihomo 版本响应失败: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return nil, fmt.Errorf("未获取到有效的 Mihomo 版本 Tag")
	}
	return &release, nil
}

func selectAsset(assets []githubAsset) (*githubAsset, error) {
	if len(assets) == 0 {
		return nil, fmt.Errorf("mihomo 官方 release 中无资源列表")
	}

	goos := strings.ToLower(runtime.GOOS)
	var preferredArchs []string
	switch runtime.GOARCH {
	case "amd64":
		preferredArchs = []string{"amd64-v3", "amd64-v2", "amd64-v1", "amd64-compatible", "amd64"}
	case "arm64":
		preferredArchs = []string{"arm64"}
	case "386":
		preferredArchs = []string{"386"}
	default:
		preferredArchs = []string{runtime.GOARCH}
	}

	for _, arch := range preferredArchs {
		for _, asset := range assets {
			name := strings.ToLower(strings.TrimSpace(asset.Name))
			if !strings.HasPrefix(name, "mihomo-") {
				continue
			}
			if strings.Contains(name, "sha256") || strings.HasSuffix(name, ".txt") {
				continue
			}
			if strings.Contains(name, goos) && strings.Contains(name, arch) && strings.HasSuffix(name, ".gz") {
				res := asset
				return &res, nil
			}
		}
	}

	return nil, fmt.Errorf("未找到适合当前架构 %s/%s 的 Mihomo .gz 资源包", runtime.GOOS, runtime.GOARCH)
}

func installPreparedBinary(ctx context.Context, tempPath string, installPath string, fileName string, source string, releaseTag string) (*InstalledKernelBinary, error) {
	detectedVersion, err := detectMihomoVersion(ctx, tempPath)
	if err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("检测 Mihomo 版本失败: %w", err)
	}

	if err = os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil { //nolint:gosec,mnd
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("创建目标安装目录失败: %w", err)
	}

	_ = os.Remove(installPath)
	if err = os.Rename(tempPath, installPath); err != nil {
		// Fallback for cross-device link rename
		if errCopy := copyFile(tempPath, installPath); errCopy != nil {
			_ = os.Remove(tempPath)
			return nil, fmt.Errorf("覆盖 Mihomo 二进制失败: %w", err)
		}
		_ = os.Remove(tempPath)
	}
	_ = os.Chmod(installPath, 0o755) //nolint:gosec // executable permissions required

	return &InstalledKernelBinary{
		KernelType:      "mihomo",
		InstallPath:     installPath,
		BinarySource:    source,
		DetectedVersion: detectedVersion,
		FileName:        strings.TrimSpace(fileName),
		ReleaseTag:      strings.TrimSpace(releaseTag),
		InstalledAt:     time.Now(),
	}, nil
}

func writeTempFile(tempDir string, patternPrefix string, _ string, reader io.Reader) (string, error) {
	if strings.TrimSpace(tempDir) == "" {
		tempDir = os.TempDir()
	}
	_ = os.MkdirAll(tempDir, 0o755) //nolint:gosec,mnd
	tempFile, err := os.CreateTemp(tempDir, patternPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	tempPath := tempFile.Name()
	if _, err = io.Copy(tempFile, reader); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("保存二进制流失败: %w", err)
	}
	_ = tempFile.Close()
	_ = os.Chmod(tempPath, 0o755) //nolint:gosec // executable permissions required
	return tempPath, nil
}

func detectMihomoVersion(ctx context.Context, binaryPath string) (string, error) {
	cmd := exec.CommandContext(ctx, binaryPath, "-v")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("执行 %s -v 失败: %w (output: %s)", binaryPath, err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 && lines[0] != "" {
		return lines[0], nil
	}
	return "", fmt.Errorf("未获得有效的版本输出")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}
