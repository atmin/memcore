package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMove(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	note := func(name, body string) string {
		return "---\nname: " + name + "\ndescription: \"Read when you test " + name + ".\"\n---\n\n" + body
	}
	pushedByHand(t, home, repo, map[string]string{
		"dev/memories/old-fact.md":     memoryText("old-fact", "Read when you test the old fact.", "See [[old-fact]] itself."),
		"dev/memories/linking-fact.md": memoryText("linking-fact", "Read before you follow a link.", "See [[old-fact]]."),
		"dev/memories/same.md":         memoryText("same", "Read before you test a shared name.", "A fact."),
		"dev/memories/uses-same.md":    memoryText("uses-same", "Read before you use a shared name.", "See [[same]] and [the note](../notes/same.md)."),
		"dev/memories/t/README.md":     topicReadme("t", "Read when you test a topic."),
		"dev/memories/t/in-topic.md":   memoryText("in-topic", "Read before you go into a topic.", "See [the old fact](../old-fact.md)."),
		"dev/notes/same.md":            note("same", "A note.\n"),
		"dev/notes/linking.md":         note("linking", "See [the old fact](../memories/old-fact.md).\n"),
		"dev/plans/p.md":               note("p", "A plan.\n"),
		"dev/skills/s/SKILL.md":        note("s", "See [the old fact](../../memories/old-fact.md).\n"),
		"dev/bin/run-x":                "#!/bin/sh\n# Run when you test.\n",
		"dev/glossary.md":              "# Glossary\n\n- **fact**: a thing that holds. Not: forbidden-fact.\n",
		"dev/notes/flow.md":            "---\nname: flow\ndescription: 'Read when you test a flow list.'\ntags: [a, b]\n---\n\nSee [a gone page](../plans/gone.md).\n",
		"dev/memories/empty/README.md": topicReadme("empty", "Read when you build [the plan](../../plans/p.md)."),
		"dev/memories/empty-link.md":   memoryText("empty-link", "Read before you link an empty topic.", "See [the empty topic](empty/README.md)."),
	})

	t.Run("a memory, and every link to it", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "old-fact", "new-fact", "--reason", "Clearer."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if got := message(t, repo); got != "move: dev/memories/old-fact.md to dev/memories/new-fact.md\n\nClearer.\n\n" {
			t.Errorf("message = %q", got)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "memories", "old-fact.md")); err == nil {
			t.Error("the old path is still there")
		}
		for p, want := range map[string]string{
			"dev/memories/new-fact.md":     "name: new-fact\n",
			"dev/memories/linking-fact.md": "See [[new-fact]].",
			"dev/memories/t/in-topic.md":   "[the old fact](../new-fact.md)",
			"dev/notes/linking.md":         "[the old fact](../memories/new-fact.md)",
			"dev/skills/s/SKILL.md":        "[the old fact](../../memories/new-fact.md)",
			"dev/README.md":                "- [new-fact](memories/new-fact.md): Read when you test the old fact.",
		} {
			if got := readT(t, filepath.Join(repo, filepath.FromSlash(p))); !strings.Contains(got, want) {
				t.Errorf("%s =\n%s\nwant it to hold %q", p, got, want)
			}
		}
		if got := readT(t, filepath.Join(repo, "dev", "memories", "new-fact.md")); !strings.Contains(got, "See [[new-fact]] itself.") {
			t.Errorf("the moved memory =\n%s\nwant its own [[name]] renamed", got)
		}
		clean(t, repo)
	})

	t.Run("a note keeps the [[name]] of a memory with its name", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "dev/notes/same.md", "other-note", "--reason", "Clearer."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		got := readT(t, filepath.Join(repo, "dev", "memories", "uses-same.md"))
		if !strings.Contains(got, "See [[same]] and [the note](../notes/other-note.md).") {
			t.Errorf("uses-same =\n%s\nwant [[same]] kept and the link moved", got)
		}
		if got := readT(t, filepath.Join(repo, "dev", "notes", "other-note.md")); !strings.Contains(got, "name: other-note\n") {
			t.Errorf("the moved note =\n%s\nwant its new name", got)
		}
		clean(t, repo)
	})

	t.Run("a note with an old fault and a frontmatter that is not canonical", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "dev/notes/flow.md", "flow-two", "--reason", "Clearer."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		want := "---\nname: flow-two\ndescription: 'Read when you test a flow list.'\ntags: [a, b]\n---\n\nSee [a gone page](../plans/gone.md).\n"
		if got := readT(t, filepath.Join(repo, "dev", "notes", "flow-two.md")); got != want {
			t.Errorf("the moved note = %q, want only its name changed", got)
		}
		clean(t, repo)
	})

	t.Run("a plan", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "dev/plans/p.md", "q", "--reason", "Clearer."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if got := readT(t, filepath.Join(repo, "dev", "plans", "q.md")); !strings.Contains(got, "name: q\n") {
			t.Errorf("the moved plan =\n%s\nwant its new name", got)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "memories", "empty", "README.md")); err != nil {
			t.Errorf("a move pruned the empty topic folder whose link it rewrote: %v", err)
		}
		clean(t, repo)
	})

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"a skill", []string{"dev/skills/s/SKILL.md", "x"}, "a memory, a note or a plan"},
		{"a script", []string{"dev/bin/run-x", "x"}, "a memory, a note or a plan"},
		{"a topic index", []string{"dev/memories/t/README.md", "x"}, "a memory, a note or a plan"},
		{"a level index", []string{"dev/README.md", "x"}, "a memory, a note or a plan"},
		{"a taken name", []string{"new-fact", "linking-fact"}, "taken"},
		{"a taken note path", []string{"dev/notes/linking.md", "other-note"}, "exists"},
		{"the same name", []string{"new-fact", "new-fact"}, "same"},
		{"a bad name", []string{"new-fact", "Bad_Name"}, "lower case"},
		{"a new fault", []string{"new-fact", "forbidden-fact"}, "fault"},
		{"a missing document", []string{"no-such-fact", "x"}, "no document"},
	} {
		t.Run(tt.name+" refuses", func(t *testing.T) {
			before := gitT(t, repo, "rev-parse", "HEAD")
			out, err := run(t, dev, "", append([]string{"move"}, append(tt.args, "--reason", "A test.")...)...)
			if err == nil || !strings.Contains(err.Error()+out, tt.want) {
				t.Errorf("error = %v, output %q, want one that holds %q", err, out, tt.want)
			}
			if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
				t.Error("the refused move made a commit")
			}
			clean(t, repo)
		})
	}

	t.Run("no reason refuses", func(t *testing.T) {
		if _, err := run(t, dev, "", "move", "new-fact", "x"); err == nil || !strings.Contains(err.Error(), "--reason") {
			t.Errorf("error = %v, want the usage", err)
		}
	})
}
