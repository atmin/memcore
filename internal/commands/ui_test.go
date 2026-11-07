package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/ui"
)

// oddPaths are paths that git quotes unless it runs with -z.
var oddPaths = []string{"lab/notes/two words.md", "lab/notes/café.md", "lab/notes/a\"quote.md"}

// commitOddPaths commits a file at each odd path, by hand, with the word "needle" in it.
func commitOddPaths(t *testing.T, repo string) {
	t.Helper()
	for _, p := range oddPaths {
		writeT(t, repo, p, "A needle in "+filepath.Base(p)+".\n")
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "test: odd paths")
}

func TestUIQueue(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	ctx := context.Background()

	t.Run("the order of inbox, with a fixup that folds", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		pushYes(t, home)
		remember(t, dev, "deploy-fact", "Read when you deploy the shop.")
		reviseT(t, dev, memoryText("deploy-fact", "Read when you deploy the shop.", "Sharper."), "deploy-fact", "Sharper.")
		remember(t, dev, "merge-fact", "Read before you merge a branch.")
		commitOddPaths(t, repo)
		q, err := uiBackend{repo: repo, dir: home}.Queue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !q.Folded || q.Warning != "" || q.Main != headCommit(repo) || q.Accepted == "" {
			t.Errorf("queue = %+v", q)
		}
		inbox, err := run(t, home, "", "inbox")
		if err != nil {
			t.Fatalf("inbox: %v\n%s", err, inbox)
		}
		at := 0
		for _, e := range q.Entries {
			i := strings.Index(inbox[at:], e.Subject)
			if i < 0 {
				t.Fatalf("inbox lacks %q after byte %d, or lists it earlier:\n%s", e.Subject, at, inbox)
			}
			at += i + len(e.Subject)
		}
		if len(q.Entries) != 3 || q.Entries[0].Subject != "test: odd paths" || q.Entries[2].Subject != "remember: dev/memories/deploy-fact.md" || len(q.Entries[2].Fixups) != 1 {
			t.Fatalf("entries = %+v, want the odd paths, merge-fact, and deploy-fact with its fixup", q.Entries)
		}
		var paths []string
		for _, f := range q.Entries[0].Files {
			paths = append(paths, f.Path)
			if !strings.HasPrefix(f.Path, "lab/notes/") {
				continue // the fold writes the indexes into the commit
			}
			if f.After == nil || f.Before != nil || !strings.Contains(f.Patch, "+A needle") {
				t.Errorf("file %+v", f)
			}
		}
		for _, p := range oddPaths {
			if !strings.Contains(strings.Join(paths, "\n"), p) {
				t.Errorf("paths %q lack %q", paths, p)
			}
		}
	})

	t.Run("no accepted history", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		q, err := uiBackend{repo: repo, dir: home}.Queue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if q.Accepted != "" || len(q.Entries) == 0 || q.Entries[len(q.Entries)-1].Subject != "init: create the memory repo" {
			t.Errorf("queue = %+v, want the first commit of the repo", q)
		}
		for _, f := range q.Entries[len(q.Entries)-1].Files {
			if f.Before != nil || f.After == nil {
				t.Errorf("a file of the first commit: %+v", f)
			}
		}
	})

	t.Run("the fallback when the preview fails", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		pushYes(t, home)
		remember(t, dev, "deploy-fact", "Read when you deploy the shop.")
		reviseT(t, dev, memoryText("deploy-fact", "Read when you deploy the shop.", "Sharper."), "deploy-fact", "Sharper.")
		writeT(t, repo, ".git/info/exclude", "*.tmp\n")
		writeT(t, repo, "dev/scratch.tmp", "An ignored file.\n")
		q, err := uiBackend{repo: repo, dir: home}.Queue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if q.Folded || !strings.Contains(q.Warning, "dev/scratch.tmp") || len(q.Entries) != 2 {
			t.Fatalf("queue = %+v, want the warning and two commits as they are", q)
		}
		if !strings.HasPrefix(q.Entries[0].Subject, "fixup! ") || q.Entries[0].Commit != headCommit(repo) || len(q.Entries[0].Files) == 0 || q.Entries[0].Files[0].Patch == "" {
			t.Errorf("newest entry = %+v, want the fixup with its patch", q.Entries[0])
		}
	})
}

