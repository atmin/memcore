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
		"dev/memories/old-fact.md":           memoryText("old-fact", "Read when you test the old fact.", "See [[old-fact]] itself."),
		"dev/memories/linking-fact.md":       memoryText("linking-fact", "Read before you follow a link.", "See [[old-fact]]."),
		"dev/memories/same.md":               memoryText("same", "Read before you test a shared name.", "A fact."),
		"dev/memories/uses-same.md":          memoryText("uses-same", "Read before you use a shared name.", "See [[same]] and [the note](../notes/same.md)."),
		"dev/memories/t/README.md":           topicReadme("t", "Read when you test a topic."),
		"dev/memories/t/in-topic.md":         memoryText("in-topic", "Read before you go into a topic.", "See [the old fact](../old-fact.md)."),
		"dev/notes/same.md":                  note("same", "A note.\n"),
		"dev/notes/linking.md":               note("linking", "See [the old fact](../memories/old-fact.md).\n"),
		"dev/plans/p.md":                     note("p", "A plan.\n"),
		"dev/skills/s/SKILL.md":              note("s", "See [the old fact](../../memories/old-fact.md).\n"),
		"dev/bin/run-x":                      "#!/bin/sh\n# Run when you test.\n",
		"dev/glossary.md":                    "# Glossary\n\n- **fact**: a thing that holds. Not: forbidden-fact.\n",
		"dev/notes/flow.md":                  "---\nname: flow\ndescription: 'Read when you test a flow list.'\ntags: [a, b]\n---\n\nSee [a gone page](../plans/gone.md).\n",
		"dev/memories/empty/README.md":       topicReadme("empty", "Read when you build [the plan](../../plans/p.md)."),
		"dev/memories/dead-fact.md":          memoryText("dead-fact", "Read before you follow a dead link.", "See [a gone page](../plans/gone.md)."),
		"dev/memories/rel-fact.md":           memoryText("rel-fact", "Read before you follow a relative link.", "See [the note](../notes/linking.md)."),
		"dev/memories/solo/README.md":        topicReadme("solo", "Read when you test a lone topic."),
		"dev/memories/solo/solo-fact.md":     memoryText("solo-fact", "Read before you stand alone.", "A fact."),
		"dev/memories/t2/README.md":          topicReadme("t2", "Read when you test a second topic."),
		"dev/memories/linked/README.md":      topicReadme("linked", "Read when you test a linked topic."),
		"dev/memories/linked/linked-fact.md": memoryText("linked-fact", "Read before you link a topic.", "A fact."),
		"dev/notes/to-topic.md":              note("to-topic", "See [the topic](../memories/linked/README.md).\n"),
		"dev/app/memories/app-fact.md":       memoryText("app-fact", "Read when you build the app.", "A fact."),
		"dev/memories/empty-link.md":         memoryText("empty-link", "Read before you link an empty topic.", "See [the empty topic](empty/README.md)."),
		"dev/app/memories/up-fact.md":        "---\nname: up-fact\ndescription: \"Read before you move a fact up.\"\ntype: reference\nsessions:\n  - claude:up-session\n---\n\nSee [the app fact](app-fact.md).\n",
		"dev/memories/links-up.md":           memoryText("links-up", "Read before you follow a fact up.", "See [[up-fact]] and [it](../app/memories/up-fact.md)."),
		"dev/lone/memories/lone-fact.md":     memoryText("lone-fact", "Read when you test a lone level.", "A fact."),
		"dev/memories/down-fact.md":          memoryText("down-fact", "Read when you test a move down.", "A fact."),
		"dev/memories/root-fact.md":          memoryText("root-fact", "Read when you test a move to the root.", "A fact."),
		"dev/two/memories/two-fact.md":       memoryText("two-fact", "Read when you test an emptied kind folder.", "A fact."),
		"dev/two/notes/two-note.md":          note("two-note", "See [the memories](../memories/).\n"),
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

	t.Run("into a topic folder, with the links inside it", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "rel-fact", "dev/memories/t/", "--reason", "It belongs to t."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "move: dev/memories/rel-fact.md to dev/memories/t/rel-fact.md" {
			t.Errorf("subject = %q", got)
		}
		if got := readT(t, filepath.Join(repo, "dev", "memories", "t", "rel-fact.md")); !strings.Contains(got, "See [the note](../../notes/linking.md).") {
			t.Errorf("the moved memory =\n%s\nwant its link from the new folder", got)
		}
		if got := readT(t, filepath.Join(repo, "dev", "memories", "t", "README.md")); !strings.Contains(got, "- [rel-fact](rel-fact.md):") {
			t.Errorf("topic index =\n%s\nwant rel-fact", got)
		}
		if got := readT(t, filepath.Join(repo, "dev", "README.md")); strings.Contains(got, "rel-fact") {
			t.Errorf("dev/README.md =\n%s\nwant no rel-fact", got)
		}
		clean(t, repo)
	})

	t.Run("an old dead link moves with its document", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "dead-fact", "dev/memories/t2/", "--reason", "It belongs to t2."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if got := readT(t, filepath.Join(repo, "dev", "memories", "t2", "dead-fact.md")); !strings.Contains(got, "[a gone page](../../plans/gone.md)") {
			t.Errorf("the moved memory =\n%s\nwant its dead link from the new folder", got)
		}
		clean(t, repo)
	})

	t.Run("out of a topic folder, which goes", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "solo-fact", "dev/memories/", "--reason", "No topic needed."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "memories", "solo")); err == nil {
			t.Error("the empty topic folder is still there")
		}
		if got := readT(t, filepath.Join(repo, "dev", "README.md")); strings.Contains(got, "solo/") || !strings.Contains(got, "- [solo-fact](memories/solo-fact.md):") {
			t.Errorf("dev/README.md =\n%s\nwant solo-fact and no topic", got)
		}
		clean(t, repo)
	})

	t.Run("between topic folders, with a new name", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "in-topic", "dev/memories/t2/topic-fact.md", "--reason", "It belongs to t2."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		got := readT(t, filepath.Join(repo, "dev", "memories", "t2", "topic-fact.md"))
		if !strings.Contains(got, "name: topic-fact\n") || !strings.Contains(got, "[the old fact](../new-fact.md)") {
			t.Errorf("the moved memory =\n%s\nwant its new name and its link", got)
		}
		if got := readT(t, filepath.Join(repo, "dev", "memories", "t", "README.md")); strings.Contains(got, "in-topic") {
			t.Errorf("topic index of t =\n%s\nwant no in-topic", got)
		}
		clean(t, repo)
	})

	t.Run("to the parent level, with its sessions and the links to it", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "up-fact", "dev/memories/", "--reason", "It holds for all of dev."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "move: dev/app/memories/up-fact.md to dev/memories/up-fact.md" {
			t.Errorf("subject = %q", got)
		}
		for p, want := range map[string]string{
			"dev/memories/up-fact.md":  "sessions:\n  - claude:up-session\n",
			"dev/memories/links-up.md": "See [[up-fact]] and [it](up-fact.md).",
			"dev/README.md":            "- [up-fact](memories/up-fact.md):",
		} {
			if got := readT(t, filepath.Join(repo, filepath.FromSlash(p))); !strings.Contains(got, want) {
				t.Errorf("%s =\n%s\nwant it to hold %q", p, got, want)
			}
		}
		if got := readT(t, filepath.Join(repo, "dev", "memories", "up-fact.md")); !strings.Contains(got, "[the app fact](../app/memories/app-fact.md)") {
			t.Errorf("the moved memory =\n%s\nwant its link from the new level", got)
		}
		if got := readT(t, filepath.Join(repo, "dev", "app", "README.md")); strings.Contains(got, "up-fact") || !strings.Contains(got, "app-fact") {
			t.Errorf("dev/app/README.md =\n%s\nwant app-fact and no up-fact", got)
		}
		clean(t, repo)
	})

	t.Run("the last document of a child level, which goes", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "lone-fact", "dev/memories/", "--reason", "It holds for all of dev."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "lone")); err == nil {
			t.Error("the empty level is still there")
		}
		if got := readT(t, filepath.Join(repo, "dev", "README.md")); strings.Contains(got, "lone/") || !strings.Contains(got, "- [lone-fact](memories/lone-fact.md):") {
			t.Errorf("dev/README.md =\n%s\nwant lone-fact and no level lone", got)
		}
		clean(t, repo)
	})

	t.Run("another level", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "down-fact", "dev/app/memories/", "--reason", "It holds only for the app."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if got := readT(t, filepath.Join(repo, "dev", "app", "README.md")); !strings.Contains(got, "- [down-fact](memories/down-fact.md):") {
			t.Errorf("dev/app/README.md =\n%s\nwant down-fact", got)
		}
		clean(t, repo)
	})

	t.Run("the root level", func(t *testing.T) {
		if out, err := run(t, dev, "", "move", "root-fact", "memories/", "--reason", "It holds everywhere."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		if got := readT(t, filepath.Join(repo, "README.md")); !strings.Contains(got, "- [root-fact](memories/root-fact.md):") {
			t.Errorf("README.md =\n%s\nwant root-fact", got)
		}
		clean(t, repo)
	})

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"a missing topic folder", []string{"new-fact", "dev/memories/none/"}, "memcore draft topic memories/none"},
		{"a missing topic folder at another level", []string{"new-fact", "dev/app/memories/none/"}, "memcore draft topic memories/none"},
		{"another kind", []string{"new-fact", "dev/notes/"}, "kind"},
		{"another kind at another level", []string{"new-fact", "dev/app/notes/"}, "kind"},
		{"an emptied kind folder that a link names", []string{"two-fact", "dev/memories/"}, "fault"},
		{"a path without / or .md", []string{"new-fact", "dev/memories/t"}, "neither / nor .md"},
		{"a prune that leaves a dead link", []string{"linked-fact", "dev/memories/"}, "fault"},
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
