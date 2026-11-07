package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"testing"
)

func TestVendorHashes(t *testing.T) {
	readme, err := static.ReadFile("static/README.md")
	if err != nil {
		t.Fatal(err)
	}
	// Each section names its file in its heading, and its hash in a line after it.
	sections := regexp.MustCompile("(?s)## `(vendor/[^`]+)`.*?\\*\\*SHA-256:\\*\\* `([0-9a-f]{64})`").FindAllStringSubmatch(string(readme), -1)
	if len(sections) != 3 {
		t.Fatalf("the README pins %d files, want 3", len(sections))
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
