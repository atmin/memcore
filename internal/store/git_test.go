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

func TestGitStdin(t *testing.T) {
	dir := t.TempDir()
	if _, err := Git(dir, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	out, err := GitStdin(dir, "hello\n", "hash-object", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out), "ce013625030ba8dba906f756967f9e9ca394464a"; got != want {
		t.Errorf("hash = %s, want %s", got, want)
	}
}

func TestGitIndex(t *testing.T) {
	dir := t.TempDir()
	if _, err := Git(dir, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_INDEX_FILE", filepath.Join(dir, "wrong.index")) // as a git hook would set it
	index := filepath.Join(dir, ".git", "private.index")
	if _, err := GitIndex(dir, index, "", "read-tree", "--empty"); err != nil {
		t.Fatal(err)
	}
	blob := "ce013625030ba8dba906f756967f9e9ca394464a"
	if _, err := GitStdin(dir, "hello\n", "hash-object", "-w", "--stdin"); err != nil {
		t.Fatal(err)
	}
	if _, err := GitIndex(dir, index, "100644 "+blob+"\tnotes/hello.txt\n", "update-index", "--index-info"); err != nil {
		t.Fatal(err)
	}
	tree, err := GitIndex(dir, index, "", "write-tree")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Git(dir, "ls-tree", "-r", "--name-only", strings.TrimSpace(tree))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "notes/hello.txt" {
		t.Errorf("tree holds %q, want notes/hello.txt", got)
	}
	if out, _ := Git(dir, "ls-files"); out != "" {
		t.Errorf("the index of the repo holds %q, want nothing", out)
	}
}
