package commands

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/ui"
)

// searchLimit is the most hits that a search of the window returns.
const searchLimit = 200

// uiBackend gives the window the data of the memory repo, and runs the actions of the person through Run.
type uiBackend struct {
	repo string // the memory repo
	dir  string // the working directory of memcore ui, which the use log records for an action
}

// uiResolved runs after a read resolves its commits. Tests replace it to move a ref in the middle of a read.
var uiResolved = func() {}

// resolved returns the tip of main and the accepted commit, once for a read. The accepted commit is "" before
// the first push. A rebase moves main only at its end, so a read never sees a commit in its middle.
func (b uiBackend) resolved() (main, accepted string, err error) {
	main, ok := revParse(b.repo, "refs/heads/main")
	if !ok {
		return "", "", fmt.Errorf("read the tip of main in %s", tilde(b.repo))
	}
	accepted, _ = revParse(b.repo, acceptedRef)
	uiResolved()
	return main, accepted, nil
}

// Head returns the tip of main.
func (b uiBackend) Head(context.Context) (string, error) {
	main, _, err := b.resolved()
	return main, err
}

// Queue fetches, and returns the queue as the fold at push leaves it, newest first, as inbox lists it. When the
// preview fails, it returns the warning and each commit of the queue as it is.
func (b uiBackend) Queue(context.Context) (ui.Queue, error) {
	unlock, err := store.Lock(b.repo)
	if err != nil {
		return ui.Queue{}, err
	}
	defer unlock()
	q := ui.Queue{Entries: []ui.Entry{}} // an empty queue is a list, never null, for the page
	if err := Fetch(b.repo); err != nil {
		q.Warning = fmt.Sprintf("%v. The queue may sit behind the accepted history.", err)
	}
	if q.Main, q.Accepted, err = b.resolved(); err != nil {
		return ui.Queue{}, err
	}
	shas, err := revList(b.repo, q.Main)
	if err != nil {
		return ui.Queue{}, err
	}
	entries, err := preview(b.repo, true)
	if err != nil {
		q.Warning = strings.TrimSpace(q.Warning + " " + fmt.Sprintf("%v. The queue lists each commit as it is, without the fold.", err))
		for i := len(shas) - 1; i >= 0; i-- {
			p, _, err := describe(b.repo, shas[i], true)
			if err != nil {
				return ui.Queue{}, err
			}
			p.target = shas[i]
			q.Entries = append(q.Entries, entryOf(p))
		}
		return q, nil
	}
	q.Folded = true
	held := map[string]bool{}
	for i := len(entries) - 1; i >= 0; i-- {
		p := entries[i]
		held[p.target] = true
		for _, f := range p.fixups {
			held[f] = true
		}
		q.Entries = append(q.Entries, entryOf(p))
	}
	for _, sha := range shas {
		if !held[sha] {
			q.Dropped++
		}
	}
	return q, nil
}

// entryOf turns a commit of the preview into an entry of the window.
func entryOf(p previewed) ui.Entry {
	e := ui.Entry{Commit: p.target, Subject: p.subject, Body: p.body, Fixups: p.fixups, Files: []ui.File{}}
	if e.Fixups == nil {
		e.Fixups = []string{}
	}
	for i, f := range p.files {
		file := ui.File{Status: f.status, Path: f.path, Kind: docKind(f.path)}
		if i < len(p.diffs) {
			d := p.diffs[i]
			file.Patch, file.Words, file.Before, file.After = d.patch, d.words, d.before, d.after
		}
		e.Files = append(e.Files, file)
	}
	return e
}

// changed returns the paths that the queue changes, from the accepted commit to main. Before the first push,
// every path counts.
func (b uiBackend) changed(main, accepted string) (map[string]bool, error) {
	paths := map[string]bool{}
	var out string
	var err error
	if accepted == "" {
		out, err = store.Git(b.repo, "ls-tree", "-r", "-z", "--name-only", main)
	} else {
		out, err = store.Git(b.repo, "diff", "-z", "--name-only", "--no-renames", "--no-ext-diff", "--no-textconv", accepted, main)
	}
	if err != nil {
		return nil, fmt.Errorf("list the files that the queue changes: %w", err)
	}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths[p] = true
		}
	}
	return paths, nil
}

