package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// exclude makes git ignore the patterns in the memory repo alone, through .git/info/exclude.
func exclude(t *testing.T, repo string, patterns ...string) {
	t.Helper()
	writeT(t, repo, ".git/info/exclude", strings.Join(patterns, "\n")+"\n")
}

// survives fails the test unless the file still holds its content.
func survives(t *testing.T, repo, rel, content string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil || string(b) != content {
		t.Errorf("the ignored file %s holds %q, %v; want %q", rel, b, err, content)
	}
}

// gone fails the test when the path exists.
func gone(t *testing.T, repo, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(rel))); err == nil {
		t.Errorf("%s is still there", rel)
	}
}

func TestIgnoredInWrites(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	t.Run("a folder with only an ignored file gives no stale index", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "a-fact", "Read when you test a fact.")
		exclude(t, repo, ".DS_Store")
		writeT(t, repo, "lab/.DS_Store", "finder")
		writeT(t, repo, "dev/memories/old/.DS_Store", "finder")
		if stale, err := staleIndexes(repo); err != nil || len(stale) != 0 {
			t.Errorf("staleIndexes = %q, %v; want none", stale, err)
		}
	})

	t.Run("an ignored folder with a README.md", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", "Read when you test a fact.")
		exclude(t, repo, ".idea/")
		writeT(t, repo, "dev/.idea/README.md", "# Editor\n")
		writeT(t, repo, "dev/.idea/workspace.xml", "<x/>\n")
		if stale, err := staleIndexes(repo); err != nil || len(stale) != 0 {
			t.Errorf("staleIndexes = %q, %v; want none", stale, err)
		}
		if out, err := run(t, dev, memoryText("a-fact", "Read when you test a fact.", "Sharper."), "revise", "a-fact", "--reason", "Sharper."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		pushYes(t, home)
		if got := readT(t, filepath.Join(repo, "dev", "README.md")); strings.Contains(got, ".idea") {
			t.Errorf("dev/README.md =\n%s\nwant no level .idea", got)
		}
		survives(t, repo, "dev/.idea/README.md", "# Editor\n")
		survives(t, repo, "dev/.idea/workspace.xml", "<x/>\n")
		clean(t, repo)
	})

	t.Run("forget of the last memory of a level with an ignored file", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		proj := mkdirT(t, filepath.Join(home, "work", "proj"))
		remember(t, proj, "only-fact", "Read when you work on the project.")
		exclude(t, repo, ".DS_Store")
		writeT(t, repo, "work/proj/.DS_Store", "finder")
		if out, err := run(t, proj, "", "forget", "only-fact", "--reason", "The project ended."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		gone(t, repo, "work/proj/README.md")
		gone(t, repo, "work/README.md")
		if strings.Contains(readT(t, filepath.Join(repo, "README.md")), "[work]") {
			t.Error("the root index still lists work")
		}
		survives(t, repo, "work/proj/.DS_Store", "finder")
		clean(t, repo)

		// The folder of the level stays with its ignored file, and a new memory makes the level again.
		remember(t, proj, "new-fact", "Read when you start the project again.")
		if !strings.Contains(readT(t, filepath.Join(repo, "README.md")), "[work]") {
			t.Error("the root index does not list work")
		}
		if !strings.Contains(readT(t, filepath.Join(repo, "work", "proj", "README.md")), "[new-fact]") {
			t.Error("work/proj/README.md does not list new-fact")
		}
		clean(t, repo)
	})

	topic := map[string]string{
		"dev/memories/stedo/README.md": topicReadme("stedo", "Read when you query Stedo."),
		"dev/memories/stedo/only.md":   memoryText("only", "Read when you test the only fact.", "A fact."),
		"dev/memories/kept.md":         memoryText("kept", "Read when you test a kept fact.", "A fact."),
	}
	linked := map[string]string{
		"dev/notes/link.md": "---\nname: link\ndescription: \"Read when you test.\"\n---\n\nSee [Stedo](../memories/stedo/README.md).\n",
	}
	for p, c := range topic {
		linked[p] = c
	}
	for _, tt := range []struct {
		name  string
		files map[string]string
		args  []string
		fails string // a part of the error, or "" for a success
	}{
		{"forget of the last document of a topic folder", topic, []string{"forget", "only", "--reason", "Gone."}, ""},
		{"forget of the last document of a linked topic folder refuses", linked, []string{"forget", "only", "--reason", "Gone."}, "dev/notes/link.md"},
		{"move of the last document of a topic folder", topic, []string{"move", "only", "dev/memories/", "--reason", "No topic."}, ""},
		{"move of the last document of a linked topic folder refuses", linked, []string{"move", "only", "dev/memories/", "--reason", "No topic."}, "fault"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			dev := mkdirT(t, filepath.Join(home, "dev"))
			pushedByHand(t, home, repo, tt.files)
			exclude(t, repo, ".DS_Store")
			writeT(t, repo, "dev/memories/stedo/.DS_Store", "finder")
			before := gitT(t, repo, "rev-parse", "HEAD")
			out, err := run(t, dev, "", tt.args...)
			if tt.fails != "" {
				if err == nil || !strings.Contains(err.Error()+out, tt.fails) {
					t.Errorf("%s: %v\n%s\nwant a refusal with %q", tt.args[0], err, out, tt.fails)
				}
				if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
					t.Error("the refusal made a commit")
				}
			} else {
				if err != nil {
					t.Fatalf("%s: %v\n%s", tt.args[0], err, out)
				}
				gone(t, repo, "dev/memories/stedo/README.md")
				if got := readT(t, filepath.Join(repo, "dev", "README.md")); strings.Contains(got, "stedo") {
					t.Errorf("dev/README.md =\n%s\nwant no topic folder", got)
				}
			}
			survives(t, repo, "dev/memories/stedo/.DS_Store", "finder")
			clean(t, repo)
		})
	}

	t.Run("drop reports no orphan skill for an ignored skill folder", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		exclude(t, repo, "old-skill/")
		writeT(t, repo, "dev/skills/old-skill/run.sh", "#!/bin/sh\n")
		if orphans, err := orphanSkills(repo); err != nil || len(orphans) != 0 {
			t.Errorf("orphanSkills = %q, %v; want none", orphans, err)
		}
	})

	t.Run("forget of a skill folder that holds only ignored files", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", "Read when you test a fact.")
		exclude(t, repo, "*.bak")
		writeT(t, repo, "dev/skills/junk/a.bak", "backup")
		before := gitT(t, repo, "rev-parse", "HEAD")
		_, err := run(t, dev, "", "forget", "dev/skills/junk", "--reason", "Junk.")
		if err == nil || !strings.Contains(err.Error(), "dev/skills/junk") {
			t.Errorf("error = %v, want a refusal that names dev/skills/junk", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("the refused forget made a commit")
		}
		survives(t, repo, "dev/skills/junk/a.bak", "backup")
		clean(t, repo)
	})

	t.Run("forget of an ignored file refuses", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", "Read when you test a fact.")
		exclude(t, repo, "scratch.md")
		writeT(t, repo, "dev/notes/scratch.md", "scratch")
		before := gitT(t, repo, "rev-parse", "HEAD")
		_, err := run(t, dev, "", "forget", "dev/notes/scratch.md", "--reason", "Scratch.")
		if err == nil || !strings.Contains(err.Error(), "dev/notes/scratch.md") {
			t.Errorf("error = %v, want a refusal that names dev/notes/scratch.md", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("the refused forget made a commit")
		}
		survives(t, repo, "dev/notes/scratch.md", "scratch")
		clean(t, repo)
	})

	t.Run("an ignored level index stops a write and a push", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		lab := mkdirT(t, filepath.Join(home, "lab"))
		commitByHand(t, repo, map[string]string{
			"lab/memories/by-hand.md": memoryText("by-hand", "Read when you test a memory by hand.", "A fact."),
		})
		exclude(t, repo, "/lab/README.md")
		writeT(t, repo, "lab/README.md", "mine")
		before := gitT(t, repo, "rev-parse", "HEAD")
		_, err := run(t, lab, memoryText("new-fact", "Read before you deploy the lab.", "A fact."), "remember")
		if err == nil || !strings.Contains(err.Error(), "lab/README.md") {
			t.Errorf("remember: %v, want a refusal that names lab/README.md", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("the refused remember made a commit")
		}
		var asked string
		_, err = runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push")
		if err == nil || !strings.Contains(err.Error(), "lab/README.md") {
			t.Errorf("push: %v, want a refusal that names lab/README.md", err)
		}
		survives(t, repo, "lab/README.md", "mine")
		clean(t, repo)
	})

	t.Run("remember into a topic folder with an ignored index", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		remember(t, dev, "a-fact", "Read when you test a fact.")
		exclude(t, repo, "/dev/memories/hidden/README.md")
		index := topicReadme("hidden", "Read when you test a hidden topic.")
		writeT(t, repo, "dev/memories/hidden/README.md", index)
		if out, err := run(t, dev, memoryText("inside", "Read before you deploy inside a hidden container.", "A fact."), "remember", "--topic", "hidden"); err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		survives(t, repo, "dev/memories/hidden/README.md", index)
		if got := gitT(t, repo, "show", "--name-only", "--format=", "HEAD"); strings.Contains(got, "hidden/README.md") {
			t.Errorf("the commit changes\n%s\nwant no topic index", got)
		}
		clean(t, repo)
	})
}

