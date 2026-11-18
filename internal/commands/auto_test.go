package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/guards"
)

// authored runs f with date as the author date of each commit that f makes.
func authored(t *testing.T, date time.Time, f func()) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_DATE", date.Format(time.RFC3339)) // the end of the test restores the variable
	f()
	os.Unsetenv("GIT_AUTHOR_DATE")
}

// before returns the time d before now, to the second, as git keeps a date.
func before(d time.Duration) time.Time {
	return time.Now().Add(-d).Truncate(time.Second).UTC()
}

// keyT returns the key of a commit.
func keyT(t *testing.T, repo, rev string) string {
	t.Helper()
	_, key, err := keyOf(repo, strings.TrimSpace(gitT(t, repo, "rev-parse", rev)))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// rejectPushes makes the remote reject every push, with a pre-receive hook.
func rejectPushes(t *testing.T, remote string) {
	t.Helper()
	writeT(t, remote, "hooks/pre-receive", "#!/bin/sh\necho 'the remote rejects this push' >&2\nexit 1\n")
	if err := os.Chmod(filepath.Join(remote, "hooks", "pre-receive"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// noPerson is the confirmer of a push with no terminal.
func noPerson(string) (bool, error) { return false, guards.ErrNoPerson }

func TestAutoPushDelay(t *testing.T) {
	tests := []struct {
		name, value string // value "" leaves the key unset
		delay       time.Duration
		on, fails   bool
	}{
		{name: "unset"},
		{name: "a day", value: "24h", delay: 24 * time.Hour, on: true},
		{name: "no delay", value: "0s", on: true},
		{name: "a negative delay", value: "-1h", fails: true},
		{name: "no duration", value: "soon", fails: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, repo, _ := newMemory(t)
			if tt.value != "" {
				gitT(t, repo, "config", "memcore.autopush", tt.value)
			}
			delay, on, err := autoPushDelay(repo)
			if tt.fails {
				if err == nil || !strings.Contains(err.Error(), "memcore.autopush") || !strings.Contains(err.Error(), tt.value) {
					t.Fatalf("autoPushDelay = %v, want an error that names the key and the value %q", err, tt.value)
				}
				if on {
					t.Error("a setting that memcore cannot read turned the automatic push on")
				}
				return
			}
			if err != nil || delay != tt.delay || on != tt.on {
				t.Errorf("autoPushDelay = %v, %v, %v; want %v, %v", delay, on, err, tt.delay, tt.on)
			}
		})
	}
}

func TestRecordChanged(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	const fact = "Read when you test a fact."
	old, fresh := before(72*time.Hour), before(time.Hour)
	// oldWithFreshFixup remembers a memory three days ago and revises it an hour ago, after a pushed init.
	oldWithFreshFixup := func(t *testing.T, home string) {
		t.Helper()
		pushYes(t, home)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		authored(t, old, func() { remember(t, dev, "a-fact", fact) })
		authored(t, fresh, func() { reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.") })
	}
	// recorded checks that the queue is one folded commit with the old date, and that the record holds want for it.
	recorded := func(t *testing.T, repo string, want time.Time) {
		t.Helper()
		if got := strings.TrimSpace(gitT(t, repo, "rev-list", "--count", "origin/main..main")); got != "1" {
			t.Fatalf("the queue holds %s commits, want the one folded commit", got)
		}
		if got, err := authorDate(repo, "HEAD"); err != nil || !got.Equal(old) {
			t.Fatalf("the folded commit has the author date %v, %v; want the date %v of its target", got, err, old)
		}
		changed, err := readChanged(repo)
		if err != nil {
			t.Fatal(err)
		}
		if got := changed[keyT(t, repo, "HEAD")]; !got.Equal(want) {
			t.Errorf("the record holds %v for the folded commit, want %v\n%v", got, want, changed)
		}
	}

	t.Run("a chain with a fresh fixup", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		oldWithFreshFixup(t, home)
		if _, err := fold(repo); err != nil {
			t.Fatal(err)
		}
		recorded(t, repo, fresh)
	})

	t.Run("a fresh fixup that restores one of two documents", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		writeT(t, repo, "dev/notes/a.md", "---\nname: a\ndescription: \"Read when you test a.\"\n---\n\nA.\n")
		writeT(t, repo, "dev/notes/b.md", "---\nname: b\ndescription: \"Read when you test b.\"\n---\n\nB.\n")
		commitAt(t, repo, "test: two notes", old.Format(time.RFC3339))
		if err := os.Remove(filepath.Join(repo, "dev", "notes", "b.md")); err != nil {
			t.Fatal(err)
		}
		commitAt(t, repo, "fixup! test: two notes", fresh.Format(time.RFC3339))
		if _, err := fold(repo); err != nil {
			t.Fatal(err)
		}
		if got := gitT(t, repo, "show", "--format=", "--name-only", "HEAD"); strings.Contains(got, "b.md") {
			t.Fatalf("the folded commit changes %q, want it without b.md", got)
		}
		recorded(t, repo, fresh)
	})

	t.Run("a push of the person that is declined", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		oldWithFreshFixup(t, home)
		var asked string
		if out, err := runWith(t, Env{Dir: home, Confirm: answer(false, &asked)}, "push"); err != nil {
			t.Fatalf("push: %v\n%s", err, out)
		}
		recorded(t, repo, fresh)
	})

	t.Run("a push of an agent that the remote rejects", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		oldWithFreshFixup(t, home)
		rejectPushes(t, remote)
		if out, err := runWith(t, Env{Dir: home, Confirm: noPerson}, "push"); err == nil || !strings.Contains(err.Error(), "push to origin") {
			t.Fatalf("push = %v, want the rejection of the remote\n%s", err, out)
		}
		recorded(t, repo, fresh)
	})

	t.Run("a key that left the queue leaves the record", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		oldWithFreshFixup(t, home)
		pushYes(t, home)
		gone := keyT(t, repo, "HEAD")
		if changed, err := readChanged(repo); err != nil || changed[gone].IsZero() {
			t.Fatalf("the record before the next fold = %v, %v; want the pushed chain", changed, err)
		}
		dev := filepath.Join(home, "dev")
		remember(t, dev, "deploys", "Read before you deploy on Fridays.")
		reviseT(t, dev, memoryText("deploys", "Read before you deploy on Fridays.", "A sharper fact."), "deploys", "Sharper.")
		if _, err := fold(repo); err != nil {
			t.Fatal(err)
		}
		changed, err := readChanged(repo)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := changed[gone]; ok || len(changed) != 1 || changed[keyT(t, repo, "HEAD")].IsZero() {
			t.Errorf("the record = %v, want only the chain of the queue", changed)
		}
	})

	t.Run("a newer date of the record stays", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		oldWithFreshFixup(t, home)
		if _, err := fold(repo); err != nil {
			t.Fatal(err)
		}
		// The fixup is gone, so the commits of the chain are old. A second record keeps the date of the first.
		chains, _, err := chainsOf(repo)
		if err != nil {
			t.Fatal(err)
		}
		if err := recordChanged(repo, chains); err != nil {
			t.Fatal(err)
		}
		recorded(t, repo, fresh)
	})

	t.Run("a missing file reads as empty", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		if changed, err := readChanged(repo); err != nil || len(changed) != 0 {
			t.Errorf("readChanged = %v, %v; want an empty record", changed, err)
		}
	})

	t.Run("a broken file is an error", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		oldWithFreshFixup(t, home)
		writeT(t, repo, ".git/memcore/changed.json", "{\"half")
		if _, err := readChanged(repo); err == nil || !strings.Contains(err.Error(), "changed.json") {
			t.Errorf("readChanged = %v, want an error that names the file", err)
		}
		before := headCommit(repo)
		if _, err := fold(repo); err == nil || !strings.Contains(err.Error(), "changed.json") {
			t.Errorf("fold = %v, want an error that names the file", err)
		}
		if headCommit(repo) != before {
			t.Error("a fold that cannot record the dates rewrote the queue")
		}
	})
}
