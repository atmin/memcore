package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// conflictError is a rebase that stopped on a conflict in a document, and that memcore aborted.
// abort returns it only when the abort succeeded. So a caller may say that the queue is as it was.
type conflictError struct{ paths []string }

func (e *conflictError) Error() string {
	return "a rebase stopped on a conflict in " + strings.Join(e.paths, ", ") + ", so memcore aborted it"
}

// rebase runs git rebase with args. Indexes are generated. So at each stop on a conflict that only indexes
// hold, it writes those indexes again from the documents. Then it continues.
// A conflict in any other document aborts the rebase and returns a conflictError.
// The caller holds the write lock.
func rebase(repo string, args ...string) error {
	return rebaseEditor(repo, "true", args...)
}

// rebaseEditor is rebase with a sequence editor, the command that writes the todo of an interactive rebase.
func rebaseEditor(repo, editor string, args ...string) error {
	if rebasing(repo) {
		// The rebase of the person stays: an abort below would end it.
		return errors.New("a rebase is in progress in the memory repo: the person finishes it or aborts it first")
	}
	// A stop may be at a commit that lacks a rule of the tip. So the paths that git ignores now stay ignored.
	first, err := store.OpenTree(repo)
	if err != nil {
		return err
	}
	_, err = store.Git(repo, append([]string{"-c", "core.editor=true", "-c", "sequence.editor=" + editor, "-c", "rebase.rescheduleFailedExec=false", "rebase", "--empty=drop"}, args...)...)
	done, stuck := -1, 0
	for err != nil {
		// A deliberate stop moves the rebase on. The same stop three times means no progress.
		if n := progress(repo); n == done {
			if stuck++; stuck == 3 {
				return abort(repo, fmt.Errorf("the rebase stopped three times at one step: %w", err))
			}
		} else {
			done, stuck = n, 0
		}
		if !rebasing(repo) {
			return fmt.Errorf("rebase the review queue: %w", err)
		}
		out, _ := store.Git(repo, "diff", "--name-only", "-z", "--diff-filter=U")
		paths := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
		if out == "" {
			if serr := settle(repo, first); serr != nil {
				return abort(repo, fmt.Errorf("rebase the review queue: %w; %v", err, serr))
			}
			_, err = store.Git(repo, "-c", "core.editor=true", "-c", "rebase.rescheduleFailedExec=false", "rebase", "--continue")
			continue
		}
		t, rerr := treeAtStop(repo, first)
		if rerr == nil {
			rerr = resolveIndexes(t, paths)
		}
		if rerr != nil {
			return abort(repo, rerr)
		}
		_, err = store.Git(repo, "-c", "core.editor=true", "-c", "rebase.rescheduleFailedExec=false", "rebase", "--continue")
	}
	return nil
}

// treeAtStop opens a reader at a stop of a rebase. It also skips each path of first, the reader of the start.
func treeAtStop(repo string, first store.Tree) (store.Tree, error) {
	t, err := store.OpenTree(repo)
	if err != nil {
		return t, err
	}
	return t.With(first)
}

// resolveIndexes resolves a conflict that only indexes hold, at the paths of a stopped rebase or cherry-pick.
// Indexes are generated, so it writes each of them again from the documents, and stages it. For a topic index
// it keeps the frontmatter that sameFrontmatter merges. A conflict in any other document, or in the
// frontmatter of a topic index, returns a conflictError, and the caller ends its rebase or its cherry-pick.
// It reads the working tree through t, which the caller opens at the stop.
func resolveIndexes(t store.Tree, paths []string) error {
	repo := t.Root()
	var levels, topics []string
	for _, p := range paths {
		if store.IsTopicIndex(p) {
			fm, ok := sameFrontmatter(repo, p)
			if !ok {
				return &conflictError{paths: paths}
			}
			if err := noSymlink(repo, p); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(p)), fm, 0o644); err != nil {
				return fmt.Errorf("write the frontmatter of %s: %w", p, err)
			}
			topics = append(topics, path.Dir(p))
			continue
		}
		level, ok := indexLevel(p)
		if !ok {
			return &conflictError{paths: paths}
		}
		levels = append(levels, level)
	}
	deepestFirst(topics)
	for _, tf := range topics {
		index := path.Join(tf, "README.md")
		if err := store.WriteTopicIndexIn(t, tf); err != nil {
			return err
		}
		if _, err := store.Git(repo, "--literal-pathspecs", "add", "--", index); err != nil {
			return fmt.Errorf("stage the index %s: %w", index, err)
		}
	}
	// The ancestors of a conflicting index list its level, and a merge may have dropped that line.
	var all []string
	for _, level := range levels {
		for _, l := range store.Path(level) {
			if !slices.Contains(all, l) {
				all = append(all, l)
			}
		}
	}
	for _, level := range all {
		index := path.Join(store.Folder(level), "README.md")
		if err := noSymlink(repo, index); err != nil {
			return err
		}
		if err := store.WriteIndexIn(t, level); err != nil {
			return err
		}
		if _, err := store.Git(repo, "--literal-pathspecs", "add", "--", index); err != nil {
			return fmt.Errorf("stage the index %s: %w", index, err)
		}
	}
	return nil
}

