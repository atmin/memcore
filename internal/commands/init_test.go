package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	t.Run("empty remote", func(t *testing.T) {
		_, repo, remote := newMemory(t)
		if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s")); got != "init: create the memory repo" {
			t.Errorf("history = %q", got)
		}
		if got := strings.TrimSpace(gitT(t, repo, "remote", "get-url", "origin")); got != remote {
			t.Errorf("origin = %q, want %q", got, remote)
		}
		if got := strings.TrimSpace(gitT(t, repo, "branch", "--show-current")); got != "main" {
			t.Errorf("branch = %q, want main", got)
		}
		voice := readT(t, filepath.Join(repo, "skills", "voice", "SKILL.md"))
		if !strings.Contains(voice, "name: voice") {
			t.Errorf("voice skill = %q", voice)
		}
		index := readT(t, filepath.Join(repo, "README.md"))
		if !strings.HasPrefix(index, "# ~\n\n## Skills\n\n- [voice](skills/voice/SKILL.md): ") {
			t.Errorf("root index = %q", index)
		}
		if got := strings.TrimSpace(gitT(t, repo, "status", "--porcelain")); got != "" {
			t.Errorf("status = %q, want clean", got)
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "memcore")); err != nil {
			t.Errorf("local state: %v", err)
		}
	})

	t.Run("second run", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		head := gitT(t, repo, "rev-parse", "HEAD")
		out, err := run(t, home, "", "init", remote)
		if err != nil {
			t.Fatalf("second init: %v\n%s", err, out)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != head {
			t.Errorf("HEAD moved from %s to %s", head, got)
		}
		if got := strings.TrimSpace(gitT(t, repo, "status", "--porcelain")); got != "" {
			t.Errorf("status = %q, want clean", got)
		}
		if !strings.Contains(out, "already") {
			t.Errorf("output = %q, want one that says the repo is already set up", out)
		}
	})

	t.Run("remote that holds a memory repo", func(t *testing.T) {
		_, first, remote := newMemory(t)
		gitT(t, first, "push", "-q", "origin", "main")

		home := newHome(t)
		out, err := run(t, home, "", "init", remote)
		if err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		repo := filepath.Join(home, "memory")
		if got, want := gitT(t, repo, "rev-parse", "HEAD"), gitT(t, first, "rev-parse", "HEAD"); got != want {
			t.Errorf("HEAD = %s, want the pushed %s", got, want)
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "memcore")); err != nil {
			t.Errorf("local state: %v", err)
		}
		if out, err := run(t, home, "", "init", remote); err != nil || !strings.Contains(out, "already") {
			t.Errorf("init after the clone: %v\n%s", err, out)
		}
	})

	t.Run("remote whose HEAD names master", func(t *testing.T) {
		_, first, remote := newMemory(t)
		gitT(t, first, "push", "-q", "origin", "main")
		gitT(t, remote, "symbolic-ref", "HEAD", "refs/heads/master")

		home := newHome(t)
		if out, err := run(t, home, "", "init", remote); err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		repo := filepath.Join(home, "memory")
		if got := strings.TrimSpace(gitT(t, repo, "branch", "--show-current")); got != "main" {
			t.Errorf("branch = %q, want main", got)
		}
		if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
			t.Errorf("root index: %v", err)
		}
	})

	t.Run("failed clone", func(t *testing.T) {
		remote := bareRemote(t)
		other := t.TempDir()
		gitT(t, other, "init", "-q", "-b", "master")
		gitT(t, other, "commit", "-q", "--allow-empty", "-m", "first")
		gitT(t, other, "push", "-q", remote, "master")

		home := newHome(t)
		if out, err := run(t, home, "", "init", remote); err == nil {
			t.Fatalf("init of a remote without main: no error\n%s", out)
		}
		entries, err := os.ReadDir(home)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("home holds %v after a failed init, want nothing", entries)
		}
	})

	t.Run("concurrent runs", func(t *testing.T) {
		home := newHome(t)
		remote := bareRemote(t)
		errs := make(chan error, 2)
		for range 2 {
			go func() {
				var out strings.Builder
				errs <- runInit(Env{Dir: home, Out: &out, Err: &out}, []string{remote})
			}()
		}
		for range 2 {
			if err := <-errs; err != nil {
				t.Errorf("init: %v", err)
			}
		}
		repo := filepath.Join(home, "memory")
		if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s")); got != "init: create the memory repo" {
			t.Errorf("history = %q, want one commit", got)
		}
	})

	t.Run("a commit by hand after init", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		writeT(t, repo, "plans/first.md", "---\nname: first\ndescription: \"Read when you plan.\"\n---\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a plan by hand, so the root index is behind")
		head := gitT(t, repo, "rev-parse", "HEAD")
		out, err := run(t, home, "", "init", remote)
		if err != nil || !strings.Contains(out, "already") {
			t.Fatalf("init after a commit by hand: %v, output %q, want one that says the repo is already set up", err, out)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != head {
			t.Errorf("HEAD moved from %s to %s", head, got)
		}
	})

	t.Run("foreign git repo", func(t *testing.T) {
		home := newHome(t)
		repo := mkdirT(t, filepath.Join(home, "memory"))
		gitT(t, repo, "init", "-q", "-b", "main")
		writeT(t, repo, "README.md", "# ~\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "Describe my notes")
		out, err := run(t, home, "", "init", bareRemote(t))
		if err == nil || !strings.Contains(err.Error(), "not a memory repo") {
			t.Fatalf("error = %v, want a refusal\n%s", err, out)
		}
		if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s")); got != "Describe my notes" {
			t.Errorf("history = %q, want it untouched", got)
		}
	})

	t.Run("foreign directory", func(t *testing.T) {
		home := newHome(t)
		writeT(t, home, "memory/README.md", "# My notes\n")
		out, err := run(t, home, "", "init", bareRemote(t))
		if err == nil || !strings.Contains(err.Error(), "not a memory repo") {
			t.Fatalf("error = %v, want a refusal\n%s", err, out)
		}
		if got := readT(t, filepath.Join(home, "memory", "README.md")); got != "# My notes\n" {
			t.Errorf("README.md = %q, want it untouched", got)
		}
		if _, err := os.Stat(filepath.Join(home, "memory", ".git")); err == nil {
			t.Error("init created .git in a foreign directory")
		}
	})
}