func TestUITree(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "deploy-fact", "Read when you deploy the shop.")
	pushYes(t, home)
	commitOddPaths(t, repo)
	tree, err := uiBackend{repo: repo, dir: home}.Tree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]ui.TreeDoc{}
	for _, d := range tree.Docs {
		docs[d.Path] = d
	}
	for _, p := range oddPaths {
		if d, ok := docs[p]; !ok || !d.Unreviewed || d.Kind != "notes" {
			t.Errorf("tree entry of %s = %+v, %v, want an unreviewed note", p, d, ok)
		}
	}
	if d := docs["dev/memories/deploy-fact.md"]; d.Kind != "memories" || d.Unreviewed {
		t.Errorf("tree entry of the accepted memory = %+v", d)
	}
	if tree.Main != headCommit(repo) || tree.Accepted == "" {
		t.Errorf("tree = %s %s", tree.Main, tree.Accepted)
	}
}

func TestUITreePinned(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	pushYes(t, home)
	commitOddPaths(t, repo)
	main := headCommit(repo)
	defer func(f func()) { uiResolved = f }(uiResolved)
	uiResolved = func() { gitT(t, repo, "update-ref", acceptedRef, main) } // a push lands in the middle of the read
	tree, err := uiBackend{repo: repo, dir: home}.Tree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tree.Accepted == main {
		t.Fatal("the read saw the accepted commit after the move")
	}
	for _, d := range tree.Docs {
		if strings.HasPrefix(d.Path, "lab/notes/") && !d.Unreviewed {
			t.Errorf("%s is reviewed, but the accepted commit of the read lacks it", d.Path)
		}
	}
}

func TestUIDoc(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	pushYes(t, home)
	commitOddPaths(t, repo)
	b := uiBackend{repo: repo, dir: home}
	for _, tt := range []struct {
		path string
		want error
		text string
	}{
		{"lab/notes/café.md", nil, "A needle in café.md.\n"},
		{"skills/voice/SKILL.md", nil, ""},
		{"/etc/passwd", ui.ErrBadInput, ""},
		{"../outside.md", ui.ErrBadInput, ""},
		{"lab/../skills/voice/SKILL.md", ui.ErrBadInput, ""},
		{"", ui.ErrBadInput, ""},
		{"lab/notes/absent.md", ui.ErrNotFound, ""},
		{"lab/notes", ui.ErrNotFound, ""},
	} {
		d, err := b.Doc(context.Background(), tt.path)
		if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
			t.Errorf("Doc(%q) error = %v, want %v", tt.path, err, tt.want)
			continue
		}
		if tt.text != "" && d.Text != tt.text {
			t.Errorf("Doc(%q) = %q, want %q", tt.path, d.Text, tt.text)
		}
		if tt.path == "lab/notes/café.md" && !d.Unreviewed {
			t.Errorf("Doc(%q) is reviewed, want unreviewed", tt.path)
		}
		if tt.path == "skills/voice/SKILL.md" && (d.Unreviewed || d.Text == "") {
			t.Errorf("Doc(%q) = %+v, want the accepted skill", tt.path, d)
		}
	}
}

func TestUISearch(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	commitOddPaths(t, repo)
	b := uiBackend{repo: repo, dir: home}
	ctx := context.Background()

	h, err := b.Search(ctx, "NEEDLE")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, hit := range h.Hits {
		got[hit.Path] = hit.Line
	}
	for _, p := range oddPaths {
		if got[p] != 1 {
			t.Errorf("hits = %+v, want %s at line 1", h.Hits, p)
		}
	}
	if h.More || h.Main != headCommit(repo) {
		t.Errorf("hits = %+v", h)
	}

	if h, err := b.Search(ctx, "no such words anywhere"); err != nil || len(h.Hits) != 0 || h.Hits == nil {
		t.Errorf("search without a hit = %+v, %v", h, err)
	}
	if _, err := b.Search(ctx, " "); !errors.Is(err, ui.ErrBadInput) {
		t.Errorf("an empty search: %v, want ErrBadInput", err)
	}
	if h, err := b.Search(ctx, "-e"); err != nil || len(h.Hits) != 0 {
		t.Errorf("a search for -e = %+v, %v", h, err)
	}

	writeT(t, repo, "lab/notes/haystack.md", strings.Repeat("needle\n", searchLimit+50))
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "test: a haystack")
	if h, err := b.Search(ctx, "needle"); err != nil || len(h.Hits) != searchLimit || !h.More {
		t.Errorf("a search past the limit = %d hits, more %v, %v", len(h.Hits), h.More, err)
	}
}

