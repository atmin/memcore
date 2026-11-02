package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGit(t *testing.T) {
	dir := t.TempDir()
	if _, err := Git(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	out, err := Git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("toplevel = %q, want %q", got, want)
	}

	_, err = Git(dir, "rev-parse", "--verify", "no-such-ref")
	if err == nil {
		t.Fatal("bad command: no error")
	}
	for _, part := range []string{"git rev-parse --verify no-such-ref", "fatal"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not hold %q", err, part)
		}
	}
}

func TestGitIgnoresRepoVariables(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	for _, d := range []string{dir, other} {
		if _, err := Git(d, "init", "-b", "main"); err != nil {
			t.Fatalf("git init: %v", err)
		}
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	out, err := Git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("toplevel = %q, want %q", got, want)
	}
}
