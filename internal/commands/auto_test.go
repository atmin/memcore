package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
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

// noteText returns a note with its frontmatter.
func noteText(name, body string) string {
	return "---\nname: " + name + "\ndescription: \"Read when you test " + name + ".\"\n---\n\n" + body + "\n"
}

// handNote commits a note of ~/dev by hand, with an author date.
func handNote(t *testing.T, repo, name string, date time.Time) {
	t.Helper()
	writeT(t, repo, "dev/notes/"+name+".md", noteText(name, "A note."))
	commitAt(t, repo, "test: the note "+name, date.Format(time.RFC3339))
}

// draftT runs draft in dir, and fails the test on an error.
func draftT(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := run(t, dir, "", append([]string{"draft"}, args...)...); err != nil {
		t.Fatalf("draft %v: %v\n%s", args, err, out)
	}
}

// declinedPush runs a push that the person declines, which folds the queue and pushes nothing.
func declinedPush(t *testing.T, home string) {
	t.Helper()
	var asked string
	if out, err := runWith(t, Env{Dir: home, Confirm: answer(false, &asked)}, "push"); err != nil || asked == "" {
		t.Fatalf("push = %v, asked %q\n%s", err, asked, out)
	}
}

func TestSortChains(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	const fact = "Read when you test a fact."
	const delay = 24 * time.Hour
	old, fresh := before(72*time.Hour), before(time.Hour)
	type want struct {
		state  string
		reason string    // a part of the reason, or ""
		ripe   time.Time // the time of a waiting chain, or zero
	}
	cases := []struct {
		name string
		// queue builds the review queue on top of a pushed init, and returns the verdict of each chain, oldest first.
		queue func(t *testing.T, home, repo, dev string) []want
		fails string // a part of the error, or ""
	}{
		{name: "a ripe memory", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, old, func() { remember(t, dev, "a-fact", fact) })
			return []want{{state: statePush}}
		}},
		{name: "a fresh memory", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, fresh, func() { remember(t, dev, "a-fact", fact) })
			return []want{{state: stateWait, ripe: fresh.Add(delay)}}
		}},
		{name: "an old memory with a fresh fixup", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, old, func() { remember(t, dev, "a-fact", fact) })
			authored(t, fresh, func() { reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.") })
			return []want{{state: stateWait, ripe: fresh.Add(delay)}}
		}},
		{name: "an old memory whose fresh fixup a declined push folded", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, old, func() { remember(t, dev, "a-fact", fact) })
			authored(t, fresh, func() { reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.") })
			declinedPush(t, home)
			return []want{{state: stateWait, ripe: fresh.Add(delay)}}
		}},
		{name: "an old memory whose fresh fixup by hand a declined push folded", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, old, func() { remember(t, dev, "a-fact", fact) })
			writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "A sharper fact."))
			commitAt(t, repo, "fixup! remember: dev/memories/a-fact.md", fresh.Format(time.RFC3339))
			declinedPush(t, home)
			return []want{{state: stateWait, ripe: fresh.Add(delay)}}
		}},
		{name: "an old target of two documents whose fresh fixup restored one before a declined push", queue: func(t *testing.T, home, repo, dev string) []want {
			writeT(t, repo, "dev/notes/a.md", noteText("a", "A."))
			writeT(t, repo, "dev/notes/b.md", noteText("b", "B."))
			commitAt(t, repo, "test: two notes", old.Format(time.RFC3339))
			if err := os.Remove(filepath.Join(repo, "dev", "notes", "b.md")); err != nil {
				t.Fatal(err)
			}
			commitAt(t, repo, "fixup! test: two notes", fresh.Format(time.RFC3339))
			declinedPush(t, home)
			return []want{{state: stateWait, ripe: fresh.Add(delay)}}
		}},
		{name: "a broken record", fails: "changed.json", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, old, func() { remember(t, dev, "a-fact", fact) })
			writeT(t, repo, ".git/memcore/changed.json", "{\"half")
			return nil
		}},
		{name: "a skill", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, old, func() { draftT(t, dev, "skill", "a-skill", "--description", "Use when you test.") })
			return []want{{state: stateHeld, reason: "only the person pushes dev/skills/a-skill/SKILL.md"}}
		}},
		{name: "a remember and its forget", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, old, func() {
				remember(t, dev, "a-fact", fact)
				if out, err := run(t, dev, "", "forget", "a-fact", "--reason", "A mistake."); err != nil {
					t.Fatalf("forget: %v\n%s", err, out)
				}
			})
			return []want{{state: stateHeld, reason: "its commits cancel each other"}}
		}},
		{name: "a ripe note after a held skill", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, old, func() {
				draftT(t, dev, "skill", "a-skill", "--description", "Use when you test.")
				draftT(t, dev, "note", "slow-search", "--description", "Read when search is slow.")
			})
			return []want{{state: stateHeld, reason: "dev/skills/a-skill/SKILL.md"}, {state: statePush}}
		}},
		{name: "a ripe move of a document that a fresh chain made", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, fresh, func() { remember(t, dev, "a-fact", fact) })
			made := short(headCommit(repo))
			authored(t, old, func() {
				if out, err := run(t, dev, "", "move", "a-fact", "the-fact", "--reason", "The name states the topic."); err != nil {
					t.Fatalf("move: %v\n%s", err, out)
				}
			})
			return []want{{state: stateWait, ripe: fresh.Add(delay)}, {state: stateWait, ripe: fresh.Add(delay), reason: made}}
		}},
		{name: "a ripe note in a topic folder that a fresh chain drafted", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, fresh, func() {
				draftT(t, dev, "topic", "notes/incidents", "--description", "Read when you look into an incident.")
			})
			drafted := short(headCommit(repo))
			authored(t, old, func() {
				draftT(t, dev, "note", "slow-search", "--topic", "incidents", "--description", "Read when search is slow.")
			})
			return []want{{state: stateWait, ripe: fresh.Add(delay)}, {state: stateWait, ripe: fresh.Add(delay), reason: drafted}}
		}},
		{name: "a ripe note beside a fresh note of another topic folder", queue: func(t *testing.T, home, repo, dev string) []want {
			draftT(t, dev, "topic", "notes/incidents", "--description", "Read when you look into an incident.")
			draftT(t, dev, "topic", "notes/trials", "--description", "Read when you try a tool.")
			pushYes(t, home)
			authored(t, fresh, func() {
				draftT(t, dev, "note", "slow-search", "--topic", "incidents", "--description", "Read when search is slow.")
			})
			authored(t, old, func() {
				draftT(t, dev, "note", "new-editor", "--topic", "trials", "--description", "Read when you pick an editor.")
			})
			return []want{{state: stateWait, ripe: fresh.Add(delay)}, {state: statePush}}
		}},
		{name: "a ripe note after a held note with one document", queue: func(t *testing.T, home, repo, dev string) []want {
			authored(t, old, func() {
				draftT(t, dev, "note", "slow-search", "--description", "Read when search is slow.")
				draftT(t, dev, "skill", "a-skill", "--description", "Use when you test.")
			})
			// A hand commit changes the skill and a note together, so the note is held with the skill.
			writeT(t, repo, "dev/notes/slow-search.md", noteText("slow-search", "A finding."))
			writeT(t, repo, "dev/skills/a-skill/SKILL.md", "---\nname: a-skill\ndescription: \"Use when you test.\"\n---\n\nSteps.\n")
			commitAt(t, repo, "test: a skill and a note", old.Format(time.RFC3339))
			both := short(headCommit(repo))
			handNote(t, repo, "other", old)
			writeT(t, repo, "dev/notes/slow-search.md", noteText("slow-search", "A second finding."))
			commitAt(t, repo, "test: the note again", old.Format(time.RFC3339))
			return []want{{state: statePush}, {state: stateHeld}, {state: stateHeld}, {state: statePush}, {state: stateHeld, reason: both}}
		}},
		{name: "a fresh change of a note that a held chain changes", queue: func(t *testing.T, home, repo, dev string) []want {
			writeT(t, repo, "dev/notes/slow-search.md", noteText("slow-search", "A finding."))
			writeT(t, repo, "dev/skills/a-skill/SKILL.md", "---\nname: a-skill\ndescription: \"Use when you test.\"\n---\n\nSteps.\n")
			commitAt(t, repo, "test: a skill and a note", old.Format(time.RFC3339))
			both := short(headCommit(repo))
			writeT(t, repo, "dev/notes/slow-search.md", noteText("slow-search", "A second finding."))
			commitAt(t, repo, "test: the note again", fresh.Format(time.RFC3339))
			return []want{{state: stateHeld}, {state: stateHeld, reason: both}}
		}},
		{name: "a waiting change of a note that a later chain keeps waiting", queue: func(t *testing.T, home, repo, dev string) []want {
			handNote(t, repo, "slow-search", fresh)
			first := short(headCommit(repo))
			writeT(t, repo, "dev/notes/slow-search.md", noteText("slow-search", "A second finding."))
			commitAt(t, repo, "test: the note again", before(2*time.Hour).Format(time.RFC3339))
			return []want{{state: stateWait, ripe: fresh.Add(delay)}, {state: stateWait, ripe: fresh.Add(delay), reason: first}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			pushYes(t, home)
			wants := c.queue(t, home, repo, mkdirT(t, filepath.Join(home, "dev")))
			got, err := sortQueue(repo, delay, time.Now())
			if c.fails != "" {
				if err == nil || !strings.Contains(err.Error(), c.fails) {
					t.Fatalf("sortQueue = %v, want an error that holds %q", err, c.fails)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(wants) {
				t.Fatalf("sortQueue gave %d verdicts, want %d\n%s", len(got), len(wants), gitT(t, repo, "log", "--oneline", "origin/main..main"))
			}
			for i, w := range wants {
				g := got[i]
				if g.state != w.state || !strings.Contains(g.reason, w.reason) || (!w.ripe.IsZero() && !g.ripe.Equal(w.ripe)) {
					t.Errorf("chain %d, %q: verdict = %s, %q, %v; want %s, %q, %v", i, g.chain.target.subject, g.state, g.reason, g.ripe, w.state, w.reason, w.ripe)
				}
				if (g.state == stateWait) == g.ripe.IsZero() {
					t.Errorf("chain %d: state %s with the time %v", i, g.state, g.ripe)
				}
				if strings.Contains(g.reason, g.chain.target.subject) {
					t.Errorf("chain %d: the reason %q quotes a subject", i, g.reason)
				}
			}
		})
	}
}

func TestQueueHasMerge(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	if got, err := queueHasMerge(repo); err != nil || got {
		t.Errorf("queueHasMerge with no accepted history = %v, %v; want false", got, err)
	}
	pushYes(t, home)
	gitT(t, repo, "checkout", "-q", "-b", "side")
	handNote(t, repo, "side", before(time.Hour))
	gitT(t, repo, "checkout", "-q", "main")
	remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", "Read when you test a fact.")
	if got, err := queueHasMerge(repo); err != nil || got {
		t.Errorf("queueHasMerge of a queue with one memory = %v, %v; want false", got, err)
	}
	gitT(t, repo, "merge", "-q", "--no-ff", "--no-edit", "side")
	if got, err := queueHasMerge(repo); err != nil || !got {
		t.Errorf("queueHasMerge of a queue with a merge = %v, %v; want true", got, err)
	}
	// A merge that the remote holds is no merge of the queue.
	gitT(t, repo, "push", "-q", "origin", "main")
	gitT(t, repo, "update-ref", acceptedRef, "HEAD")
	if got, err := queueHasMerge(repo); err != nil || got {
		t.Errorf("queueHasMerge after the merge is accepted = %v, %v; want false", got, err)
	}
}

// fakeToken returns a text that guards.Secrets reads as a hosting token. No service issued it.
func fakeToken() string {
	return "ghp_" + strings.Repeat("a1B2", 9)
}

func TestSortChainsSecret(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	old := before(72 * time.Hour)
	cases := []struct {
		name, body, message string
		state, reason       string
	}{
		{name: "a note with a token", body: "The token is " + fakeToken() + ".", message: "test: a note", state: stateHeld, reason: "a likely secret in dev/notes/trial.md"},
		{name: "a clean note with a token in its commit message", body: "A note.", message: "test: a note\n\nThe token is " + fakeToken() + ".", state: stateHeld, reason: "a likely secret in the commit message"},
		{name: "a clean note with a token in its subject", body: "A note.", message: "test: a note for " + fakeToken(), state: stateHeld, reason: "a likely secret in the commit message"},
		{name: "a clean note", body: "A note.", message: "test: a note", state: statePush},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			pushYes(t, home)
			writeT(t, repo, "dev/notes/trial.md", noteText("trial", c.body))
			commitAt(t, repo, c.message, old.Format(time.RFC3339))
			got, err := sortQueue(repo, 24*time.Hour, time.Now())
			if err != nil || len(got) != 1 {
				t.Fatalf("sortQueue = %v, %v; want one verdict", got, err)
			}
			if got[0].state != c.state || got[0].reason != c.reason {
				t.Errorf("verdict = %s, %q; want %s, %q", got[0].state, got[0].reason, c.state, c.reason)
			}
			if strings.Contains(got[0].reason, fakeToken()) {
				t.Errorf("the reason %q quotes the secret", got[0].reason)
			}
		})
	}

	t.Run("a token in a file name", func(t *testing.T) {
		for _, body := range []string{"A note.", "The token is " + fakeToken() + "."} {
			home, repo, _ := newMemory(t)
			pushYes(t, home)
			writeT(t, repo, "dev/notes/"+fakeToken()+".md", noteText("trial", body))
			commitAt(t, repo, "test: a note", old.Format(time.RFC3339))
			got, err := sortQueue(repo, 24*time.Hour, time.Now())
			if err != nil || len(got) != 1 || got[0].state != stateHeld || got[0].reason != "a likely secret in dev/notes/[secret:hosting-token].md" {
				t.Errorf("sortQueue with the body %q = %+v, %v; want the chain held, with a placeholder in the path", body, got, err)
			}
		}
	})

	t.Run("a token in a fixup", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		handNote(t, repo, "trial", old)
		writeT(t, repo, "dev/notes/trial.md", noteText("trial", "The token is "+fakeToken()+"."))
		commitAt(t, repo, "fixup! test: the note trial", old.Format(time.RFC3339))
		got, err := sortQueue(repo, 24*time.Hour, time.Now())
		if err != nil || len(got) != 1 || got[0].state != stateHeld || got[0].reason != "a likely secret in dev/notes/trial.md" {
			t.Errorf("sortQueue = %+v, %v; want the chain held for its fixup", got, err)
		}
	})
}

