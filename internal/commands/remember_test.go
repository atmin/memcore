package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func memoryText(name, description, body string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %q\ntype: feedback\nsessions: []\n---\n\n%s\n", name, description, body)
}

// head returns the subject of the last commit.
func head(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(gitT(t, repo, "log", "-1", "--format=%s"))
}

func clean(t *testing.T, repo string) {
	t.Helper()
	if got := strings.TrimSpace(gitT(t, repo, "status", "--porcelain")); got != "" {
		t.Errorf("status = %q, want clean", got)
	}
}

func TestRemember(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, remote := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	app := mkdirT(t, filepath.Join(dev, "app"))
	gitT(t, app, "init", "-q", "-b", "main")
	writeT(t, repo, "glossary.md", "- **memory repo**: the repo. Not: memory store.\n")
	gitT(t, repo, "add", "glossary.md")
	gitT(t, repo, "commit", "-q", "-m", "test: add a glossary")

	t.Run("first memory at a new level", func(t *testing.T) {
		out, err := run(t, dev, memoryText("small-commits", "Read before you commit: one change per commit.", "Commit one change at a time."), "remember")
		if err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "remember: dev/memories/small-commits.md" {
			t.Errorf("commit = %q", got)
		}
		if got := readT(t, filepath.Join(repo, "README.md")); !strings.HasPrefix(got, "# ~\n\n") || !strings.Contains(got, "- [dev](dev/README.md): Read when you work under ~/dev.") {
			t.Errorf("root index = %q", got)
		}
		clean(t, repo)
		if out, err := run(t, home, "", "init", remote); err != nil || !strings.Contains(out, "already") {
			t.Errorf("init after remember: %v, output %q, want one that says the repo is already set up", err, out)
		}
	})

	t.Run("first memory at a new level under an existing level", func(t *testing.T) {
		out, err := run(t, filepath.Join(app), memoryText("tests-before-code", "Read before you write code: the tests come first.", "Write the tests first."), "remember")
		if err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "remember: dev/app/memories/tests-before-code.md" {
			t.Errorf("commit = %q", got)
		}
		want := "# ~/dev/app\n\n## Memories\n\n- [tests-before-code](memories/tests-before-code.md): Read before you write code: the tests come first.\n"
		if got := readT(t, filepath.Join(repo, "dev", "app", "README.md")); got != want {
			t.Errorf("index of dev/app =\n%s\nwant\n%s", got, want)
		}
		if got := readT(t, filepath.Join(repo, "dev", "README.md")); !strings.Contains(got, "- [app](app/README.md): Read when you work under ~/dev/app.") {
			t.Errorf("index of dev = %q, want the new child level", got)
		}
		files := strings.Fields(gitT(t, repo, "show", "--name-only", "--format="))
		if strings.Join(files, " ") != "dev/README.md dev/app/README.md dev/app/memories/tests-before-code.md" {
			t.Errorf("committed files = %q", files)
		}
		clean(t, repo)
	})

	t.Run("second memory at the same level", func(t *testing.T) {
		out, err := run(t, app, memoryText("api-versions", "Read when you call the API of the app.", "The API has two versions."), "remember")
		if err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		got := readT(t, filepath.Join(repo, "dev", "app", "README.md"))
		if !strings.Contains(got, "[api-versions]") || !strings.Contains(got, "[tests-before-code]") {
			t.Errorf("index of dev/app = %q", got)
		}
		clean(t, repo)
	})

	refusals := []struct {
		name, dir, stdin, want string
	}{
		{"taken name", dev, memoryText("api-versions", "Read when you deploy the shop to production servers.", "Another fact."), "taken by dev/app/memories/api-versions.md"},
		{"near-duplicate", app, memoryText("api-calls", "Read when you call the API of the app server.", "A fact."), "near-duplicates"},
		{"dead reference", app, memoryText("linked", "Read when you link notes between separate projects.", "See [[no-such-memory]]."), "[[no-such-memory]]"},
		{"forbidden word", app, memoryText("worded", "Read when you choose names for stored things.", "The memory store keeps it."), `forbidden word "memory store"`},
		{"likely secret", app, memoryText("keyed", "Read when you deploy the shop to production servers.", "The key is "+"AKIA"+"IOSFODNN7EXAMPLE."), "likely secret"},
		{"bad frontmatter", app, "---\nname: Bad Name\n---\n", "parse the memory"},
		{"outside home", t.TempDir(), memoryText("outside", "Read when you test.", "A fact."), "outside home"},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			before := gitT(t, repo, "rev-parse", "HEAD")
			out, err := run(t, tt.dir, tt.stdin, "remember")
			if err == nil || !strings.Contains(err.Error()+out, tt.want) {
				t.Fatalf("error = %v, output %q, want a refusal that holds %q", err, out, tt.want)
			}
			if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
				t.Error("a refused memory made a commit")
			}
			clean(t, repo)
		})
	}

	t.Run("near-duplicate lists the matches", func(t *testing.T) {
		out, _ := run(t, app, memoryText("api-calls", "Read when you call the API of the app server.", "A fact."), "remember")
		if !strings.Contains(out, "dev/app/memories/api-versions.md") {
			t.Errorf("output = %q, want the near-duplicate", out)
		}
	})

	t.Run("near-duplicate with --new", func(t *testing.T) {
		out, err := run(t, app, memoryText("api-calls", "Read when you call the API of the app server.", "A fact."), "remember", "--new")
		if err != nil {
			t.Fatalf("remember --new: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "remember: dev/app/memories/api-calls.md" {
			t.Errorf("commit = %q", got)
		}
	})

	t.Run("read, when, before and you do not count", func(t *testing.T) {
		out, err := run(t, app, memoryText("deploys", "Read when you deploy.", "A fact."), "remember")
		if err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		if got := head(t, repo); got != "remember: dev/app/memories/deploys.md" {
			t.Errorf("commit = %q", got)
		}
	})

	t.Run("session from the environment", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "d4d1d99a-9439-4b7d-ae7a-25f063f909c9")
		if out, err := run(t, app, memoryText("with-session", "Read when you trace a memory back to its session transcript.", "A fact."), "remember"); err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		got := readT(t, filepath.Join(repo, "dev", "app", "memories", "with-session.md"))
		if !strings.Contains(got, "sessions:\n  - claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9\n") {
			t.Errorf("memory = %q", got)
		}
	})

	t.Run("sessions only from the environment", func(t *testing.T) {
		text := strings.Replace(memoryText("given-session", "Read when you check where sessions of a memory come from.", "A fact."), "sessions: []", "sessions:\n  - claude:made-up", 1)
		if out, err := run(t, app, text, "remember"); err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		if got := readT(t, filepath.Join(repo, "dev", "app", "memories", "given-session.md")); !strings.Contains(got, "sessions: []\n") {
			t.Errorf("memory = %q, want no sessions", got)
		}
	})

	t.Run("symlinked level", func(t *testing.T) {
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(repo, "trips")); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(filepath.Join(repo, "trips"))
		dir := mkdirT(t, filepath.Join(home, "trips"))
		_, err := run(t, dir, memoryText("linked-away", "Read when you follow a symlink out of the repo.", "A fact."), "remember")
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("error = %v, want a refusal of the symlink", err)
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("remember wrote %v outside the repo", entries)
		}
	})

	t.Run("a target with changes", func(t *testing.T) {
		index := filepath.Join(repo, "dev", "README.md")
		original := readT(t, index)
		if err := os.WriteFile(index, []byte(original+"A hand edit.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := run(t, app, memoryText("over-an-edit", "Read when you write over a changed index of a level.", "A fact."), "remember")
		if err == nil || !strings.Contains(err.Error(), "dev/README.md") {
			t.Fatalf("error = %v, want a refusal that names dev/README.md", err)
		}
		if got := readT(t, index); got != original+"A hand edit.\n" {
			t.Errorf("index = %q, want the hand edit kept", got)
		}
		if err := os.WriteFile(index, []byte(original), 0o644); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("no memory repo", func(t *testing.T) {
		h := newHome(t)
		if _, err := run(t, h, memoryText("x", "Read when you test.", "A fact."), "remember"); err == nil || !strings.Contains(err.Error(), "memcore init") {
			t.Errorf("error = %v, want one that names memcore init", err)
		}
	})

	escaped := []struct {
		name, dir string
		repo      bool
		file      string
		index     string // the index that links the folder
		line      string
	}{
		{"plain ~/bin", "bin", false, "+bin/memories/paths.md", "README.md", "- [bin](+bin/README.md): Read when you work under ~/bin."},
		{"plain ~/Bin", "Bin", false, "+Bin/memories/paths.md", "README.md", "- [Bin](+Bin/README.md): Read when you work under ~/Bin."},
		{"repo at ~/dev/notes", "dev/notes", true, "dev/+notes/memories/paths.md", "dev/README.md", "- [notes](+notes/README.md): Read when you work under ~/dev/notes."},
	}
	for _, tt := range escaped {
		t.Run(tt.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			dir := mkdirT(t, filepath.Join(home, filepath.FromSlash(tt.dir)))
			if tt.repo {
				gitT(t, dir, "init", "-q", "-b", "main")
			}
			out, err := run(t, dir, memoryText("paths", "Read when you add a script to your PATH.", "A fact."), "remember")
			if err != nil {
				t.Fatalf("remember: %v\n%s", err, out)
			}
			if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(tt.file))); err != nil {
				t.Errorf("memory not at %s: %v", tt.file, err)
			}
			if got := head(t, repo); got != "remember: "+tt.file {
				t.Errorf("commit = %q", got)
			}
			if got := readT(t, filepath.Join(repo, filepath.FromSlash(tt.index))); !strings.Contains(got, tt.line) {
				t.Errorf("index %s = %q, want %q", tt.index, got, tt.line)
			}
			if got := readT(t, filepath.Join(repo, "README.md")); !strings.HasPrefix(got, "# ~\n\n") {
				t.Errorf("root index = %q, want the title # ~", got)
			}
			clean(t, repo)
		})
	}
}

