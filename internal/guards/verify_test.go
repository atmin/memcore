package guards

import (
	"os"
	"os/exec"
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

func TestRelink(t *testing.T) {
	memory := func(name, body string) string {
		return "---\nname: " + name + "\ndescription: \"Read when you test.\"\n---\n\n" + body
	}
	repo := t.TempDir()
	files := map[string]string{
		"dev/memories/old-name.md":  memory("old-name", "See [[old-name]] itself and [b](b-fact.md).\n"),
		"dev/memories/b-fact.md":    memory("b-fact", "See [[old-name]], [[ old-name ]], [old](old-name.md#why), [old2](<old-name.md>), [x](./old-name.md?x=1), `[[old-name]]` and [[old-name-two]].\n"),
		"dev/notes/n.md":            memory("n", "See [old](../memories/old-name.md) and [abs](/dev/memories/old-name.md).\n\n```\n[old](../memories/old-name.md)\n```\n\n[ref]: ../memories/old-name.md\n"),
		"dev/skills/s/SKILL.md":     memory("s", "See [old](../../memories/old-name.md).\n"),
		"dev/memories/t/README.md":  memory("t", "# memories/t in ~/dev\n\nSee [x](../old-name.md).\n"),
		"dev/README.md":             "# ~/dev\n\n## Memories\n\n- [old-name](memories/old-name.md): Read when you test.\n",
		"dev/notes/same.md":         memory("same", "A note.\n"),
		"dev/memories/same.md":      memory("same-memory", "A memory.\n"),
		"dev/memories/uses-same.md": memory("uses-same", "See [[same]] and [the note](../notes/same.md).\n"),
		"dev/memories/untouched.md": memory("untouched", "See [b](b-fact.md).\n"),
	}
	for p, c := range files {
		writeT(t, repo, p, c)
	}

	t.Run("a memory", func(t *testing.T) {
		got, err := Relink(repo, "dev/memories/old-name.md", "dev/memories/new-name.md", "old-name", "new-name", true)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"dev/memories/old-name.md": memory("old-name", "See [[new-name]] itself and [b](b-fact.md).\n"),
			"dev/memories/b-fact.md":   memory("b-fact", "See [[new-name]], [[new-name]], [old](new-name.md#why), [old2](<new-name.md>), [x](new-name.md?x=1), `[[old-name]]` and [[old-name-two]].\n"),
			"dev/notes/n.md":           memory("n", "See [old](../memories/new-name.md) and [abs](/dev/memories/new-name.md).\n\n```\n[old](../memories/old-name.md)\n```\n\n[ref]: ../memories/new-name.md\n"),
			"dev/skills/s/SKILL.md":    memory("s", "See [old](../../memories/new-name.md).\n"),
			"dev/memories/t/README.md": memory("t", "# memories/t in ~/dev\n\nSee [x](../new-name.md).\n"),
		}
		if len(got) != len(want) {
			t.Errorf("Relink changed %d documents, want %d: %v", len(got), len(want), keys(got))
		}
		for p, w := range want {
			if string(got[p]) != w {
				t.Errorf("%s =\n%s\nwant\n%s", p, got[p], w)
			}
		}
	})

	t.Run("into a topic folder, with the links inside it", func(t *testing.T) {
		repo := t.TempDir()
		writeT(t, repo, "dev/memories/m.md", memory("m", "[a](a.md) [n](../notes/n.md#x) [abs](/dev/notes/n.md) [web](https://x.y) [top](#top) [self](m.md) [q](?view=1) `[c](a.md)`\n"))
		writeT(t, repo, "dev/memories/a.md", memory("a", "[m](m.md)\n"))
		writeT(t, repo, "dev/notes/n.md", memory("n", "[m](../memories/m.md)\n"))
		got, err := Relink(repo, "dev/memories/m.md", "dev/memories/t/m.md", "m", "m", true)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"dev/memories/m.md": memory("m", "[a](../a.md) [n](../../notes/n.md#x) [abs](/dev/notes/n.md) [web](https://x.y) [top](#top) [self](m.md) [q](?view=1) `[c](a.md)`\n"),
			"dev/memories/a.md": memory("a", "[m](t/m.md)\n"),
			"dev/notes/n.md":    memory("n", "[m](../memories/t/m.md)\n"),
		}
		if len(got) != len(want) {
			t.Errorf("Relink changed %v, want %d documents", keys(got), len(want))
		}
		for p, w := range want {
			if string(got[p]) != w {
				t.Errorf("%s =\n%s\nwant\n%s", p, got[p], w)
			}
		}
	})

	t.Run("a note keeps the [[name]] of a memory", func(t *testing.T) {
		got, err := Relink(repo, "dev/notes/same.md", "dev/notes/other.md", "same", "other", false)
		if err != nil {
			t.Fatal(err)
		}
		if w := memory("uses-same", "See [[same]] and [the note](../notes/other.md).\n"); string(got["dev/memories/uses-same.md"]) != w || len(got) != 1 {
			t.Errorf("Relink = %v, want only uses-same with\n%s", got, w)
		}
	})
}