// frozen returns HEAD, the refs and the changes of the working tree of a repo, to compare before and after.
func frozen(t *testing.T, repo string) string {
	t.Helper()
	return gitT(t, repo, "rev-parse", "HEAD") + gitT(t, repo, "for-each-ref", "--format=%(refname) %(objectname)") +
		gitT(t, repo, "status", "--porcelain", "--untracked-files=all")
}

// chainsT returns the chains of the review queue.
func chainsT(t *testing.T, repo string) []*chain {
	t.Helper()
	chains, _, err := chainsOf(repo)
	if err != nil {
		t.Fatal(err)
	}
	return chains
}

func TestBuildAside(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	const fact = "Read when you test a fact."
	old := before(72 * time.Hour)
	// build runs buildAside on some chains of the queue, and checks that the memory repo stays as it was.
	build := func(t *testing.T, repo string, chains []*chain) (clone, tip string, built []*chain, err error) {
		t.Helper()
		was := frozen(t, repo)
		tmp := t.TempDir()
		tip, built, err = buildAside(repo, tmp, chains)
		if got := frozen(t, repo); got != was {
			t.Errorf("buildAside changed the memory repo:\n%s\nwant\n%s", got, was)
		}
		return filepath.Join(tmp, "clone"), tip, built, err
	}
	// onBase checks that the built commits are n commits on the accepted commit, and returns their subjects.
	onBase := func(t *testing.T, repo, clone, tip string, n int) {
		t.Helper()
		base := strings.TrimSpace(gitT(t, repo, "rev-parse", acceptedRef))
		if got := strings.TrimSpace(gitT(t, clone, "rev-list", "--count", base+".."+tip)); got != fmt.Sprint(n) {
			t.Fatalf("the clone holds %s commits on the accepted commit, want %d", got, n)
		}
		if got := strings.TrimSpace(gitT(t, clone, "rev-parse", tip+"~"+fmt.Sprint(n))); got != base {
			t.Errorf("the built commits stand on %s, want the accepted commit %s", got, base)
		}
	}
	sameTree := func(t *testing.T, repo, clone, tip string) {
		t.Helper()
		if got, want := gitT(t, clone, "rev-parse", tip+"^{tree}"), gitT(t, repo, "rev-parse", "HEAD^{tree}"); got != want {
			t.Errorf("the tree of the built tip differs from the tree of the queue:\n%s", gitT(t, clone, "diff", "--stat", "main", tip))
		}
	}

	t.Run("two chains", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", fact)
		draftT(t, dev, "note", "slow-search", "--description", "Read when search is slow.")
		clone, tip, built, err := build(t, repo, chainsT(t, repo))
		if err != nil || len(built) != 2 {
			t.Fatalf("buildAside = %d chains, %v; want two", len(built), err)
		}
		onBase(t, repo, clone, tip, 2)
		sameTree(t, repo, clone, tip)
		for _, rev := range []string{"~1", ""} {
			got, want := gitT(t, clone, "show", "-s", "--format=%B", tip+rev), strings.TrimRight(gitT(t, repo, "show", "-s", "--format=%B", "HEAD"+rev), "\n")+"\n\nPushed: auto\n\n"
			if got != want {
				t.Errorf("the built message = %q, want %q", got, want)
			}
		}
		// The first built commit holds the memory with its indexes, and not the note.
		first := tip + "~1"
		if got := gitT(t, clone, "show", first+":dev/README.md"); !strings.Contains(got, "a-fact") || strings.Contains(got, "slow-search") {
			t.Errorf("the index of the first built commit =\n%s", got)
		}
	})

	t.Run("a chain with a fixup", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		authored(t, old, func() { remember(t, dev, "a-fact", fact) })
		reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		// With these settings, a commit with the default cleanup would lose the trailer as a comment.
		gitT(t, repo, "config", "commit.cleanup", "strip")
		gitT(t, repo, "config", "core.commentChar", "P")
		clone, tip, built, err := build(t, repo, chainsT(t, repo))
		if err != nil || len(built) != 1 {
			t.Fatalf("buildAside = %d chains, %v; want one", len(built), err)
		}
		onBase(t, repo, clone, tip, 1)
		sameTree(t, repo, clone, tip)
		if got, err := authorDate(clone, tip); err != nil || !got.Equal(old) {
			t.Errorf("the built commit has the author date %v, %v; want the date %v of its target", got, err, old)
		}
		got := gitT(t, clone, "show", "-s", "--format=%an <%ae>%n%B", tip)
		want := strings.TrimRight(gitT(t, repo, "show", "-s", "--format=%an <%ae>%n%B", "HEAD~1"), "\n") + "\n\nPushed: auto\n\n"
		if got != want {
			t.Errorf("the built commit = %q, want the author and the message of its target, with the trailer: %q", got, want)
		}
	})

	t.Run("a note in a new level", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		draftT(t, mkdirT(t, filepath.Join(home, "lab")), "note", "slow-search", "--description", "Read when search is slow.")
		clone, tip, built, err := build(t, repo, chainsT(t, repo))
		if err != nil || len(built) != 1 {
			t.Fatalf("buildAside = %d chains, %v; want one", len(built), err)
		}
		sameTree(t, repo, clone, tip)
		if got := gitT(t, clone, "show", tip+":README.md"); !strings.Contains(got, "[lab](lab/README.md)") {
			t.Errorf("the root index of the built commit =\n%s\nwant the level lab", got)
		}
	})

	t.Run("a chain whose change the accepted history holds", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		pushYes(t, home)
		draftT(t, mkdirT(t, filepath.Join(home, "dev")), "note", "slow-search", "--description", "Read when search is slow.")
		other := secondClone(t, remote)
		gitT(t, other, "fetch", "-q", repo, "main")
		gitT(t, other, "cherry-pick", "-x", "FETCH_HEAD")
		gitT(t, other, "push", "-q", "origin", "main")
		// The accepted commit moves without a rebase of the queue, which would drop the chain.
		gitT(t, repo, "fetch", "-q", "origin", "+refs/heads/main:"+acceptedRef)
		chains := chainsT(t, repo)
		if len(chains) != 1 {
			t.Fatalf("the queue holds %d chains, want one", len(chains))
		}
		_, tip, built, err := build(t, repo, chains)
		if err != nil || tip != "" || len(built) != 0 {
			t.Errorf("buildAside = %q, %d chains, %v; want no commit", tip, len(built), err)
		}
	})

	t.Run("a note beside a note of the same level that is not built", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		draftT(t, dev, "note", "slow-search", "--description", "Read when search is slow.")
		draftT(t, dev, "note", "new-editor", "--description", "Read when you pick an editor.")
		chains := chainsT(t, repo)
		clone, tip, built, err := build(t, repo, chains[1:])
		if err != nil || len(built) != 1 {
			t.Fatalf("buildAside = %d chains, %v; want one", len(built), err)
		}
		onBase(t, repo, clone, tip, 1)
		if got := gitT(t, clone, "show", tip+":dev/README.md"); !strings.Contains(got, "new-editor") || strings.Contains(got, "slow-search") {
			t.Errorf("the index of the built commit =\n%s\nwant only the built note", got)
		}
		if got := gitT(t, clone, "show", tip+":README.md"); !strings.Contains(got, "[dev](dev/README.md)") {
			t.Errorf("the root index of the built commit =\n%s\nwant the level dev", got)
		}
		if got := gitT(t, clone, "show", "--format=", "--name-only", tip); strings.Contains(got, "slow-search") {
			t.Errorf("the built commit changes %q, want it without the other note", got)
		}
	})

	t.Run("a note beside a memory of a new level that is not built", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		remember(t, mkdirT(t, filepath.Join(home, "lab")), "a-fact", fact)
		draftT(t, mkdirT(t, filepath.Join(home, "dev")), "note", "new-editor", "--description", "Read when you pick an editor.")
		// A folder that no commit holds is an empty level, which the index lists in the memory repo too.
		mkdirT(t, filepath.Join(repo, "shop"))
		clone, tip, built, err := build(t, repo, chainsT(t, repo)[1:])
		if err != nil || len(built) != 1 {
			t.Fatalf("buildAside = %d chains, %v; want one", len(built), err)
		}
		got := gitT(t, clone, "show", tip+":README.md")
		if strings.Contains(got, "lab") || !strings.Contains(got, "[dev](dev/README.md)") || !strings.Contains(got, "[shop](shop/README.md)") {
			t.Errorf("the root index of the built commit =\n%s\nwant dev and shop, without the level of the other chain", got)
		}
	})

	t.Run("two chains that conflict in a document", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		handNote(t, repo, "trial", old)
		writeT(t, repo, "dev/notes/trial.md", noteText("trial", "A second finding."))
		commitAt(t, repo, "test: the note again", old.Format(time.RFC3339))
		chains := chainsT(t, repo)
		_, tip, built, err := build(t, repo, chains[1:])
		if err == nil || !strings.Contains(err.Error(), short(chains[1].target.sha)) || !strings.Contains(err.Error(), "dev/notes/trial.md") {
			t.Errorf("buildAside = %q, %d chains, %v; want an error that names the chain and the document", tip, len(built), err)
		}
		if strings.Contains(fmt.Sprint(err), "test: the note again") {
			t.Errorf("the error %v quotes a subject", err)
		}
	})

	t.Run("no accepted history", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		if _, _, err := buildAside(repo, t.TempDir(), chainsT(t, repo)); err == nil {
			t.Error("buildAside with no accepted history: no error")
		}
	})
}

