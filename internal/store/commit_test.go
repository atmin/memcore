package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommit(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-b", "main")
	writeT(t, repo, "dev/memories/a.md", "a\n", 0o644)
	writeT(t, repo, "dev/README.md", "# ~/dev\n", 0o644)
	writeT(t, repo, "other.md", "not part of the change\n", 0o644)

	if err := Commit(repo, []string{"dev/memories/a.md", "dev/README.md"}, "remember: dev/memories/a.md"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := strings.TrimSpace(gitT(t, repo, "log", "-1", "--format=%s")); got != "remember: dev/memories/a.md" {
		t.Errorf("message = %q", got)
	}
	if got := strings.Fields(gitT(t, repo, "show", "--name-only", "--format=")); strings.Join(got, " ") != "dev/README.md dev/memories/a.md" {
		t.Errorf("committed paths = %q", got)
	}

	if err := os.Remove(filepath.Join(repo, "dev/memories/a.md")); err != nil {
		t.Fatal(err)
	}
	if err := Commit(repo, []string{"dev/memories/a.md"}, "forget: dev/memories/a.md"); err != nil {
		t.Fatalf("Commit of a removal: %v", err)
	}
	if got := strings.TrimSpace(gitT(t, repo, "status", "--porcelain")); got != "?? other.md" {
		t.Errorf("status = %q, want only the untouched file", got)
	}
}

func TestCommitLiteralPaths(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-b", "main")
	writeT(t, repo, "old [ab]/README.md", "# ~/old [ab]\n", 0o644)
	writeT(t, repo, "old a/README.md", "not part of the change\n", 0o644)

	if err := Commit(repo, []string{"old [ab]/README.md"}, "remember: old [ab]/README.md"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := strings.TrimSpace(gitT(t, repo, "show", "--name-only", "--format=")); got != "old [ab]/README.md" {
		t.Errorf("committed paths = %q, want only the bracketed path", got)
	}
}
