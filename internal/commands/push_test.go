package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

func answer(yes bool, asked *string) func(string) (bool, error) {
	return func(prompt string) (bool, error) {
		*asked = prompt
		return yes, nil
	}
}

func TestPush(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	t.Run("yes", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "small-commits", "Read before you commit: one change per commit.")
		var asked string
		out, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push")
		if err != nil {
			t.Fatalf("push: %v\n%s", err, out)
		}
		if asked != "Push 2 commits to origin? [y/N] " {
			t.Errorf("prompt = %q", asked)
		}
		if got, want := gitT(t, remote, "rev-parse", "main"), gitT(t, repo, "rev-parse", "HEAD"); got != want {
			t.Errorf("remote main = %s, want %s", got, want)
		}
		if got := strings.TrimSpace(gitT(t, repo, "rev-list", "--count", "origin/main..main")); got != "0" {
			t.Errorf("queue holds %s commits after the push, want 0", got)
		}
	})

	t.Run("no", func(t *testing.T) {
		home, _, remote := newMemory(t)
		var asked string
		out, err := runWith(t, Env{Dir: home, Confirm: answer(false, &asked)}, "push")
		if err != nil {
			t.Fatalf("push: %v\n%s", err, out)
		}
		if _, err := store.Git(remote, "rev-parse", "--verify", "-q", "main"); err == nil {
			t.Error("the remote has main after the person said no")
		}
		if !strings.Contains(out, "Pushed nothing") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("an agent", func(t *testing.T) {
		home, _, remote := newMemory(t)
		t.Setenv("CLAUDECODE", "1")
		_, err := runWith(t, Env{Dir: home, Confirm: guards.Confirm}, "push")
		if err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
			t.Fatalf("error = %v, want a refusal that names CLAUDECODE", err)
		}
		if _, err := store.Git(remote, "rev-parse", "--verify", "-q", "main"); err == nil {
			t.Error("the remote has main after the refusal")
		}
	})

	t.Run("nothing to push", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		gitT(t, repo, "push", "-q", "origin", "main")
		asked := ""
		out, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push")
		if err != nil || asked != "" || !strings.Contains(out, "Nothing to push") {
			t.Errorf("push = %v, asked %q, output %q", err, asked, out)
		}
	})
}