// autoOn turns the automatic push on, with a delay of a day.
func autoOn(t *testing.T, repo string) {
	t.Helper()
	gitT(t, repo, "config", "memcore.autopush", "24h")
}

// pushAutoT runs push --auto with a confirmer that fails the test, because the automatic push never asks.
func pushAutoT(t *testing.T, home string) (string, error) {
	t.Helper()
	asks := func(prompt string) (bool, error) {
		t.Errorf("the automatic push asked %q", prompt)
		return false, nil
	}
	return runWith(t, Env{Dir: home, Confirm: asks}, "push", "--auto")
}

// queueT returns the subjects of the review queue, newest first.
func queueT(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(gitT(t, repo, "log", "--format=%s", acceptedRef+"..main"))
}

// afterPush makes the next pushes of memcore run f after the remote took them, and return its error.
func afterPush(t *testing.T, f func() error) {
	t.Helper()
	prev := gitPush
	gitPush = func(repo string, held *os.File, args ...string) error {
		if err := prev(repo, held, args...); err != nil {
			return err
		}
		return f()
	}
	t.Cleanup(func() { gitPush = prev })
}

// noRecord checks that no record and no ref of an automatic push stay.
func noRecord(t *testing.T, repo string) {
	t.Helper()
	if _, waits, err := readPending(repo); err != nil || waits {
		t.Errorf("the record of the automatic push stays: %v, %v", waits, err)
	}
	if _, ok := revParse(repo, autoRef); ok {
		t.Error("the ref of the automatic push stays")
	}
}

// hasRecord checks that the record and the ref of an automatic push stay.
func hasRecord(t *testing.T, repo string) {
	t.Helper()
	if _, waits, err := readPending(repo); err != nil || !waits {
		t.Errorf("the record of the automatic push = %v, %v; want it to stay", waits, err)
	}
	if _, ok := revParse(repo, autoRef); !ok {
		t.Error("the ref of the automatic push is gone")
	}
}

func TestRefused(t *testing.T) {
	cases := []struct {
		name, text string
		want       bool
	}{
		{"a hook that declines", "remote: no\nTo /r\n ! [remote rejected] 0123456 -> main (pre-receive hook declined)\nerror: failed to push some refs to '/r'", true},
		{"no fast-forward", "To /r\n ! [rejected]        0123456 -> main (fetch first)\nerror: failed to push some refs to '/r'", true},
		{"a remote that reports no status", "To /r\n ! [remote failure]  0123456 -> main (remote failed to report status)\nerror: failed to push some refs to '/r'", false},
		{"a tag that the remote rejects", "To /r\n ! [remote rejected] v1 -> v1 (hook declined)\nerror: failed to push some refs to '/r'", false},
		{"a connection that breaks", "fatal: the remote end hung up unexpectedly", false},
		{"a hook that prints the words", "remote: ! [remote rejected] by nobody\nfatal: the remote end hung up unexpectedly", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := refused(errors.New("git push: exit status 1: " + c.text)); got != c.want {
				t.Errorf("refused = %v, want %v", got, c.want)
			}
		})
	}
	if refused(nil) {
		t.Error("refused(nil) = true")
	}
}

func TestPushArgs(t *testing.T) {
	args := pushArgs("0123456789abcdef")
	if len(args) == 0 || args[0] != "push" || !slices.Contains(args, "0123456789abcdef:refs/heads/main") {
		t.Fatalf("pushArgs = %q, want a push of the tip to main", args)
	}
	for _, a := range args {
		if a == "-f" || strings.HasPrefix(a, "--force") || strings.HasPrefix(a, "+") || a == "--mirror" || a == "--delete" || a == "-d" {
			t.Errorf("pushArgs = %q, and %q can rewrite the remote", args, a)
		}
	}
}

