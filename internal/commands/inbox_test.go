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
