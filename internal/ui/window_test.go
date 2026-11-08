package ui

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeInfo is a file that stat finds.
type fakeInfo struct{ fs.FileInfo }

func (fakeInfo) IsDir() bool { return false }

func TestWindowPicksBrowser(t *testing.T) {
	defer func(g string, l func(string) (string, error), s func(string) (fs.FileInfo, error), c func(string, ...string) *exec.Cmd) {
		goos, lookPath, stat, command = g, l, s, c
	}(goos, lookPath, stat, command)
	chrome := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	tests := []struct {
		name     string
		goos     string
		have     string // the one browser that exists, as a path on macOS or a name on Linux
		wantBin  string
		wantStop bool
	}{
		{"chrome on macOS", "darwin", chrome, chrome, true},
		{"edge on macOS", "darwin", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", true},
		{"no browser on macOS", "darwin", "", "open", false},
		{"chromium on Linux", "linux", "chromium", "/usr/bin/chromium", true},
		{"no browser on Linux", "linux", "", "xdg-open", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goos = tt.goos
			stat = func(p string) (fs.FileInfo, error) {
				if tt.goos == "darwin" && p == tt.have {
					return fakeInfo{}, nil
				}
				return nil, fs.ErrNotExist
			}
			lookPath = func(name string) (string, error) {
				if tt.goos == "linux" && name == tt.have {
					return "/usr/bin/" + name, nil
				}
				return "", errors.New("not found")
			}
			var bin string
			var args []string
			command = func(name string, a ...string) *exec.Cmd {
				bin, args = name, a
				if strings.HasSuffix(name, "open") {
					return exec.Command("true")
				}
				return exec.Command("sleep", "30")
			}
			stop, err := Window("http://127.0.0.1:1/#token")
			if err != nil {
				t.Fatal(err)
			}
			if bin != tt.wantBin {
				t.Errorf("started %q, want %q", bin, tt.wantBin)
			}
			if !tt.wantStop {
				if stop != nil || !slices.Equal(args, []string{"http://127.0.0.1:1/#token"}) {
					t.Errorf("the default opener got %q, and stop is %v", args, stop != nil)
				}
				return
			}
			if stop == nil || len(args) != 4 || args[0] != "--app=http://127.0.0.1:1/#token" || args[2] != "--no-first-run" || args[3] != "--no-default-browser-check" {
				t.Fatalf("browser arguments = %q", args)
			}
			profile := strings.TrimPrefix(args[1], "--user-data-dir=")
			if _, err := os.Stat(profile); err != nil || !strings.HasPrefix(filepath.Base(profile), "memcore-ui-") {
				t.Errorf("the profile %s: %v", profile, err)
			}
			start := time.Now()
			stop()
			if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the profile remains after stop: %v", err)
			}
			if time.Since(start) > 4*time.Second {
				t.Error("stop waited for the kill, but sleep ends on SIGTERM")
			}
		})
	}
}

func TestOpenExternal(t *testing.T) {
	defer func(g string, c func(string, ...string) *exec.Cmd) { goos, command = g, c }(goos, command)
	for _, tt := range []struct {
		goos, link, wantBin string
	}{
		{"darwin", "https://example.com/a", "open"},
		{"linux", "http://example.com/a", "xdg-open"},
		{"darwin", "mailto:a@example.com", "open"},
		{"darwin", "file:///etc/passwd", ""},
		{"linux", "javascript:alert(1)", ""},
		{"darwin", "/relative", ""},
	} {
		goos = tt.goos
		var bin string
		var args []string
		command = func(name string, a ...string) *exec.Cmd {
			bin, args = name, a
			return exec.Command("true")
		}
		err := OpenExternal(tt.link)
		if tt.wantBin == "" {
			if err == nil || bin != "" {
				t.Errorf("OpenExternal(%q) ran %q, %v, want a refusal", tt.link, bin, err)
			}
			continue
		}
		if err != nil || bin != tt.wantBin || !slices.Equal(args, []string{tt.link}) {
			t.Errorf("OpenExternal(%q) on %s ran %q %q, %v, want %s", tt.link, tt.goos, bin, args, err, tt.wantBin)
		}
	}
}