func TestInbox(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, _, remote := newMemory(t)

	out, err := run(t, home, "", "inbox")
	if err != nil {
		t.Fatalf("inbox: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 unpushed commit") || !strings.Contains(out, "init: create the memory repo") {
		t.Errorf("inbox before the push = %q", out)
	}

	var asked string
	if out, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if asked != "Push 1 commit to origin? [y/N] " {
		t.Errorf("prompt = %q", asked)
	}
	out, err = run(t, home, "", "inbox")
	if err != nil {
		t.Fatalf("inbox: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing waits for review") {
		t.Errorf("inbox after the push = %q", out)
	}

	remember(t, mkdirT(t, filepath.Join(home, "dev")), "small-commits", "Read before you commit: one change per commit.")
	out, _ = run(t, home, "", "inbox")
	if !strings.Contains(out, "remember: dev/memories/small-commits.md") || strings.Contains(out, "init: create") {
		t.Errorf("inbox after a new memory = %q", out)
	}

	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, home, "", "inbox")
	if err != nil {
		t.Fatalf("inbox without the remote: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning: fetch the remote") || !strings.Contains(out, "remember: dev/memories/small-commits.md") {
		t.Errorf("inbox without the remote = %q, want a warning and the queue", out)
	}
}

// pushYes runs push with a confirmer that says yes, and fails the test on an error.
func pushYes(t *testing.T, dir string) string {
	t.Helper()
	var asked string
	out, err := runWith(t, Env{Dir: dir, Confirm: answer(true, &asked)}, "push")
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	return out
}

// subjects returns the subjects of the remote main, newest first.
func subjects(t *testing.T, remote string) string {
	t.Helper()
	return strings.TrimSpace(gitT(t, remote, "log", "--format=%s", "main"))
}

func TestFold(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	revision := func(name, description, body string) string { return memoryText(name, description, body) }

	t.Run("a revision folds into its remember", func(t *testing.T) {
		home, _, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", "Read when you test a fact.")
		if out, err := run(t, dev, revision("a-fact", "Read when you test a fact.", "A sharper fact."), "revise", "a-fact", "--reason", "Sharper."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		pushYes(t, home)
		if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q", got)
		}
		if got := gitT(t, remote, "show", "main:dev/memories/a-fact.md"); !strings.Contains(got, "A sharper fact.") {
			t.Errorf("remote memory = %q, want the revision", got)
		}
	})

	t.Run("a remember, then a forget, leave no commit", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "kept-fact", "Read before you keep a fact.")
		remember(t, dev, "mistake", "Read when you test a mistake.")
		if out, err := run(t, dev, "", "forget", "mistake", "--reason", "A mistake."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		out := pushYes(t, home)
		if got := subjects(t, remote); got != "remember: dev/memories/kept-fact.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q\n%s", got, out)
		}
		if got := gitT(t, remote, "show", "main:dev/README.md"); strings.Contains(got, "mistake") {
			t.Errorf("remote index = %q, want no mistake", got)
		}
		clean(t, repo)
	})

	t.Run("a fixup moved past a later memory at the same level", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "first-fact", "Read when you test the first fact.")
		remember(t, dev, "second-fact", "Read before you deploy on Fridays.")
		if out, err := run(t, dev, revision("first-fact", "Read when you test the first fact, now revised.", "A fact."), "revise", "first-fact", "--reason", "A sharper line."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		pushYes(t, home)
		if got := subjects(t, remote); got != "remember: dev/memories/second-fact.md\nremember: dev/memories/first-fact.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q", got)
		}
		ix, err := store.Lines(repo, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if got := gitT(t, remote, "show", "main:dev/README.md"); got != store.Render(ix) || !strings.Contains(got, "now revised") {
			t.Errorf("remote index =\n%s\nwant the generated index with the revision", got)
		}
		first := strings.Fields(gitT(t, remote, "rev-list", "--reverse", "main"))[1]
		if got := gitT(t, remote, "show", first+":dev/README.md"); strings.Contains(got, "second-fact") || !strings.Contains(got, "now revised") {
			t.Errorf("index at the first remember =\n%s\nwant only the revised first fact", got)
		}
	})

	t.Run("nested fixups fold", func(t *testing.T) {
		home, _, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", "Read when you test a fact.")
		for _, body := range []string{"Once.", "Twice."} {
			if out, err := run(t, dev, revision("a-fact", "Read when you test a fact.", body), "revise", "a-fact", "--reason", body); err != nil {
				t.Fatalf("revise: %v\n%s", err, out)
			}
		}
		pushYes(t, home)
		if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q", got)
		}
		if got := gitT(t, remote, "show", "main:dev/memories/a-fact.md"); !strings.Contains(got, "Twice.") {
			t.Errorf("remote memory = %q, want the last revision", got)
		}
	})

	t.Run("the accepted commits keep their shas", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "pushed-fact", "Read when you test a pushed fact.")
		pushYes(t, home)
		accepted := strings.TrimSpace(gitT(t, remote, "rev-parse", "main"))
		remember(t, dev, "later-fact", "Read before you deploy on Fridays.")
		if out, err := run(t, dev, revision("later-fact", "Read before you deploy on Fridays.", "Never."), "revise", "later-fact", "--reason", "Firmer."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		pushYes(t, home)
		if got := strings.TrimSpace(gitT(t, remote, "rev-parse", "main~1")); got != accepted {
			t.Errorf("the accepted commit moved from %s to %s", accepted, got)
		}
		if e := last(t, repo); len(e.Folded) != 1 {
			t.Errorf("folded = %q, want the one fixup", e.Folded)
		}
	})

	t.Run("a dropped chain that made the level of a later memory", func(t *testing.T) {
		home, _, remote := newMemory(t)
		lab := mkdirT(t, filepath.Join(home, "lab"))
		remember(t, lab, "first-lab", "Read when you test the first lab fact.")
		remember(t, lab, "kept-lab", "Read before you deploy on Fridays.")
		if out, err := run(t, lab, "", "forget", "first-lab", "--reason", "A mistake."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		pushYes(t, home)
		if got := gitT(t, remote, "show", "main:README.md"); !strings.Contains(got, "[lab](lab/README.md)") {
			t.Errorf("remote root index =\n%s\nwant the level lab", got)
		}
		if got := gitT(t, remote, "show", "main:lab/README.md"); strings.Contains(got, "first-lab") || !strings.Contains(got, "kept-lab") {
			t.Errorf("remote lab index =\n%s\nwant only kept-lab", got)
		}
	})

	t.Run("a revision back to the accepted text, then on", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		note := "---\nname: trial\ndescription: \"Read when you try it.\"\n---\n\n"
		writeT(t, repo, "dev/notes/trial.md", note+"A.\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a note")
		pushYes(t, home)
		for _, body := range []string{"B.", "A.", "C."} {
			if out, err := run(t, dev, note+body+"\n", "revise", "dev/notes/trial.md", "--reason", body); err != nil {
				t.Fatalf("revise to %s: %v\n%s", body, err, out)
			}
		}
		pushYes(t, home)
		if got := gitT(t, remote, "show", "main:dev/notes/trial.md"); !strings.HasSuffix(got, "C.\n") {
			t.Errorf("remote note = %q, want C", got)
		}
		if got := strings.Fields(subjects(t, remote))[0]; got != "revise:" {
			t.Errorf("remote history = %q, want one revise on top", subjects(t, remote))
		}
		clean(t, repo)
	})

	t.Run("a chain that changes only a mode", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		tool := filepath.Join(repo, "dev", "bin", "tool")
		writeT(t, repo, "dev/bin/tool", "#!/bin/sh\n# Run when you test.\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a helper")
		pushYes(t, home)
		writeT(t, repo, "dev/bin/tool", "#!/bin/sh\n# Run when you test.\nexit 0\n")
		if err := os.Chmod(tool, 0o755); err != nil {
			t.Fatal(err)
		}
		gitT(t, repo, "commit", "-q", "-am", "test: a script that runs")
		if out, err := run(t, dev, "#!/bin/sh\n# Run when you test.\n", "revise", "dev/bin/tool", "--reason", "Back to the text."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		pushYes(t, home)
		if got := gitT(t, remote, "ls-tree", "main", "dev/bin/tool"); !strings.HasPrefix(got, "100755 ") {
			t.Errorf("remote mode = %q, want 100755", got)
		}
	})

	t.Run("a repo that reschedules a failed exec", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		gitT(t, repo, "config", "rebase.rescheduleFailedExec", "true")
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", "Read when you test a fact.")
		if out, err := run(t, dev, revision("a-fact", "Read when you test a fact.", "Sharper."), "revise", "a-fact", "--reason", "Sharper."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		pushYes(t, home)
		if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
			t.Errorf("remote history = %q", got)
		}
	})

	t.Run("an uncommitted change refuses", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", "Read when you test a fact.")
		writeT(t, repo, "dev/notes/draft.md", "A note by hand.\n")
		var asked string
		_, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push")
		if err == nil || !strings.Contains(err.Error(), "dev/notes/draft.md") {
			t.Errorf("push: error = %v, want a refusal that names the uncommitted file", err)
		}
		if _, err := store.Git(remote, "rev-parse", "--verify", "-q", "main"); err == nil {
			t.Error("the remote has main after the refusal")
		}
	})
}
