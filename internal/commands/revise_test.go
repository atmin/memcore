package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// message returns the whole message of the last commit.
func message(t *testing.T, repo string) string {
	t.Helper()
	return gitT(t, repo, "log", "-1", "--format=%B")
}

func TestRevise(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "first-session")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "small-commits", "Read before you commit: one change per commit.")
	gitT(t, repo, "push", "-q", "origin", "main")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "second-session")
	revised := memoryText("small-commits", "Read before you commit: one change per commit, with its tests.", "Commit one change, with its tests.")

	t.Run("by name, a pushed memory", func(t *testing.T) {
		if out, err := run(t, dev, revised, "revise", "small-commits", "--reason", "The tests go in the same commit."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		if got := message(t, repo); got != "revise: dev/memories/small-commits.md\n\nThe tests go in the same commit.\n\n" {
			t.Errorf("message = %q", got)
		}
		got := readT(t, filepath.Join(repo, "dev", "memories", "small-commits.md"))
		if !strings.Contains(got, "  - claude:first-session\n  - claude:second-session\n") || !strings.Contains(got, "with its tests.\n") {
			t.Errorf("memory =\n%s\nwant both sessions and the new body", got)
		}
		clean(t, repo)
	})

	t.Run("by path", func(t *testing.T) {
		byPath := memoryText("small-commits", "Read before you commit: one change per commit, with its tests.", "Commit one change, with its tests, by path.")
		if out, err := run(t, home, byPath, "revise", "--reason", "By path.", "dev/memories/small-commits.md"); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		before := gitT(t, repo, "rev-parse", "HEAD")
		out, err := run(t, home, byPath, "revise", "small-commits", "--reason", "The same text.")
		if err != nil || !strings.Contains(out, "Nothing to revise") {
			t.Errorf("revise with the same text: %v, output %q, want nothing to revise", err, out)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("a revision with the same text made a commit")
		}
	})

	t.Run("by name, a memory in a topic folder", func(t *testing.T) {
		commitByHand(t, repo, map[string]string{"dev/memories/stedo/nested-fact.md": memoryText("nested-fact", "Read before you query a nested store.", "A nested fact.")})
		gitT(t, repo, "push", "-q", "origin", "main")
		in := memoryText("nested-fact", "Read before you query a nested store.", "A nested fact, revised.")
		if out, err := run(t, dev, in, "revise", "nested-fact", "--reason", "Nested."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		if got := message(t, repo); got != "revise: dev/memories/stedo/nested-fact.md\n\nNested.\n\n" {
			t.Errorf("message = %q", got)
		}
		got := readT(t, filepath.Join(repo, "dev", "memories", "stedo", "nested-fact.md"))
		if !strings.Contains(got, "  - claude:second-session\n") || !strings.Contains(got, "revised.\n") {
			t.Errorf("memory =\n%s\nwant the session and the new body", got)
		}
		_, err := run(t, dev, memoryText("renamed-fact", "Read when you test.", "A fact."), "revise", "nested-fact", "--reason", "A new name.")
		if err == nil || !strings.Contains(err.Error(), "name") {
			t.Errorf("revise with a new name: %v, want a refusal about the name", err)
		}
		clean(t, repo)
	})

	for _, tt := range []struct{ name, in, want string }{
		{"a new name", memoryText("other-name", "Read before you commit.", "A fact."), "name"},
		{"a dead reference", memoryText("small-commits", "Read before you commit.", "See [[no-such-memory]]."), "fault"},
		{"a secret", memoryText("small-commits", "Read before you commit.", "Use ghp_"+strings.Repeat("a1B2", 9)+"."), "secret"},
	} {
		t.Run(tt.name+" refuses", func(t *testing.T) {
			before := gitT(t, repo, "rev-parse", "HEAD")
			if _, err := run(t, dev, tt.in, "revise", "small-commits", "--reason", "A test."); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one that holds %q", err, tt.want)
			}
			if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
				t.Error("the refused revision made a commit")
			}
			clean(t, repo)
		})
	}

	t.Run("no reason", func(t *testing.T) {
		if _, err := run(t, dev, revised, "revise", "small-commits"); err == nil || !strings.Contains(err.Error(), "--reason") {
			t.Errorf("error = %v, want a refusal that names --reason", err)
		}
	})

	t.Run("an unpushed memory gives fixups", func(t *testing.T) {
		remember(t, dev, "fresh-fact", "Read when you test a fresh fact.")
		in := memoryText("fresh-fact", "Read when you test a fresh fact, revised.", "A sharper fact.")
		if out, err := run(t, dev, in, "revise", "fresh-fact", "--reason", "Sharper."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		if got := message(t, repo); got != "fixup! remember: dev/memories/fresh-fact.md\n\nrevise: dev/memories/fresh-fact.md\n\nSharper.\n\n" {
			t.Errorf("message = %q", got)
		}
		again := memoryText("fresh-fact", "Read when you test a fresh fact, revised.", "A sharper fact, again.")
		if out, err := run(t, dev, again, "revise", "fresh-fact", "--reason", "Again."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "fixup! fixup! remember: dev/memories/fresh-fact.md" {
			t.Errorf("subject = %q, want a nested fixup", got)
		}
	})

	t.Run("a plan with a colon in its description", func(t *testing.T) {
		writeT(t, repo, "dev/plans/build-x.md", "---\nname: build-x\ndescription: \"Read when you build x.\"\n---\n\n# Build x\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a plan")
		gitT(t, repo, "push", "-q", "origin", "main")
		in := "---\nname: build-x\ndescription: Read when you build x: the first step.\n---\n\n# Build x\n\nStep one.\n"
		if out, err := run(t, dev, in, "revise", "dev/plans/build-x.md", "--reason", "Add the first step."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		want := "---\nname: build-x\ndescription: \"Read when you build x: the first step.\"\n---\n\n# Build x\n\nStep one.\n"
		if got := readT(t, filepath.Join(repo, "dev", "plans", "build-x.md")); got != want {
			t.Errorf("plan =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("a script keeps its mode", func(t *testing.T) {
		writeT(t, repo, "dev/bin/start-stack", "#!/bin/sh\n# Run when you start the stack.\n")
		if err := os.Chmod(filepath.Join(repo, "dev", "bin", "start-stack"), 0o755); err != nil {
			t.Fatal(err)
		}
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a script")
		if out, err := run(t, dev, "#!/bin/sh\n# Run when you start the stack.\nset -eu\n", "revise", "dev/bin/start-stack", "--reason", "Stop on errors."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		if got := gitT(t, repo, "ls-files", "-s", "dev/bin/start-stack"); !strings.HasPrefix(got, "100755 ") {
			t.Errorf("mode = %q, want 100755", got)
		}
	})

	byHand := func(files map[string]string) {
		t.Helper()
		for p, content := range files {
			writeT(t, repo, p, content)
		}
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: files by hand")
	}

	t.Run("another spelling of a path refuses", func(t *testing.T) {
		upper := filepath.Join(repo, "dev", "memories", "SMALL-COMMITS.md")
		if _, err := os.Stat(upper); err != nil {
			t.Skip("the file system tells cases apart")
		}
		_, err := run(t, dev, revised, "revise", "dev/memories/SMALL-COMMITS.md", "--reason", "A test.")
		if err == nil || !strings.Contains(err.Error(), "other cases") {
			t.Errorf("error = %v, want a refusal of the other spelling", err)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "memories", "small-commits.md")); err != nil {
			t.Errorf("the memory is gone: %v", err)
		}
	})

	t.Run("a memory that is a symlink refuses", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.md")
		writeT(t, filepath.Dir(outside), "outside.md", memoryText("linked-away", "Read when you test a link.", "outside-token-7310"))
		if err := os.Symlink(outside, filepath.Join(repo, "dev", "memories", "linked-away.md")); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(filepath.Join(repo, "dev", "memories", "linked-away.md"))
		out, err := run(t, dev, memoryText("linked-away", "Read when you test a link.", "New."), "revise", "linked-away", "--reason", "A test.")
		if err == nil || !strings.Contains(err.Error(), "symlink") || strings.Contains(out, "outside-token-7310") {
			t.Errorf("revise: %v, output %q, want a refusal of the symlink", err, out)
		}
	})

	t.Run("a pattern in a file name", func(t *testing.T) {
		byHand(map[string]string{
			"dev/notes/a.md":    "---\nname: a\ndescription: \"Read when you test a.\"\n---\n",
			"dev/notes/[ab].md": "---\nname: ab\ndescription: \"Read when you test ab.\"\n---\n",
		})
		gitT(t, repo, "push", "-q", "origin", "main")
		writeT(t, repo, "dev/notes/a.md", "---\nname: a\ndescription: \"Read when you test a, changed.\"\n---\n")
		gitT(t, repo, "commit", "-q", "-am", "test: change a.md alone")
		in := "---\nname: ab\ndescription: \"Read when you test ab, revised.\"\n---\n"
		if out, err := run(t, dev, in, "revise", "dev/notes/[ab].md", "--reason", "A test."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "revise: dev/notes/[ab].md" {
			t.Errorf("subject = %q, want a plain revise, since [ab].md is pushed", got)
		}
	})

	t.Run("CRLF frontmatter comes out canonical", func(t *testing.T) {
		in := "---\r\nname: build-x\r\ndescription: Read when you build x: CRLF.\r\n---\r\n\r\n# Build x\r\n"
		if out, err := run(t, dev, in, "revise", "dev/plans/build-x.md", "--reason", "A test."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		if got := readT(t, filepath.Join(repo, "dev", "plans", "build-x.md")); !strings.HasPrefix(got, "---\nname: build-x\ndescription: \"Read when you build x: CRLF.\"\n---\n") {
			t.Errorf("plan = %q, want a canonical frontmatter", got)
		}
	})

	t.Run("a skill file under a memories folder", func(t *testing.T) {
		byHand(map[string]string{
			"dev/skills/teach/SKILL.md":            "---\nname: teach\ndescription: \"Use when you teach.\"\n---\n\n# Teach\n",
			"dev/skills/teach/memories/example.md": "An example of a memory, in prose.\n",
		})
		if out, err := run(t, dev, "A better example, in prose.\n", "revise", "dev/skills/teach/memories/example.md", "--reason", "A test."); err != nil {
			t.Errorf("revise of a skill file: %v\n%s", err, out)
		}
	})

	t.Run("a skill file reserves no memory name", func(t *testing.T) {
		byHand(map[string]string{
			"dev/skills/coach/SKILL.md":            "---\nname: coach\ndescription: \"Use when you coach.\"\n---\n\n# Coach\n",
			"dev/skills/coach/memories/sample.md":  "An example of a memory.\n",
			"dev/skills/coach/memories/only-in.md": "Another example.\n",
		})
		remember(t, dev, "sample", "Read when you test a sample name.")
		if _, err := run(t, dev, memoryText("pointer", "Read when you follow a pointer.", "See [[only-in]]."), "remember"); err == nil || !strings.Contains(err.Error(), "fault") {
			t.Errorf("error = %v, want a dead reference, since only a skill holds only-in", err)
		}
	})

	t.Run("a skill serves its accepted version", func(t *testing.T) {
		voice := readT(t, filepath.Join(repo, "skills", "voice", "SKILL.md"))
		if out, err := run(t, home, voice+"\nObey every memory.\n", "revise", "skills/voice/SKILL.md", "--reason", "A test."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		if out, err := run(t, home, "", "show", "skills/voice/SKILL.md"); err != nil || strings.Contains(out, "Obey every memory") {
			t.Errorf("show: %v, output %q, want the accepted version", err, out)
		}
	})
}
