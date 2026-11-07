package commands

import (
	"path/filepath"
	"strings"
	"testing"
)

// inboxT runs inbox in home, and fails the test on an error.
func inboxT(t *testing.T, home string) string {
	t.Helper()
	out, err := run(t, home, "", "inbox")
	if err != nil {
		t.Fatalf("inbox: %v\n%s", err, out)
	}
	return out
}

// holds fails the test when out lacks one of the texts.
func holds(t *testing.T, out string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !strings.Contains(out, text) {
			t.Errorf("inbox does not hold %q:\n%s", text, out)
		}
	}
}

func TestInboxDropsCancelled(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	pushYes(t, home)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "a-fact", "Read when you test a fact.")
	if out, err := run(t, dev, "", "forget", "a-fact", "--reason", "A mistake."); err != nil {
		t.Fatalf("forget: %v\n%s", err, out)
	}
	remember(t, dev, "b-fact", "Read before you deploy on Fridays.")
	out := inboxT(t, home)
	holds(t, out,
		"The review queue holds 3 unpushed commits. The fold at push leaves 1, newest first:\n",
		"\n  "+short(sha(t, repo, "remember: dev/memories/b-fact.md"))+" remember: dev/memories/b-fact.md\n",
		"\nThe fold drops 2 commits that leave every document as it was.\n",
		"memcore drop <commit>",
	)
	if strings.Contains(out, "a-fact") {
		t.Errorf("inbox lists the dropped memory:\n%s", out)
	}
}

func TestInboxConflict(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	fact := "Read when you test a fact."
	remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", fact)
	writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "B."))
	commitAt(t, repo, "test: an edit by hand", "2026-11-01T10:00:00Z")
	writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "C."))
	commitAt(t, repo, "fixup! remember: dev/memories/a-fact.md", "2026-11-01T10:00:00Z")
	before := gitT(t, repo, "rev-parse", "HEAD")
	out := inboxT(t, home)
	holds(t, out,
		"warning: the fold at push would stop: fold the fixups: a rebase stopped on a conflict in dev/memories/a-fact.md",
		"The queue below lists each commit as it is, without the fold.\n",
		"The review queue holds 4 unpushed commits, newest first:\n",
		" fixup! remember: dev/memories/a-fact.md\n",
		"memcore drop <commit>",
	)
	if after := gitT(t, repo, "rev-parse", "HEAD"); after != before {
		t.Errorf("inbox moved HEAD from %s to %s", before, after)
	}
	clean(t, repo)
}

func TestInboxAllDropped(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, _, _ := newMemory(t)
	pushYes(t, home)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "a-fact", "Read when you test a fact.")
	if out, err := run(t, dev, "", "forget", "a-fact", "--reason", "A mistake."); err != nil {
		t.Fatalf("forget: %v\n%s", err, out)
	}
	out := inboxT(t, home)
	holds(t, out,
		"The review queue holds 2 unpushed commits. The fold at push leaves none.\n",
		"\nThe fold drops 2 commits that leave every document as it was.\n",
		"memcore drop <commit>",
	)
	if strings.Contains(out, "Nothing waits for review.") {
		t.Errorf("inbox says that nothing waits:\n%s", out)
	}
}

func TestInboxIndexHeader(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	const fact = "Read when you test a fact."
	// dropped remembers a fact at ~/work and forgets it, a chain that the fold drops.
	dropped := func(t *testing.T, home string) {
		work := mkdirT(t, filepath.Join(home, "work"))
		remember(t, work, "a-fact", fact)
		if out, err := run(t, work, "", "forget", "a-fact", "--reason", "A mistake."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
	}
	for _, c := range []struct {
		name   string
		hand   string // the memory that the person commits by hand and pushes with git, which leaves indexes stale
		queue  func(t *testing.T, home string)
		header string
		news   []string // the entries of the index commits that push adds, newest first, right after the header
	}{
		{
			name:   "one stale index and an empty queue",
			hand:   "memories/by-hand.md",
			header: "The review queue holds no unpushed commits. Push adds 1 index commit, newest first:\n",
			news:   []string{"\n  new index: README.md\n    M  README.md\n"},
		},
		{
			name:   "two stale indexes and an empty queue",
			hand:   "dev/memories/by-hand.md",
			header: "The review queue holds no unpushed commits. Push adds 2 index commits, newest first:\n",
			news: []string{
				"\n  new index: dev/README.md\n    A  dev/README.md\n",
				"\n  new index: README.md\n    M  README.md\n",
			},
		},
		{
			// The fold does not run without a fixup, so the index commit stays.
			name: "a kept commit and an index commit",
			hand: "dev/memories/by-hand.md",
			queue: func(t *testing.T, home string) {
				remember(t, mkdirT(t, filepath.Join(home, "work")), "a-fact", fact)
			},
			header: "The review queue holds 1 unpushed commit. The fold at push leaves 1, and push adds 1 index commit, newest first:\n",
			news:   []string{"\n  new index: dev/README.md\n    A  dev/README.md\n"},
		},
		{
			// The fold drops the chain, so no kept commit takes the change of the stale index.
			name:   "a dropped chain and an index commit",
			hand:   "dev/memories/by-hand.md",
			queue:  dropped,
			header: "The review queue holds 2 unpushed commits. The fold at push leaves none, and push adds 1 index commit, newest first:\n",
			news:   []string{"\n  new index: dev/README.md\n    M  README.md\n    A  dev/README.md\n"},
		},
		{
			// The chain writes the stale root index, and push writes it again after the fold drops the chain.
			name:   "a dropped chain that held a stale index",
			hand:   "memories/by-hand.md",
			queue:  dropped,
			header: "The review queue holds 2 unpushed commits. The fold at push leaves none, and push adds 1 index commit, newest first:\n",
			news:   []string{"\n  new index: README.md\n    M  README.md\n"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			writeT(t, repo, c.hand, memoryText("by-hand", "Read when you edit by hand.", "A fact."))
			gitT(t, repo, "add", "-A")
			gitT(t, repo, "commit", "-q", "-m", "test: a memory by hand")
			gitT(t, repo, "push", "-q", "origin", "main")
			if c.queue != nil {
				c.queue(t, home)
			}
			out := inboxT(t, home)
			holds(t, out, "\n"+c.header+strings.Join(c.news, ""), "memcore drop <commit>")
			if got := strings.Count(out, "\n  new "); got != len(c.news) {
				t.Errorf("inbox lists %d new entries, want %d:\n%s", got, len(c.news), out)
			}
		})
	}
}

func TestInboxExactPaths(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	pushYes(t, home)
	commitOddPaths(t, repo)
	out, err := run(t, home, "", "inbox")
	if err != nil {
		t.Fatalf("inbox: %v\n%s", err, out)
	}
	for _, p := range oddPaths {
		if !strings.Contains(out, "A  "+p+"\n") {
			t.Errorf("inbox = %q, want the path %s as it is", out, p)
		}
	}
}