// indexLevel returns the level whose index is the path, and false when the path is no index.
func indexLevel(p string) (string, bool) {
	if path.Base(p) != "README.md" {
		return "", false
	}
	folder := path.Dir(p)
	if folder == "." {
		folder = ""
	}
	return store.LevelOf(folder)
}

// sameFrontmatter merges the frontmatter of a conflicting topic index. The body is generated, so only the
// frontmatter can conflict. When the sides agree, or only one side changed it from the base, it returns the
// merged frontmatter. It returns false when a side lacks the file, or both sides changed it differently.
func sameFrontmatter(repo, p string) ([]byte, bool) {
	read := func(stage string) ([]byte, bool) {
		text, err := store.Git(repo, "--literal-pathspecs", "show", stage+p)
		if err != nil {
			return nil, false
		}
		if !store.HasFrontmatter(text) {
			return []byte{}, true
		}
		parsed, _, err := store.ParseFrontmatter(strings.NewReader(text))
		if err != nil {
			return nil, false
		}
		return parsed.Bytes(), true
	}
	ours, ok := read(":2:")
	if !ok {
		return nil, false
	}
	theirs, ok := read(":3:")
	if !ok {
		return nil, false
	}
	if string(ours) == string(theirs) {
		return ours, true
	}
	base, ok := read(":1:")
	switch {
	case !ok:
		return nil, false
	case string(base) == string(ours):
		return theirs, true
	case string(base) == string(theirs):
		return ours, true
	}
	return nil, false
}

// abort aborts the rebase in progress and returns err. When the abort fails, it returns that failure, with the
// message of err but not err itself. So no caller reports the queue as it was.
func abort(repo string, err error) error {
	if _, aerr := store.Git(repo, "rebase", "--abort"); aerr != nil {
		return fmt.Errorf("%v; abort the rebase: %w: the person finishes or aborts the rebase", err, aerr)
	}
	return err
}

