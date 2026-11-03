package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraft(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, remote := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))

	for _, tt := range []struct {
		name    string
		args    []string
		path    string
		holds   string
		mode    os.FileMode
		indexed string
	}{
		{"plan", []string{"plan", "build-x", "--description", "Read when you build x."}, "dev/plans/build-x.md", "## Acceptance criteria", 0o644, "- [build-x](plans/build-x.md): Read when you build x."},
		{"note", []string{"note", "slow-search", "--description", "Read when search is slow."}, "dev/notes/slow-search.md", "# Slow search", 0o644, "- [slow-search](notes/slow-search.md): Read when search is slow."},
		{"script", []string{"script", "start-stack", "--description", "Run when you start the stack."}, "dev/bin/start-stack", "#!/bin/sh\n# Run when you start the stack.\n# Usage: start-stack\nset -eu\n", 0o755, "- [start-stack](bin/start-stack): Run when you start the stack."},
		{"helper", []string{"helper", "stack_db.py"}, "dev/bin/stack_db.py", "", 0o644, ""},
		{"skill", []string{"skill", "deploy", "--description", "Use when you deploy."}, "dev/skills/deploy/SKILL.md", "name: deploy\ndescription: Use when you deploy.\n", 0o644, "- [deploy](skills/deploy/SKILL.md): Use when you deploy."},
		{"a file inside a skill", []string{"skill", "deploy/check.sh", "--exec"}, "dev/skills/deploy/check.sh", "", 0o755, ""},
		{"conventions", []string{"conventions"}, "dev/conventions.md", "# Conventions of ~/dev", 0o644, "- [conventions](conventions.md): Read before you change anything in this repo."},
		{"glossary", []string{"glossary"}, "dev/glossary.md", "# Glossary of ~/dev", 0o644, "- [glossary](glossary.md): Read before you name anything at this level."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, dev, "", append([]string{"draft"}, tt.args...)...)
			if err != nil {
				t.Fatalf("draft: %v\n%s", err, out)
			}
			if got := head(t, repo); got != "draft: "+tt.path {
				t.Errorf("subject = %q", got)
			}
			full := filepath.Join(repo, filepath.FromSlash(tt.path))
			info, err := os.Stat(full)
			if err != nil {
				t.Fatalf("no draft at %s: %v", tt.path, err)
			}
			if info.Mode().Perm() != tt.mode {
				t.Errorf("mode = %v, want %v", info.Mode().Perm(), tt.mode)
			}
			if got := readT(t, full); !strings.Contains(got, tt.holds) {
				t.Errorf("draft =\n%s\nwant it to hold %q", got, tt.holds)
			}
			if tt.indexed != "" && !strings.Contains(readT(t, filepath.Join(repo, "dev", "README.md")), tt.indexed) {
				t.Errorf("the index does not hold %q", tt.indexed)
			}
			clean(t, repo)
		})
	}

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"an existing path", []string{"plan", "build-x", "--description", "Read when you build x."}, "exists"},
		{"no description", []string{"note", "quiet", "--description", ""}, "--description"},
		{"a bad name", []string{"plan", "Build_X", "--description", "Read when you build x."}, "lower case"},
		{"a skill file without its skill", []string{"skill", "missing/check.sh"}, "no skill"},
		{"a script name in the repo", []string{"script", "start-stack", "--at", "~", "--description", "Run when you test."}, "dev/bin/start-stack"},
	} {
		t.Run(tt.name+" refuses", func(t *testing.T) {
			if _, err := run(t, dev, "", append([]string{"draft"}, tt.args...)...); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one that holds %q", err, tt.want)
			}
		})
	}

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"a secret in a description", []string{"note", "leaky", "--description", "Read when you use ghp_" + strings.Repeat("a1B2", 9) + "."}, "secret"},
		{"a dead reference in a description", []string{"note", "dangling", "--description", "Read when you see [[no-such-memory]]."}, "fault"},
		{"a script description of two lines", []string{"script", "two-lines", "--description", "Run when you test.\nrm -rf ~"}, "one line"},
		{"a file inside a skill with no name", []string{"skill", "/deploy/check.sh"}, "skill name"},
	} {
		t.Run(tt.name+" refuses", func(t *testing.T) {
			before := gitT(t, repo, "rev-parse", "HEAD")
			if _, err := run(t, dev, "", append([]string{"draft"}, tt.args...)...); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one that holds %q", err, tt.want)
			}
			if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
				t.Error("the refused draft made a commit")
			}
		})
	}

	t.Run("helpers and skill files reserve no script name", func(t *testing.T) {
		if out, err := run(t, dev, "", "draft", "skill", "deploy/bin/rollout", "--exec"); err != nil {
			t.Fatalf("draft skill file: %v\n%s", err, out)
		}
		for _, name := range []string{"stack_db.py", "rollout"} {
			if out, err := run(t, dev, "", "draft", "script", name, "--at", "~", "--description", "Run when you test "+name+"."); err != nil {
				t.Errorf("draft script %s: %v\n%s", name, err, out)
			}
		}
	})

	t.Run("a command on a relative PATH entry refuses", func(t *testing.T) {
		bin := t.TempDir()
		writeT(t, bin, "dotted", "#!/bin/sh\n")
		if err := os.Chmod(filepath.Join(bin, "dotted"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(bin)
		t.Setenv("PATH", "."+string(os.PathListSeparator)+os.Getenv("PATH"))
		if _, err := run(t, dev, "", "draft", "script", "dotted", "--description", "Run when you test."); err == nil || !strings.Contains(err.Error(), "PATH") {
			t.Errorf("error = %v, want a refusal of the command on PATH", err)
		}
	})

	t.Run("a script name on PATH refuses", func(t *testing.T) {
		bin := t.TempDir()
		writeT(t, bin, "taken", "#!/bin/sh\n")
		if err := os.Chmod(filepath.Join(bin, "taken"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if _, err := run(t, dev, "", "draft", "script", "taken", "--description", "Run when you test."); err == nil || !strings.Contains(err.Error(), filepath.Join(bin, "taken")) {
			t.Errorf("error = %v, want a refusal that names %s", err, filepath.Join(bin, "taken"))
		}
	})

	t.Run("a revision after a draft folds into the draft", func(t *testing.T) {
		if out, err := run(t, dev, "", "draft", "note", "trial", "--description", "Read when you try it."); err != nil {
			t.Fatalf("draft: %v\n%s", err, out)
		}
		in := "---\nname: trial\ndescription: Read when you try it.\n---\n\n# Trial\n\nIt worked.\n"
		if out, err := run(t, dev, in, "revise", "dev/notes/trial.md", "--reason", "The findings."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		pushYes(t, home)
		history := subjects(t, remote)
		if strings.Count(history, "dev/notes/trial.md") != 1 || !strings.Contains(history, "draft: dev/notes/trial.md") {
			t.Errorf("remote history = %q, want one draft commit for the note", history)
		}
		if got := gitT(t, remote, "show", "main:dev/notes/trial.md"); !strings.Contains(got, "It worked.") {
			t.Errorf("remote note = %q, want the revision", got)
		}
	})
}
