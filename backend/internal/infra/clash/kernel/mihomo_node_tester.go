package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var execCommandContext = exec.CommandContext

type MihomoNodeTestInput struct {
	BinaryPath   string
	ProxyName    string
	MetadataJSON string
	TestURL      string
	Timeout      time.Duration
}

type MihomoNodeTestResult struct {
	LatencyMS int
}

//nolint:contextcheck
func TestNodeWithMihomo(ctx context.Context, input MihomoNodeTestInput) (*MihomoNodeTestResult, error) {
	binaryPath, err := LocateBinary(input.BinaryPath)
	if err != nil {
		return nil, err
	}

	testURL := strings.TrimSpace(input.TestURL)
	if testURL == "" {
		return nil, fmt.Errorf("测试 URL 不能为空")
	}
	if _, err := url.ParseRequestURI(testURL); err != nil {
		return nil, fmt.Errorf("测试 URL 无效: %v", err)
	}

	timeout := input.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	proxyMap, err := decodeProxyMetadata(input.MetadataJSON)
	if err != nil {
		return nil, err
	}
	proxyName := strings.TrimSpace(input.ProxyName)
	if proxyName == "" {
		proxyName = strings.TrimSpace(stringValue(proxyMap["name"]))
	}
	if proxyName == "" {
		return nil, fmt.Errorf("节点名称为空")
	}
	proxyMap["name"] = proxyName

	port, err := allocateLocalPort(ctx)
	if err != nil {
		return nil, err
	}
	configBytes, err := buildSingleProxyConfig(proxyMap, proxyName, port)
	if err != nil {
		return nil, err
	}

	tempDir, err := os.MkdirTemp("", "foam-mihomo-node-test-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时测试目录失败: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	configPath := filepath.Join(tempDir, "config.yaml")
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		return nil, fmt.Errorf("写入临时测试配置失败: %v", err)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout+8*time.Second)
	defer cancel()

	var output bytes.Buffer
	cmd := execCommandContext(commandCtx, binaryPath, "-d", tempDir, "-f", configPath)
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 Mihomo 节点测试失败: %v", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	if err := waitForProxyPort(commandCtx, waitCh, &output, port); err != nil {
		terminateProcess(cmd, waitCh)
		return nil, err
	}

	testResult, err := executeHTTPRequestThroughProxy(commandCtx, port, testURL, timeout)
	terminateProcess(cmd, waitCh)
	if err != nil {
		return nil, err
	}
	return testResult, nil
}

func decodeProxyMetadata(raw string) (map[string]any, error) {
	var proxyMap map[string]any
	if err := json.Unmarshal([]byte(raw), &proxyMap); err != nil {
		return nil, fmt.Errorf("解析节点元数据失败: %v", err)
	}
	if len(proxyMap) == 0 {
		return nil, fmt.Errorf("节点元数据为空")
	}
	return proxyMap, nil
}

func buildSingleProxyConfig(proxyMap map[string]any, proxyName string, mixedPort int) ([]byte, error) {
	config := map[string]any{
		"mixed-port": mixedPort,
		"allow-lan":  false,
		"mode":       "rule",
		"log-level":  "silent",
		"ipv6":       true,
		"proxies":    []any{proxyMap},
		"proxy-groups": []map[string]any{
			{
				"name":    "FOAM-NODE-TEST",
				"type":    "select",
				"proxies": []string{proxyName},
			},
		},
		"rules": []string{
			"MATCH,FOAM-NODE-TEST",
		},
	}
	return yaml.Marshal(config)
}

func allocateLocalPort(ctx context.Context) (int, error) {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("分配本地测试端口失败: %v", err)
	}
	defer func() {
		_ = listener.Close()
	}()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || addr.Port <= 0 {
		return 0, fmt.Errorf("获取本地测试端口失败")
	}
	return addr.Port, nil
}

func waitForProxyPort(ctx context.Context, waitCh <-chan error, output *bytes.Buffer, port int) error {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	dialer := &net.Dialer{Timeout: 200 * time.Millisecond}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 mihomo 测试代理启动超时: %v | logs=%s", ctx.Err(), strings.TrimSpace(output.String()))
		case err := <-waitCh:
			if err == nil {
				return fmt.Errorf("mihomo 测试进程提前退出 | logs=%s", strings.TrimSpace(output.String()))
			}
			return fmt.Errorf("mihomo 测试进程启动失败: %v | logs=%s", err, strings.TrimSpace(output.String()))
		case <-ticker.C:
			conn, err := dialer.DialContext(ctx, "tcp", address)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}

func executeHTTPRequestThroughProxy(ctx context.Context, port int, targetURL string, timeout time.Duration) (*MihomoNodeTestResult, error) {
	proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("构造本地测试代理地址失败: %v", err)
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyURL(proxyURL),
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: timeout,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	defer transport.CloseIdleConnections()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建测试请求失败: %v", err)
	}

	startedAt := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("通过内核发起测试请求失败: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)

	if response.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("测试请求返回异常状态码: %d", response.StatusCode)
	}

	return &MihomoNodeTestResult{
		LatencyMS: int(time.Since(startedAt).Milliseconds()),
	}, nil
}

func terminateProcess(cmd *exec.Cmd, waitCh <-chan error) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", value)
	}
}
