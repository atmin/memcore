package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
