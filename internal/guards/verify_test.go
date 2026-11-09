package guards

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeT(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyContent(t *testing.T) {
	repo := t.TempDir()
	writeT(t, repo, "README.md", "# ~\n")
	writeT(t, repo, "glossary.md", "- **memory repo**: the repo. Not: memory store, mem repo.\n")
	writeT(t, repo, "dev/glossary.md", "- **level**: a folder. Not: tier.\n")
	writeT(t, repo, "dev/memories/known.md", "---\nname: known\n---\n")
	writeT(t, repo, "dev/notes/n.md", "# A note\n")
	writeT(t, repo, "dev/notes/a(b).md", "# A note\n")

	const head = "---\nname: x\ndescription: \"Read when you test.\"\n---\n\n"
	tests := []struct {
		name     string
		path     string
		content  string
		faults   []string
		warnings []string
	}{
		{name: "clean", path: "dev/memories/x.md", content: head + "See [the note](../notes/n.md) and [[known]].\n"},
		{name: "dead link", path: "dev/memories/x.md", content: head + "See [the note](../notes/gone.md).\n", faults: []string{`x.md:6: dead link to "../notes/gone.md"`}},
		{name: "link with an anchor and a url", path: "dev/memories/x.md", content: head + "See [a](../notes/n.md#top), [b](https://example.com/x) and [c](#here).\n"},
		{name: "dead name", path: "dev/memories/x.md", content: head + "See [[unknown]].\n", faults: []string{"[[unknown]]"}},
		{name: "own name", path: "dev/memories/x.md", content: head + "This is [[x]].\n"},
		{name: "forbidden word", path: "dev/memories/x.md", content: head + "The Memory Store holds it.\n", faults: []string{`x.md:6: forbidden word "memory store"`}},
		{name: "forbidden word at the start of a line", path: "dev/memories/x.md", content: head + "One line.\nmemory store\n", faults: []string{`x.md:7: forbidden word "memory store"`}},
		{name: "forbidden word of a nearer glossary", path: "dev/memories/x.md", content: head + "Each tier has an index.\n", faults: []string{`"tier"`}},
		{name: "forbidden word of a level off the path", path: "work/memories/x.md", content: head + "Each tier has an index.\n"},
		{name: "part of a word", path: "dev/memories/x.md", content: head + "The frontier moves.\n"},
		{
			name:    "forbidden word in code and in links",
			path:    "dev/memories/x.md",
			content: head + "Run `memory store` here.\n\n```\nmemory store\n```\n\nSee [memory store](../notes/n.md) and <https://tier.example.com>.\n",
		},
		{name: "dead reference link", path: "dev/memories/x.md", content: head + "See [the note][n].\n\n[n]: ../notes/gone.md\n", faults: []string{`x.md:8: dead link to "../notes/gone.md"`}},
		{name: "live reference link", path: "dev/memories/x.md", content: head + "See [the note][n].\n\n  [n]: ../notes/n.md \"A note\"\n"},
		{name: "parentheses in a link path", path: "dev/memories/x.md", content: head + "See [it](../notes/a(b).md) and [gone](../notes/c(d).md).\n", faults: []string{`dead link to "../notes/c(d).md"`}},
		{name: "indented code", path: "dev/memories/x.md", content: head + "Run this:\n\n    memory store\n\tmemory store\n\nDone.\n"},
		{name: "indented list text is prose", path: "dev/memories/x.md", content: head + "- An item.\n\n    The memory store.\n", faults: []string{`x.md:8: forbidden word`}},
		{name: "a shorter fence stays inside the code", path: "dev/memories/x.md", content: head + "````\n```\nmemory store\n````\n"},
		{name: "a fence with text after it stays inside the code", path: "dev/memories/x.md", content: head + "```\n``` not a close\nmemory store\n```\nThe memory store.\n", faults: []string{`x.md:10: forbidden word`}},
		{name: "description without when", path: "dev/memories/x.md", content: "---\nname: x\ndescription: \"The tests of a step.\"\n---\n\nBody.\n", warnings: []string{"no moment to read"}},
		{name: "a note without frontmatter", path: "dev/notes/x.md", content: "# A note\n", warnings: []string{"no frontmatter"}},
		{name: "a skill without a description", path: "dev/skills/deploy/SKILL.md", content: "---\nname: deploy\n---\n", warnings: []string{"no description"}},
		{name: "an index without frontmatter", path: "dev/README.md", content: "# ~/dev\n"},
		{name: "a path that does not exist yet", path: "dev/app/memories/x.md", content: head + "See [the root](../../../README.md) and [[known]]. A tier.\n", faults: []string{`"tier"`}},
		{name: "a glossary may name its forbidden words", path: "dev/glossary.md", content: "- **level**: a folder. Not: tier.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := VerifyContent(repo, tt.path, []byte(tt.content))
			if err != nil {
				t.Fatal(err)
			}
			check(t, "faults", r.Faults, tt.faults)
			check(t, "warnings", r.Warnings, tt.warnings)
		})
	}
}

