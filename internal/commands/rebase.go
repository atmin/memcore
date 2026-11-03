package commands

import (
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
// abort returns it only when the abort succeeded, so a caller may say that the queue is as it was.
type conflictError struct{ paths []string }

func (e *conflictError) Error() string {
	return "a rebase stopped on a conflict in " + strings.Join(e.paths, ", ") + ", so memcore aborted it"
}

// rebase runs git rebase with args. At each stop on a conflict that only indexes hold,
// it writes those indexes again from the documents and continues, since indexes are generated.
// A conflict in any other document aborts the rebase and returns a conflictError.
// The caller holds the write lock.
func rebase(repo string, args ...string) error {
	return rebaseEditor(repo, "true", args...)
}

// rebaseEditor is rebase with a sequence editor, the command that writes the todo of an interactive rebase.
func rebaseEditor(repo, editor string, args ...string) error {
	_, err := store.Git(repo, append([]string{"-c", "core.editor=true", "-c", "sequence.editor=" + editor, "-c", "rebase.rescheduleFailedExec=false", "rebase"}, args...)...)
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
			if serr := settle(repo); serr != nil {
				return abort(repo, fmt.Errorf("rebase the review queue: %w; %v", err, serr))
			}
			_, err = store.Git(repo, "-c", "core.editor=true", "-c", "rebase.rescheduleFailedExec=false", "rebase", "--continue")
			continue
		}
		var levels []string
		for _, p := range paths {
			level, ok := indexLevel(p)
			if !ok {
				return abort(repo, &conflictError{paths: paths})
			}
			levels = append(levels, level)
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
				return abort(repo, err)
			}
			if err := store.WriteIndex(repo, level); err != nil {
				return abort(repo, err)
			}
			if _, err := store.Git(repo, "--literal-pathspecs", "add", "--", index); err != nil {
				return abort(repo, fmt.Errorf("stage the index %s: %w", index, err))
			}
		}
		_, err = store.Git(repo, "-c", "core.editor=true", "-c", "rebase.rescheduleFailedExec=false", "rebase", "--continue")
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

// abort aborts the rebase in progress and returns err. When the abort fails, it returns that failure,
// with the message of err but not err itself, so no caller reports the queue as it was.
func abort(repo string, err error) error {
	if _, aerr := store.Git(repo, "rebase", "--abort"); aerr != nil {
		return fmt.Errorf("%v; abort the rebase: %w: the person finishes or aborts the rebase", err, aerr)
	}
	return err
}

// settle handles a stop of a rebase that holds no conflict. After an "exec false" line, which the fold puts
// after each commit, it regenerates the indexes that the commit left stale and amends the commit, since an
// earlier commit that the fold dropped may have written them. After a fixup that empties its target for now,
// as a revision back to the text before it, it keeps the empty commit, so the next fixup applies.
// Any other stop is an error.
func settle(repo string) error {
	done, err := os.ReadFile(filepath.Join(gitPath(repo, "rebase-merge"), "done"))
	if err != nil {
		return fmt.Errorf("read the progress of the rebase: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(done)), "\n")
	switch last := lines[len(lines)-1]; {
	case strings.HasPrefix(last, "exec false"):
		levels, err := levelFolders(repo)
		if err != nil {
			return err
		}
		var indexes []string
		for _, l := range levels {
			index := path.Join(store.Folder(l), "README.md")
			if err := noSymlink(repo, index); err != nil {
				return err
			}
			if err := store.WriteIndex(repo, l); err != nil {
				return err
			}
			indexes = append(indexes, index)
		}
		if _, err := store.Git(repo, append([]string{"--literal-pathspecs", "add", "-A", "--"}, indexes...)...); err != nil {
			return fmt.Errorf("stage the indexes: %w", err)
		}
		if _, err := store.Git(repo, "diff", "--cached", "--quiet"); err == nil {
			return nil // every index was right
		}
		if _, err := store.Git(repo, "commit", "-q", "--amend", "--allow-empty", "--no-edit"); err != nil {
			return fmt.Errorf("amend the indexes into the commit: %w", err)
		}
		return nil
	case strings.HasPrefix(last, "fixup "):
		if _, err := store.Git(repo, "diff", "--cached", "--quiet", "HEAD~1"); err != nil {
			return fmt.Errorf("the rebase stopped after %q for a reason that memcore does not handle", last)
		}
		if _, err := store.Git(repo, "commit", "-q", "--amend", "--allow-empty", "--no-edit"); err != nil {
			return fmt.Errorf("keep the commit that a fixup empties: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("the rebase stopped after %q for a reason that memcore does not handle", last)
	}
}

// levelFolders returns every level of the working tree that has an index or a document.
func levelFolders(repo string) ([]string, error) {
	var levels []string
	err := filepath.WalkDir(repo, func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(repo, f)
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
		ix, err := store.Lines(repo, level)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(f, "README.md")); err == nil || len(ix.Lines) > 0 {
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