func TestBudgetWarning(t *testing.T) {
	tests := []struct {
		level string
		n     int
		want  string
	}{
		{"", 40, ""},
		{"dev/app", 40, ""},
		{"", 41, "warning: ~ has 41 lines in orient, over the budget of 40: merge or move memories to fit"},
		{"dev/app", 41, "warning: ~/dev/app has 41 lines in orient, over the budget of 40: merge or move memories to fit"},
	}
	for _, tt := range tests {
		if got := budgetWarning(tt.level, tt.n); got != tt.want {
			t.Errorf("budgetWarning(%q, %d) = %q, want %q", tt.level, tt.n, got, tt.want)
		}
	}
}

func TestRememberSizeWarning(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	tests := []struct {
		name  string
		level string // the level of the memory, under home
		long  int    // the memories with long descriptions at the level before remember
		want  string // the title of the level in the warning, or "" for no warning
	}{
		{"a level under the limit", "shop", 0, ""},
		{"a level over the limit", "shop", 30, "~/shop"},
		{"the root over the limit", "", 30, "~"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			dir := mkdirT(t, filepath.Join(home, filepath.FromSlash(tt.level)))
			if tt.long > 0 {
				longLevel(t, repo, tt.level, tt.long)
			}
			out, err := run(t, dir, memoryText("branch-names", "Read before you name a branch of a repo.", "A fact."), "remember")
			if err != nil {
				t.Fatalf("remember: %v\n%s", err, out)
			}
			full, err := run(t, dir, "", "orient")
			if err != nil {
				t.Fatalf("orient: %v\n%s", err, full)
			}
			if tt.want == "" {
				if strings.Contains(out, "warning: orient in") || len(full) > hookBytes {
					t.Errorf("orient prints %d bytes, and remember printed\n%s\nwant no warning of the size", len(full), out)
				}
				return
			}
			want := fmt.Sprintf("warning: orient in %s prints %d bytes, over the 9500 that the SessionStart hook shows in full: merge or move memories to fit\n", tt.want, len(full))
			if !strings.Contains(out, want) {
				t.Errorf("remember printed\n%s\nwant %q", out, want)
			}
			if strings.Contains(out, "over the budget") {
				t.Errorf("remember printed\n%s\nwant no warning of the budget, which counts lines", out)
			}
		})
	}
}

