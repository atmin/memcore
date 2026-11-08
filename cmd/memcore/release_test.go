package main

import (
	"os"
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
