package kernel

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Rain-kl/Foam/backend/internal/infra/config"
)

func TestDefaultMihomoBinaryPath_FallsBackToDataCore(t *testing.T) {
	t.Setenv(config.EnvClashMihomoBinaryPath, "")
	if got := DefaultMihomoBinaryPath(); got != "./data/core/mihomo" {
		t.Fatalf("DefaultMihomoBinaryPath() = %q, want ./data/core/mihomo", got)
	}
}

func TestDefaultMihomoBinaryPath_UsesEnv(t *testing.T) {
	t.Setenv(config.EnvClashMihomoBinaryPath, "/opt/mihomo")
	if got := DefaultMihomoBinaryPath(); got != "/opt/mihomo" {
		t.Fatalf("DefaultMihomoBinaryPath() = %q, want /opt/mihomo", got)
	}
}

func TestDefaultMihomoBinaryPath_TrimsEnv(t *testing.T) {
	t.Setenv(config.EnvClashMihomoBinaryPath, "  /opt/mihomo  ")
	if got := DefaultMihomoBinaryPath(); got != "/opt/mihomo" {
		t.Fatalf("DefaultMihomoBinaryPath() = %q, want /opt/mihomo", got)
	}
}

func TestWithWindowsExecutableSuffix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		goos string
		want string
	}{
		{
			name: "windows adds exe",
			path: `C:\app\data\core\mihomo`,
			goos: "windows",
			want: `C:\app\data\core\mihomo.exe`,
		},
		{
			name: "windows keeps existing exe",
			path: `C:\app\data\core\mihomo.exe`,
			goos: "windows",
			want: `C:\app\data\core\mihomo.exe`,
		},
		{
			name: "windows keeps uppercase EXE",
			path: `C:\app\data\core\mihomo.EXE`,
			goos: "windows",
			want: `C:\app\data\core\mihomo.EXE`,
		},
		{
			name: "linux unchanged",
			path: "/opt/mihomo",
			goos: "linux",
			want: "/opt/mihomo",
		},
		{
			name: "darwin unchanged",
			path: "./data/core/mihomo",
			goos: "darwin",
			want: "./data/core/mihomo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := withWindowsExecutableSuffix(tt.path, tt.goos)
			if got != tt.want {
				t.Errorf("withWindowsExecutableSuffix(%q, %q) = %q, want %q", tt.path, tt.goos, got, tt.want)
			}
		})
	}
}

func TestBinaryCandidates_WindowsTriesExeAndBare(t *testing.T) {
	t.Parallel()

	got := binaryCandidates(`C:\app\mihomo.exe`, "windows")
	if len(got) != 2 || got[0] != `C:\app\mihomo.exe` || got[1] != `C:\app\mihomo` {
		t.Fatalf("binaryCandidates(exe, windows) = %#v, want [mihomo.exe mihomo]", got)
	}

	got = binaryCandidates(`C:\app\mihomo`, "windows")
	if len(got) != 2 || got[0] != `C:\app\mihomo` || got[1] != `C:\app\mihomo.exe` {
		t.Fatalf("binaryCandidates(bare, windows) = %#v, want [mihomo mihomo.exe]", got)
	}

	got = binaryCandidates("/opt/mihomo", "linux")
	if len(got) != 1 || got[0] != "/opt/mihomo" {
		t.Fatalf("binaryCandidates(linux) = %#v, want single path", got)
	}
}

func TestResolveBinaryPath_EmptyUsesDefaultAbsolute(t *testing.T) {
	t.Setenv(config.EnvClashMihomoBinaryPath, "")
	got := ResolveBinaryPath("")
	if got == "" || got == "mihomo" {
		t.Fatalf("ResolveBinaryPath(\"\") = %q, want resolved absolute path", got)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("ResolveBinaryPath(\"\") = %q, want absolute", got)
	}
	base := filepath.Base(got)
	if runtime.GOOS == "windows" {
		if !strings.EqualFold(base, "mihomo.exe") {
			t.Fatalf("ResolveBinaryPath(\"\") base = %q, want mihomo.exe", base)
		}
		return
	}
	if base != "mihomo" {
		t.Fatalf("ResolveBinaryPath(\"\") base = %q, want mihomo", base)
	}
}

func TestResolveBinaryPath_BareNameIsProjectRelative(t *testing.T) {
	got := ResolveBinaryPath("mihomo")
	if got == "mihomo" {
		t.Fatal("ResolveBinaryPath(\"mihomo\") = \"mihomo\", want project-relative absolute path")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("ResolveBinaryPath(\"mihomo\") = %q, want absolute", got)
	}
}

func TestResolveBinaryPath_Idempotent(t *testing.T) {
	first := ResolveBinaryPath("./data/core/mihomo")
	second := ResolveBinaryPath(first)
	if first != second {
		t.Fatalf("ResolveBinaryPath is not idempotent: first %q second %q", first, second)
	}
}

func TestResolveBinaryPath_EnvOverride(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "from-env")
	t.Setenv(config.EnvClashMihomoBinaryPath, configured)
	got := ResolveBinaryPath("")
	want, err := filepath.Abs(configured)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Fatalf("ResolveBinaryPath(\"\") = %q, want env path %q", got, want)
	}
}

func TestLocateBinary_FindsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mihomo")
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	got, err := LocateBinary(path)
	if err != nil {
		t.Fatalf("LocateBinary(%q) error = %v, want nil", path, err)
	}
	if got != path {
		t.Fatalf("LocateBinary(%q) = %q, want %q", path, got, path)
	}
}

func TestLocateBinary_MissingReportsResolvedPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := LocateBinary(missing)
	if err == nil {
		t.Fatal("LocateBinary() error = nil, want ErrBinaryNotFound")
	}
	if !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("LocateBinary() error = %v, want ErrBinaryNotFound", err)
	}
	if !strings.Contains(err.Error(), filepath.Base(missing)) {
		t.Fatalf("LocateBinary() error = %q, want to mention %q", err, filepath.Base(missing))
	}
	if strings.TrimPrefix(err.Error(), ErrBinaryNotFound.Error()+": ") == "mihomo" {
		t.Fatalf("LocateBinary() error = %q, still bare mihomo", err)
	}
}

func TestLocateBinary_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := LocateBinary(dir)
	if err == nil {
		t.Fatal("LocateBinary(dir) error = nil, want error")
	}
}

func TestResolveProjectDataPath_EmptyIsRootNotMihomo(t *testing.T) {
	got := ResolveProjectDataPath("")
	if strings.Contains(got, "mihomo") {
		t.Fatalf("ResolveProjectDataPath(\"\") = %q, must not default to mihomo binary", got)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("ResolveProjectDataPath(\"\") = %q, want absolute", got)
	}
}
