package commands

import (
	"os"
	"path/filepath"
	"slices"
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

// foldCase is a review queue that push folds. queue builds it in a new memory repo, with any push before it, and
// returns the check of what the next push leaves. preview checks the preview of the queue beyond its equality with
// the push, or is nil.
type foldCase struct {
	name    string
	queue   func(t *testing.T, home, repo, remote string) (pushed func(t *testing.T, out string))
	preview func(t *testing.T, repo string, got []previewed)
}

// reviseT runs revise of a document in dir with stdin, and fails the test on an error.
func reviseT(t *testing.T, dir, stdin, name, reason string) {
	t.Helper()
	if out, err := run(t, dir, stdin, "revise", name, "--reason", reason); err != nil {
		t.Fatalf("revise %s: %v\n%s", name, err, out)
	}
}

// commitAt commits every change of the working tree with a message and an author date, as the person would.
func commitAt(t *testing.T, repo, message, date string) {
	t.Helper()
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "--date", date, "-m", message)
}

// handIndex commits, by hand, the index of ~/dev that a description of a-fact would give. The memory keeps its text.
func handIndex(t *testing.T, repo, description, message, date string) {
	t.Helper()
	p := filepath.Join(repo, "dev", "memories", "a-fact.md")
	text := readT(t, p)
	writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", description, "A fact."))
	if err := store.WriteIndex(repo, "dev"); err != nil {
		t.Fatal(err)
	}
	writeT(t, repo, "dev/memories/a-fact.md", text)
	commitAt(t, repo, message, date)
}

