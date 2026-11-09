package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// secondClone clones the remote into a new directory, as another machine would.
func secondClone(t *testing.T, remote string) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "memory")
	gitT(t, t.TempDir(), "clone", "-q", remote, other)
	return other
}

func TestFetch(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	t.Run("a pushed commit lands under the queue", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		gitT(t, repo, "push", "-q", "origin", "main")
		other := secondClone(t, remote)
		writeT(t, other, "work/notes/2026-11-02-elsewhere.md", "From another machine.\n")
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: a commit from another machine")
		gitT(t, other, "push", "-q", "origin", "main")
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "small-commits", "Read before you commit: one change per commit.")

		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		got := strings.Fields(strings.ReplaceAll(gitT(t, repo, "log", "--format=%s|"), " ", "_"))
		want := "remember:_dev/memories/small-commits.md| test:_a_commit_from_another_machine| init:_create_the_memory_repo|"
		if strings.Join(got, " ") != want {
			t.Errorf("history = %q, want %q", got, want)
		}
		if got := strings.TrimSpace(gitT(t, repo, "rev-list", "--count", "origin/main..main")); got != "1" {
			t.Errorf("queue holds %s commits, want 1", got)
		}
	})

	t.Run("both sides add a memory at one level", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		gitT(t, repo, "push", "-q", "origin", "main")
		other := secondClone(t, remote)
		t.Setenv("MEMCORE_REPO", other)
		remember(t, dev, "from-elsewhere", "Read when you work on another machine.")
		gitT(t, other, "push", "-q", "origin", "main")
		t.Setenv("MEMCORE_REPO", "")
		remember(t, dev, "from-here", "Read when you work on this machine.")

		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		index := readT(t, filepath.Join(repo, "dev", "README.md"))
		ix, err := store.Lines(repo, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if index != store.Render(ix) || !strings.Contains(index, "from-elsewhere") || !strings.Contains(index, "from-here") {
			t.Errorf("dev/README.md =\n%s\nwant both memories, as generated", index)
		}
		if got := strings.TrimSpace(gitT(t, repo, "rev-list", "--count", "origin/main..main")); got != "1" {
			t.Errorf("queue holds %s commits, want 1", got)
		}
		clean(t, repo)
	})

	t.Run("a conflict stops with the path", func(t *testing.T) {
		_, repo, remote := newMemory(t)
		gitT(t, repo, "push", "-q", "origin", "main")
		other := secondClone(t, remote)
		writeT(t, other, "work/notes/same.md", "The remote version.\n")
		gitT(t, other, "add", "-A")
		gitT(t, other, "commit", "-q", "-m", "test: the remote version")
		gitT(t, other, "push", "-q", "origin", "main")
		writeT(t, repo, "work/notes/same.md", "The local version.\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: the local version")
		before := gitT(t, repo, "rev-parse", "HEAD")

		err := Fetch(repo)
		if err == nil || !strings.Contains(err.Error(), "work/notes/same.md") {
			t.Fatalf("error = %v, want a conflict that names work/notes/same.md", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Errorf("HEAD = %s, want it back at %s", got, before)
		}
		for _, d := range []string{"rebase-merge", "rebase-apply"} {
			if _, err := os.Stat(filepath.Join(repo, ".git", d)); err == nil {
				t.Errorf("a rebase is still in progress: .git/%s exists", d)
			}
		}
		clean(t, repo)

		if _, err := store.Git(repo, "rebase", "origin/main"); err == nil {
			t.Fatal("the rebase by hand: no conflict")
		}
		err = Fetch(repo)
		if err == nil || !strings.Contains(err.Error(), "rebase is in progress") {
			t.Fatalf("error = %v, want a refusal of the rebase in progress", err)
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "rebase-merge")); err != nil {
			t.Errorf("Fetch ended the rebase of the person: %v", err)
		}
	})

	t.Run("another branch", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		gitT(t, repo, "push", "-q", "origin", "main")
		gitT(t, repo, "checkout", "-q", "-b", "draft")
		if err := Fetch(repo); err == nil || !strings.Contains(err.Error(), "main") {
			t.Errorf("error = %v, want a refusal that names main", err)
		}
	})
}