func TestIgnoredInReads(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	t.Run("orient counts no ignored plan and no ignored note", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		commitByHand(t, repo, map[string]string{
			"dev/plans/p.md": "---\nname: p\ndescription: \"Read when you build p.\"\n---\n",
			"dev/notes/n.md": "---\nname: n\ndescription: \"Read when you test n.\"\n---\n",
		})
		exclude(t, repo, "*.draft.md", "old/")
		writeT(t, repo, "dev/plans/q.draft.md", "---\nname: q\n---\n")
		writeT(t, repo, "dev/notes/m.draft.md", "---\nname: m\n---\n")
		writeT(t, repo, "dev/notes/old/o.md", "---\nname: o\n---\n")
		out, err := run(t, dev, "", "orient")
		if err != nil {
			t.Fatalf("orient: %v\n%s", err, out)
		}
		if !strings.Contains(out, "1 plan and 1 note:") {
			t.Errorf("orient =\n%s\nwant 1 plan and 1 note", out)
		}
	})

	t.Run("remember accepts a name that only an ignored memory holds", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		exclude(t, repo, "/lab/memories/taken.md")
		writeT(t, repo, "lab/memories/taken.md", memoryText("taken", "Read when you test an ignored memory.", "A fact."))
		remember(t, dev, "taken", "Read before you deploy the shop.")
		survives(t, repo, "lab/memories/taken.md", memoryText("taken", "Read when you test an ignored memory.", "A fact."))
		clean(t, repo)
	})

	t.Run("draft script accepts a name that only an ignored script holds", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		exclude(t, repo, "/lab/bin/ignored-tool")
		writeT(t, repo, "lab/bin/ignored-tool", "#!/bin/sh\n# Run when you test.\n")
		if err := os.Chmod(filepath.Join(repo, "lab", "bin", "ignored-tool"), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := run(t, dev, "", "draft", "script", "ignored-tool", "--description", "Run when you test a script."); err != nil {
			t.Fatalf("draft script: %v\n%s", err, out)
		}
		clean(t, repo)
	})

	t.Run("draft refuses a file of a skill whose SKILL.md is ignored", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		exclude(t, repo, "/dev/skills/ghost/SKILL.md")
		writeT(t, repo, "dev/skills/ghost/SKILL.md", "---\nname: ghost\ndescription: \"Use when you test.\"\n---\n")
		if _, err := run(t, dev, "", "draft", "skill", "ghost/check.sh"); err == nil || !strings.Contains(err.Error(), "no skill ghost") {
			t.Errorf("error = %v, want a refusal that says there is no skill ghost", err)
		}
		clean(t, repo)
	})

	t.Run("the import writes no memory over an ignored file", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		claude := filepath.Join(home, ".claude")
		shop := mkdirT(t, filepath.Join(home, "dev", "shop"))
		remember(t, shop, "a-fact", "Read when you test a fact.")
		exclude(t, repo, "/dev/shop/memories/deploy-day.md")
		writeT(t, repo, "dev/shop/memories/deploy-day.md", "mine")
		autoMemory(t, claude, shop, "deploy_day.md", flat("deploy-day", "Read before you deploy.", "project", "Deploy on Fridays."))
		importNow(t, home, repo, claude)
		survives(t, repo, "dev/shop/memories/deploy-day.md", "mine")
		if got := gitT(t, repo, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(got, "dev/shop/memories/deploy-day-shop.md") {
			t.Errorf("the import commit changes\n%s\nwant dev/shop/memories/deploy-day-shop.md", got)
		}
		clean(t, repo)
	})

	t.Run("the import reads no name of an ignored memory", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		exclude(t, repo, "*.bak.md")
		writeT(t, repo, "dev/memories/kept.md", memoryText("kept", "Read when you test a kept fact.", "A fact."))
		writeT(t, repo, "dev/memories/old.bak.md", memoryText("old.bak", "Read when you test a backup.", "A fact."))
		tree, err := store.OpenTree(repo)
		if err != nil {
			t.Fatal(err)
		}
		names, err := memoryNames(tree)
		if err != nil {
			t.Fatal(err)
		}
		if !names["kept"] || names["old.bak"] {
			t.Errorf("memoryNames = %v, want kept and not old.bak", names)
		}
	})
}