func TestUINoHelpers(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	pushYes(t, home)
	remember(t, dev, "deploy-fact", "Read when you deploy the shop.")
	marker := filepath.Join(t.TempDir(), "ran")
	helper := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch '"+marker+"'\ncat \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "config", "diff.external", helper)
	gitT(t, repo, "config", "diff.mark.textconv", helper)
	writeT(t, repo, ".git/info/attributes", "* diff=mark\n")
	b := uiBackend{repo: repo, dir: home}
	ctx := context.Background()
	if _, err := b.Queue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Doc(ctx, "dev/memories/deploy-fact.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Search(ctx, "deploy"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tree(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a read of the window ran a diff helper of the git configuration")
	}
	// The check above means something only if git runs the helper without the flags.
	gitT(t, repo, "diff", "HEAD~1", "HEAD")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("git diff did not run the helper, so the test proves nothing: %v", err)
	}
}

func TestUIPush(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	ctx := context.Background()

	t.Run("the head pushes, and a queue with a skill pushes", func(t *testing.T) {
		home, repo, remote := newMemory(t) // the first commit holds the voice skill
		b := uiBackend{repo: repo, dir: home}
		r, err := b.Push(ctx, headCommit(repo))
		if err != nil || r.Error != "" || !strings.Contains(r.Output, "Pushed") {
			t.Fatalf("push = %+v, %v", r, err)
		}
		if got := gitT(t, remote, "rev-parse", "main"); got != gitT(t, repo, "rev-parse", "HEAD") {
			t.Errorf("remote main = %s, want the tip of the queue", got)
		}
		e := last(t, repo)
		if e.Command != "push" || e.By == "agent" || len(e.Pushed) == 0 {
			t.Errorf("event = %+v, want a push of the person", e)
		}
	})

	t.Run("a stale head pushes nothing", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		seen := headCommit(repo)
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "deploy-fact", "Read when you deploy the shop.")
		r, err := uiBackend{repo: repo, dir: home}.Push(ctx, seen)
		if err != nil || !strings.Contains(r.Error, "the queue changed since the review") {
			t.Fatalf("push = %+v, %v, want the refusal in the result", r, err)
		}
		if _, err := store.Git(remote, "rev-parse", "--verify", "-q", "main"); err == nil {
			t.Error("the remote has main after the refusal")
		}
	})

	t.Run("no head", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		for _, head := range []string{"", "--help", "main"} {
			if _, err := (uiBackend{repo: repo, dir: home}).Push(ctx, head); !errors.Is(err, ui.ErrBadInput) {
				t.Errorf("push with the head %q: %v, want ErrBadInput", head, err)
			}
		}
	})
}

func TestUIDrop(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	ctx := context.Background()
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "unwanted", "Read when you test an unwanted fact.")
	reviseT(t, dev, memoryText("unwanted", "Read when you test an unwanted fact.", "Still unwanted."), "unwanted", "A test.")
	remember(t, dev, "wanted", "Read before you deploy on Fridays.")
	b := uiBackend{repo: repo, dir: home}
	r, err := b.Drop(ctx, sha(t, repo, "remember: dev/memories/unwanted.md"), "Not a fact.")
	if err != nil || r.Error != "" {
		t.Fatalf("drop = %+v, %v", r, err)
	}
	if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s")); got != "remember: dev/memories/wanted.md\ninit: create the memory repo" {
		t.Errorf("history = %q", got)
	}
	e := last(t, repo)
	if e.Command != "drop" || len(e.Dropped) != 2 || e.Reason != "Not a fact." {
		t.Errorf("event = %+v, want the drop of two commits with the reason", e)
	}
	if r, err := b.Drop(ctx, strings.Repeat("0", 40), ""); err != nil || r.Error == "" {
		t.Errorf("a drop of no commit = %+v, %v, want the refusal in the result", r, err)
	}
	for _, commit := range []string{"", "--help", "-h", "HEAD"} {
		if _, err := b.Drop(ctx, commit, "A reason."); !errors.Is(err, ui.ErrBadInput) {
			t.Errorf("a drop of %q: %v, want ErrBadInput", commit, err)
		}
	}
}

func TestUIDropReasonLikeAFlag(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	for _, reason := range []string{"--help", "-h"} {
		t.Run(reason, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			dev := mkdirT(t, filepath.Join(home, "dev"))
			remember(t, dev, "unwanted", "Read when you test an unwanted fact.")
			r, err := uiBackend{repo: repo, dir: home}.Drop(context.Background(), sha(t, repo, "remember: dev/memories/unwanted.md"), reason)
			if err != nil || r.Error != "" {
				t.Fatalf("drop = %+v, %v", r, err)
			}
			if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s")); got != "init: create the memory repo" {
				t.Errorf("history = %q, want the memory dropped", got)
			}
			if e := last(t, repo); e.Command != "drop" || e.Reason != reason {
				t.Errorf("event = %+v, want the drop with the reason %q", e, reason)
			}
		})
	}
}
