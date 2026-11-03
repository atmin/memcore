package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

func TestScriptLinks(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, remote := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	link := filepath.Join(home, ".local", "bin", "hello")
	script := func(text string) string { return "#!/bin/sh\n# Run when you greet.\n" + text + "\n" }

	if out, err := run(t, dev, "", "draft", "script", "hello", "--description", "Run when you greet."); err != nil {
		t.Fatalf("draft: %v\n%s", err, out)
	}
	if out, err := run(t, dev, script("echo accepted"), "revise", "dev/bin/hello", "--reason", "Say hello."); err != nil {
		t.Fatalf("revise: %v\n%s", err, out)
	}

	t.Run("a pushed script gets a link into the checkout", func(t *testing.T) {
		pushYes(t, home)
		target, err := os.Readlink(link)
		if err != nil {
			t.Fatalf("no link at %s: %v", link, err)
		}
		if !strings.HasPrefix(target, filepath.Join(store.StateDir(repo), "accepted")+string(filepath.Separator)) {
			t.Errorf("the link points at %s, want the accepted checkout", target)
		}
		if got := readT(t, link); got != script("echo accepted") {
			t.Errorf("the linked script = %q", got)
		}
	})

	t.Run("an unpushed revision changes nothing that the link runs", func(t *testing.T) {
		if out, err := run(t, dev, script("echo unreviewed"), "revise", "dev/bin/hello", "--reason", "Louder."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		if got := readT(t, link); got != script("echo accepted") {
			t.Errorf("the linked script = %q, want the accepted one", got)
		}
	})

	t.Run("a forgotten script loses its link after the push", func(t *testing.T) {
		if out, err := run(t, dev, "", "forget", "dev/bin/hello", "--reason", "No longer used."); err != nil {
			t.Fatalf("forget: %v\n%s", err, out)
		}
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("the link went before the push: %v", err)
		}
		pushYes(t, home)
		if _, err := os.Lstat(link); err == nil {
			t.Error("the link of the forgotten script is still there")
		}
	})

	byHand := func(message string, files map[string]string, modes map[string]os.FileMode) {
		t.Helper()
		for p, content := range files {
			writeT(t, repo, p, content)
			if m, ok := modes[p]; ok {
				if err := os.Chmod(filepath.Join(repo, filepath.FromSlash(p)), m); err != nil {
					t.Fatal(err)
				}
			}
		}
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", message)
	}
	bin := filepath.Join(home, ".local", "bin")

	t.Run("a symlink and an untracked file get no link", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(repo, "dev", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../notes/plain.md", filepath.Join(repo, "dev", "bin", "sneaky")); err != nil {
			t.Fatal(err)
		}
		byHand("test: a symlink in bin", map[string]string{"dev/notes/plain.md": "---\nname: plain\ndescription: \"Read when you test.\"\n---\n"}, nil)
		pushYes(t, home)
		writeT(t, filepath.Join(store.StateDir(repo), "accepted", "dev", "bin"), "stray", "#!/bin/sh\n")
		os.Chmod(filepath.Join(store.StateDir(repo), "accepted", "dev", "bin", "stray"), 0o755)
		byHand("test: a note", map[string]string{"dev/notes/other.md": "---\nname: other\ndescription: \"Read when you test.\"\n---\n"}, nil)
		pushYes(t, home)
		for _, name := range []string{"sneaky", "stray"} {
			if _, err := os.Lstat(filepath.Join(bin, name)); err == nil {
				t.Errorf("%s got a link", name)
			}
		}
	})

	t.Run("two scripts with one name refuse the push", func(t *testing.T) {
		start := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
		byHand("test: two scripts named twin", map[string]string{"dev/bin/twin": "#!/bin/sh\n", "lab/bin/twin": "#!/bin/sh\n"}, map[string]os.FileMode{"dev/bin/twin": 0o755, "lab/bin/twin": 0o755})
		var asked string
		_, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push")
		if err == nil || !strings.Contains(err.Error(), "dev/bin/twin") || !strings.Contains(err.Error(), "lab/bin/twin") {
			t.Errorf("push: error = %v, want a refusal that names both scripts", err)
		}
		gitT(t, repo, "reset", "-q", "--hard", start)
	})

	t.Run("a script that becomes a helper loses its link", func(t *testing.T) {
		byHand("test: a script", map[string]string{"dev/bin/demoted": "#!/bin/sh\n"}, map[string]os.FileMode{"dev/bin/demoted": 0o755})
		pushYes(t, home)
		if _, err := os.Lstat(filepath.Join(bin, "demoted")); err != nil {
			t.Fatalf("no link for the script: %v", err)
		}
		os.Chmod(filepath.Join(repo, "dev", "bin", "demoted"), 0o644)
		gitT(t, repo, "commit", "-q", "-am", "test: the script becomes a helper")
		pushYes(t, home)
		if _, err := os.Lstat(filepath.Join(bin, "demoted")); err == nil {
			t.Error("the helper kept the link of the script")
		}
	})

	t.Run("a foreign link that names the checkout through .. stays foreign", func(t *testing.T) {
		start := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
		sly := filepath.Join(bin, "sly")
		if err := os.Symlink(store.StateDir(repo)+"/accepted/../outside", sly); err != nil { // a literal ..
			t.Fatal(err)
		}
		byHand("test: a script named sly", map[string]string{"dev/bin/sly": "#!/bin/sh\n"}, map[string]os.FileMode{"dev/bin/sly": 0o755})
		var asked string
		if _, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err == nil || !strings.Contains(err.Error(), sly) {
			t.Errorf("push: error = %v, want a refusal that names %s", err, sly)
		}
		gitT(t, repo, "reset", "-q", "--hard", start)
		os.Remove(sly)
	})

	t.Run("a link that cannot go makes an error", func(t *testing.T) {
		byHand("test: a script named sticky", map[string]string{"dev/bin/sticky": "#!/bin/sh\n"}, map[string]os.FileMode{"dev/bin/sticky": 0o755})
		pushYes(t, home)
		gitT(t, repo, "rm", "-q", "dev/bin/sticky")
		gitT(t, repo, "commit", "-q", "-m", "test: the script goes")
		if err := os.Chmod(bin, 0o555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(bin, 0o755)
		var asked string
		if _, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err == nil || !strings.Contains(err.Error(), "sticky") {
			t.Errorf("push: error = %v, want one that names the link that stayed", err)
		}
	})

	t.Run("a foreign link through .. holds its name for draft", func(t *testing.T) {
		sly := filepath.Join(bin, "sly-draft")
		outside := filepath.Join(store.StateDir(repo), "outside")
		writeT(t, filepath.Dir(outside), "outside", "#!/bin/sh\n")
		if err := os.Chmod(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(outside)
		if err := os.Symlink(store.StateDir(repo)+"/accepted/../outside", sly); err != nil { // a literal ..
			t.Fatal(err)
		}
		defer os.Remove(sly)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if _, err := run(t, dev, "", "draft", "script", "sly-draft", "--description", "Run when you test."); err == nil || !strings.Contains(err.Error(), "PATH") {
			t.Errorf("draft: error = %v, want a refusal of the foreign link", err)
		}
	})

	t.Run("a foreign file that appears while the person answers", func(t *testing.T) {
		start := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
		byHand("test: a script named late", map[string]string{"dev/bin/late": "#!/bin/sh\n"}, map[string]os.FileMode{"dev/bin/late": 0o755})
		late := filepath.Join(bin, "late")
		confirm := func(string) (bool, error) {
			writeT(t, bin, "late", "#!/bin/sh\necho mine\n")
			return true, nil
		}
		before := gitT(t, remote, "rev-parse", "main")
		if _, err := runWith(t, Env{Dir: home, Confirm: confirm}, "push"); err == nil || !strings.Contains(err.Error(), late) {
			t.Errorf("push: error = %v, want a refusal that names %s", err, late)
		}
		if got := gitT(t, remote, "rev-parse", "main"); got != before {
			t.Error("the refused push moved the remote")
		}
		gitT(t, repo, "reset", "-q", "--hard", start)
		os.Remove(late)
	})

	t.Run("a foreign file with the name of a new script refuses the push", func(t *testing.T) {
		foreign := filepath.Join(home, ".local", "bin", "clash")
		writeT(t, filepath.Dir(foreign), "clash", "#!/bin/sh\necho mine\n")
		if out, err := run(t, dev, "", "draft", "script", "clash", "--description", "Run when you test a clash."); err != nil {
			t.Fatalf("draft: %v\n%s", err, out)
		}
		before := gitT(t, remote, "rev-parse", "main")
		var asked string
		if _, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "push"); err == nil || !strings.Contains(err.Error(), foreign) {
			t.Errorf("push: error = %v, want a refusal that names %s", err, foreign)
		}
		if got := gitT(t, remote, "rev-parse", "main"); got != before {
			t.Error("the refused push moved the remote")
		}
		if got := readT(t, foreign); got != "#!/bin/sh\necho mine\n" {
			t.Errorf("the foreign file = %q, want it untouched", got)
		}
	})
}