func TestRecoverRefusesAnAgent(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, remote := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	first := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	remember(t, dev, "pushed-then-lost", "Read when the remote loses a commit.")
	pushYes(t, home)
	remember(t, dev, "still-unpushed", "Read when a commit waits in the queue.")
	gitT(t, remote, "update-ref", "refs/heads/main", first)
	if err := Fetch(repo); err == nil || !strings.Contains(err.Error(), "memcore recover") {
		t.Fatalf("Fetch: error = %v, want one that names memcore recover", err)
	}
	refs := gitT(t, repo, "for-each-ref")
	queue := gitT(t, repo, "log", "--format=%H", "origin/main..main")

	t.Setenv("CLAUDECODE", "1")
	if _, err := run(t, home, "", "recover"); err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("recover by an agent: error = %v, want a refusal that names CLAUDECODE", err)
	}
	if got := gitT(t, repo, "for-each-ref"); got != refs {
		t.Errorf("refs after the refusal =\n%s\nwant\n%s", got, refs)
	}
	if got := gitT(t, repo, "log", "--format=%H", "origin/main..main"); got != queue {
		t.Errorf("queue after the refusal = %q, want %q", got, queue)
	}
	if _, ok, err := readMarker(repo); !ok || err != nil {
		t.Errorf("the marker of the rewritten remote = %v, %v after the refusal, want it kept", ok, err)
	}
}

func TestRewrittenRemote(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, remote := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	first := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	remember(t, dev, "pushed-then-lost", "Read when the remote loses a commit.")
	var asked string
	if out, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	lost := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	remember(t, dev, "still-unpushed", "Read when a commit waits in the queue.")
	gitT(t, remote, "update-ref", "refs/heads/main", first)

	err := Fetch(repo)
	if err == nil || !strings.Contains(err.Error(), "memcore recover") {
		t.Fatalf("Fetch: error = %v, want one that names memcore recover", err)
	}
	if got := strings.TrimSpace(gitT(t, repo, "rev-parse", "origin/main")); got != lost {
		t.Errorf("origin/main = %s, want it still at %s", got, lost)
	}
	if got := strings.TrimSpace(gitT(t, repo, "rev-list", "--count", "origin/main..main")); got != "1" {
		t.Errorf("queue holds %s commits, want 1", got)
	}

	if _, err := run(t, dev, memoryText("a-third", "Read when you write after a rewrite.", "A fact."), "remember"); err == nil || !strings.Contains(err.Error(), "memcore recover") {
		t.Errorf("remember: error = %v, want a refusal that names memcore recover", err)
	}
	if _, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err == nil || !strings.Contains(err.Error(), "memcore recover") {
		t.Errorf("push: error = %v, want a refusal that names memcore recover", err)
	}
	out, _ := run(t, home, "", "inbox")
	if !strings.Contains(out, "memcore recover") || !strings.Contains(out, "remember: dev/memories/pushed-then-lost.md") {
		t.Errorf("inbox = %q, want the lost commit and memcore recover", out)
	}

	before := gitT(t, repo, "rev-parse", "HEAD")
	if out, err := runWith(t, Env{Dir: home, Confirm: answer(false, &asked)}, "recover"); err != nil {
		t.Fatalf("recover, no: %v\n%s", err, out)
	}
	if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
		t.Errorf("recover with no moved HEAD from %s to %s", before, got)
	}

	if out, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "recover"); err != nil {
		t.Fatalf("recover, yes: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(gitT(t, repo, "rev-parse", "origin/main")); got != first {
		t.Errorf("origin/main = %s, want the remote tip %s", got, first)
	}
	if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s", "origin/main..main")); got != "remember: dev/memories/still-unpushed.md" {
		t.Errorf("queue = %q, want only the unpushed commit", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "dev", "memories", "pushed-then-lost.md")); err == nil {
		t.Error("the lost memory is still in the working tree")
	}
	clean(t, repo)
	remember(t, dev, "a-third", "Read when you write after a recovery.")

	gitT(t, repo, "reflog", "expire", "--expire=now", "--all")
	gitT(t, repo, "gc", "-q", "--prune=now")
	if _, err := store.Git(repo, "cat-file", "-e", lost); err != nil {
		t.Errorf("the dropped accepted commit %s is gone after gc: %v", lost, err)
	}
}

