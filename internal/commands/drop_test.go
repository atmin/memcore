package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// sha returns the commit of the last commit with a subject.
func sha(t *testing.T, repo, subject string) string {
	t.Helper()
	out := gitT(t, repo, "log", "--format=%H %s")
	for _, line := range strings.Split(out, "\n") {
		if h, s, ok := strings.Cut(line, " "); ok && s == subject {
			return h
		}
	}
	t.Fatalf("no commit %q in\n%s", subject, out)
	return ""
}

func TestDrop(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	var asked string
	yes := func(dir string) Env { return Env{Dir: dir, Confirm: answer(true, &asked)} }

	t.Run("a dropped memory leaves the queue and the index, with its fixups", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "unwanted", "Read when you test an unwanted fact.")
		if out, err := run(t, dev, memoryText("unwanted", "Read when you test an unwanted fact.", "Still unwanted."), "revise", "unwanted", "--reason", "A test."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		remember(t, dev, "wanted", "Read before you deploy on Fridays.")
		if out, err := runWith(t, yes(home), "drop", sha(t, repo, "remember: dev/memories/unwanted.md"), "--reason", "Not a fact."); err != nil {
			t.Fatalf("drop: %v\n%s", err, out)
		}
		if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s")); got != "remember: dev/memories/wanted.md\ninit: create the memory repo" {
			t.Errorf("history = %q", got)
		}
		if index := readT(t, filepath.Join(repo, "dev", "README.md")); strings.Contains(index, "unwanted") || !strings.Contains(index, "wanted") {
			t.Errorf("index =\n%s\nwant only the wanted memory", index)
		}
		e := last(t, repo)
		if len(e.Dropped) != 2 || e.Reason != "Not a fact." {
			t.Errorf("event = %+v, want two dropped commits and the reason", e)
		}
		clean(t, repo)
	})

	t.Run("an accepted commit refuses", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "pushed-fact", "Read when you test a pushed fact.")
		gitT(t, repo, "push", "-q", "origin", "main")
		if _, err := runWith(t, yes(home), "drop", "HEAD"); err == nil || !strings.Contains(err.Error(), "accepted") {
			t.Errorf("error = %v, want a refusal of an accepted commit", err)
		}
	})

	t.Run("a drop that leaves a reference dead refuses", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "base-fact", "Read when you test a base fact.")
		if out, err := run(t, dev, memoryText("linking-fact", "Read before you follow a link.", "See [[base-fact]]."), "remember"); err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		before := gitT(t, repo, "rev-parse", "HEAD")
		if _, err := runWith(t, yes(home), "drop", sha(t, repo, "remember: dev/memories/base-fact.md")); err == nil || !strings.Contains(err.Error(), "dev/memories/linking-fact.md") {
			t.Errorf("error = %v, want a refusal that names the linking memory", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("the refused drop moved HEAD")
		}
		clean(t, repo)
	})

	t.Run("a fixup goes with the fixups after it", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", "Read when you test a fact.")
		for _, body := range []string{"Once.", "Twice."} {
			if out, err := run(t, dev, memoryText("a-fact", "Read when you test a fact.", body), "revise", "a-fact", "--reason", body); err != nil {
				t.Fatalf("revise: %v\n%s", err, out)
			}
		}
		if out, err := runWith(t, yes(home), "drop", sha(t, repo, "fixup! remember: dev/memories/a-fact.md")); err != nil {
			t.Fatalf("drop: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "remember: dev/memories/a-fact.md" {
			t.Errorf("head = %q, want the remember alone", got)
		}
		if got := readT(t, filepath.Join(repo, "dev", "memories", "a-fact.md")); !strings.Contains(got, "A fact.") {
			t.Errorf("memory = %q, want the remembered text", got)
		}
		clean(t, repo)
	})

	t.Run("a rebase of the person stays", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", "Read when you test a fact.")
		store.Git(repo, "-c", "sequence.editor=true", "rebase", "-i", "--exec", "false", "HEAD~1")
		if !rebasing(repo) {
			t.Fatal("the test did not pause a rebase")
		}
		if _, err := runWith(t, yes(home), "drop", "HEAD"); err == nil || !strings.Contains(err.Error(), "rebase is in progress") {
			t.Errorf("error = %v, want a refusal of the rebase in progress", err)
		}
		if !rebasing(repo) {
			t.Error("drop ended the rebase of the person")
		}
	})

	t.Run("another branch refuses", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", "Read when you test a fact.")
		gitT(t, repo, "checkout", "-q", "-b", "draft")
		if _, err := runWith(t, yes(home), "drop", "HEAD"); err == nil || !strings.Contains(err.Error(), "main") {
			t.Errorf("error = %v, want a refusal that names main", err)
		}
	})

	t.Run("a failed check puts the queue back", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		writeT(t, repo, "dev/notes/target.md", "---\nname: target\ndescription: \"Read when you test.\"\n---\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a target")
		if err := os.Symlink("target.md", filepath.Join(repo, "dev", "notes", "link.md")); err != nil {
			t.Fatal(err)
		}
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a link to the target")
		before := gitT(t, repo, "rev-parse", "HEAD")
		if _, err := runWith(t, yes(home), "drop", sha(t, repo, "test: a target")); err == nil {
			t.Error("drop of the target of a link: no error")
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("the failed drop left the queue changed")
		}
	})

	t.Run("a skill without its SKILL.md refuses", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		if out, err := run(t, dev, "", "draft", "skill", "deploy", "--description", "Use when you deploy."); err != nil {
			t.Fatalf("draft: %v\n%s", err, out)
		}
		if out, err := run(t, dev, "", "draft", "skill", "deploy/check.sh"); err != nil {
			t.Fatalf("draft: %v\n%s", err, out)
		}
		mkdirT(t, filepath.Join(repo, "dev", "skills", "empty-folder"))
		writeT(t, repo, "lab/skills/old-orphan/run.sh", "#!/bin/sh\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: an orphan from before")
		remember(t, dev, "unrelated", "Read before you deploy on Fridays.")
		if out, err := runWith(t, yes(home), "drop", sha(t, repo, "remember: dev/memories/unrelated.md")); err != nil {
			t.Errorf("an unrelated drop next to an old orphan and an empty folder: %v\n%s", err, out)
		}
		before := gitT(t, repo, "rev-parse", "HEAD")
		if _, err := runWith(t, yes(home), "drop", sha(t, repo, "draft: dev/skills/deploy/SKILL.md")); err == nil || !strings.Contains(err.Error(), "dev/skills/deploy") {
			t.Errorf("error = %v, want a refusal that names the skill", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("the refused drop moved HEAD")
		}
	})

	t.Run("no", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", "Read when you test a fact.")
		before := gitT(t, repo, "rev-parse", "HEAD")
		if out, err := runWith(t, Env{Dir: home, Confirm: answer(false, &asked)}, "drop", "HEAD"); err != nil {
			t.Fatalf("drop: %v\n%s", err, out)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("drop moved HEAD after the person said no")
		}
	})

	t.Run("an agent refuses", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", "Read when you test a fact.")
		t.Setenv("CLAUDECODE", "1")
		before := gitT(t, repo, "rev-parse", "HEAD")
		if _, err := run(t, home, "", "drop", "HEAD"); err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
			t.Errorf("error = %v, want the refusal of an agent", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("an agent moved HEAD")
		}
		os.Unsetenv("CLAUDECODE")
	})
}

func TestInboxDetails(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, _, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "a-fact", "Read when you test a fact.")
	if out, err := run(t, dev, memoryText("a-fact", "Read when you test a fact.", "Sharper."), "revise", "a-fact", "--reason", "The fact grew sharper."); err != nil {
		t.Fatalf("revise: %v\n%s", err, out)
	}
	out, err := run(t, home, "", "inbox")
	if err != nil {
		t.Fatalf("inbox: %v\n%s", err, out)
	}
	for _, want := range []string{"fixup! remember: dev/memories/a-fact.md", "The fact grew sharper.", "A  dev/memories/a-fact.md", "M  dev/memories/a-fact.md", "memcore drop"} {
		if !strings.Contains(out, want) {
			t.Errorf("inbox does not hold %q:\n%s", want, out)
		}
	}
}
