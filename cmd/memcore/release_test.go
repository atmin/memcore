package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pinned matches a uses: line that names a full commit sha.
var pinned = regexp.MustCompile(`^\s*(- )?uses: [\w./-]+@[0-9a-f]{40}( #.*)?$`)

// workflow reads a workflow of the repo, and checks that every action in it is pinned to a commit.
func workflow(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../.github/workflows/" + name)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	uses := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "uses:") {
			uses++
			if !pinned.MatchString(line) {
				t.Errorf("%s: the action is not pinned to a commit sha: %q", name, line)
			}
		}
	}
	if uses == 0 {
		t.Errorf("%s uses no action", name)
	}
	return text
}

func TestReleaseWorkflow(t *testing.T) {
	text := workflow(t, "release.yml")
	for _, want := range []string{
		"tags: ['v*']",
		"CGO_ENABLED=0",
		"darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64",
		"-tags nonative", "-glibc", "-static",
		"SHA256SUMS",
		versionFlag + "${GITHUB_REF_NAME}",
		"actions/checkout@", "actions/setup-go@",
		"go test ./...",
		"gh release create",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release.yml holds no %q", want)
		}
	}
}

func TestPagesWorkflow(t *testing.T) {
	text := workflow(t, "pages.yml")
	for _, want := range []string{
		"actions/checkout@",
		"site/index.html", "site/install", "internal/ui/static/icon.svg",
		"path: _site",
		"actions/configure-pages@", "actions/upload-pages-artifact@", "actions/deploy-pages@",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("pages.yml holds no %q", want)
		}
	}
}

// TestNativeBuilds builds memcore with CGO_ENABLED=0 for each target of a release. Each build holds glaze, the
// native window, and the build with -tags nonative holds none.
func TestNativeBuilds(t *testing.T) {
	for _, tt := range []struct {
		goos, goarch, tags string
		glaze              bool
	}{
		{"darwin", "arm64", "", true},
		{"darwin", "amd64", "", true},
		{"linux", "arm64", "", true},
		{"linux", "amd64", "", true},
		{"linux", "amd64", "nonative", false},
	} {
		t.Run(tt.goos+"/"+tt.goarch+" "+tt.tags, func(t *testing.T) {
			t.Parallel()
			bin := filepath.Join(t.TempDir(), "memcore")
			build := exec.Command("go", "build", "-tags", tt.tags, "-o", bin, ".")
			build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+tt.goos, "GOARCH="+tt.goarch)
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}
			out, err := exec.Command("go", "version", "-m", bin).CombinedOutput()
			if err != nil {
				t.Fatalf("go version -m: %v\n%s", err, out)
			}
			if got := strings.Contains(string(out), "github.com/crgimenes/glaze"); got != tt.glaze {
				t.Errorf("the build holds glaze: %v, want %v\n%s", got, tt.glaze, out)
			}
		})
	}
}