func TestPushAuto(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	const fact = "Read when you test a fact."
	old, fresh := before(72*time.Hour), before(time.Hour)
	// start gives a memory repo with a pushed init and the automatic push on.
	start := func(t *testing.T) (home, repo, remote, dev string) {
		t.Helper()
		home, repo, remote = newMemory(t)
		pushYes(t, home)
		autoOn(t, repo)
		return home, repo, remote, mkdirT(t, filepath.Join(home, "dev"))
	}
	ripeNote := func(t *testing.T, dev, name string) {
		t.Helper()
		authored(t, old, func() { draftT(t, dev, "note", name, "--description", "Read when you test "+name+".") })
	}
	heldSkill := func(t *testing.T, dev string) {
		t.Helper()
		authored(t, old, func() { draftT(t, dev, "skill", "a-skill", "--description", "Use when you test.") })
	}
	remoteTip := func(t *testing.T, remote string) string {
		t.Helper()
		return strings.TrimSpace(gitT(t, remote, "rev-parse", "main"))
	}
	// unmoved checks that a run left the remote and HEAD as they were.
	unmoved := func(t *testing.T, repo, remote, head, tip string) {
		t.Helper()
		if got := headCommit(repo); got != head {
			t.Errorf("HEAD moved from %s to %s", head, got)
		}
		if got := remoteTip(t, remote); got != tip {
			t.Errorf("the remote moved from %s to %s", tip, got)
		}
	}

	t.Run("the setting off", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		pushYes(t, home)
		authored(t, old, func() { remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", fact) })
		head, tip := headCommit(repo), remoteTip(t, remote)
		out, err := pushAutoT(t, home)
		if err != nil || out != "The automatic push is off.\n" {
			t.Errorf("push --auto = %q, %v", out, err)
		}
		unmoved(t, repo, remote, head, tip)
	})

	t.Run("nothing ripe, with a stale index", func(t *testing.T) {
		home, repo, remote, _ := start(t)
		writeT(t, repo, "dev/memories/by-hand.md", memoryText("by-hand", "Read when you edit by hand.", "A fact."))
		commitAt(t, repo, "test: a memory by hand", fresh.Format(time.RFC3339))
		other := secondClone(t, remote)
		writeT(t, other, "work/notes/elsewhere.md", noteText("elsewhere", "From another machine."))
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: a commit from another machine")
		gitT(t, other, "push", "-q", "origin", "main")
		head, accepted := headCommit(repo), acceptedCommit(repo)
		out, err := pushAutoT(t, home)
		if err != nil || out != "Nothing is ripe.\n" {
			t.Errorf("push --auto = %q, %v", out, err)
		}
		if headCommit(repo) != head || acceptedCommit(repo) != accepted {
			t.Error("a run with nothing ripe changed the queue or fetched")
		}
		if stale, _ := staleIndexes(repo); len(stale) == 0 {
			t.Error("a run with nothing ripe regenerated the stale index")
		}
	})

	t.Run("two ripe memories", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			remember(t, dev, "deploys", "Read before you deploy on Fridays.")
		})
		tree := gitT(t, repo, "rev-parse", "HEAD^{tree}")
		out, err := pushAutoT(t, home)
		if err != nil || out != "Pushed 2 commits automatically.\n" {
			t.Fatalf("push --auto = %q, %v", out, err)
		}
		if got := subjects(t, remote); got != "remember: dev/memories/deploys.md\nremember: dev/memories/a-fact.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q", got)
		}
		if got := queueT(t, repo); got != "" {
			t.Errorf("the queue = %q, want it empty", got)
		}
		if head := headCommit(repo); head != acceptedCommit(repo) || head != remoteTip(t, remote) {
			t.Errorf("HEAD = %s, want the accepted commit %s and the remote tip %s", head, acceptedCommit(repo), remoteTip(t, remote))
		}
		if got := gitT(t, repo, "rev-parse", "HEAD^{tree}"); got != tree {
			t.Error("the pushed tree differs from the tree of the queue")
		}
		if out := gitT(t, repo, "status", "--porcelain"); out != "" {
			t.Errorf("the working tree = %q, want it clean", out)
		}
		noRecord(t, repo)
		e := last(t, repo)
		if e.Command != "push" || e.Outcome != "ok" || e.By != "auto" || len(e.Pushed) != 2 || e.Pushed[1] != remoteTip(t, remote) {
			t.Errorf("logged push = %+v, want an automatic push of two commits", e)
		}
	})

	t.Run("two commits with one key, a ripe note and a held skill", func(t *testing.T) {
		home, repo, remote, _ := start(t)
		writeT(t, repo, "dev/notes/trial.md", noteText("trial", "A note."))
		commitAt(t, repo, "test: by hand", old.Format(time.RFC3339))
		writeT(t, repo, "dev/skills/a-skill/SKILL.md", "---\nname: a-skill\ndescription: \"Use when you test.\"\n---\n\nSteps.\n")
		commitAt(t, repo, "test: by hand", old.Format(time.RFC3339))
		if keyT(t, repo, "HEAD") != keyT(t, repo, "HEAD~1") {
			t.Fatal("the two commits have different keys")
		}
		if out, err := pushAutoT(t, home); err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("push --auto = %q, %v", out, err)
		}
		if _, err := store.Git(remote, "cat-file", "-e", "main:dev/skills/a-skill/SKILL.md"); err == nil {
			t.Error("the remote holds the skill")
		}
		if _, err := store.Git(remote, "cat-file", "-e", "main:dev/notes/trial.md"); err != nil {
			t.Errorf("the remote lacks the note: %v", err)
		}
		if got := gitT(t, repo, "log", "--format=", "--name-only", acceptedRef+"..main"); !strings.Contains(got, "dev/skills/a-skill/SKILL.md") || strings.Contains(got, "trial.md") {
			t.Errorf("the queue changes %q, want only the skill", got)
		}
		noRecord(t, repo)
	})

	t.Run("a ripe note behind a held skill", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		heldSkill(t, dev)
		ripeNote(t, dev, "slow-search")
		if out, err := pushAutoT(t, home); err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("push --auto = %q, %v", out, err)
		}
		if got := subjects(t, remote); got != "draft: dev/notes/slow-search.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q, want the note", got)
		}
		if got := queueT(t, repo); got != "draft: dev/skills/a-skill/SKILL.md" {
			t.Errorf("the queue = %q, want only the skill", got)
		}
		if got := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD~1")); got != remoteTip(t, remote) || got != acceptedCommit(repo) {
			t.Errorf("the skill stands on %s, want the new accepted commit %s", got, remoteTip(t, remote))
		}
		if got, err := authorDate(repo, "HEAD"); err != nil || !got.Equal(old) {
			t.Errorf("the skill has the author date %v, %v; want %v", got, err, old)
		}
		if stale, err := staleIndexes(repo); err != nil || len(stale) > 0 {
			t.Errorf("stale indexes = %q, %v; want none", stale, err)
		}
		if got := readT(t, filepath.Join(repo, "dev", "README.md")); !strings.Contains(got, "a-skill") || !strings.Contains(got, "slow-search") {
			t.Errorf("the index of dev =\n%s\nwant the skill and the note", got)
		}
		if got := gitT(t, remote, "show", "main:dev/README.md"); strings.Contains(got, "a-skill") {
			t.Errorf("the remote index of dev =\n%s\nwant it without the skill", got)
		}
		noRecord(t, repo)
	})

	t.Run("a ripe note behind a held script whose name a foreign file holds", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() { draftT(t, dev, "script", "a-tool", "--description", "Run when you test.") })
		writeT(t, home, ".local/bin/a-tool", "#!/bin/sh\n")
		ripeNote(t, dev, "slow-search")
		if out, err := pushAutoT(t, home); err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("push --auto = %q, %v", out, err)
		}
		if got := subjects(t, remote); got != "draft: dev/notes/slow-search.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q, want the note", got)
		}
		if got := queueT(t, repo); got != "draft: dev/bin/a-tool" {
			t.Errorf("the queue = %q, want only the script", got)
		}
	})

	t.Run("a ripe chain with a fixup", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		})
		if out, err := pushAutoT(t, home); err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("push --auto = %q, %v", out, err)
		}
		if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q, want the remember folded", got)
		}
		if got := gitT(t, remote, "show", "main:dev/memories/a-fact.md"); !strings.Contains(got, "A sharper fact.") {
			t.Errorf("the remote memory =\n%s\nwant the revised text", got)
		}
		if got, err := authorDate(remote, "main"); err != nil || !got.Equal(old) {
			t.Errorf("the pushed commit has the author date %v, %v; want %v", got, err, old)
		}
		if got := queueT(t, repo); got != "" {
			t.Errorf("the queue = %q, want it empty", got)
		}
		noRecord(t, repo)
	})

	t.Run("a fresh chain with a fixup", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() { remember(t, dev, "a-fact", fact) })
		authored(t, fresh, func() { reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.") })
		head, tip := headCommit(repo), remoteTip(t, remote)
		if out, err := pushAutoT(t, home); err != nil || out != "Nothing is ripe.\n" {
			t.Errorf("push --auto = %q, %v", out, err)
		}
		unmoved(t, repo, remote, head, tip)
		if got := queueT(t, repo); got != "fixup! remember: dev/memories/a-fact.md\nremember: dev/memories/a-fact.md" {
			t.Errorf("the queue = %q, want the two commits", got)
		}
		first, _ := authorDate(repo, "HEAD~1")
		second, _ := authorDate(repo, "HEAD")
		if !first.Equal(old) || !second.Equal(fresh) {
			t.Errorf("the author dates = %v and %v, want %v and %v", first, second, old, fresh)
		}
	})

	t.Run("a ripe chain whose change the fetched history holds", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		other := secondClone(t, remote)
		gitT(t, other, "fetch", "-q", repo, "main")
		gitT(t, other, "cherry-pick", "-x", "FETCH_HEAD")
		gitT(t, other, "push", "-q", "origin", "main")
		tip := remoteTip(t, remote)
		if out, err := pushAutoT(t, home); err != nil || out != "Nothing is ripe.\n" {
			t.Errorf("push --auto = %q, %v", out, err)
		}
		if got := remoteTip(t, remote); got != tip {
			t.Errorf("the remote moved from %s to %s", tip, got)
		}
		if head := headCommit(repo); head != tip || queueT(t, repo) != "" {
			t.Errorf("HEAD = %s with the queue %q, want the fetched tip and no queue", head, queueT(t, repo))
		}
		noRecord(t, repo)
	})

	t.Run("a ripe remember and its ripe forget", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			if out, err := run(t, dev, "", "forget", "a-fact", "--reason", "A mistake."); err != nil {
				t.Fatalf("forget: %v\n%s", err, out)
			}
		})
		head, tip := headCommit(repo), remoteTip(t, remote)
		if out, err := pushAutoT(t, home); err != nil || out != "Nothing is ripe.\n" {
			t.Errorf("push --auto = %q, %v", out, err)
		}
		unmoved(t, repo, remote, head, tip)
	})

	t.Run("a merge in the queue", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		gitT(t, repo, "checkout", "-q", "-b", "side")
		handNote(t, repo, "side", old)
		gitT(t, repo, "checkout", "-q", "main")
		ripeNote(t, dev, "slow-search")
		authored(t, old, func() { gitT(t, repo, "merge", "-q", "--no-ff", "--no-edit", "side") })
		head, tip := headCommit(repo), remoteTip(t, remote)
		if out, err := pushAutoT(t, home); err != nil || out != "The queue holds a merge commit, so the person pushes.\n" {
			t.Errorf("push --auto = %q, %v", out, err)
		}
		unmoved(t, repo, remote, head, tip)
	})

	t.Run("a fetched change of the same document", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		draftT(t, dev, "note", "trial", "--description", "Read when you test trial.")
		pushYes(t, home)
		other := secondClone(t, remote)
		writeT(t, other, "dev/notes/trial.md", noteText("trial", "From another machine."))
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: the note from another machine")
		gitT(t, other, "push", "-q", "origin", "main")
		authored(t, old, func() { reviseT(t, dev, noteText("trial", "From this machine."), "dev/notes/trial.md", "A finding.") })
		head, tip := headCommit(repo), remoteTip(t, remote)
		out, err := pushAutoT(t, home)
		if err == nil || !strings.Contains(err.Error(), "change the same documents") {
			t.Errorf("push --auto = %q, %v; want the conflict of the fetch", out, err)
		}
		unmoved(t, repo, remote, head, tip)
		noRecord(t, repo)
	})

	t.Run("a remote that rejects the push", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		})
		rejectPushes(t, remote)
		head, tip := headCommit(repo), remoteTip(t, remote)
		out, err := pushAutoT(t, home)
		if err == nil || !strings.Contains(err.Error(), "push to origin") {
			t.Fatalf("push --auto = %q, %v; want the rejection of the remote", out, err)
		}
		unmoved(t, repo, remote, head, tip)
		// The remote said no, so it never gets this built tip, and the record goes at once.
		noRecord(t, repo)
		if e := last(t, repo); e.Outcome != "error" || len(e.Pushed) != 0 {
			t.Errorf("logged push = %+v, want an error with no pushed commit", e)
		}
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		noRecord(t, repo)
		unmoved(t, repo, remote, head, tip)
		if err := os.Remove(filepath.Join(remote, "hooks", "pre-receive")); err != nil {
			t.Fatal(err)
		}
		pushYes(t, home)
		if got := gitT(t, remote, "log", "--format=%B", "main"); strings.Contains(got, "Pushed: auto") {
			t.Errorf("a push of the person sent the trailer of an automatic push:\n%s", got)
		}
		if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q", got)
		}
	})

	t.Run("a remote that rejects the push, with settings of git that change its output", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		gitT(t, repo, "config", "color.transport", "always")
		gitT(t, repo, "config", "push.followTags", "true")
		rejectPushes(t, remote)
		head, tip := headCommit(repo), remoteTip(t, remote)
		if out, err := pushAutoT(t, home); err == nil || !strings.Contains(err.Error(), "push to origin") {
			t.Fatalf("push --auto = %q, %v; want the rejection of the remote", out, err)
		}
		noRecord(t, repo)
		unmoved(t, repo, remote, head, tip)
	})

	t.Run("a remote that rejects the push after another clone advanced it", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		heldSkill(t, dev)
		other := secondClone(t, remote)
		writeT(t, other, "work/notes/elsewhere.md", noteText("elsewhere", "From another machine."))
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: a commit from another machine")
		gitT(t, other, "push", "-q", "origin", "main")
		rejectPushes(t, remote)
		tip := remoteTip(t, remote)
		out, err := pushAutoT(t, home)
		if err == nil || !strings.Contains(err.Error(), "push to origin") {
			t.Fatalf("push --auto = %q, %v; want the rejection of the remote", out, err)
		}
		// The queue is as the fetch left it: the same two commits, on the fetched tip.
		if got := queueT(t, repo); got != "draft: dev/skills/a-skill/SKILL.md\ndraft: dev/notes/slow-search.md" {
			t.Errorf("the queue = %q, want the note and the skill", got)
		}
		if got := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD~2")); got != tip || acceptedCommit(repo) != tip || remoteTip(t, remote) != tip {
			t.Errorf("the queue stands on %s, want the fetched tip %s", got, tip)
		}
		if got := gitT(t, repo, "log", "--format=%B", acceptedRef+"..main"); strings.Contains(got, "Pushed: auto") {
			t.Errorf("a commit of the queue has the trailer:\n%s", got)
		}
		noRecord(t, repo)
		head := headCommit(repo)
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		noRecord(t, repo)
		unmoved(t, repo, remote, head, tip)
	})

	t.Run("a push that the remote accepts and that reports a failure", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		heldSkill(t, dev)
		head := headCommit(repo)
		afterPush(t, func() error { return errors.New("the connection broke") })
		out, err := pushAutoT(t, home)
		if err == nil || !strings.Contains(err.Error(), "the connection broke") {
			t.Fatalf("push --auto = %q, %v; want the failure of the push", out, err)
		}
		if got := subjects(t, remote); got != "draft: dev/notes/slow-search.md\ninit: create the memory repo" {
			t.Fatalf("remote history = %q, want the note", got)
		}
		if e := last(t, repo); e.Outcome != "error" || len(e.Pushed) != 0 {
			t.Errorf("logged push = %+v, want an error with no pushed commit", e)
		}
		if headCommit(repo) != head {
			t.Error("a push that reports a failure changed the queue")
		}
		hasRecord(t, repo)
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := queueT(t, repo); got != "draft: dev/skills/a-skill/SKILL.md" {
			t.Errorf("the queue after the fetch = %q, want only the skill", got)
		}
		if got := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD~1")); got != remoteTip(t, remote) {
			t.Errorf("the skill stands on %s, want the remote tip", got)
		}
		noRecord(t, repo)
	})

	t.Run("a move of the accepted checkout that fails after the push", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		// A file in the place of ~/.local/bin fails syncAccepted.
		afterPush(t, func() error {
			writeT(t, home, ".local/bin", "no folder\n")
			return nil
		})
		out, err := pushAutoT(t, home)
		if err == nil || !strings.Contains(err.Error(), "the remote has the automatic push") {
			t.Fatalf("push --auto = %q, %v; want the failure after the push", out, err)
		}
		if e := last(t, repo); e.Outcome != "error" || e.By != "auto" || len(e.Pushed) != 1 || e.Pushed[0] != remoteTip(t, remote) {
			t.Errorf("logged push = %+v, want the pushed commit", e)
		}
		hasRecord(t, repo)
		if err := os.Remove(filepath.Join(home, ".local", "bin")); err != nil {
			t.Fatal(err)
		}
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := queueT(t, repo); got != "" || headCommit(repo) != remoteTip(t, remote) {
			t.Errorf("the queue after the fetch = %q, want it empty on the remote tip", got)
		}
		noRecord(t, repo)
	})

	t.Run("a record that names a commit that the queue lacks", func(t *testing.T) {
		home, repo, _, dev := start(t)
		// A chain with a fixup builds into one commit, which no commit of the queue equals.
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		})
		afterPush(t, func() error { return errors.New("the connection broke") })
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		gitT(t, repo, "commit", "-q", "--amend", "-m", "fixup! remember: dev/memories/a-fact.md\n\nAmended by hand.")
		head := headCommit(repo)
		err := Fetch(repo)
		if err == nil || !strings.Contains(err.Error(), "by hand") || !strings.Contains(err.Error(), "autopush.pending") {
			t.Errorf("Fetch = %v, want an error that tells the person to remove the commits by hand", err)
		}
		if headCommit(repo) != head {
			t.Error("finishDrop changed a queue that lacks a commit of the record")
		}
		hasRecord(t, repo)
	})

	t.Run("a hand edit after the push and before the removal", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		heldSkill(t, dev)
		head := headCommit(repo)
		afterPush(t, func() error {
			writeT(t, repo, "dev/notes/by-hand.md", noteText("by-hand", "An edit by hand."))
			return nil
		})
		out, err := pushAutoT(t, home)
		if err == nil || !strings.Contains(err.Error(), "the next fetch removes its commits") {
			t.Fatalf("push --auto = %q, %v; want the report that the next fetch finishes the removal", out, err)
		}
		if got := subjects(t, remote); got != "draft: dev/notes/slow-search.md\ninit: create the memory repo" {
			t.Fatalf("remote history = %q, want the note", got)
		}
		if headCommit(repo) != head {
			t.Error("the removal moved HEAD over a hand edit")
		}
		if got := readT(t, filepath.Join(repo, "dev", "notes", "by-hand.md")); !strings.Contains(got, "An edit by hand.") {
			t.Errorf("the hand edit = %q", got)
		}
		hasRecord(t, repo)
		if e := last(t, repo); e.By != "auto" || len(e.Pushed) != 1 {
			t.Errorf("logged push = %+v, want the pushed commit", e)
		}
		commitAt(t, repo, "test: an edit by hand", fresh.Format(time.RFC3339))
		if out, err := run(t, home, "", "inbox"); err != nil {
			t.Fatalf("inbox: %v\n%s", err, out)
		}
		if got := queueT(t, repo); got != "test: an edit by hand\ndraft: dev/skills/a-skill/SKILL.md" {
			t.Errorf("the queue after inbox = %q, want it without the pushed note", got)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "notes", "slow-search.md")); err != nil {
			t.Errorf("the pushed note is gone from the working tree: %v", err)
		}
		noRecord(t, repo)
	})

	// failPush makes the next pushes of memcore fail before the remote gets them, as a connection that breaks.
	failPush := func(t *testing.T) (restore func()) {
		t.Helper()
		prev := gitPush
		gitPush = func(string, *os.File, ...string) error { return errors.New("the connection broke") }
		t.Cleanup(func() { gitPush = prev })
		return func() { gitPush = prev }
	}
	// factAndSkill gives a ripe chain of a memory with a fixup, and a held skill.
	factAndSkill := func(t *testing.T, dev string) {
		t.Helper()
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		})
		heldSkill(t, dev)
	}
	const skillOnly = "draft: dev/skills/a-skill/SKILL.md"
	// plainSubjects checks that no commit of the remote has the subject of a fixup.
	plainSubjects := func(t *testing.T, remote string) {
		t.Helper()
		if got := subjects(t, remote); strings.Contains(got, "fixup!") {
			t.Errorf("the remote holds the subject of a fixup:\n%s", got)
		}
	}

	t.Run("a record whose built tip the remote can still get", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		head, tip := headCommit(repo), remoteTip(t, remote)
		// The built tip is a commit that only this machine holds.
		built := commitOn(t, repo, tip, "draft: dev/notes/slow-search.md", []treeFile{{"dev/notes/slow-search.md", "100644", "A note.\n"}})
		gitT(t, repo, "update-ref", autoRef, built)
		if err := writePending(repo, pending{Tip: built, Commits: []string{head}}); err != nil {
			t.Fatal(err)
		}
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		hasRecord(t, repo)
		unmoved(t, repo, remote, head, tip)
		_ = home
	})

	t.Run("a record whose built tip can no longer land", func(t *testing.T) {
		_, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		head, tip := headCommit(repo), remoteTip(t, remote)
		built := commitOn(t, repo, tip, "draft: dev/notes/slow-search.md", []treeFile{{"dev/notes/slow-search.md", "100644", "A note.\n"}})
		gitT(t, repo, "update-ref", autoRef, built)
		if err := writePending(repo, pending{Tip: built, Commits: []string{head}}); err != nil {
			t.Fatal(err)
		}
		// Another machine moves the remote on. The remote now rejects the built tip, because it is no fast-forward.
		other := secondClone(t, remote)
		writeT(t, other, "work/notes/elsewhere.md", noteText("elsewhere", "From another machine."))
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: a commit from another machine")
		gitT(t, other, "push", "-q", "origin", "main")
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		noRecord(t, repo)
		if got := queueT(t, repo); got != "draft: dev/notes/slow-search.md" || strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD~1")) != remoteTip(t, remote) {
			t.Errorf("the queue = %q, want the note on the remote tip", got)
		}
	})

	t.Run("a push that the remote takes after its client died, with a fetch in between", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		factAndSkill(t, dev)
		head := headCommit(repo)
		failPush(t)
		if out, err := pushAutoT(t, home); err == nil || !strings.Contains(err.Error(), "the connection broke") {
			t.Fatalf("push --auto = %q, %v; want the failure of the push", out, err)
		}
		rec, _, _ := readPending(repo)
		// The fetch in between finds no built tip on the remote. The record stays, and so does the queue.
		if err := Fetch(repo); err != nil {
			t.Fatalf("the fetch in between: %v", err)
		}
		hasRecord(t, repo)
		if headCommit(repo) != head {
			t.Error("the fetch in between changed the queue")
		}
		// The remote takes the built tip late.
		gitT(t, repo, "push", "-q", "origin", rec.Tip+":refs/heads/main")
		if err := Fetch(repo); err != nil {
			t.Fatalf("the fetch after the remote took the tip: %v", err)
		}
		if got := queueT(t, repo); got != skillOnly || strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD~1")) != remoteTip(t, remote) {
			t.Errorf("the queue = %q, want only the skill, on the remote tip", got)
		}
		noRecord(t, repo)
	})

	t.Run("the next automatic push sends the same built tip", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		factAndSkill(t, dev)
		restore := failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		rec, _, _ := readPending(repo)
		restore()
		var sent []string
		prev := gitPush
		t.Cleanup(func() { gitPush = prev })
		gitPush = func(repo string, held *os.File, args ...string) error {
			sent = args
			if held == nil {
				t.Error("the second push gave its git process no lock to hold")
			}
			return prev(repo, held, args...)
		}
		out, err := pushAutoT(t, home)
		if err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("the second push --auto = %q, %v", out, err)
		}
		if !slices.Contains(sent, rec.Tip+":refs/heads/main") || remoteTip(t, remote) != rec.Tip {
			t.Errorf("the second push sent %q, want the built tip %s of the first push", sent, rec.Tip)
		}
		if got := queueT(t, repo); got != skillOnly {
			t.Errorf("the queue = %q, want only the skill", got)
		}
		if e := last(t, repo); e.By != "auto" || len(e.Pushed) != 1 || e.Pushed[0] != rec.Tip {
			t.Errorf("logged push = %+v, want the built tip by auto", e)
		}
		noRecord(t, repo)
	})

	t.Run("a push of the person while a record stays", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		factAndSkill(t, dev)
		restore := failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		restore()
		head, tip := headCommit(repo), remoteTip(t, remote)
		// A push of the person would fold the recorded commits, so it refuses until the automatic push ends.
		var asked string
		_, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push")
		if err == nil || !strings.Contains(err.Error(), "memcore push --auto") || asked != "" {
			t.Fatalf("push = %v, asked %q; want a refusal that names push --auto", err, asked)
		}
		hasRecord(t, repo)
		unmoved(t, repo, remote, head, tip)
		if out, err := pushAutoT(t, home); err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("the second push --auto = %q, %v", out, err)
		}
		pushYes(t, home)
		if got := queueT(t, repo); got != "" {
			t.Errorf("the queue = %q, want it empty", got)
		}
		noRecord(t, repo)
	})

	t.Run("a second push that a hook rejects keeps the record of an open push", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		factAndSkill(t, dev)
		restore := failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		restore()
		hasRecord(t, repo)
		rejectPushes(t, remote)
		head, tip := headCommit(repo), remoteTip(t, remote)
		out, err := pushAutoT(t, home)
		if err == nil || !strings.Contains(err.Error(), "push to origin") {
			t.Fatalf("the second push --auto = %q, %v; want the rejection of the remote", out, err)
		}
		// The first push is still open: the remote can take it late. So the no of the second push ends nothing.
		hasRecord(t, repo)
		unmoved(t, repo, remote, head, tip)
		if !strings.Contains(err.Error(), pendingPath(repo)) {
			t.Errorf("the error = %v, want one that names the file of the record", err)
		}
		// The remote takes the first push late, and the fetch removes the pushed chain.
		if err := os.Remove(filepath.Join(remote, "hooks", "pre-receive")); err != nil {
			t.Fatal(err)
		}
		rec, _, _ := readPending(repo)
		gitT(t, repo, "push", "-q", "origin", rec.Tip+":refs/heads/main")
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := queueT(t, repo); got != skillOnly {
			t.Errorf("the queue = %q, want only the skill", got)
		}
		noRecord(t, repo)
	})

	t.Run("a second push that fails keeps the record", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		factAndSkill(t, dev)
		head, tip := headCommit(repo), remoteTip(t, remote)
		failPush(t)
		for i := 0; i < 2; i++ {
			if out, err := pushAutoT(t, home); err == nil || !strings.Contains(err.Error(), "the connection broke") {
				t.Fatalf("push --auto %d = %q, %v; want the failure of the push", i, out, err)
			}
			hasRecord(t, repo)
			unmoved(t, repo, remote, head, tip)
		}
	})

	t.Run("a fresh fixup after a push that failed", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		factAndSkill(t, dev)
		restore := failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		rec, _, _ := readPending(repo)
		restore()
		// The remote can still get the built tip. So the second push sends that tip, and the correction stays.
		authored(t, fresh, func() {
			reviseT(t, dev, memoryText("a-fact", fact, "The sharpest fact."), "a-fact", "Sharpest.")
		})
		out, err := pushAutoT(t, home)
		if err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("the second push --auto = %q, %v", out, err)
		}
		if got := remoteTip(t, remote); got != rec.Tip {
			t.Errorf("the remote tip = %s, want the built tip %s of the first push", got, rec.Tip)
		}
		// The record sent the last change of the memory, so the correction is a commit of its own, and no fixup.
		if got := queueT(t, repo); got != "revise: dev/memories/a-fact.md\n"+skillOnly {
			t.Errorf("the queue = %q, want the correction and the skill", got)
		}
		if got := readT(t, filepath.Join(repo, "dev/memories/a-fact.md")); !strings.Contains(got, "The sharpest fact.") {
			t.Errorf("the memory lost its correction:\n%s", got)
		}
		noRecord(t, repo)
		pushYes(t, home)
		plainSubjects(t, remote)
		if got := gitT(t, remote, "show", "main:dev/memories/a-fact.md"); !strings.Contains(got, "The sharpest fact.") {
			t.Errorf("the remote lacks the correction:\n%s", got)
		}
	})

	t.Run("a ripe correction and a ripe forget after a push that failed", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			remember(t, dev, "b-fact", "Use before a deploy on a Friday.")
		})
		restore := failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		restore()
		authored(t, old, func() {
			reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
			reviseT(t, dev, memoryText("a-fact", fact, "The sharpest fact."), "a-fact", "Sharpest.")
			if out, err := run(t, dev, "", "forget", "b-fact", "--reason", "A mistake."); err != nil {
				t.Fatalf("forget: %v\n%s", err, out)
			}
		})
		// The second correction is a fixup of the first one, which the record did not send.
		if got := queueT(t, repo); !strings.HasPrefix(got, "forget: dev/memories/b-fact.md\nfixup! revise: dev/memories/a-fact.md\nrevise: dev/memories/a-fact.md\n") {
			t.Fatalf("the queue = %q, want a forget, a fixup and a revise on the recorded commits", got)
		}
		// The second push sends the built tip, and the third sends the correction and the forget.
		for i, want := range []string{"Pushed 2 commits automatically.\n", "Pushed 2 commits automatically.\n"} {
			if out, err := pushAutoT(t, home); err != nil || out != want {
				t.Fatalf("push --auto %d = %q, %v; want %q", i+2, out, err, want)
			}
		}
		plainSubjects(t, remote)
		if got := queueT(t, repo); got != "" {
			t.Errorf("the queue = %q, want it empty", got)
		}
		if got := gitT(t, remote, "ls-tree", "-r", "--name-only", "main"); strings.Contains(got, "b-fact.md") || !strings.Contains(got, "a-fact.md") {
			t.Errorf("the remote holds these files, want a-fact and no b-fact:\n%s", got)
		}
		if got := gitT(t, remote, "show", "main:dev/memories/a-fact.md"); !strings.Contains(got, "The sharpest fact.") {
			t.Errorf("the remote lacks the correction:\n%s", got)
		}
		noRecord(t, repo)
	})

	t.Run("a correction after a hand fixup of a recorded commit", func(t *testing.T) {
		home, repo, _, dev := start(t)
		authored(t, old, func() { remember(t, dev, "a-fact", fact) })
		failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		// The person commits a fixup by hand. Its target leaves the queue with the record, so memcore adds no
		// second fixup to that chain.
		writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "A fact by hand."))
		gitT(t, repo, "commit", "-q", "-am", "fixup! remember: dev/memories/a-fact.md")
		reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		if got := strings.TrimSpace(gitT(t, repo, "log", "-1", "--format=%s")); got != "revise: dev/memories/a-fact.md" {
			t.Errorf("the subject of the correction = %q, want a plain revise", got)
		}
	})

	t.Run("a hand fixup of a recorded commit is held after the removal", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() { remember(t, dev, "a-fact", fact) })
		restore := failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		restore()
		authored(t, old, func() {
			writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "A fact by hand."))
			gitT(t, repo, "commit", "-q", "-am", "fixup! remember: dev/memories/a-fact.md")
		})
		if out, err := pushAutoT(t, home); err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("the second push --auto = %q, %v", out, err)
		}
		// The fixup lost its target. It is ripe, and still only the person sends its subject.
		tip := remoteTip(t, remote)
		if out, err := pushAutoT(t, home); err != nil || out != "Nothing is ripe.\n" || remoteTip(t, remote) != tip {
			t.Errorf("the third push --auto = %q, %v; want nothing ripe", out, err)
		}
		if got := queueT(t, repo); got != "fixup! remember: dev/memories/a-fact.md" {
			t.Errorf("the queue = %q, want the hand fixup", got)
		}
		plainSubjects(t, remote)
	})

	t.Run("a forget after a push that failed, and a remote that takes the push late", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() { remember(t, dev, "a-fact", fact) })
		restore := failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		rec, _, _ := readPending(repo)
		restore()
		if out, err := run(t, dev, "", "forget", "a-fact", "--reason", "A mistake."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		head := headCommit(repo)
		// A fold would drop the remember with its forget, so the push of the person refuses.
		var asked string
		_, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push")
		if err == nil || !strings.Contains(err.Error(), "memcore push --auto") || asked != "" || headCommit(repo) != head {
			t.Fatalf("push = %v, asked %q; want a refusal that leaves the queue", err, asked)
		}
		hasRecord(t, repo)
		// The remote takes the built tip late. The forget stays in the queue, so the memory stays forgotten.
		gitT(t, repo, "push", "-q", "origin", rec.Tip+":refs/heads/main")
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		noRecord(t, repo)
		// The record sent the remember, so its forget is a commit of its own, and no fixup.
		if got := queueT(t, repo); got != "forget: dev/memories/a-fact.md" {
			t.Errorf("the queue = %q, want only the forget", got)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev/memories/a-fact.md")); err == nil {
			t.Error("the forgotten memory is back")
		}
		pushYes(t, home)
		plainSubjects(t, remote)
		if got := gitT(t, remote, "ls-tree", "-r", "--name-only", "main"); strings.Contains(got, "a-fact.md") {
			t.Errorf("the remote still holds the forgotten memory:\n%s", got)
		}
	})

	t.Run("a recover while a record stays", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		first := remoteTip(t, remote)
		remember(t, dev, "pushed-then-lost", "Read when the remote loses a commit.")
		pushYes(t, home)
		factAndSkill(t, dev)
		restore := failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		restore()
		// The remote loses a commit that the built tip stands on. So the built tip must never land.
		gitT(t, remote, "update-ref", "refs/heads/main", first)
		if err := Fetch(repo); err == nil || !strings.Contains(err.Error(), "memcore recover") {
			t.Fatalf("Fetch: error = %v, want one that names memcore recover", err)
		}
		hasRecord(t, repo)
		var asked string
		if out, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "recover"); err != nil {
			t.Fatalf("recover: %v\n%s", err, out)
		}
		noRecord(t, repo)
		if out, err := pushAutoT(t, home); err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("push --auto after the recover = %q, %v", out, err)
		}
		if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q, want the memory on the init commit, without the lost commit", got)
		}
		if got := queueT(t, repo); got != skillOnly {
			t.Errorf("the queue = %q, want only the skill", got)
		}
		noRecord(t, repo)
	})

	t.Run("the setting off while a record stays", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		factAndSkill(t, dev)
		restore := failPush(t)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		rec, _, _ := readPending(repo)
		restore()
		gitT(t, repo, "config", "--unset", "memcore.autopush")
		// The remote may hold the built tip already, so the push ends also with the setting off.
		if out, err := pushAutoT(t, home); err != nil || out != "Pushed 1 commit automatically.\n" {
			t.Fatalf("the second push --auto = %q, %v", out, err)
		}
		if got := remoteTip(t, remote); got != rec.Tip {
			t.Errorf("the remote tip = %s, want the built tip %s", got, rec.Tip)
		}
		noRecord(t, repo)
		head := headCommit(repo)
		authored(t, old, func() { draftT(t, dev, "note", "later", "--description", "Read when you test later.") })
		if out, err := pushAutoT(t, home); err != nil || out != "The automatic push is off.\n" || remoteTip(t, remote) != rec.Tip {
			t.Errorf("the third push --auto = %q, %v; want it off", out, err)
		}
		_ = head
	})

	t.Run("a record whose commit the accepted history holds", func(t *testing.T) {
		_, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		heldSkill(t, dev)
		// The built commit is the commit of the queue itself, as when both get one ID.
		note := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD~1"))
		gitT(t, repo, "push", "-q", "origin", note+":refs/heads/main")
		gitT(t, repo, "update-ref", autoRef, note)
		if err := writePending(repo, pending{Tip: note, Commits: []string{note}}); err != nil {
			t.Fatal(err)
		}
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := queueT(t, repo); got != "draft: dev/skills/a-skill/SKILL.md" || remoteTip(t, remote) != note {
			t.Errorf("the queue = %q, want only the skill", got)
		}
		noRecord(t, repo)
	})

	t.Run("a branch switch after the push and before the removal", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		})
		heldSkill(t, dev)
		head := headCommit(repo)
		afterPush(t, func() error {
			gitT(t, repo, "checkout", "-q", "-b", "side", "HEAD~1")
			return nil
		})
		out, err := pushAutoT(t, home)
		if err == nil || !strings.Contains(err.Error(), "checks out main first") {
			t.Fatalf("push --auto = %q, %v; want the refusal of another branch", out, err)
		}
		side := strings.TrimSpace(gitT(t, repo, "rev-parse", "side"))
		if got := strings.TrimSpace(gitT(t, repo, "rev-parse", "main")); got != head || side != strings.TrimSpace(gitT(t, repo, "rev-parse", head+"~1")) {
			t.Errorf("the removal moved main to %s or the other branch to %s", got, side)
		}
		hasRecord(t, repo)
		gitT(t, repo, "checkout", "-q", "main")
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := queueT(t, repo); got != "draft: dev/skills/a-skill/SKILL.md" || strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD~1")) != remoteTip(t, remote) {
			t.Errorf("the queue after the fetch = %q, want only the skill, on the remote tip", got)
		}
		noRecord(t, repo)
	})

	t.Run("a push that still runs when a fetch looks", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		})
		heldSkill(t, dev)
		head := headCommit(repo)
		// memcore was killed during its push, and its git process still runs: it holds the lock of the push.
		ripe := chainsT(t, repo)[:1]
		tmp := t.TempDir()
		tip, built, err := buildAside(repo, tmp, ripe)
		if err != nil || len(built) != 1 {
			t.Fatalf("buildAside = %d chains, %v", len(built), err)
		}
		gitT(t, repo, "fetch", "-q", "--no-tags", "--no-write-fetch-head", "--refmap=", filepath.Join(tmp, "clone"), "HEAD")
		gitT(t, repo, "update-ref", autoRef, tip)
		if err := writePending(repo, pending{Tip: tip, Commits: []string{ripe[0].target.sha, ripe[0].fixups[0].sha}}); err != nil {
			t.Fatal(err)
		}
		pushing, free, err := lockPush(repo)
		if err != nil || !free {
			t.Fatalf("lockPush = %v, %v", free, err)
		}
		still := func(t *testing.T, err error) {
			t.Helper()
			if err == nil || !strings.Contains(err.Error(), "an automatic push still runs") {
				t.Errorf("error = %v, want the refusal while a push runs", err)
			}
			if headCommit(repo) != head {
				t.Error("a command during a push changed the queue")
			}
			hasRecord(t, repo)
		}
		still(t, Fetch(repo))
		var asked string
		_, err = runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push")
		still(t, err)
		if asked != "" {
			t.Errorf("a push during an automatic push asked %q", asked)
		}
		_, err = pushAutoT(t, home)
		still(t, err)
		// The push ends now, and the remote takes the tip.
		gitT(t, repo, "push", "-q", "origin", tip+":refs/heads/main")
		pushing.Close()
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := queueT(t, repo); got != "draft: dev/skills/a-skill/SKILL.md" || strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD~1")) != remoteTip(t, remote) {
			t.Errorf("the queue after the fetch = %q, want only the skill, on the remote tip", got)
		}
		noRecord(t, repo)
	})

	t.Run("the git process of the push holds the lock", func(t *testing.T) {
		home, repo, _, dev := start(t)
		ripeNote(t, dev, "slow-search")
		prev := gitPush
		t.Cleanup(func() { gitPush = prev })
		gitPush = func(repo string, held *os.File, args ...string) error {
			if held == nil {
				t.Error("the automatic push gave its git process no lock to hold")
			} else if _, free, err := lockPush(repo); err != nil || free {
				t.Errorf("lockPush during the push = %v, %v; want the lock held", free, err)
			}
			if _, waits, err := readPending(repo); err != nil || !waits {
				t.Errorf("the record during the push = %v, %v; want it written before the push", waits, err)
			}
			return prev(repo, held, args...)
		}
		if out, err := pushAutoT(t, home); err != nil {
			t.Fatalf("push --auto: %v\n%s", err, out)
		}
		if f, free, err := lockPush(repo); err != nil || !free {
			t.Errorf("lockPush after the push = %v, %v; want the lock free", free, err)
		} else {
			f.Close()
		}
		noRecord(t, repo)
	})

	t.Run("a drop while a record stays", func(t *testing.T) {
		home, repo, _, dev := start(t)
		ripeNote(t, dev, "slow-search")
		heldSkill(t, dev)
		afterPush(t, func() error { return errors.New("the connection broke") })
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		head := headCommit(repo)
		var asked string
		_, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "drop", head, "--reason", "A test.")
		if err == nil || !strings.Contains(err.Error(), "run memcore inbox first") || !strings.Contains(err.Error(), "memcore push --auto") || asked != "" || headCommit(repo) != head {
			t.Errorf("drop = %v, asked %q; want a refusal that leaves the queue", err, asked)
		}
		hasRecord(t, repo)
	})

	t.Run("an interruption after the removal and before the end of the record", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		})
		heldSkill(t, dev)
		sent := strings.Fields(gitT(t, repo, "rev-list", "--reverse", acceptedRef+"..HEAD~1"))
		if out, err := pushAutoT(t, home); err != nil {
			t.Fatalf("push --auto: %v\n%s", err, out)
		}
		head, tip := headCommit(repo), remoteTip(t, remote)
		// The record is as the removal left it before it ended: the commits are gone from the queue.
		gitT(t, repo, "update-ref", autoRef, tip)
		rec := pending{Tip: tip, Commits: sent, Removing: true, Base: tip}
		if err := writePending(repo, rec); err != nil {
			t.Fatal(err)
		}
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch after a removal that ended: %v", err)
		}
		noRecord(t, repo)
		unmoved(t, repo, remote, head, tip)
		// The same holds when another machine advanced the remote before this fetch.
		other := secondClone(t, remote)
		writeT(t, other, "work/notes/elsewhere.md", noteText("elsewhere", "From another machine."))
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: a commit from another machine")
		gitT(t, other, "push", "-q", "origin", "main")
		gitT(t, repo, "update-ref", autoRef, tip)
		if err := writePending(repo, rec); err != nil {
			t.Fatal(err)
		}
		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch after a removal that ended and a remote that advanced: %v", err)
		}
		noRecord(t, repo)
		if got := queueT(t, repo); got != "draft: dev/skills/a-skill/SKILL.md" || strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD~1")) != remoteTip(t, remote) {
			t.Errorf("the queue = %q, want only the skill, on the remote tip", got)
		}
		head, tip = headCommit(repo), remoteTip(t, remote)
		// Without the mark of a removal, the same record means that the person changed the queue.
		gitT(t, repo, "update-ref", autoRef, tip)
		rec.Removing = false
		if err := writePending(repo, rec); err != nil {
			t.Fatal(err)
		}
		if err := Fetch(repo); err == nil || !strings.Contains(err.Error(), "by hand") {
			t.Errorf("Fetch = %v, want the error of a queue that lacks a commit of the record", err)
		}
		hasRecord(t, repo)
	})

	t.Run("an aborted removal and then a queue that the person rewrote", func(t *testing.T) {
		home, repo, _, dev := start(t)
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
		})
		afterPush(t, func() error { return errors.New("the connection broke") })
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the failure of the push", out)
		}
		// A removal started and was aborted, so its mark stays. Then the person rewrites both commits by hand.
		rec, _, err := readPending(repo)
		if err != nil {
			t.Fatal(err)
		}
		rec.Removing, rec.Base = true, acceptedCommit(repo)
		if err := writePending(repo, rec); err != nil {
			t.Fatal(err)
		}
		gitT(t, repo, "reset", "-q", "--soft", "HEAD~2")
		gitT(t, repo, "commit", "-q", "-m", "test: the memory, rewritten by hand")
		head := headCommit(repo)
		if err := Fetch(repo); err == nil || !strings.Contains(err.Error(), "by hand") {
			t.Errorf("Fetch = %v, want the error of a queue that lacks a commit of the record", err)
		}
		if headCommit(repo) != head {
			t.Error("finishDrop changed a queue that the person rewrote")
		}
		hasRecord(t, repo)
	})

	t.Run("no accepted history", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		autoOn(t, repo)
		authored(t, old, func() {
			draftT(t, mkdirT(t, filepath.Join(home, "dev")), "note", "slow-search", "--description", "Read when search is slow.")
		})
		head := headCommit(repo)
		out, err := pushAutoT(t, home)
		if err != nil || out != "The remote holds no accepted history, so the person pushes first.\n" {
			t.Errorf("push --auto = %q, %v", out, err)
		}
		if _, err := store.Git(remote, "rev-parse", "--verify", "-q", "main"); err == nil || headCommit(repo) != head {
			t.Error("a run with no accepted history pushed or changed the queue")
		}
	})

	t.Run("a working tree with changes", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		writeT(t, repo, "dev/notes/by-hand.md", noteText("by-hand", "An edit by hand."))
		head, tip := headCommit(repo), remoteTip(t, remote)
		if out, err := pushAutoT(t, home); err == nil || !strings.Contains(err.Error(), "dev/notes/by-hand.md") {
			t.Errorf("push --auto = %q, %v; want a refusal that names the file", out, err)
		}
		unmoved(t, repo, remote, head, tip)
	})

	t.Run("--auto with --expect", func(t *testing.T) {
		home, repo, remote, dev := start(t)
		ripeNote(t, dev, "slow-search")
		head, tip := headCommit(repo), remoteTip(t, remote)
		asks := func(string) (bool, error) { t.Error("push asked"); return false, nil }
		out, err := runWith(t, Env{Dir: home, Confirm: asks}, "push", "--auto", "--expect", head)
		if err == nil || !strings.Contains(err.Error(), "usage: memcore push [--expect <commit> | --auto]") {
			t.Errorf("push --auto --expect = %q, %v; want a usage error", out, err)
		}
		unmoved(t, repo, remote, head, tip)
	})
}

