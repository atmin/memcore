package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPagesStagesScripts checks that the workflow of GitHub Pages deploys each script of the site: each file
// of site/ without an extension.
func TestPagesStagesScripts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "pages.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var stage string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "cp site/") {
			stage = line
		}
	}
	if stage == "" {
		t.Fatal("pages.yml copies nothing from site/")
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || strings.Contains(e.Name(), ".") {
			continue
		}
		n++
		if !strings.Contains(stage+" ", " site/"+e.Name()+" ") {
			t.Errorf("pages.yml does not deploy site/%s: %s", e.Name(), strings.TrimSpace(stage))
		}
	}
	if n < 2 {
		t.Errorf("site/ holds %d scripts, want install and uninstall", n)
	}
}