// foldCases holds the review queues that TestFold and TestPreviewMatchesPush share.
func foldCases() []foldCase {
	const (
		fact     = "Read when you test a fact."
		sharper  = "Read when you test a fact, now sharper."
		someDate = "2026-11-01T10:00:00Z"
	)
	return []foldCase{
		{
			name: "a revision folds into its remember",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				dev := mkdirT(t, filepath.Join(home, "dev"))
				remember(t, dev, "a-fact", fact)
				reviseT(t, dev, memoryText("a-fact", fact, "A sharper fact."), "a-fact", "Sharper.")
				return func(t *testing.T, out string) {
					if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
						t.Errorf("remote history = %q", got)
					}
					if got := gitT(t, remote, "show", "main:dev/memories/a-fact.md"); !strings.Contains(got, "A sharper fact.") {
						t.Errorf("remote memory = %q, want the revision", got)
					}
				}
			},
		},
		{
			name: "a remember, then a forget, leave no commit",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				dev := mkdirT(t, filepath.Join(home, "dev"))
				remember(t, dev, "kept-fact", "Read before you keep a fact.")
				remember(t, dev, "mistake", "Read when you test a mistake.")
				if out, err := run(t, dev, "", "forget", "mistake", "--reason", "A mistake."); err != nil {
					t.Fatalf("forget: %v\n%s", err, out)
				}
				return func(t *testing.T, out string) {
					if got := subjects(t, remote); got != "remember: dev/memories/kept-fact.md\ninit: create the memory repo" {
						t.Errorf("remote history = %q\n%s", got, out)
					}
					if got := gitT(t, remote, "show", "main:dev/README.md"); strings.Contains(got, "mistake") {
						t.Errorf("remote index = %q, want no mistake", got)
					}
					clean(t, repo)
				}
			},
		},
		{
			name: "a fixup moved past a later memory at the same level",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				dev := mkdirT(t, filepath.Join(home, "dev"))
				remember(t, dev, "first-fact", "Read when you test the first fact.")
				remember(t, dev, "second-fact", "Read before you deploy on Fridays.")
				reviseT(t, dev, memoryText("first-fact", "Read when you test the first fact, now revised.", "A fact."), "first-fact", "A sharper line.")
				return func(t *testing.T, out string) {
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
				}
			},
		},
		{
			name: "nested fixups fold",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				dev := mkdirT(t, filepath.Join(home, "dev"))
				remember(t, dev, "a-fact", fact)
				for _, body := range []string{"Once.", "Twice."} {
					reviseT(t, dev, memoryText("a-fact", fact, body), "a-fact", body)
				}
				return func(t *testing.T, out string) {
					if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
						t.Errorf("remote history = %q", got)
					}
					if got := gitT(t, remote, "show", "main:dev/memories/a-fact.md"); !strings.Contains(got, "Twice.") {
						t.Errorf("remote memory = %q, want the last revision", got)
					}
				}
			},
		},
		{
			name: "the accepted commits keep their shas",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				dev := mkdirT(t, filepath.Join(home, "dev"))
				remember(t, dev, "pushed-fact", "Read when you test a pushed fact.")
				pushYes(t, home)
				accepted := strings.TrimSpace(gitT(t, remote, "rev-parse", "main"))
				remember(t, dev, "later-fact", "Read before you deploy on Fridays.")
				reviseT(t, dev, memoryText("later-fact", "Read before you deploy on Fridays.", "Never."), "later-fact", "Firmer.")
				return func(t *testing.T, out string) {
					if got := strings.TrimSpace(gitT(t, remote, "rev-parse", "main~1")); got != accepted {
						t.Errorf("the accepted commit moved from %s to %s", accepted, got)
					}
					if e := last(t, repo); len(e.Folded) != 1 {
						t.Errorf("folded = %q, want the one fixup", e.Folded)
					}
				}
			},
		},
		{
			name: "a dropped chain that made the level of a later memory",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				lab := mkdirT(t, filepath.Join(home, "lab"))
				remember(t, lab, "first-lab", "Read when you test the first lab fact.")
				remember(t, lab, "kept-lab", "Read before you deploy on Fridays.")
				if out, err := run(t, lab, "", "forget", "first-lab", "--reason", "A mistake."); err != nil {
					t.Fatalf("forget: %v\n%s", err, out)
				}
				return func(t *testing.T, out string) {
					if got := gitT(t, remote, "show", "main:README.md"); !strings.Contains(got, "[lab](lab/README.md)") {
						t.Errorf("remote root index =\n%s\nwant the level lab", got)
					}
					if got := gitT(t, remote, "show", "main:lab/README.md"); strings.Contains(got, "first-lab") || !strings.Contains(got, "kept-lab") {
						t.Errorf("remote lab index =\n%s\nwant only kept-lab", got)
					}
				}
			},
		},
		{
			name: "a revision back to the accepted text, then on",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				dev := mkdirT(t, filepath.Join(home, "dev"))
				note := "---\nname: trial\ndescription: \"Read when you try it.\"\n---\n\n"
				writeT(t, repo, "dev/notes/trial.md", note+"A.\n")
				gitT(t, repo, "add", "-A")
				gitT(t, repo, "commit", "-q", "-m", "test: a note")
				pushYes(t, home)
				for _, body := range []string{"B.", "A.", "C."} {
					reviseT(t, dev, note+body+"\n", "dev/notes/trial.md", body)
				}
				return func(t *testing.T, out string) {
					if got := gitT(t, remote, "show", "main:dev/notes/trial.md"); !strings.HasSuffix(got, "C.\n") {
						t.Errorf("remote note = %q, want C", got)
					}
					if got := strings.Fields(subjects(t, remote))[0]; got != "revise:" {
						t.Errorf("remote history = %q, want one revise on top", subjects(t, remote))
					}
					clean(t, repo)
				}
			},
		},
		{
			name: "a chain that changes only a mode",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
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
				reviseT(t, dev, "#!/bin/sh\n# Run when you test.\n", "dev/bin/tool", "Back to the text.")
				return func(t *testing.T, out string) {
					if got := gitT(t, remote, "ls-tree", "main", "dev/bin/tool"); !strings.HasPrefix(got, "100755 ") {
						t.Errorf("remote mode = %q, want 100755", got)
					}
				}
			},
		},
		{
			name: "a repo that reschedules a failed exec",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				gitT(t, repo, "config", "rebase.rescheduleFailedExec", "true")
				dev := mkdirT(t, filepath.Join(home, "dev"))
				remember(t, dev, "a-fact", fact)
				reviseT(t, dev, memoryText("a-fact", fact, "Sharper."), "a-fact", "Sharper.")
				return func(t *testing.T, out string) {
					if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
						t.Errorf("remote history = %q", got)
					}
				}
			},
		},
		{
			name: "a target keeps its body, and the body of its fixup goes",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				dev := mkdirT(t, filepath.Join(home, "dev"))
				note := "---\nname: trial\ndescription: \"Read when you try it.\"\n---\n\n"
				writeT(t, repo, "dev/notes/trial.md", note+"A.\n")
				commitAt(t, repo, "test: a note\n\nThe body of the target.", someDate)
				reviseT(t, dev, note+"B.\n", "dev/notes/trial.md", "The body of the fixup.")
				return func(t *testing.T, out string) {
					if got := gitT(t, remote, "log", "--format=%B", "main"); !strings.Contains(got, "The body of the target.") || strings.Contains(got, "The body of the fixup.") {
						t.Errorf("remote messages =\n%s\nwant the body of the target only", got)
					}
				}
			},
			preview: func(t *testing.T, repo string, got []previewed) {
				var bodies []string
				for _, p := range got {
					bodies = append(bodies, p.body)
				}
				if !slices.Contains(bodies, "The body of the target.") || slices.ContainsFunc(bodies, func(b string) bool { return strings.Contains(b, "The body of the fixup.") }) {
					t.Errorf("bodies = %q, want the body of the target only", bodies)
				}
			},
		},
		{
			name: "two chains with one subject, and the fold drops the first",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				dev := mkdirT(t, filepath.Join(home, "dev"))
				remember(t, dev, "a-fact", fact)
				if out, err := run(t, dev, "", "forget", "a-fact", "--reason", "A mistake."); err != nil {
					t.Fatalf("forget: %v\n%s", err, out)
				}
				remember(t, dev, "a-fact", sharper)
				reviseT(t, dev, memoryText("a-fact", sharper, "Sharper."), "a-fact", "Sharper.")
				return func(t *testing.T, out string) {
					if got := subjects(t, remote); got != "remember: dev/memories/a-fact.md\ninit: create the memory repo" {
						t.Errorf("remote history = %q", got)
					}
				}
			},
			preview: func(t *testing.T, repo string, got []previewed) {
				second := sha(t, repo, "remember: dev/memories/a-fact.md") // the newest commit with the subject
				fixup := sha(t, repo, "fixup! remember: dev/memories/a-fact.md")
				if len(got) != 2 || got[1].target != second || !slices.Equal(got[1].fixups, []string{fixup}) {
					t.Errorf("preview = %+v, want init, then the second remember %s with the fixup %s", got, second, fixup)
				}
			},
		},
		{
			name: "a target that an earlier fixup empties",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				dev := mkdirT(t, filepath.Join(home, "dev"))
				lab := mkdirT(t, filepath.Join(home, "lab"))
				remember(t, dev, "a-fact", fact)
				handIndex(t, repo, sharper, "test: an index by hand", someDate)
				reviseT(t, dev, memoryText("a-fact", sharper, "A fact."), "a-fact", "Sharper.")
				remember(t, lab, "b-fact", "Read before you deploy on Fridays.")
				return func(t *testing.T, out string) {
					if got := subjects(t, remote); got != "remember: lab/memories/b-fact.md\nremember: dev/memories/a-fact.md\ninit: create the memory repo" {
						t.Errorf("remote history = %q", got)
					}
					if got := gitT(t, remote, "show", "main:dev/README.md"); !strings.Contains(got, sharper) {
						t.Errorf("remote index =\n%s\nwant the sharper description", got)
					}
				}
			},
			preview: func(t *testing.T, repo string, got []previewed) {
				var targets []string
				for _, p := range got {
					targets = append(targets, p.target)
				}
				want := []string{sha(t, repo, "init: create the memory repo"), sha(t, repo, "remember: dev/memories/a-fact.md"), sha(t, repo, "remember: lab/memories/b-fact.md")}
				if !slices.Equal(targets, want) {
					t.Errorf("targets = %q, want %q", targets, want)
				}
			},
		},
		{
			// The root index lists the empty folder, and the stop after the edit amends that line into it.
			name: "an empty folder of a level",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", fact)
				pushYes(t, home)
				writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "An edit."))
				commitAt(t, repo, "test: an edit by hand", someDate)
				writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "A second edit."))
				commitAt(t, repo, "fixup! test: an edit by hand", someDate)
				mkdirT(t, filepath.Join(repo, "lab"))
				return func(t *testing.T, out string) {
					if got := gitT(t, remote, "show", "--name-only", "--format=%s", "main"); got != "test: an edit by hand\n\nREADME.md\ndev/memories/a-fact.md\n" {
						t.Errorf("remote top commit =\n%s\nwant the edit with the root index", got)
					}
					if got := gitT(t, remote, "show", "main:README.md"); !strings.Contains(got, "lab/README.md") {
						t.Errorf("remote root index =\n%s\nwant the empty level lab", got)
					}
				}
			},
		},
		{
			// The stop after the edit amends the root index into it, and git cleans the message of an amend.
			name: "a repo that strips comment lines from messages",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", fact)
				pushYes(t, home)
				gitT(t, repo, "config", "commit.cleanup", "strip")
				writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "An edit."))
				gitT(t, repo, "add", "-A")
				gitT(t, repo, "commit", "-q", "--cleanup=verbatim", "-m", "test: an edit by hand\n\n# A line that the cleanup strips.\nThe body.")
				writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "A second edit."))
				commitAt(t, repo, "fixup! test: an edit by hand", someDate)
				mkdirT(t, filepath.Join(repo, "lab"))
				return func(t *testing.T, out string) {
					if got := gitT(t, remote, "log", "-1", "--format=%B", "main"); strings.Contains(got, "# A line") || !strings.Contains(got, "The body.") {
						t.Errorf("remote message = %q, want the body without the comment line", got)
					}
				}
			},
		},
		{
			name: "a stale index and an empty queue",
			queue: func(t *testing.T, home, repo, remote string) func(*testing.T, string) {
				writeT(t, repo, "dev/memories/by-hand.md", memoryText("by-hand", "Read when you edit by hand.", "A fact."))
				gitT(t, repo, "add", "-A")
				gitT(t, repo, "commit", "-q", "-m", "test: a memory by hand")
				gitT(t, repo, "push", "-q", "origin", "main")
				return func(t *testing.T, out string) {
					if got := subjects(t, remote); !strings.HasPrefix(got, "index: ") {
						t.Errorf("remote history = %q, want an index on top", got)
					}
				}
			},
		},
	}
}