// trailers returns, for each commit of a range, whether its message ends with the trailer of an automatic push.
func trailers(t *testing.T, repo, rng string) []bool {
	t.Helper()
	var marked []bool
	for _, sha := range strings.Fields(gitT(t, repo, "rev-list", "--reverse", rng)) {
		message := strings.TrimRight(gitT(t, repo, "show", "-s", "--format=%B", sha), "\n")
		marked = append(marked, strings.HasSuffix(message, "\n\nPushed: auto"))
		if strings.Count(message, "Pushed: auto") > 1 {
			t.Errorf("the commit %s holds the trailer twice:\n%s", short(sha), message)
		}
	}
	return marked
}

func TestPushAutoTrailer(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	const fact = "Read when you test a fact."
	old := before(72 * time.Hour)
	// noTrailer checks that no commit of the queue has the trailer.
	noTrailer := func(t *testing.T, repo, when string) {
		t.Helper()
		if marked := trailers(t, repo, acceptedRef+"..main"); slices.Contains(marked, true) {
			t.Errorf("%s, a commit of the queue has the trailer: %v", when, marked)
		}
		if out := gitT(t, repo, "log", "--format=%B", acceptedRef+"..main"); strings.Contains(out, "Pushed: auto") {
			t.Errorf("%s, the queue holds the trailer:\n%s", when, out)
		}
	}

	t.Run("each pushed commit ends with the trailer", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		pushYes(t, home)
		autoOn(t, repo)
		// A setting that removes the lines of a comment must not remove the trailer.
		gitT(t, repo, "config", "commit.cleanup", "strip")
		gitT(t, repo, "config", "core.commentChar", "P")
		dev := mkdirT(t, filepath.Join(home, "dev"))
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			draftT(t, dev, "note", "trial", "--description", "Read when you test trial.")
			draftT(t, dev, "skill", "a-skill", "--description", "Use when you test.")
		})
		noTrailer(t, repo, "before the push")
		if out, err := pushAutoT(t, home); err != nil || out != "Pushed 2 commits automatically.\n" {
			t.Fatalf("push --auto = %q, %v", out, err)
		}
		noTrailer(t, repo, "after the push")
		if got := queueT(t, repo); got != "draft: dev/skills/a-skill/SKILL.md" {
			t.Errorf("the queue = %q, want only the skill", got)
		}
		// The remote holds the init that the person pushed, and then the two commits of the automatic push.
		if got := trailers(t, remote, "main"); len(got) != 3 || got[0] || !got[1] || !got[2] {
			t.Errorf("the trailers of the remote = %v, want them on the two automatic commits only\n%s", got, gitT(t, remote, "log", "--format=%B", "main"))
		}
	})

	t.Run("a reason stays above the trailer", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		draftT(t, dev, "note", "trial", "--description", "Read when you test trial.")
		pushYes(t, home)
		autoOn(t, repo)
		authored(t, old, func() { reviseT(t, dev, noteText("trial", "A finding."), "dev/notes/trial.md", "The trial ended.") })
		if out, err := pushAutoT(t, home); err != nil {
			t.Fatalf("push --auto: %v\n%s", err, out)
		}
		if got, want := gitT(t, remote, "show", "-s", "--format=%B", "main"), "revise: dev/notes/trial.md\n\nThe trial ended.\n\nPushed: auto\n\n"; got != want {
			t.Errorf("the pushed message = %q, want %q", got, want)
		}
	})

	t.Run("no commit of the queue has the trailer after a rejected push", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		pushYes(t, home)
		autoOn(t, repo)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		authored(t, old, func() {
			remember(t, dev, "a-fact", fact)
			draftT(t, dev, "skill", "a-skill", "--description", "Use when you test.")
		})
		rejectPushes(t, remote)
		if out, err := pushAutoT(t, home); err == nil {
			t.Fatalf("push --auto = %q, want the rejection of the remote", out)
		}
		noTrailer(t, repo, "after a rejected push")
		if err := Fetch(repo); err != nil {
			t.Fatal(err)
		}
		noTrailer(t, repo, "after the fetch that follows a rejected push")
		if err := os.Remove(filepath.Join(remote, "hooks", "pre-receive")); err != nil {
			t.Fatal(err)
		}
		pushYes(t, home)
		if got := trailers(t, remote, "main"); slices.Contains(got, true) {
			t.Errorf("a push of the person sent the trailer: %v", got)
		}
	})
}

