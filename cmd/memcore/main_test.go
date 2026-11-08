package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// versionFlag sets the version of a build. The release workflow passes the same flag, with the tag as the version.
const versionFlag = "-X github.com/atmin/memcore/internal/commands.Version="

func TestVersionLdflags(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "memcore")
	build := exec.Command("go", "build", "-o", bin, "-ldflags", versionFlag+"v9.9.9", ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "HOME", "MEMCORE_REPO", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_SESSION_ID":
			continue
		}
		env = append(env, kv)
	}
	home := t.TempDir()
	cmd := exec.Command(bin, "version")
	cmd.Env = append(env, "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "memcore v9.9.9") {
		t.Errorf("memcore version: %v, output %q, want memcore v9.9.9", err, out)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("memcore version wrote %d entries into HOME", len(entries))
	}
}

func TestWorkingDir(t *testing.T) {
	fails := func() (string, error) { return "", errors.New("getwd: no such file or directory") }
	works := func() (string, error) { return "/work", nil }
	tests := []struct {
		name    string
		args    []string
		getwd   func() (string, error)
		want    string
		wantErr bool
	}{
		{"hook", []string{"tool"}, fails, "", false},
		{"orient", nil, fails, "", true},
		{"hook", []string{"prompt"}, fails, "", true},
		{"hook", []string{"tool"}, works, "/work", false},
		{"orient", nil, works, "/work", false},
	}
	for _, tt := range tests {
		got, err := workingDir(tt.name, tt.args, tt.getwd)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("workingDir(%q, %q) = %q, %v, want %q and an error: %v", tt.name, tt.args, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestBundleArgs(t *testing.T) {
	const bundle = "/Users/a/Applications/memcore.app/Contents/MacOS/memcore"
	for _, tt := range []struct {
		name, exe  string
		args, want []string
	}{
		{"a bundle with no arguments", bundle, nil, []string{"ui", "--app"}},
		{"a bundle with the -psn_ argument", bundle, []string{"-psn_0_123"}, []string{"ui", "--app"}},
		{"a bundle with a command", bundle, []string{"version"}, nil},
		{"an executable outside a bundle", "/Users/a/.local/bin/memcore", nil, nil},
	} {
		got, ok := bundleArgs(tt.exe, tt.args)
		if ok != (tt.want != nil) || strings.Join(got, " ") != strings.Join(tt.want, " ") {
			t.Errorf("%s: bundleArgs = %q, %v, want %q", tt.name, got, ok, tt.want)
		}
	}
}

// TestBundleLog runs memcore as memcore.app, where it writes its errors to ~/Library/Logs/memcore.log.
func TestBundleLog(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only macOS runs memcore.app")
	}
	bin := filepath.Join(t.TempDir(), "memcore.app", "Contents", "MacOS", "memcore")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	home := t.TempDir()
	// An agent may not run memcore ui, so the application fails at once, and writes why.
	cmd := exec.Command(bin)
	cmd.Env = []string{"HOME=" + home, "CLAUDECODE=1", "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("memcore.app ran as an agent:\n%s", out)
	}
	if len(out) != 0 {
		t.Errorf("memcore.app wrote to its standard error, not the log:\n%s", out)
	}
	log, err := os.ReadFile(filepath.Join(home, "Library", "Logs", "memcore.log"))
	if err != nil || !strings.Contains(string(log), "memcore ui:") || !strings.Contains(string(log), "CLAUDECODE") {
		t.Errorf("memcore.log = %q, %v, want the refusal of ui", log, err)
	}
}