func keys(m map[string][]byte) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestFaultsWith(t *testing.T) {
	repo := t.TempDir()
	writeT(t, repo, "dev/memories/a.md", "---\nname: a\ndescription: \"Read when you test.\"\n---\n\nA.\n")
	writeT(t, repo, "dev/notes/n.md", "---\nname: n\ndescription: \"Read when you test.\"\n---\n\nSee [a](../memories/a.md) and [[a]].\n")

	faults, err := FaultsWith(repo, map[string][]byte{"dev/memories/b.md": []byte("---\nname: b\ndescription: \"Read when you test.\"\n---\n\nB.\n")}, []string{"dev/memories/a.md"})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "faults", faults, []string{`dev/notes/n.md:6: dead link to "../memories/a.md"`, `dev/notes/n.md:6: dead reference [[a]]`})

	faults, err = FaultsWith(repo, map[string][]byte{
		"dev/memories/b.md": []byte("---\nname: b\ndescription: \"Read when you test.\"\n---\n\nB.\n"),
		"dev/notes/n.md":    []byte("---\nname: n\ndescription: \"Read when you test.\"\n---\n\nSee [b](../memories/b.md) and [[b]].\n"),
	}, []string{"dev/memories/a.md"})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "faults", faults, nil)

	faults, err = FaultsWith(repo, map[string][]byte{"dev/notes/new.md": []byte("See [[nobody]].\n")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "faults", faults, []string{`dev/notes/new.md:1: dead reference [[nobody]]`})
}