func TestFold(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	for _, c := range foldCases() {
		t.Run(c.name, func(t *testing.T) {
			home, repo, remote := newMemory(t)
			check := c.queue(t, home, repo, remote)
			check(t, pushYes(t, home))
		})
	}

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

func TestStaleIndex(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, remote := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "small-commits", "Read before you commit: one change per commit.")
	writeT(t, repo, "dev/memories/by-hand.md", memoryText("by-hand", "Read when you edit by hand.", "A fact."))
	writeT(t, repo, "lab/notes/trial.md", "---\nname: trial\ndescription: \"Read when you try the lab.\"\n---\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "test: documents by hand")

	out, err := run(t, home, "", "inbox")
	if err != nil {
		t.Fatalf("inbox: %v\n%s", err, out)
	}
	for _, p := range []string{"dev/README.md", "lab/README.md", "  README.md"} {
		if !strings.Contains(out, p) {
			t.Errorf("inbox = %q, want the stale index %s", out, p)
		}
	}

	writeT(t, repo, "dev/README.md", "# ~/dev\n\nAn edit by hand.\n")
	before := gitT(t, repo, "rev-parse", "HEAD")
	var asked string
	if _, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err == nil || !strings.Contains(err.Error(), "dev/README.md") {
		t.Errorf("push over an uncommitted index: error = %v, want a refusal that names it", err)
	}
	if got := readT(t, filepath.Join(repo, "dev", "README.md")); got != "# ~/dev\n\nAn edit by hand.\n" {
		t.Errorf("the uncommitted index = %q, want it as the person left it", got)
	}
	if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
		t.Error("the refused push moved HEAD")
	}
	gitT(t, repo, "checkout", "--", "dev/README.md")

	pushYes(t, home)
	history := subjects(t, remote)
	for _, s := range []string{"index: dev/README.md", "index: lab/README.md", "index: README.md"} {
		if !strings.Contains(history, s) {
			t.Errorf("remote history = %q, want %s", history, s)
		}
	}
	if out, _ := run(t, home, "", "inbox"); strings.Contains(out, "README.md") {
		t.Errorf("inbox after the push = %q, want no stale index", out)
	}
}