func TestRememberConcurrent(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dir := mkdirT(t, filepath.Join(home, "work", "shop"))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, body := range []string{"The first fact.", "The second fact."} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out strings.Builder
			env := Env{Dir: dir, In: strings.NewReader(memoryText("same-name", "Read when you test locks.", body)), Out: &out, Err: &out}
			errs[i] = runRemember(env, nil)
		}()
	}
	wg.Wait()

	failed := 0
	for _, err := range errs {
		if err != nil {
			failed++
			if !strings.Contains(err.Error(), "taken") {
				t.Errorf("the losing run failed with %v, want a taken name", err)
			}
		}
	}
	if failed != 1 {
		t.Fatalf("errors = %v, want exactly one refusal", errs)
	}
	if got := strings.Count(gitT(t, repo, "log", "--format=%s"), "remember: work/shop/memories/same-name.md"); got != 1 {
		t.Errorf("%d commits of the memory, want 1", got)
	}
	for level, want := range map[string]string{
		"":          "- [work](work/README.md)",
		"work":      "- [shop](shop/README.md)",
		"work/shop": "- [same-name](memories/same-name.md)",
	} {
		got := readT(t, filepath.Join(repo, filepath.FromSlash(level), "README.md"))
		if strings.Count(got, want) != 1 {
			t.Errorf("index of %q = %q, want %q once", level, got, want)
		}
	}
	clean(t, repo)
}

