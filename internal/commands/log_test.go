package commands

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// events reads the use log of a memory repo.
func events(t *testing.T, repo string) []Event {
	t.Helper()
	f, err := os.Open(filepath.Join(store.StateDir(repo), "log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var all []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("log line %q: %v", sc.Text(), err)
		}
		all = append(all, e)
	}
	return all
}

// last returns the last event of the use log.
func last(t *testing.T, repo string) Event {
	t.Helper()
	all := events(t, repo)
	if len(all) == 0 {
		t.Fatal("the use log is empty")
	}
	return all[len(all)-1]
}

func TestLog(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	t.Setenv("CLAUDE_CODE_SESSION_ID", "session-1")

	t.Run("remember", func(t *testing.T) {
		before := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
		out, err := run(t, dev, memoryText("small-commits", "Read before you commit: one change per commit.", "Commit one change, body-token-5521."), "remember")
		if err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		e := last(t, repo)
		after := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
		if e.Command != "remember" || e.Outcome != "ok" || e.Error != "" {
			t.Errorf("event = %+v, want an ok remember", e)
		}
		if e.Session != "session-1" || e.Agent != "claude" {
			t.Errorf("session %q, agent %q, want session-1 and claude", e.Session, e.Agent)
		}
		if e.Level != "~/dev" || e.Commit != before {
			t.Errorf("level %q, commit %q, want ~/dev and %s", e.Level, e.Commit, before)
		}
		if len(e.Paths) != 1 || e.Paths[0] != "dev/memories/small-commits.md" {
			t.Errorf("paths = %q", e.Paths)
		}
		if len(e.Made) != 1 || e.Made[0] != after {
			t.Errorf("made = %q, want [%s]", e.Made, after)
		}
		if e.Time == "" {
			t.Error("the event has no time")
		}
	})

	t.Run("orient", func(t *testing.T) {
		if out, err := run(t, dev, "", "orient"); err != nil {
			t.Fatalf("orient: %v\n%s", err, out)
		}
		e := last(t, repo)
		for _, level := range []string{"header", "~/dev", "~"} {
			if e.Words[level] == 0 {
				t.Errorf("words = %v, want a count for %s", e.Words, level)
			}
		}
	})

	t.Run("a failed show", func(t *testing.T) {
		if _, err := run(t, dev, "", "show", "dev/memories/absent.md"); err == nil {
			t.Fatal("show of an absent path: no error")
		}
		e := last(t, repo)
		if e.Command != "show" || e.Outcome != "error" || !strings.Contains(e.Error, "no document") {
			t.Errorf("event = %+v, want a failed show", e)
		}
	})

	t.Run("malformed input", func(t *testing.T) {
		in := "---\nname: leak\nthe secret-token-9917 sits here\n---\n\nBody.\n"
		if _, err := run(t, dev, in, "remember"); err == nil {
			t.Fatal("remember of malformed input: no error")
		}
		e := last(t, repo)
		if e.Outcome != "error" || !strings.Contains(e.Error, "frontmatter line 3") {
			t.Errorf("event = %+v, want the cause of the error", e)
		}
	})

	t.Run("a name too long for the file system", func(t *testing.T) {
		name := "zz-token-8812-" + strings.Repeat("a", 300)
		if _, err := run(t, dev, memoryText(name, "Read when a name runs long.", "A fact."), "remember"); err == nil {
			t.Fatal("remember of a name over 255 bytes: no error")
		}
		if e := last(t, repo); e.Outcome != "error" || strings.Contains(e.Error, "zz-token-8812") {
			t.Errorf("event = %+v, want an error without the name", e)
		}
	})

	t.Run("an unknown command", func(t *testing.T) {
		var out strings.Builder
		err := Run(Env{Dir: dev, In: strings.NewReader(""), Out: &out, Err: &out}, "forgett", nil)
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("error = %v, want ErrUnknown", err)
		}
		if e := last(t, repo); e.Command != "forgett" || e.Outcome != "error" {
			t.Errorf("event = %+v, want a failed forgett", e)
		}
	})

	t.Run("push", func(t *testing.T) {
		queue := strings.Fields(gitT(t, repo, "rev-list", "--reverse", "HEAD"))
		var asked string
		if out, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err != nil {
			t.Fatalf("push: %v\n%s", err, out)
		}
		e := last(t, repo)
		if strings.Join(e.Pushed, " ") != strings.Join(queue, " ") {
			t.Errorf("pushed = %q, want %q", e.Pushed, queue)
		}
		if e.Accepted != "" {
			t.Errorf("accepted = %q before the first push, want none", e.Accepted)
		}
		if _, err := run(t, dev, "", "show", "skills/voice/SKILL.md"); err != nil {
			t.Fatalf("show: %v", err)
		}
		if e, want := last(t, repo), strings.TrimSpace(gitT(t, repo, "rev-parse", "origin/main")); e.Accepted != want {
			t.Errorf("accepted = %q after the push, want %s", e.Accepted, want)
		}
	})

	t.Run("no standard input in the log", func(t *testing.T) {
		log := readT(t, filepath.Join(store.StateDir(repo), "log.jsonl"))
		for _, token := range []string{"body-token-5521", "secret-token-9917"} {
			if strings.Contains(log, token) {
				t.Errorf("the use log holds %q from standard input", token)
			}
		}
		clean(t, repo)
	})
}

