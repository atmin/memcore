package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApply(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "small-commits", "Read before you commit: one change per commit.")

	write := func(c change) (string, error) {
		t.Helper()
		unlock, err := lockWrite(repo)
		if err != nil {
			return "", err
		}
		defer unlock()
		return apply(repo, c)
	}

	t.Run("a removal", func(t *testing.T) {
		sha, err := write(change{removes: []string{"dev/memories/small-commits.md"}, message: "forget: dev/memories/small-commits.md\n\nIt no longer holds."})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD")); got != sha {
			t.Errorf("apply returned %s, HEAD is %s", sha, got)
		}
		if got := gitT(t, repo, "log", "-1", "--format=%B"); got != "forget: dev/memories/small-commits.md\n\nIt no longer holds.\n\n" {
			t.Errorf("message = %q", got)
		}
		if strings.Contains(readT(t, filepath.Join(repo, "dev", "README.md")), "small-commits") {
			t.Error("the index still lists the removed memory")
		}
		clean(t, repo)
	})

	t.Run("an executable file", func(t *testing.T) {
		script := "#!/bin/sh\n# Run when you start the stack.\nset -eu\n"
		if _, err := write(change{writes: []file{{path: "dev/bin/start-stack", content: []byte(script), mode: 0o755}}, message: "draft: dev/bin/start-stack"}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := gitT(t, repo, "ls-files", "-s", "dev/bin/start-stack"); !strings.HasPrefix(got, "100755 ") {
			t.Errorf("mode = %q, want 100755", got)
		}
		if !strings.Contains(readT(t, filepath.Join(repo, "dev", "README.md")), "[start-stack](bin/start-stack): Run when you start the stack.") {
			t.Error("the index does not list the script")
		}
		clean(t, repo)
	})

	t.Run("a failure restores the paths", func(t *testing.T) {
		before := gitT(t, repo, "rev-parse", "HEAD")
		hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
		writeT(t, filepath.Dir(hook), "pre-commit", "#!/bin/sh\nexit 1\n")
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(hook)
		_, err := write(change{writes: []file{
			{path: "work/notes/a.md", content: []byte("A note.\n"), mode: 0o644},
			{path: "work/bin/b", content: []byte("#!/bin/sh\n"), mode: 0o755},
		}, message: "draft: work/notes/a.md"})
		if err == nil {
			t.Fatal("apply with a failing hook: no error")
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Errorf("HEAD moved to %s", got)
		}
		if _, err := os.Stat(filepath.Join(repo, "work")); err == nil {
			t.Error("the failed write left the level work")
		}
		clean(t, repo)
	})

	t.Run("the marker refuses", func(t *testing.T) {
		if err := writeMarker(repo, rewritten{accepted: "a", fetched: "b"}); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(markerPath(repo))
		if _, err := write(change{removes: []string{"dev/bin/start-stack"}, message: "forget: dev/bin/start-stack"}); err == nil || !strings.Contains(err.Error(), "memcore recover") {
			t.Errorf("error = %v, want the refusal of a rewritten remote", err)
		}
	})
}