func TestInboxAuto(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	const fact = "Read when you test a fact."
	old, fresh := before(72*time.Hour), before(time.Hour)
	lacks := func(t *testing.T, out string, texts ...string) {
		t.Helper()
		for _, text := range texts {
			if strings.Contains(out, text) {
				t.Errorf("inbox holds %q:\n%s", text, out)
			}
		}
	}

	t.Run("the setting off", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		authored(t, old, func() { remember(t, dev, "a-fact", fact) })
		writeT(t, repo, "dev/notes/trial.md", noteText("trial", "A note."))
		commitAt(t, repo, "test: a note for "+fakeToken()+"\n\nThe token is "+fakeToken()+".", old.Format(time.RFC3339))
		out := inboxT(t, home)
		lacks(t, out, "Automatic push", "pushes automatically", "held:", "ripe:", "pushed these commits automatically", fakeToken())
		holds(t, out, "test: a note for [secret:hosting-token]", "The token is [secret:hosting-token].")
	})

	t.Run("a waiting memory, a ripe note and a held skill", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		autoOn(t, repo)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		authored(t, old, func() {
			draftT(t, dev, "note", "trial", "--description", "Read when you test trial.")
			draftT(t, dev, "skill", "a-skill", "--description", "Use when you test.")
		})
		authored(t, fresh, func() { remember(t, dev, "a-fact", fact) })
		out := inboxT(t, home)
		holds(t, out,
			"Automatic push: on, after 24h.\n",
			"    A  dev/memories/a-fact.md\n    pushes automatically after "+fresh.Add(24*time.Hour).Format("2006-01-02 15:04 UTC")+"\n",
			"    A  dev/skills/a-skill/SKILL.md\n    held: only the person pushes dev/skills/a-skill/SKILL.md\n",
			"    A  dev/notes/trial.md\n    ripe: the next automatic push sends it\n")
		lacks(t, out, "pushed these commits automatically")
	})

	t.Run("a chain with a fixup, as the fold leaves it", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		autoOn(t, repo)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		authored(t, old, func() { remember(t, dev, "a-fact", fact) })
		authored(t, fresh, func() { reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.") })
		out := inboxT(t, home)
		holds(t, out, "    1 fixup folds in.\n", "    pushes automatically after "+fresh.Add(24*time.Hour).Format("2006-01-02 15:04 UTC")+"\n")
		if n := strings.Count(out, "pushes automatically after"); n != 1 {
			t.Errorf("inbox shows the state %d times, want once for the folded chain:\n%s", n, out)
		}
	})

	t.Run("no delay, and a merge", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		gitT(t, repo, "config", "memcore.autopush", "0s")
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", fact)
		holds(t, inboxT(t, home), "Automatic push: on, with no delay.\n", "    ripe: the next automatic push sends it\n")
		gitT(t, repo, "config", "memcore.autopush", "soon")
		out, err := run(t, home, "", "inbox")
		if err != nil || !strings.Contains(out, "warning: read the setting memcore.autopush") || !strings.Contains(out, "remember: dev/memories/a-fact.md") {
			t.Errorf("inbox with a setting that memcore cannot read = %v\n%s\nwant a warning and the queue", err, out)
		}
	})

	t.Run("a secret that spans the lines of a commit body", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		key := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\nQUJDREVGR0hJSktMTU5PUA\n-----END OPENSSH PRIVATE KEY-----"
		writeT(t, repo, "dev/notes/trial.md", noteText("trial", "A note."))
		commitAt(t, repo, "test: a note\n\nThe key:\n"+key+"\nUse it with care.", old.Format(time.RFC3339))
		for _, setting := range []string{"", "24h"} {
			if setting != "" {
				gitT(t, repo, "config", "memcore.autopush", setting)
			}
			out := inboxT(t, home)
			holds(t, out, "[secret:private-key]", "Use it with care.")
			lacks(t, out, "b3BlbnNzaC1rZXktdjEAAAAA", "QUJDREVGR0hJSktMTU5PUA", "BEGIN OPENSSH")
		}
	})

	t.Run("a remote that lost a commit with a token in its subject", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		base := headCommit(repo)
		writeT(t, repo, "dev/notes/trial.md", noteText("trial", "A note."))
		commitAt(t, repo, "test: a note for "+fakeToken(), old.Format(time.RFC3339))
		if err := writeMarker(repo, rewritten{accepted: headCommit(repo), fetched: base}); err != nil {
			t.Fatal(err)
		}
		for _, setting := range []string{"", "24h"} {
			if setting != "" {
				gitT(t, repo, "config", "memcore.autopush", setting)
			}
			out, _ := run(t, home, "", "inbox")
			holds(t, out, "The remote no longer holds these accepted commits.", "test: a note for [secret:hosting-token]")
			lacks(t, out, fakeToken())
		}
	})

	t.Run("a held note with a token in its commit message", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		autoOn(t, repo)
		writeT(t, repo, "dev/notes/trial.md", noteText("trial", "A note."))
		commitAt(t, repo, "test: a note\n\nThe token is "+fakeToken()+".", old.Format(time.RFC3339))
		out := inboxT(t, home)
		holds(t, out, "The token is [secret:hosting-token].", "    held: a likely secret in the commit message\n")
		lacks(t, out, fakeToken())
	})

	t.Run("a clean chain behind a chain with a token in its subject", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		autoOn(t, repo)
		writeT(t, repo, "dev/notes/trial.md", noteText("trial", "A note."))
		commitAt(t, repo, "test: a note for "+fakeToken(), old.Format(time.RFC3339))
		first := short(headCommit(repo))
		writeT(t, repo, "dev/notes/trial.md", noteText("trial", "A finding."))
		commitAt(t, repo, "test: the note again", old.Format(time.RFC3339))
		out := inboxT(t, home)
		holds(t, out, "test: a note for [secret:hosting-token]", "    held: a likely secret in the commit message\n",
			"    held: it changes a document of the chain "+first+", which is held\n")
		lacks(t, out, fakeToken())
	})

	t.Run("the automatic pushes since the last push of the person", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		pushYes(t, home)
		autoOn(t, repo)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		auto := func(name string) string {
			t.Helper()
			authored(t, old, func() { draftT(t, dev, "note", name, "--description", "Read when you test "+name+".") })
			if out, err := pushAutoT(t, home); err != nil || out != "Pushed 1 commit automatically.\n" {
				t.Fatalf("push --auto = %q, %v", out, err)
			}
			return abbrev(repo, acceptedCommit(repo)) + " draft: dev/notes/" + name + ".md"
		}
		const section = "memcore pushed these commits automatically since the last push of the person, newest first:\n"
		first := auto("first")
		second := auto("second")
		holds(t, inboxT(t, home), "Nothing waits for review.\n\n"+section+"  "+second+"\n  "+first+"\n")

		authored(t, old, func() { draftT(t, dev, "skill", "a-skill", "--description", "Use when you test.") })
		declinedPush(t, home)
		holds(t, inboxT(t, home), section+"  "+second+"\n  "+first+"\n")

		pushYes(t, home)
		lacks(t, inboxT(t, home), "pushed these commits automatically")

		third := auto("third")
		out := inboxT(t, home)
		holds(t, out, section+"  "+third+"\n")
		lacks(t, out, first, second)
	})
}