// gitT runs the installed git in dir, as a test author.
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestVerifyGoneNames(t *testing.T) {
	repo := t.TempDir()
	memory := func(name string) string {
		return "---\nname: " + name + "\ndescription: \"Read when you test.\"\n---\n\nA fact.\n"
	}
	gitT(t, repo, "init", "-q", "-b", "main")
	writeT(t, repo, "dev/memories/old-name.md", memory("old-name"))
	writeT(t, repo, "dev/memories/gone-fact.md", memory("gone-fact"))
	writeT(t, repo, "dev/memories/solo.md", memory("solo"))
	writeT(t, repo, "dev/memories/stedo/README.md", "---\nname: stedo\ndescription: \"Read when you test.\"\n---\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "add")
	gitT(t, repo, "mv", "dev/memories/old-name.md", "dev/memories/stedo/new-name.md")
	writeT(t, repo, "dev/memories/stedo/new-name.md", memory("new-name"))
	gitT(t, repo, "rm", "-q", "dev/memories/gone-fact.md", "dev/memories/solo.md")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "move and forget")

	const head = "---\nname: x\ndescription: \"Read when you test.\"\n---\n\n"
	tests := []struct {
		name     string
		path     string
		content  string
		warnings []string
	}{
		{name: "a renamed memory", path: "dev/memories/x.md", content: head + "See old-name.\n", warnings: []string{"x.md:6: the text names old-name, a memory that moved to dev/memories/stedo/new-name.md"}},
		{name: "a forgotten memory", path: "dev/memories/x.md", content: head + "See gone-fact for it.\n", warnings: []string{"x.md:6: the text names gone-fact, a memory that no longer exists"}},
		{name: "a name in inline code", path: "dev/memories/x.md", content: head + "See `gone-fact`.\n", warnings: []string{"gone-fact, a memory that no longer exists"}},
		{name: "a current name", path: "dev/memories/x.md", content: head + "See new-name.\n"},
		{name: "a fenced block", path: "dev/memories/x.md", content: head + "```\ngone-fact\n```\n"},
		{name: "a name of one word", path: "dev/memories/x.md", content: head + "A solo run.\n"},
		{name: "a longer word", path: "dev/memories/x.md", content: head + "See gone-fact-two.\n"},
		{name: "a note", path: "dev/notes/x.md", content: head + "The memory gone-fact went.\n"},
		{name: "a plan", path: "dev/plans/x.md", content: head + "Forget gone-fact.\n", warnings: []string{"gone-fact"}},
		{name: "a level index", path: "dev/README.md", content: "# ~/dev\n\n- gone-fact\n"},
		{name: "a topic index", path: "dev/memories/stedo/README.md", content: "---\nname: stedo\ndescription: \"Read when you test.\"\n---\n\n- gone-fact\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := VerifyContent(repo, tt.path, []byte(tt.content))
			if err != nil {
				t.Fatal(err)
			}
			check(t, "faults", r.Faults, nil)
			check(t, "warnings", r.Warnings, tt.warnings)
		})
	}

	t.Run("a path that a later document reuses", func(t *testing.T) {
		chain := t.TempDir()
		gitT(t, chain, "init", "-q", "-b", "main")
		step := func(msg string) {
			gitT(t, chain, "add", "-A")
			gitT(t, chain, "commit", "-q", "-m", msg)
		}
		writeT(t, chain, "dev/memories/first-name.md", memory("first-name"))
		step("add")
		gitT(t, chain, "mv", "dev/memories/first-name.md", "dev/memories/mid-name.md")
		writeT(t, chain, "dev/memories/mid-name.md", memory("mid-name"))
		step("rename")
		gitT(t, chain, "mv", "dev/memories/mid-name.md", "dev/memories/last-name.md")
		writeT(t, chain, "dev/memories/last-name.md", memory("last-name"))
		step("rename again")
		writeT(t, chain, "dev/memories/mid-name.md", "---\nname: mid-name\ndescription: \"Read when you test another thing.\"\n---\n\nAnother fact entirely, with other words.\n")
		step("reuse")
		gitT(t, chain, "mv", "dev/memories/mid-name.md", "dev/memories/other-name.md")
		writeT(t, chain, "dev/memories/other-name.md", "---\nname: other-name\ndescription: \"Read when you test another thing.\"\n---\n\nAnother fact entirely, with other words.\n")
		step("rename the reuse")
		r, err := VerifyContent(chain, "dev/memories/x.md", []byte(head+"See first-name.\n"))
		if err != nil {
			t.Fatal(err)
		}
		check(t, "warnings", r.Warnings, []string{"first-name, a memory that moved to dev/memories/last-name.md"})
	})

	t.Run("a repo without git", func(t *testing.T) {
		plain := t.TempDir()
		r, err := VerifyContent(plain, "dev/memories/x.md", []byte(head+"See gone-fact.\n"))
		if err != nil {
			t.Fatal(err)
		}
		check(t, "warnings", r.Warnings, nil)
	})

	t.Run("faults leave the history alone", func(t *testing.T) {
		writeT(t, repo, "dev/memories/y.md", head+"See gone-fact.\n")
		faults, err := Faults(repo, nil)
		if err != nil {
			t.Fatal(err)
		}
		check(t, "faults", faults, nil)
		r, err := Verify(repo, "dev/memories/y.md")
		if err != nil {
			t.Fatal(err)
		}
		check(t, "warnings", r.Warnings, []string{"y.md:6: the text names gone-fact"})
	})
}
