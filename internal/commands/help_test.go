package commands

import (
	"path/filepath"
	"strings"
	"testing"
)

// example returns the input between the heredoc lines of a help text.
func example(t *testing.T, help string) string {
	t.Helper()
	_, rest, ok := strings.Cut(help, "<<'EOF'\n")
	if !ok {
		t.Fatalf("the help holds no example:\n%s", help)
	}
	in, _, ok := strings.Cut(rest, "\nEOF")
	if !ok {
		t.Fatalf("the example has no EOF line:\n%s", help)
	}
	return in + "\n"
}

func TestHelp(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))

	t.Run("the example of remember commits", func(t *testing.T) {
		help, err := run(t, home, "", "help", "remember")
		if err != nil {
			t.Fatalf("help remember: %v\n%s", err, help)
		}
		for _, key := range []string{"name", "description", "type", "sessions"} {
			if !strings.Contains(help, "  "+key+" ") {
				t.Errorf("the help does not explain %s:\n%s", key, help)
			}
		}
		if out, err := run(t, dev, example(t, help), "remember"); err != nil {
			t.Fatalf("remember of the example: %v\n%s", err, out)
		}
		if got := head(t, repo); !strings.HasPrefix(got, "remember: dev/memories/") {
			t.Errorf("commit = %q", got)
		}
	})

	t.Run("drop records the reason and promises nothing", func(t *testing.T) {
		help, err := run(t, home, "", "help", "drop")
		if err != nil {
			t.Fatalf("help drop: %v\n%s", err, help)
		}
		if !strings.Contains(help, "and records the reason.\n") || strings.Contains(help, "sleep") {
			t.Errorf("help drop = %q", help)
		}
	})

	t.Run("--help", func(t *testing.T) {
		help, _ := run(t, home, "", "help", "remember")
		for _, flag := range []string{"--help", "-h"} {
			if out, err := run(t, home, "", "remember", flag); err != nil || out != help {
				t.Errorf("remember %s: %v, output %q, want the help", flag, err, out)
			}
		}
	})

	t.Run("the list", func(t *testing.T) {
		out, err := run(t, home, "", "help")
		if err != nil || !strings.Contains(out, "remember") || !strings.Contains(out, "memcore stats") || !strings.Contains(out, "memcore help <command>") {
			t.Errorf("help: %v, output %q, want the list of commands", err, out)
		}
		out, err = run(t, home, "", "help", "nope")
		if err == nil || !strings.Contains(out, "remember") {
			t.Errorf("help nope: %v, output %q, want an error and the list", err, out)
		}
	})

	t.Run("push", func(t *testing.T) {
		row := "  memcore push                 Fetches, folds and pushes the unpushed commits. The person confirms at the terminal. An agent runs it only when the person asks, and pushes only memories, notes, plans and indexes.\n"
		if out, err := run(t, home, "", "help"); err != nil || !strings.Contains(out, row) {
			t.Errorf("help: %v, output %q, want the row %q", err, out, row)
		}
		want := `memcore push [--expect <commit>]

The person runs it at a terminal, and confirms. An agent runs it only when the person asks.
It refuses uncommitted changes, fetches, and folds each fixup into the commit it corrects.
Then it pushes the unpushed commits. An agent pushes only memories, notes, plans and
indexes. For a skill, a script, a helper, a conventions file or a glossary, it names the
commits, and the person pushes them. With --expect <commit>, it pushes nothing unless main
is at that commit, so it pushes only the queue that the person reviewed.
`
		if out, err := run(t, home, "", "help", "push"); err != nil || out != want {
			t.Errorf("help push: %v, output\n%s\nwant\n%s", err, out, want)
		}
	})

	t.Run("ui names the application", func(t *testing.T) {
		out, err := run(t, home, "", "help", "ui")
		if err != nil || !strings.HasPrefix(out, "memcore ui\nmemcore ui --install | --uninstall\n") ||
			!strings.Contains(out, "--uninstall removes the application") || !strings.Contains(out, "work before init") {
			t.Errorf("help ui: %v, output\n%s", err, out)
		}
	})

	t.Run("input errors point to the help", func(t *testing.T) {
		for _, c := range []struct {
			dir, stdin string
			args       []string
		}{
			{dev, "Commit one change at a time.\n", []string{"remember"}},
			{dev, "", []string{"remember", "--bogus"}},
			{home, "", []string{"show"}},
			{home, "", []string{"orient", "extra"}},
			{home, "", []string{"verify"}},
			{home, "", []string{"inbox", "extra"}},
			{home, "", []string{"push", "extra"}},
			{home, "", []string{"stats", "extra"}},
			{dev, memoryText("tests-before-code", "Read when the name is taken.", "A fact."), []string{"remember"}},
			{dev, memoryText("code-tests-first", "Read before you write code: the tests of a step come first.", "A fact."), []string{"remember"}},
			{dev, memoryText("leaky", "Read when you test a secret.", "ghp_"+strings.Repeat("a1B2", 9)), []string{"remember"}},
			{home, "", []string{"show", "../outside.md"}},
			{home, "", []string{"show", "dev/memories/absent.md"}},
			{home, "", []string{"verify", "no/such/path.md"}},
			{dev, memoryText("dead-link", "Read when a link is dead.", "See [[no-such-memory]]."), []string{"remember"}},
		} {
			_, err := run(t, c.dir, c.stdin, c.args...)
			want := "memcore help " + c.args[0] + " shows the input"
			if err == nil || !strings.HasSuffix(err.Error(), want) {
				t.Errorf("%s: error = %v, want one that ends with %q", strings.Join(c.args, " "), err, want)
			}
		}
	})
}