func TestRecoverBeforeLinks(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, remote := newMemory(t)
	writeT(t, repo, "dev/bin/tool", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(repo, "dev", "bin", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "test: a script")
	pushYes(t, home)
	first := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	remember(t, mkdirT(t, filepath.Join(home, "dev")), "later-fact", "Read when you test a later fact.")
	pushYes(t, home)
	gitT(t, remote, "update-ref", "refs/heads/main", first)
	if err := Fetch(repo); err == nil {
		t.Fatal("Fetch over a rewound remote: no error")
	}
	bin := filepath.Join(home, ".local", "bin")
	if err := os.RemoveAll(bin); err != nil {
		t.Fatal(err)
	}
	writeT(t, filepath.Dir(bin), "bin", "a file where the folder was\n")

	var asked string
	_, err := runWith(t, Env{Dir: home, Confirm: answer(true, &asked)}, "recover")
	if err == nil || !strings.Contains(err.Error(), "recovery is done") {
		t.Errorf("recover: error = %v, want a link failure after a done recovery", err)
	}
	if _, err := os.Stat(markerPath(repo)); err == nil {
		t.Error("the marker came back after the link failed")
	}
	if got := strings.TrimSpace(gitT(t, repo, "rev-parse", "origin/main")); got != first {
		t.Errorf("origin/main = %s, want %s", got, first)
	}
}