func check(t *testing.T, what string, got []Finding, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %d that hold %q", what, got, len(want), want)
	}
	for i := range want {
		if s := got[i].String(); !strings.Contains(s, want[i]) {
			t.Errorf("%s[%d] = %q, want one that holds %q", what, i, s, want[i])
		}
	}
}

func TestVerify(t *testing.T) {
	repo := t.TempDir()
	writeT(t, repo, "docs/a.md", "See [b](b.md).\n")
	writeT(t, repo, "docs/b.md", "See [c](c.md).\n")
	writeT(t, repo, "docs/notes.txt", "See [c](c.md).\n")

	r, err := Verify(repo, "docs")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "faults", r.Faults, []string{`docs/b.md:1: dead link to "c.md"`})

	r, err = Verify(repo, "docs/a.md")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "faults", r.Faults, nil)

	if _, err := Verify(repo, "docs/none.md"); err == nil {
		t.Error("absent path: no error")
	}

	// A memory in a topic folder is a memory, so a [[name]] to it holds.
	writeT(t, repo, "dev/memories/stedo/nested-fact.md", "---\nname: nested-fact\ndescription: \"Read when you test.\"\n---\n\nSee [[nested-fact]] and [[other-fact]].\n")
	writeT(t, repo, "dev/memories/other-fact.md", "---\nname: other-fact\ndescription: \"Read when you test.\"\n---\n\nSee [[nested-fact]].\n")
	writeT(t, repo, "dev/notes/n.md", "---\nname: n\ndescription: \"Read when you test.\"\n---\n\nSee [[nested-fact]] and [[gone-fact]].\n")
	r, err = Verify(repo, "dev")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "faults", r.Faults, []string{`dev/notes/n.md:6: dead reference [[gone-fact]]`})
	check(t, "warnings", r.Warnings, nil)

	// A note in a topic folder needs a description, as any note does.
	writeT(t, repo, "dev/notes/incidents/bare.md", "No frontmatter.\n")
	r, err = Verify(repo, "dev/notes/incidents/bare.md")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "warnings", r.Warnings, []string{"dev/notes/incidents/bare.md:1: the document has no frontmatter"})
}

func TestVerifySet(t *testing.T) {
	repo := t.TempDir()
	writeT(t, repo, "dev/glossary.md", "# Glossary\n\n- **memory**: one fact. Not: memo.\n")
	files := map[string][]byte{
		"dev/memories/a.md": []byte("---\nname: a\ndescription: \"Read when you test.\"\n---\n\nSee [[b]] and [b](b.md) and [the folder](../memories/).\n"),
		"dev/memories/b.md": []byte("---\nname: b\ndescription: \"Read when you test.\"\n---\n\nSee [[a]].\n"),
		"dev/memories/c.md": []byte("---\nname: c\ndescription: \"Read when you test.\"\n---\n\nSee [[z]], [z](z.md), and a memo.\n"),
	}
	reports, err := VerifySet(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"dev/memories/a.md", "dev/memories/b.md"} {
		if len(reports[p].Faults) != 0 {
			t.Errorf("%s: faults %v, want none: the set holds its links", p, reports[p].Faults)
		}
	}
	check(t, "a dead link, a dead reference and a forbidden word", reports["dev/memories/c.md"].Faults,
		[]string{"dead link", "dead reference [[z]]", "forbidden word"})
}
