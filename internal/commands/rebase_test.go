package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
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

func TestPushRace(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	t.Run("a marker that appears while the person answers", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "small-commits", "Read before you commit: one change per commit.")
		confirm := func(string) (bool, error) {
			return true, writeMarker(repo, rewritten{accepted: "a", fetched: "b"})
		}
		if _, err := runWith(t, Env{Dir: home, Confirm: confirm}, "push"); err == nil || !strings.Contains(err.Error(), "memcore recover") {
			t.Errorf("push: error = %v, want the refusal of a rewritten remote", err)
		}
		if _, err := store.Git(remote, "rev-parse", "--verify", "-q", "main"); err == nil {
			t.Error("the remote has main after the refusal")
		}
	})

	t.Run("a remote that moves while the person answers", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		gitT(t, repo, "push", "-q", "origin", "main")
		remember(t, dev, "small-commits", "Read before you commit: one change per commit.")
		confirm := func(string) (bool, error) {
			other := secondClone(t, remote)
			writeT(t, other, "work/notes/elsewhere.md", "From another machine.\n")
			gitT(t, other, "add", "-A")
			gitT(t, other, "commit", "-q", "-m", "test: a commit from another machine")
			gitT(t, other, "push", "-q", "origin", "main")
			return true, nil
		}
		before := gitT(t, remote, "rev-parse", "main")
		if _, err := runWith(t, Env{Dir: home, Confirm: confirm}, "push"); err == nil || !strings.Contains(err.Error(), "run memcore push again") {
			t.Errorf("push: error = %v, want a refusal that asks for a new push", err)
		}
		if got := gitT(t, remote, "rev-parse", "main"); got == before {
			t.Fatal("the test did not move the remote")
		}
		if got, want := strings.TrimSpace(gitT(t, repo, "rev-parse", "origin/main")), strings.TrimSpace(gitT(t, remote, "rev-parse", "main")); got != want {
			t.Errorf("origin/main = %s, want the remote tip %s", got, want)
		}
	})

	t.Run("a fatal ancestry check is an error", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		if _, err := isAncestor(repo, "0000000000000000000000000000000000000001", "HEAD"); err == nil {
			t.Error("isAncestor of a missing commit: no error")
		}
	})
}
