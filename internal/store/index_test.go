package store

import (
	"os"
	"path/filepath"
	"testing"
)

// writeT writes a file under dir, with its parent folders.
func writeT(t *testing.T, dir, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	mkdirT(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func doc(description string) string {
	return "---\nname: x\ndescription: \"" + description + "\"\n---\n\nBody.\n"
}

func TestWriteIndex(t *testing.T) {
	repo := t.TempDir()
	shop := "work/shop/"
	writeT(t, repo, shop+"memories/tests-first.md", doc("Read before you write code."), 0o644)
	writeT(t, repo, shop+"memories/api-versions.md", doc("Read when you call the API: it has two versions."), 0o644)
	writeT(t, repo, shop+"skills/deploy/SKILL.md", doc("Use when you deploy the shop."), 0o644)
	writeT(t, repo, shop+"bin/start", "#!/bin/sh\n# Run when you start the local stack.\n# usage: start\n", 0o755)
	writeT(t, repo, shop+"bin/helpers.sh", "# A helper, not a script.\n", 0o644)
	writeT(t, repo, shop+"conventions.md", "# Conventions\n", 0o644)
	writeT(t, repo, shop+"glossary.md", "# Glossary\n", 0o644)
	writeT(t, repo, shop+"plans/checkout.md", doc("Read when you build the checkout."), 0o644)
	writeT(t, repo, shop+"notes/2026-11-01-slow-search.md", doc("Read when search is slow."), 0o644)
	writeT(t, repo, shop+"web/README.md", "# ~/work/shop/web\n", 0o644)
	writeT(t, repo, "work/+bin/memories/paths.md", doc("Read when you add a script to ~/bin."), 0o644)
	mkdirT(t, filepath.Join(repo, "work", "+foo"))
	mkdirT(t, filepath.Join(repo, "work", "Notes"))
	mkdirT(t, filepath.Join(repo, "empty"))
	mkdirT(t, filepath.Join(repo, ".config"))
	mkdirT(t, filepath.Join(repo, "old [draft] #2"))
	mkdirT(t, filepath.Join(repo, ".git"))

	tests := []struct {
		name  string
		level string
		want  string
	}{
		{
			name:  "every kind",
			level: "work/shop",
			want: `# ~/work/shop

## Memories

- [api-versions](memories/api-versions.md): Read when you call the API: it has two versions.
- [tests-first](memories/tests-first.md): Read before you write code.

## Skills

- [deploy](skills/deploy/SKILL.md): Use when you deploy the shop.

## Scripts

- [start](bin/start): Run when you start the local stack.

## Conventions

- [conventions](conventions.md): Read before you change anything in this repo.

## Glossary

- [glossary](glossary.md): Read before you name anything at this level.

## Plans

- [checkout](plans/checkout.md): Read when you build the checkout.

## Notes

- [2026-11-01-slow-search](notes/2026-11-01-slow-search.md): Read when search is slow.

## Levels

- [web](web/README.md): Read when you work under ~/work/shop/web.
`,
		},
		{
			name:  "no documents",
			level: "empty",
			want:  "# ~/empty\n",
		},
		{
			name:  "a child level",
			level: "work",
			want: `# ~/work

## Levels

- [bin](+bin/README.md): Read when you work under ~/work/bin.
- [shop](shop/README.md): Read when you work under ~/work/shop.
`,
		},
		{
			name:  "a level at an escaped folder",
			level: "work/bin",
			want: `# ~/work/bin

## Memories

- [paths](memories/paths.md): Read when you add a script to ~/bin.
`,
		},
		{
			name:  "the root",
			level: "",
			want: `# ~

## Levels

- [.config](.config/README.md): Read when you work under ~/.config.
- [empty](empty/README.md): Read when you work under ~/empty.
- [old \[draft\] #2](old%20%5Bdraft%5D%20%232/README.md): Read when you work under ~/old [draft] #2.
- [work](work/README.md): Read when you work under ~/work.
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := WriteIndex(repo, tt.level); err != nil {
				t.Fatalf("WriteIndex: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(Folder(tt.level)), "README.md"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("index =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestLines(t *testing.T) {
	repo := t.TempDir()
	writeT(t, repo, "dev/memories/a.md", doc("Read when you test."), 0o644)
	writeT(t, repo, "dev/plans/p.md", doc("Read when you plan."), 0o644)

	ix, err := Lines(repo, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Of(KindMemories); len(got) != 1 || got[0].Path != "dev/memories/a.md" || got[0].Text != "Read when you test." {
		t.Errorf("memories = %+v", got)
	}
	if got := ix.Of(KindPlans); len(got) != 1 {
		t.Errorf("plans = %+v", got)
	}

	writeT(t, repo, "+bin/memories/b.md", doc("Read when you script."), 0o644)
	ix, err = Lines(repo, "bin")
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Of(KindMemories); ix.Level != "bin" || len(got) != 1 || got[0].Path != "+bin/memories/b.md" {
		t.Errorf("level bin = %+v", ix)
	}

	ix, err = Lines(repo, "no/such/level")
	if err != nil || len(ix.Lines) != 0 {
		t.Errorf("absent level = %+v, %v; want no lines and no error", ix, err)
	}

	writeT(t, repo, "bad/memories/b.md", "no frontmatter\n", 0o644)
	writeT(t, repo, "bad/notes/c.md", "---\nname: c\n---\n", 0o644)
	writeT(t, repo, "bad/bin/run", "#!/bin/sh\necho run\n", 0o755)
	ix, err = Lines(repo, "bad")
	if err != nil {
		t.Fatalf("a level with bad documents: %v", err)
	}
	for _, want := range []Line{
		{KindMemories, "b", "bad/memories/b.md", "No description. Run memcore verify bad/memories/b.md."},
		{KindNotes, "c", "bad/notes/c.md", "No description. Run memcore verify bad/notes/c.md."},
		{KindScripts, "run", "bad/bin/run", "No comment line says when to run it."},
	} {
		if got := ix.Of(want.Kind); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %+v, want %+v", want.Kind, got, want)
		}
	}
}

func TestFolder(t *testing.T) {
	tests := []struct{ level, folder string }{
		{"", ""},
		{"dev/app", "dev/app"},
		{"bin", "+bin"},
		{"Bin", "+Bin"},
		{"dev/notes", "dev/+notes"},
		{"+tmp", "++tmp"},
		{".git", "+.git"},
		{"README.md", "+README.md"},
		{"work/skills/web", "work/+skills/web"},
		{".config", ".config"},
	}
	for _, tt := range tests {
		if got := Folder(tt.level); got != tt.folder {
			t.Errorf("Folder(%q) = %q, want %q", tt.level, got, tt.folder)
		}
		if got, ok := LevelOf(tt.folder); !ok || got != tt.level {
			t.Errorf("LevelOf(%q) = %q, %v; want %q, true", tt.folder, got, ok, tt.level)
		}
	}
	for _, folder := range []string{"bin", "Notes", ".git", "+foo", "dev/+app", "dev/memories"} {
		if got, ok := LevelOf(folder); ok {
			t.Errorf("LevelOf(%q) = %q, true; want no level", folder, got)
		}
	}
}
