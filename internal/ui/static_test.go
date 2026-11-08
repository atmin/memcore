package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

func TestVendorHashes(t *testing.T) {
	readme, err := static.ReadFile("static/README.md")
	if err != nil {
		t.Fatal(err)
	}
	// Each section names its file in its heading, and its hash in a line after it.
	sections := regexp.MustCompile("(?s)## `(vendor/[^`]+)`.*?\\*\\*SHA-256:\\*\\* `([0-9a-f]{64})`").FindAllStringSubmatch(string(readme), -1)
	if len(sections) != 6 {
		t.Fatalf("the README pins %d files, want 6", len(sections))
	}
	for _, m := range sections {
		data, err := static.ReadFile("static/" + m[1])
		if err != nil {
			t.Errorf("read %s: %v", m[1], err)
			continue
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != m[2] {
			t.Errorf("%s has SHA-256 %s, but the README pins %s", m[1], got, m[2])
		}
	}
	entries, err := static.ReadDir("static/vendor")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(sections) {
		t.Errorf("static/vendor holds %d files, and the README pins %d", len(entries), len(sections))
	}
}

// TestIndexLoadsVendor checks that the page loads each vendored file with defer, before app.js, and Preact
// before its hooks, which read the global that Preact sets.
func TestIndexLoadsVendor(t *testing.T) {
	data, err := static.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(data)
	at := func(src string) int {
		t.Helper()
		tag := `<script src="/static/` + src + `" defer></script>`
		i := strings.Index(index, tag)
		if i < 0 {
			t.Errorf("index.html has no %s", tag)
		}
		return i
	}
	entries, err := static.ReadDir("static/vendor")
	if err != nil {
		t.Fatal(err)
	}
	app := at("app.js")
	for _, e := range entries {
		if i := at("vendor/" + e.Name()); i >= 0 && app >= 0 && i > app {
			t.Errorf("index.html loads vendor/%s after app.js", e.Name())
		}
	}
	if p, h := at("vendor/preact.umd.js"), at("vendor/hooks.umd.js"); p >= 0 && h >= 0 && p > h {
		t.Error("index.html loads hooks.umd.js before preact.umd.js")
	}
}