func TestLogOutsideRepo(t *testing.T) {
	home := newHome(t)
	if _, err := run(t, home, "", "init", filepath.Join(home, "no-such-remote")); err == nil {
		t.Fatal("init with a missing remote: no error")
	}
	if _, err := os.Stat(filepath.Join(home, "memory")); err == nil {
		t.Error("a failed init left ~/memory")
	}
}

// kept lists the kept refs of a memory repo, by the commit that each keeps.
func kept(t *testing.T, repo string) map[string]bool {
	t.Helper()
	refs := map[string]bool{}
	for _, sha := range strings.Fields(gitT(t, repo, "for-each-ref", "--format=%(objectname)", "refs/memcore/seen/")) {
		refs[sha] = true
	}
	return refs
}

func TestKeptRefs(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, remote := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))

	remember(t, dev, "small-commits", "Read before you commit: one change per commit.")
	first := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	if !kept(t, repo)[first] {
		t.Errorf("the commit of remember, %s, has no kept ref", first)
	}

	before := len(kept(t, repo))
	var asked string
	if out, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if after := len(kept(t, repo)); after < before {
		t.Errorf("push left %d kept refs of %d", after, before)
	}
	if e := last(t, repo); !kept(t, repo)[e.Commit] || len(e.Pushed) == 0 || !kept(t, repo)[e.Pushed[len(e.Pushed)-1]] {
		t.Errorf("a push whose tip is its commit, %s, left it without a kept ref", e.Commit)
	}

	for ref := range kept(t, repo) {
		gitT(t, repo, "update-ref", "-d", "refs/memcore/seen/"+ref)
	}
	if out, err := run(t, dev, "", "orient"); err != nil {
		t.Fatalf("orient: %v\n%s", err, out)
	}
	if !kept(t, repo)[first] {
		t.Errorf("orient saw the accepted commit %s, and it has no kept ref", first)
	}

	remember(t, dev, "tests-first", "Read before you write code: the tests come first.")
	second := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	gitT(t, repo, "reset", "-q", "--hard", "HEAD~1")
	gitT(t, repo, "reflog", "expire", "--expire=now", "--all")
	gitT(t, repo, "gc", "-q", "--prune=now")
	if _, err := store.Git(repo, "cat-file", "-e", second); err != nil {
		t.Errorf("the rewritten commit %s is gone: %v", second, err)
	}

	if out := gitT(t, remote, "for-each-ref", "refs/memcore/"); strings.TrimSpace(out) != "" {
		t.Errorf("the remote holds kept refs: %s", out)
	}
}