func TestRememberAt(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	app := mkdirT(t, filepath.Join(home, "dev", "app"))
	gitT(t, app, "init", "-q", "-b", "main")
	mkdirT(t, filepath.Join(home, "dev", "other"))
	shared := mkdirT(t, filepath.Join(home, "dev", "shared"))
	if err := os.Symlink(shared, filepath.Join(app, "link")); err != nil {
		t.Fatal(err)
	}

	t.Run("a symlink before ..", func(t *testing.T) {
		if out, err := run(t, app, memoryText("through-link", "Read when you follow a link up.", "A fact."), "remember", "--at", "link/.."); err != nil {
			t.Fatalf("remember --at link/..: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "memories", "through-link.md")); err != nil {
			t.Errorf("the memory is not at dev, where link/.. leads: %v", err)
		}
	})

	t.Run("near-duplicates come from the working path", func(t *testing.T) {
		remember(t, app, "app-deploys", "Read before you deploy the app on Fridays.")
		_, err := run(t, app, memoryText("deploys-fridays", "Read before you deploy the app on Fridays.", "A fact."), "remember", "--at", "~")
		if err == nil || !strings.Contains(err.Error(), "near-duplicates") {
			t.Errorf("error = %v, want a refusal of a near-duplicate on the working path", err)
		}
	})

	for _, tt := range []struct {
		name, at, memory, want, wantErr string
	}{
		{name: "home", at: "~", memory: "about-me", want: "memories/about-me.md"},
		{name: "the parent", at: "..", memory: "across-dev", want: "dev/memories/across-dev.md"},
		{name: "a sibling", at: "~/dev/other", memory: "sideways", wantErr: "not on the path"},
		{name: "outside home", at: t.TempDir(), memory: "far-away", wantErr: "outside home"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, app, memoryText(tt.memory, "Read when you test "+tt.name+" placement.", "A fact."), "remember", "--at", tt.at)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want one that holds %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("remember --at %s: %v\n%s", tt.at, err, out)
			}
			if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(tt.want))); err != nil {
				t.Errorf("the memory is not at %s: %v", tt.want, err)
			}
		})
	}
}

func TestRememberTopics(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	files := map[string]string{
		"dev/memories/stedo/README.md": topicReadme("stedo", "Read when you query Stedo."),
		"dev/memories/stedo/bags.md":   memoryText("bags", "Read when you query the cell bags of Stedo.", "A fact."),
		"dev/memories/stedo/totes.md":  memoryText("totes", "Read when you query the totes of Stedo.", "A fact."),
		"dev/memories/stedo/labels.md": memoryText("labels", "Read when you print the labels of Stedo.", "A fact."),
	}
	for i := range 38 {
		name := fmt.Sprintf("flat-%02d", i)
		files["dev/memories/"+name+".md"] = memoryText(name, fmt.Sprintf("Read when you test flat fact number %d.", i), "A fact.")
	}
	pushedByHand(t, home, repo, files)

	t.Run("a near-duplicate inside a topic folder", func(t *testing.T) {
		before := gitT(t, repo, "rev-parse", "HEAD")
		out, err := run(t, dev, memoryText("bags-again", "Read when you query the cell bags of Stedo again.", "A fact."), "remember")
		if err == nil || !strings.Contains(out, "- dev/memories/stedo/bags.md: Read when you query the cell bags of Stedo.") {
			t.Errorf("remember: %v\n%s\nwant the near-duplicate in the topic folder", err, out)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != before {
			t.Error("remember wrote a near-duplicate")
		}
	})

	t.Run("the budget counts a topic folder once", func(t *testing.T) {
		out, err := run(t, dev, memoryText("one-more", "Read when you deploy on a Friday.", "A fact."), "remember")
		if err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
		if strings.Contains(out, "over the budget") {
			t.Errorf("output = %q, want no budget warning for 40 lines", out)
		}
	})
}