func TestStaleIndexEdges(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	t.Run("a stale index and a fixup", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", "Read when you test a fact.")
		if out, err := run(t, dev, memoryText("a-fact", "Read when you test a fact.", "Sharper."), "revise", "a-fact", "--reason", "Sharper."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		writeT(t, repo, "dev/memories/by-hand.md", memoryText("by-hand", "Read before you deploy on Fridays.", "A fact."))
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a memory by hand")
		if out, _ := run(t, home, "", "inbox"); !strings.Contains(out, "dev/README.md") {
			t.Fatalf("inbox = %q, want the stale index dev/README.md", out)
		}
		pushYes(t, home)
		if got := gitT(t, remote, "show", "main:dev/README.md"); !strings.Contains(got, "by-hand") {
			t.Errorf("remote index =\n%s\nwant the memory by hand", got)
		}
		for _, sha := range strings.Fields(gitT(t, remote, "rev-list", "main")) {
			if parents := strings.Fields(gitT(t, remote, "rev-list", "--parents", "-n", "1", sha)); len(parents) > 1 {
				if diff := gitT(t, remote, "diff", "--stat", parents[1], sha); strings.TrimSpace(diff) == "" {
					t.Errorf("the remote holds an empty commit %s", sha)
				}
			}
		}
	})

	t.Run("a level with only a helper", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		writeT(t, repo, "lab/bin/helper.sh", "# A helper, not a script.\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a helper by hand")
		if out, _ := run(t, home, "", "inbox"); !strings.Contains(out, "lab/README.md") {
			t.Errorf("inbox = %q, want the missing index lab/README.md", out)
		}
		pushYes(t, home)
		if _, err := store.Git(remote, "cat-file", "-e", "main:lab/README.md"); err != nil {
			t.Errorf("the remote has no lab/README.md: %v", err)
		}
	})

	t.Run("a failed index commit leaves the tree clean", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		writeT(t, repo, "dev/notes/n.md", "---\nname: n\ndescription: \"Read when you test.\"\n---\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a note by hand")
		hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
		writeT(t, filepath.Dir(hook), "pre-commit", "#!/bin/sh\nexit 1\n")
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}
		var asked string
		if _, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err == nil {
			t.Error("push with a failing hook: no error")
		}
		clean(t, repo)
		os.Remove(hook)
		pushYes(t, home)
	})
}
