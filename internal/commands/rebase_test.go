package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRebaseSafety(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	t.Run("a conflicting index that is a symlink", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "first", "Read when you test the first memory.")
		gitT(t, repo, "push", "-q", "origin", "main")
		outside := filepath.Join(t.TempDir(), "outside.md")
		writeT(t, filepath.Dir(outside), "outside.md", "untouched\n")
		other := secondClone(t, remote)
		os.Remove(filepath.Join(other, "dev", "README.md"))
		if err := os.Symlink(outside, filepath.Join(other, "dev", "README.md")); err != nil {
			t.Fatal(err)
		}
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: an index that is a symlink")
		gitT(t, other, "push", "-q", "origin", "main")
		remember(t, dev, "unrelated", "Read before you deploy on Fridays.")

		if err := Fetch(repo); err == nil {
			t.Error("Fetch over an index that became a symlink: no error")
		}
		if got := readT(t, outside); got != "untouched\n" {
			t.Errorf("the file outside the repo = %q, want it untouched", got)
		}
		if rebasing(repo) {
			t.Error("a rebase is still in progress")
		}
	})

	t.Run("a level that the remote empties and the queue fills", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		lab := mkdirT(t, filepath.Join(home, "lab"))
		remember(t, lab, "first-lab", "Read when you test the first lab fact.")
		gitT(t, repo, "push", "-q", "origin", "main")
		other := secondClone(t, remote)
		if err := os.RemoveAll(filepath.Join(other, "lab")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("MEMCORE_REPO", other)
		remember(t, home, "root-fact", "Read before you deploy on Fridays.") // writes the root index of other without lab
		t.Setenv("MEMCORE_REPO", "")
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: the lab goes elsewhere")
		gitT(t, other, "push", "-q", "origin", "main")
		remember(t, lab, "second-lab", "Read before you deploy on Fridays.")
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := readT(t, filepath.Join(repo, "README.md")); !strings.Contains(got, "[lab](lab/README.md)") {
			t.Errorf("root index =\n%s\nwant the level lab", got)
		}
		if got := readT(t, filepath.Join(repo, "lab", "README.md")); strings.Contains(got, "first-lab") || !strings.Contains(got, "second-lab") {
			t.Errorf("lab index =\n%s\nwant only second-lab", got)
		}
	})

	t.Run("a failed abort keeps no claim of a clean queue", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		err := abort(repo, &conflictError{paths: []string{"dev/memories/a.md"}})
		var ce *conflictError
		if err == nil || errors.As(err, &ce) || !strings.Contains(err.Error(), "abort the rebase") {
			t.Errorf("abort without a rebase: error = %v, want a failure that is no conflictError", err)
		}
	})
}
