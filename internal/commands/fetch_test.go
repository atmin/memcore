package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// secondClone clones the remote into a new directory, as another machine would.
func secondClone(t *testing.T, remote string) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "memory")
	gitT(t, t.TempDir(), "clone", "-q", remote, other)
	return other
}

func TestFetch(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	t.Run("a pushed commit lands under the queue", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		gitT(t, repo, "push", "-q", "origin", "main")
		other := secondClone(t, remote)
		writeT(t, other, "work/notes/2026-11-02-elsewhere.md", "From another machine.\n")
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: a commit from another machine")
		gitT(t, other, "push", "-q", "origin", "main")
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "small-commits", "Read before you commit: one change per commit.")

		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		got := strings.Fields(strings.ReplaceAll(gitT(t, repo, "log", "--format=%s|"), " ", "_"))
		want := "remember:_dev/memories/small-commits.md| test:_a_commit_from_another_machine| init:_create_the_memory_repo|"
		if strings.Join(got, " ") != want {
			t.Errorf("history = %q, want %q", got, want)
		}
		if got := strings.TrimSpace(gitT(t, repo, "rev-list", "--count", "origin/main..main")); got != "1" {
			t.Errorf("queue holds %s commits, want 1", got)
		}
	})

	t.Run("both sides add a memory at one level", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		gitT(t, repo, "push", "-q", "origin", "main")
		other := secondClone(t, remote)
		t.Setenv("MEMCORE_REPO", other)
		remember(t, dev, "from-elsewhere", "Read when you work on another machine.")
		gitT(t, other, "push", "-q", "origin", "main")
		t.Setenv("MEMCORE_REPO", "")
		remember(t, dev, "from-here", "Read when you work on this machine.")

		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		index := readT(t, filepath.Join(repo, "dev", "README.md"))
		ix, err := store.Lines(repo, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if index != store.Render(ix) || !strings.Contains(index, "from-elsewhere") || !strings.Contains(index, "from-here") {
			t.Errorf("dev/README.md =\n%s\nwant both memories, as generated", index)
		}
		if got := strings.TrimSpace(gitT(t, repo, "rev-list", "--count", "origin/main..main")); got != "1" {
			t.Errorf("queue holds %s commits, want 1", got)
		}
		clean(t, repo)
	})

	t.Run("a conflict stops with the path", func(t *testing.T) {
		_, repo, remote := newMemory(t)
		gitT(t, repo, "push", "-q", "origin", "main")
		other := secondClone(t, remote)
		writeT(t, other, "work/notes/same.md", "The remote version.\n")
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: the remote version")
		gitT(t, other, "push", "-q", "origin", "main")
		writeT(t, repo, "work/notes/same.md", "The local version.\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: the local version")
		before := gitT(t, repo, "rev-parse", "HEAD")

		err := Fetch(repo)
		if err == nil || !strings.Contains(err.Error(), "work/notes/same.md") {
			t.Fatalf("error = %v, want a conflict that names work/notes/same.md", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Errorf("HEAD = %s, want it back at %s", got, before)
		}
		for _, d := range []string{"rebase-merge", "rebase-apply"} {
			if _, err := os.Stat(filepath.Join(repo, ".git", d)); err == nil {
				t.Errorf("a rebase is still in progress: .git/%s exists", d)
			}
		}
		clean(t, repo)

		if _, err := store.Git(repo, "rebase", "origin/main"); err == nil {
			t.Fatal("the rebase by hand: no conflict")
		}
		err = Fetch(repo)
		if err == nil || !strings.Contains(err.Error(), "rebase is in progress") {
			t.Fatalf("error = %v, want a refusal of the rebase in progress", err)
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "rebase-merge")); err != nil {
			t.Errorf("Fetch ended the rebase of the person: %v", err)
		}
	})

	t.Run("another branch", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		gitT(t, repo, "push", "-q", "origin", "main")
		gitT(t, repo, "checkout", "-q", "-b", "draft")
		if err := Fetch(repo); err == nil || !strings.Contains(err.Error(), "main") {
			t.Errorf("error = %v, want a refusal that names main", err)
		}
	})
}
