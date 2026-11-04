package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

func TestInitClaudeSafety(t *testing.T) {
	t.Run("a source that changes during the question keeps auto-memory on", func(t *testing.T) {
		home, settings := claudeHome(t)
		fakeMemcore(t)
		src := filepath.Join(home, ".claude", "projects", projectFolder(filepath.Join(home, "dev", "shop")), "memory", "fact.md")
		before := readT(t, settings)
		_, err := runWith(t, Env{Dir: home, Confirm: func(q string) (bool, error) {
			writeT(t, filepath.Dir(src), "fact.md", flat("shop-fact", "Read when you test the shop.", "user", "A new fact."))
			return true, nil
		}}, "init", bareRemote(t))
		if err == nil || !strings.Contains(err.Error(), "changed") {
			t.Errorf("error = %v, want a refusal of the switch", err)
		}
		if got := readT(t, settings); got != before {
			t.Errorf("settings =\n%s\nwant them as they were", got)
		}
	})

	t.Run("init holds its own lock", func(t *testing.T) {
		home, _ := claudeHome(t)
		fakeMemcore(t)
		repo := filepath.Join(home, "memory")
		held := false
		_, err := runWith(t, Env{Dir: home, Confirm: func(string) (bool, error) {
			unlock, ok, err := store.TryLockFile(repo, "init.lock")
			if err == nil && ok {
				unlock()
			}
			held = err == nil && !ok
			return true, nil
		}}, "init", bareRemote(t))
		if err != nil {
			t.Fatal(err)
		}
		if !held {
			t.Error("init did not hold init.lock while it asked")
		}
	})

	t.Run("a Claude Code folder that cannot be read stops init", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root reads every folder")
		}
		home := newHome(t)
		locked := mkdirT(t, filepath.Join(home, "locked"))
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(locked, "claude"))
		if out, err := run(t, home, "", "init", bareRemote(t)); err == nil || strings.Contains(out, "No Claude Code folder") {
			t.Errorf("init: %v\n%s\nwant an error, not a skip", err, out)
		}
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		writeT(t, home, ".claude", "a file, not a folder\n")
		if _, err := run(t, home, "", "init", bareRemote(t)); err == nil {
			t.Error("init with a file at ~/.claude succeeded")
		}
	})
}
