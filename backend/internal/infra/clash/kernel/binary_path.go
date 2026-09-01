package kernel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// EnvClashMihomoBinaryPath matches config.EnvClashMihomoBinaryPath.
	// Duplicated here to avoid infra/clash/kernel → infra/config dependency.
	envClashMihomoBinaryPath = "FOAM_CLASH_MIHOMO_BINARY_PATH"
	fallbackMihomoBinaryPath = "./data/core/mihomo"
)

// ErrBinaryNotFound is returned when no mihomo executable exists at the
// resolved path (or its Windows .exe variant).
var ErrBinaryNotFound = errors.New("未找到 Mihomo 二进制文件")

// DefaultMihomoBinaryPath returns FOAM_CLASH_MIHOMO_BINARY_PATH when set,
// otherwise ./data/core/mihomo. The result is unresolved (may be relative).
func DefaultMihomoBinaryPath() string {
	if v := strings.TrimSpace(os.Getenv(envClashMihomoBinaryPath)); v != "" {
		return v
	}
	return fallbackMihomoBinaryPath
}

// ResolveProjectRoot returns the process project root. When the working
// directory is the Go module folder "backend", the parent is used so
// relative data paths resolve the same in tests and in the shipped binary.
func ResolveProjectRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if filepath.Base(cwd) == "backend" {
		return filepath.Dir(cwd)
	}
	return cwd
}

// ResolveProjectDataPath joins a relative path with the project root.
// Absolute paths are returned cleaned; empty input yields the project root.
// This helper is for data/workdir paths, not the mihomo executable.
func ResolveProjectDataPath(target string) string {
	target = strings.TrimSpace(target)
	var resolved string
	switch {
	case target == "":
		resolved = ResolveProjectRoot()
	case filepath.IsAbs(target):
		resolved = target
	default:
		resolved = filepath.Clean(filepath.Join(ResolveProjectRoot(), target))
	}
	return absOrClean(resolved)
}

// ResolveBinaryPath turns a configured mihomo path into an absolute filesystem
// path ready for Stat/exec:
//
//  1. empty → DefaultMihomoBinaryPath (env, else ./data/core/mihomo)
//  2. relative → project root
//  3. Windows → append .exe when missing
//
// It never returns a bare command name such as "mihomo". The result does not
// imply the file exists; use LocateBinary for that. Idempotent.
func ResolveBinaryPath(configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		configured = DefaultMihomoBinaryPath()
	}
	resolved := ResolveProjectDataPath(configured)
	resolved = withWindowsExecutableSuffix(resolved, runtime.GOOS)
	return absOrClean(resolved)
}

// LocateBinary resolves a configured path and returns the first existing
// non-directory candidate. On Windows both the .exe and extension-less names
// are tried so inspect, start, and node tests share the same lookup.
func LocateBinary(configured string) (string, error) {
	primary := ResolveBinaryPath(configured)
	var firstStatErr error
	for _, candidate := range binaryCandidates(primary, runtime.GOOS) {
		info, err := os.Stat(candidate)
		if err != nil {
			if firstStatErr == nil {
				firstStatErr = err
			}
			continue
		}
		if info.IsDir() {
			firstStatErr = fmt.Errorf("%s is a directory", candidate)
			continue
		}
		return candidate, nil
	}
	if firstStatErr != nil && !os.IsNotExist(firstStatErr) {
		return "", fmt.Errorf("读取 Mihomo 二进制文件失败: %w", firstStatErr)
	}
	return "", fmt.Errorf("%w: %s", ErrBinaryNotFound, primary)
}

func binaryCandidates(resolved, goos string) []string {
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return nil
	}
	out := []string{resolved}
	if goos != "windows" {
		return out
	}
	if strings.HasSuffix(strings.ToLower(resolved), ".exe") {
		alt := resolved[:len(resolved)-len(".exe")]
		if alt != "" && alt != resolved {
			out = append(out, alt)
		}
		return out
	}
	return append(out, resolved+".exe")
}

func withWindowsExecutableSuffix(path, goos string) string {
	if goos == "windows" && !strings.HasSuffix(strings.ToLower(path), ".exe") {
		return path + ".exe"
	}
	return path
}

func absOrClean(path string) string {
	if path == "" {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}