// topicByHand writes a file into a topic folder of repo, writes the indexes on its path, and commits.
func topicByHand(t *testing.T, repo, file, content, message string) {
	t.Helper()
	writeT(t, repo, file, content)
	if err := store.WriteTopicIndex(repo, "dev/memories/t"); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"dev", ""} {
		if err := store.WriteIndex(repo, l); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", message)
}

func TestFetchTopics(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	setup := func(t *testing.T) (repo, other string) {
		home, repo, remote := newMemory(t)
		pushedByHand(t, home, repo, map[string]string{
			"dev/memories/t/README.md": topicReadme("t", "Read when you test a topic."),
			"dev/memories/t/a-fact.md": memoryText("a-fact", "Read when you test fact a.", "A fact."),
		})
		return repo, secondClone(t, remote)
	}

	t.Run("both sides add a memory to one topic folder", func(t *testing.T) {
		repo, other := setup(t)
		topicByHand(t, other, "dev/memories/t/elsewhere.md", memoryText("elsewhere", "Read when you work elsewhere.", "A fact."), "test: elsewhere")
		gitT(t, other, "push", "-q", "origin", "main")
		topicByHand(t, repo, "dev/memories/t/here.md", memoryText("here", "Read when you work here.", "A fact."), "test: here")

		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		ix, err := store.TopicIndex(repo, "dev/memories/t")
		if err != nil {
			t.Fatal(err)
		}
		want, err := store.RenderTopic(repo, ix)
		if err != nil {
			t.Fatal(err)
		}
		if got := readT(t, filepath.Join(repo, "dev", "memories", "t", "README.md")); got != want || !strings.Contains(got, "elsewhere") || !strings.Contains(got, "here.md") {
			t.Errorf("topic index =\n%s\nwant both memories, as generated", got)
		}
		clean(t, repo)
	})

	t.Run("one side changes the description, and both add a memory", func(t *testing.T) {
		repo, other := setup(t)
		writeT(t, other, "dev/memories/t/README.md", topicReadme("t", "Read when you test a topic elsewhere."))
		topicByHand(t, other, "dev/memories/t/there.md", memoryText("there", "Read when you work there.", "A fact."), "test: elsewhere")
		gitT(t, other, "push", "-q", "origin", "main")
		topicByHand(t, repo, "dev/memories/t/here.md", memoryText("here", "Read when you work here.", "A fact."), "test: here")

		if err := Fetch(repo); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		got := readT(t, filepath.Join(repo, "dev", "memories", "t", "README.md"))
		if !strings.Contains(got, "test a topic elsewhere") || !strings.Contains(got, "here.md") || !strings.Contains(got, "there.md") {
			t.Errorf("topic index =\n%s\nwant the new description and both memories", got)
		}
		clean(t, repo)
	})

	t.Run("both sides change the description of a topic folder", func(t *testing.T) {
		repo, other := setup(t)
		topicByHand(t, other, "dev/memories/t/README.md", topicReadme("t", "Read when you test a topic elsewhere."), "test: elsewhere")
		gitT(t, other, "push", "-q", "origin", "main")
		topicByHand(t, repo, "dev/memories/t/README.md", topicReadme("t", "Read when you test a topic here."), "test: here")
		before := gitT(t, repo, "rev-parse", "HEAD")

		err := Fetch(repo)
		if err == nil || !strings.Contains(err.Error(), "dev/memories/t/README.md") {
			t.Fatalf("error = %v, want a conflict that names the topic index", err)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Errorf("HEAD = %s, want it back at %s", got, before)
		}
		clean(t, repo)
	})
}
