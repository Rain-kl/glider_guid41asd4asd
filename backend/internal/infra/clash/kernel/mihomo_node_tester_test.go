package kernel_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rain-kl/Foam/backend/internal/infra/clash/kernel"
)

func TestTestNodeWithMihomo_MissingBinaryUsesResolvedAbsolutePath(t *testing.T) {
	_, err := kernel.TestNodeWithMihomo(context.Background(), kernel.MihomoNodeTestInput{
		BinaryPath: "./data/core/mihomo",
		TestURL:    "https://cp.cloudflare.com/generate_204",
		Timeout:    time.Second,
	})
	if err == nil {
		t.Fatal("TestNodeWithMihomo() error = nil, want missing-binary error")
	}
	msg := err.Error()
	const prefix = "未找到 Mihomo 二进制文件: "
	if !strings.HasPrefix(msg, prefix) {
		t.Fatalf("TestNodeWithMihomo() error = %q, want prefix %q", msg, prefix)
	}
	reported := strings.TrimPrefix(msg, prefix)
	if reported == "mihomo" || reported == "./data/core/mihomo" {
		t.Fatalf("TestNodeWithMihomo() error = %q, want resolved absolute path (not bare/relative name)", msg)
	}
	if !filepath.IsAbs(reported) {
		t.Fatalf("TestNodeWithMihomo() reported path %q, want absolute path", reported)
	}
}
