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
