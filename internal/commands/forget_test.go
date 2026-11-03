package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commitByHand writes files into the repo and commits them, as the person would.
func commitByHand(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		writeT(t, repo, p, content)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "test: files by hand")
}

func TestForget(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))

	t.Run("a pushed memory", func(t *testing.T) {
		remember(t, dev, "old-fact", "Read when you test an old fact.")
		gitT(t, repo, "push", "-q", "origin", "main")
		if out, err := run(t, dev, "", "forget", "old-fact", "--reason", "It no longer holds."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		if got := message(t, repo); got != "forget: dev/memories/old-fact.md\n\nIt no longer holds.\n\n" {
			t.Errorf("message = %q", got)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "memories", "old-fact.md")); err == nil {
			t.Error("the memory is still there")
		}
		clean(t, repo)
	})

	t.Run("an unpushed memory", func(t *testing.T) {
		remember(t, dev, "fresh-fact", "Read when you test a fresh fact.")
		if out, err := run(t, dev, "", "forget", "fresh-fact", "--reason", "A mistake."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "fixup! remember: dev/memories/fresh-fact.md" {
			t.Errorf("subject = %q, want a fixup of the remember", got)
		}
	})

	t.Run("a memory that another links to", func(t *testing.T) {
		remember(t, dev, "linked-fact", "Read when you test a linked fact.")
		if out, err := run(t, dev, memoryText("linking-fact", "Read before you follow a link.", "See [[linked-fact]]."), "remember"); err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		before := gitT(t, repo, "rev-parse", "HEAD")
		_, err := run(t, dev, "", "forget", "linked-fact", "--reason", "A test.")
		if err == nil || !strings.Contains(err.Error(), "dev/memories/linking-fact.md") {
			t.Errorf("error = %v, want a refusal that names the linking memory", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("the refused forget made a commit")
		}
		clean(t, repo)
	})

	t.Run("a plan that a note links to", func(t *testing.T) {
		commitByHand(t, repo, map[string]string{
			"dev/plans/build-x.md":   "---\nname: build-x\ndescription: \"Read when you build x.\"\n---\n\n# Build x\n",
			"dev/notes/x-trial.md":   "---\nname: x-trial\ndescription: \"Read when you try x.\"\n---\n\nSee [the plan](../plans/build-x.md).\n",
			"dev/notes/unrelated.md": "---\nname: unrelated\ndescription: \"Read when you test.\"\n---\n\nSee [a gone page](../plans/gone.md).\n",
		})
		_, err := run(t, dev, "", "forget", "dev/plans/build-x.md", "--reason", "A test.")
		if err == nil || !strings.Contains(err.Error(), "dev/notes/x-trial.md") || strings.Contains(err.Error(), "unrelated") {
			t.Errorf("error = %v, want a refusal that names only the new fault", err)
		}
	})

	t.Run("a skill folder", func(t *testing.T) {
		commitByHand(t, repo, map[string]string{
			"dev/skills/deploy/SKILL.md": "---\nname: deploy\ndescription: \"Use when you deploy.\"\n---\n\n# Deploy\n",
			"dev/skills/deploy/check.sh": "#!/bin/sh\n",
		})
		gitT(t, repo, "push", "-q", "origin", "main")
		if out, err := run(t, dev, "", "forget", "dev/skills/deploy/SKILL.md", "--reason", "Retired."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "skills", "deploy")); err == nil {
			t.Error("the skill folder is still there")
		}
		if got := head(t, repo); got != "forget: dev/skills/deploy" {
			t.Errorf("subject = %q", got)
		}
		clean(t, repo)
	})

	t.Run("a link from a README inside a skill", func(t *testing.T) {
		remember(t, dev, "deploy-target", "Read before you pick a deploy target.")
		commitByHand(t, repo, map[string]string{
			"dev/skills/ship/SKILL.md":  "---\nname: ship\ndescription: \"Use when you ship.\"\n---\n\n# Ship\n",
			"dev/skills/ship/README.md": "See [[deploy-target]].\n",
		})
		_, err := run(t, dev, "", "forget", "deploy-target", "--reason", "A test.")
		if err == nil || !strings.Contains(err.Error(), "dev/skills/ship/README.md") {
			t.Errorf("error = %v, want a refusal that names the README of the skill", err)
		}
	})

	t.Run("a link into a level that the removal empties", func(t *testing.T) {
		proj := mkdirT(t, filepath.Join(home, "lab", "proj"))
		remember(t, proj, "lab-only", "Read when you work in the lab.")
		commitByHand(t, repo, map[string]string{
			"dev/notes/lab-pointer.md": "---\nname: lab-pointer\ndescription: \"Read when you look for the lab.\"\n---\n\nSee [the lab](../../lab/proj/README.md).\n",
		})
		_, err := run(t, proj, "", "forget", "lab-only", "--reason", "A test.")
		if err == nil || !strings.Contains(err.Error(), "dev/notes/lab-pointer.md") {
			t.Errorf("error = %v, want a refusal that names the note", err)
		}
	})

	t.Run("a document that comes back after a forget", func(t *testing.T) {
		remember(t, dev, "comeback", "Read when a fact comes back.")
		gitT(t, repo, "push", "-q", "origin", "main")
		if out, err := run(t, dev, "", "forget", "comeback", "--reason", "Gone."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		gitT(t, repo, "push", "-q", "origin", "main")
		remember(t, dev, "comeback", "Read when a fact comes back.")
		if out, err := run(t, dev, "", "forget", "comeback", "--reason", "Gone again."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "fixup! remember: dev/memories/comeback.md" {
			t.Errorf("subject = %q, want a fixup of the new remember", got)
		}
	})

	t.Run("a skill folder with an empty folder inside", func(t *testing.T) {
		commitByHand(t, repo, map[string]string{
			"dev/skills/build/SKILL.md": "---\nname: build\ndescription: \"Use when you build.\"\n---\n\n# Build\n",
		})
		gitT(t, repo, "push", "-q", "origin", "main")
		mkdirT(t, filepath.Join(repo, "dev", "skills", "build", "empty", "deeper"))
		if out, err := run(t, dev, "", "forget", "dev/skills/build", "--reason", "Retired."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "skills", "build")); err == nil {
			t.Error("the skill folder is still there")
		}
		if strings.Contains(readT(t, filepath.Join(repo, "dev", "README.md")), "[build]") {
			t.Error("the index still lists the skill")
		}
	})

	t.Run("a link into a folder that the removal empties", func(t *testing.T) {
		desk := mkdirT(t, filepath.Join(home, "desk"))
		remember(t, desk, "desk-only", "Read when you work at the desk.")
		commitByHand(t, repo, map[string]string{
			"desk/notes/all.md": "---\nname: all\ndescription: \"Read when you look for every desk fact.\"\n---\n\nSee [the memories](../memories/).\n",
		})
		_, err := run(t, desk, "", "forget", "desk-only", "--reason", "A test.")
		if err == nil || !strings.Contains(err.Error(), "desk/notes/all.md") {
			t.Errorf("error = %v, want a refusal that names the note", err)
		}
	})

	t.Run("an empty folder keeps its parent, and a link to the parent stays", func(t *testing.T) {
		bench := mkdirT(t, filepath.Join(home, "bench"))
		remember(t, bench, "bench-only", "Read when you work at the bench.")
		commitByHand(t, repo, map[string]string{
			"bench/notes/all.md": "---\nname: bench-all\ndescription: \"Read when you look for every bench fact.\"\n---\n\nSee [the memories](../memories/).\n",
		})
		mkdirT(t, filepath.Join(repo, "bench", "memories", "drafts"))
		if out, err := run(t, bench, "", "forget", "bench-only", "--reason", "A test."); err != nil {
			t.Errorf("forget: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, "bench", "memories")); err != nil {
			t.Errorf("the memories folder with an empty folder inside went: %v", err)
		}
	})

	t.Run("a link into a level that empties beside an empty folder", func(t *testing.T) {
		shop := mkdirT(t, filepath.Join(home, "shop"))
		remember(t, shop, "shop-only", "Read when you work at the shop.")
		commitByHand(t, repo, map[string]string{
			"dev/notes/shop-pointer.md": "---\nname: shop-pointer\ndescription: \"Read when you look for the shop.\"\n---\n\nSee [the shop](../../shop/README.md).\n",
		})
		mkdirT(t, filepath.Join(repo, "shop", "memories", "drafts"))
		_, err := run(t, shop, "", "forget", "shop-only", "--reason", "A test.")
		if err == nil || !strings.Contains(err.Error(), "dev/notes/shop-pointer.md") {
			t.Errorf("error = %v, want a refusal that names the note", err)
		}
		if _, err := os.Stat(filepath.Join(repo, "shop", "README.md")); err != nil {
			t.Errorf("the refused forget removed the level: %v", err)
		}
	})

	t.Run("a skill goes by its SKILL.md", func(t *testing.T) {
		commitByHand(t, repo, map[string]string{"dev/skills/old-skill/SKILL.md": "---\nname: old-skill\ndescription: \"Use when you test.\"\n---\n\n# Old\n"})
		gitT(t, repo, "push", "-q", "origin", "main")
		commitByHand(t, repo, map[string]string{"dev/skills/old-skill/extra.sh": "#!/bin/sh\n"})
		if out, err := run(t, dev, "", "forget", "dev/skills/old-skill", "--reason", "Retired."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "forget: dev/skills/old-skill" {
			t.Errorf("subject = %q, want a plain forget of an accepted skill", got)
		}
		commitByHand(t, repo, map[string]string{"dev/skills/new-skill/SKILL.md": "---\nname: new-skill\ndescription: \"Use when you test.\"\n---\n\n# New\n"})
		commitByHand(t, repo, map[string]string{"dev/skills/new-skill/extra.sh": "#!/bin/sh\n"})
		if out, err := run(t, dev, "", "forget", "dev/skills/new-skill", "--reason", "A mistake."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "forget: dev/skills/new-skill" {
			t.Errorf("subject = %q, want a plain forget, since two commits made the skill", got)
		}
	})

	t.Run("the last memory of a level", func(t *testing.T) {
		proj := mkdirT(t, filepath.Join(home, "work", "proj"))
		remember(t, proj, "only-fact", "Read when you work on the project.")
		gitT(t, repo, "push", "-q", "origin", "main")
		if out, err := run(t, proj, "", "forget", "only-fact", "--reason", "The project ended."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, "work")); err == nil {
			t.Error("the empty levels work and work/proj are still there")
		}
		if strings.Contains(readT(t, filepath.Join(repo, "README.md")), "[work]") {
			t.Error("the root index still lists work")
		}
		clean(t, repo)
	})
}
