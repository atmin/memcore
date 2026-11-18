package commands

import (
	"fmt"
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
			return []want{{state: stateHeld, reason: "dev/skills/a-skill/SKILL.md"}}
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
		if got, want := gitT(t, clone, "log", "--format=%s%n%b", "-2", tip), gitT(t, repo, "log", "--format=%s%n%b", "-2", "HEAD"); got != want {
			t.Errorf("the built messages = %q, want %q", got, want)
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
		gitT(t, repo, "config", "commit.cleanup", "strip")
		clone, tip, built, err := build(t, repo, chainsT(t, repo))
		if err != nil || len(built) != 1 {
			t.Fatalf("buildAside = %d chains, %v; want one", len(built), err)
		}
		onBase(t, repo, clone, tip, 1)
		sameTree(t, repo, clone, tip)
		if got, err := authorDate(clone, tip); err != nil || !got.Equal(old) {
			t.Errorf("the built commit has the author date %v, %v; want the date %v of its target", got, err, old)
		}
		if got, want := gitT(t, clone, "show", "-s", "--format=%an <%ae>%n%B", tip), gitT(t, repo, "show", "-s", "--format=%an <%ae>%n%B", "HEAD~1"); got != want {
			t.Errorf("the built commit = %q, want the author and the message of its target %q", got, want)
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