// Tree lists each file of main, with its kind and whether the queue changes it.
func (b uiBackend) Tree(context.Context) (ui.Tree, error) {
	main, accepted, err := b.resolved()
	if err != nil {
		return ui.Tree{}, err
	}
	out, err := store.Git(b.repo, "ls-tree", "-r", "-z", "--name-only", main)
	if err != nil {
		return ui.Tree{}, fmt.Errorf("list the files of main: %w", err)
	}
	changed, err := b.changed(main, accepted)
	if err != nil {
		return ui.Tree{}, err
	}
	t := ui.Tree{Main: main, Accepted: accepted, Docs: []ui.TreeDoc{}}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			t.Docs = append(t.Docs, ui.TreeDoc{Path: p, Kind: docKind(p), Unreviewed: changed[p]})
		}
	}
	return t, nil
}

// Doc returns a file of main. It refuses an empty path, an absolute path and a path with "..".
func (b uiBackend) Doc(_ context.Context, p string) (ui.Doc, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return ui.Doc{}, fmt.Errorf("the path %q is not a path in the memory repo: %w", p, ui.ErrBadInput)
	}
	main, accepted, err := b.resolved()
	if err != nil {
		return ui.Doc{}, err
	}
	text, err := blobAt(b.repo, main, p)
	if err != nil {
		return ui.Doc{}, err
	}
	if text == nil {
		return ui.Doc{}, fmt.Errorf("main holds no file %s: %w", p, ui.ErrNotFound)
	}
	changed, err := b.changed(main, accepted)
	if err != nil {
		return ui.Doc{}, err
	}
	return ui.Doc{Main: main, Path: p, Text: *text, Unreviewed: changed[p]}, nil
}

// Search returns the lines of main that hold the query, ignoring case, at most searchLimit of them.
func (b uiBackend) Search(_ context.Context, query string) (ui.Hits, error) {
	if strings.TrimSpace(query) == "" {
		return ui.Hits{}, fmt.Errorf("the search has no words: %w", ui.ErrBadInput)
	}
	main, _, err := b.resolved()
	if err != nil {
		return ui.Hits{}, err
	}
	h := ui.Hits{Main: main, Hits: []ui.Hit{}}
	out, err := store.Git(b.repo, "grep", "-z", "-n", "--no-column", "-I", "-i", "-F", "--no-textconv", "--no-color", "-e", query, main)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && out == "" {
		return h, nil // git grep exits 1 when no line matches
	}
	if err != nil {
		return ui.Hits{}, fmt.Errorf("search main: %w", err)
	}
	// Each hit is "<commit>:<path>\0<line>\0<text>\n". A path can hold a newline, so the fields end at NUL.
	for rest := out; rest != ""; {
		name, after, ok := strings.Cut(rest, "\x00")
		if !ok {
			break
		}
		num, after, ok := strings.Cut(after, "\x00")
		if !ok {
			break
		}
		text, after, _ := strings.Cut(after, "\n")
		rest = after
		n, err := strconv.Atoi(num)
		if err != nil {
			return ui.Hits{}, fmt.Errorf("search main: git grep printed the line number %q", num)
		}
		if len(h.Hits) == searchLimit {
			h.More = true
			break
		}
		h.Hits = append(h.Hits, ui.Hit{Path: strings.TrimPrefix(name, main+":"), Line: n, Text: text})
	}
	return h, nil
}

// Push is a stub until the actions of the window exist.
func (b uiBackend) Push(context.Context, string) (ui.Result, error) {
	return ui.Result{}, fmt.Errorf("the window cannot push yet")
}

// Drop is a stub until the actions of the window exist.
func (b uiBackend) Drop(context.Context, string, string) (ui.Result, error) {
	return ui.Result{}, fmt.Errorf("the window cannot drop yet")
}

var _ ui.Backend = uiBackend{}