// settle handles a stop of a rebase that holds no conflict. The fold puts an "exec false" line after each
// commit that it keeps. After that line, settle regenerates the indexes that the commit left stale, and amends
// the commit. An earlier commit that the fold dropped may have written those indexes.
//
// A pick may leave no commit, because the commit before it holds its change. Then HEAD is the commit that the
// rebase goes onto, and settle amends nothing: it commits each changed index as a new commit "index: <path>".
// After a fixup at that commit, settle returns an error, and the rebase aborts. So settle never amends the
// accepted commit.
//
// A fixup may empty its target until the next fixup, such as a revision back to the text before it. After such a fixup, settle keeps
// the empty commit, so the next fixup applies. Any other stop is an error.
//
// first is the reader that the rebase opened at its start. settle skips its paths, as treeAtStop does.
func settle(repo string, first store.Tree) error {
	done, err := os.ReadFile(filepath.Join(gitPath(repo, "rebase-merge"), "done"))
	if err != nil {
		return fmt.Errorf("read the progress of the rebase: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(done)), "\n")
	switch last := lines[len(lines)-1]; {
	case strings.HasPrefix(last, "exec false"):
		t, err := treeAtStop(repo, first)
		if err != nil {
			return err
		}
		indexes, err := writeIndexes(t)
		if err != nil {
			return err
		}
		if _, err := store.Git(repo, append([]string{"--literal-pathspecs", "add", "-A", "--"}, indexes...)...); err != nil {
			return fmt.Errorf("stage the indexes: %w", err)
		}
		if _, err := store.Git(repo, "diff", "--cached", "--quiet"); err == nil {
			return nil // every index was right
		}
		onBase, err := atOnto(repo)
		if err != nil {
			return err
		}
		if onBase {
			// The pick left no commit, so HEAD is the commit that the rebase goes onto. That may be the accepted
			// commit, which memcore never amends. Each changed index gets a commit of its own, as regenerate makes.
			out, err := store.Git(repo, "diff", "--cached", "--name-only", "--no-renames", "-z")
			if err != nil {
				return fmt.Errorf("list the changed indexes: %w", err)
			}
			for _, index := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
				if err := store.Commit(repo, []string{index}, "index: "+index); err != nil {
					return err
				}
			}
			return nil
		}
		if _, err := store.Git(repo, "commit", "-q", "--amend", "--allow-empty", "--no-edit"); err != nil {
			return fmt.Errorf("amend the indexes into the commit: %w", err)
		}
		return nil
	case strings.HasPrefix(last, "fixup "):
		if _, err := store.Git(repo, "diff", "--cached", "--quiet", "HEAD~1"); err != nil {
			return fmt.Errorf("the rebase stopped after %q for a reason that memcore does not handle", last)
		}
		// The pick of the target may have left no commit. Then the fixup would fold into the commit that the
		// rebase goes onto, which may be the accepted commit.
		if onBase, err := atOnto(repo); err != nil {
			return err
		} else if onBase {
			return fmt.Errorf("the rebase stopped after %q, and its target left no commit, so the fixup would change the commit before the queue", last)
		}
		if _, err := store.Git(repo, "commit", "-q", "--amend", "--allow-empty", "--no-edit"); err != nil {
			return fmt.Errorf("keep the commit that a fixup empties: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("the rebase stopped after %q for a reason that memcore does not handle", last)
	}
}

// writeIndexes writes every index of the working tree again from the documents: each topic index, deepest
// first, and then the index of each level. It returns their paths, and stages nothing. It reads the working
// tree through t, which the caller opens after its last change of the documents.
func writeIndexes(t store.Tree) ([]string, error) {
	repo := t.Root()
	levels, err := levelFolders(t)
	if err != nil {
		return nil, err
	}
	topics, err := topicFolders(t)
	if err != nil {
		return nil, err
	}
	var indexes []string
	for _, tf := range topics {
		index := path.Join(tf, "README.md")
		if err := noSymlink(repo, index); err != nil {
			return nil, err
		}
		if err := store.WriteTopicIndexIn(t, tf); err != nil {
			return nil, err
		}
		indexes = append(indexes, index)
	}
	for _, l := range levels {
		index := path.Join(store.Folder(l), "README.md")
		if err := noSymlink(repo, index); err != nil {
			return nil, err
		}
		if err := store.WriteIndexIn(t, l); err != nil {
			return nil, err
		}
		indexes = append(indexes, index)
	}
	return indexes, nil
}

// atOnto reports whether HEAD is the commit that the rebase in progress goes onto.
func atOnto(repo string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(gitPath(repo, "rebase-merge"), "onto"))
	if err != nil {
		return false, fmt.Errorf("read the base of the rebase: %w", err)
	}
	onto := strings.TrimSpace(string(b))
	// A lookup that fails must not read as "no", because the caller then amends HEAD.
	out, err := store.Git(repo, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	if err != nil {
		return false, fmt.Errorf("read HEAD in the rebase: %w", err)
	}
	head := strings.TrimSpace(out)
	if onto == "" || head == "" {
		return false, fmt.Errorf("read the base of the rebase: git gave the base %q and HEAD %q", onto, head)
	}
	return onto == head, nil
}

// levelFolders returns every level of the working tree that has an index or holds a file. An ignored path
// does not count.
func levelFolders(t store.Tree) ([]string, error) {
	var levels []string
	err := t.WalkDir("", func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(t.Root(), f)
		if err != nil {
			return err
		}
		folder := filepath.ToSlash(rel)
		if folder == "." {
			folder = ""
		}
		level, ok := store.LevelOf(folder)
		if !ok {
			return nil
		}
		if t.Exists(path.Join(folder, "README.md")) || holdsFile(t, folder) {
			levels = append(levels, level)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list the levels: %w", err)
	}
	return levels, nil
}

// gitPath returns a path inside the git directory of the repo, absolute.
func gitPath(repo, name string) string {
	out, err := store.Git(repo, "rev-parse", "--git-path", name)
	p := strings.TrimSpace(out)
	if err != nil || p == "" {
		return filepath.Join(repo, ".git", name)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(repo, p)
	}
	return p
}

// progress returns how many steps of the rebase in progress are done.
func progress(repo string) int {
	b, err := os.ReadFile(filepath.Join(gitPath(repo, "rebase-merge"), "done"))
	if err != nil {
		return -1
	}
	return strings.Count(string(b), "\n")
}
