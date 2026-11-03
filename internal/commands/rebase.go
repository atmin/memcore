package commands

import (
	"fmt"
	"path"
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
	_, err := store.Git(repo, append([]string{"-c", "core.editor=true", "-c", "sequence.editor=true", "rebase"}, args...)...)
	for err != nil {
		if !rebasing(repo) {
			return fmt.Errorf("rebase the review queue: %w", err)
		}
		out, _ := store.Git(repo, "diff", "--name-only", "-z", "--diff-filter=U")
		paths := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
		if out == "" {
			return abort(repo, fmt.Errorf("rebase the review queue: %w", err))
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
		_, err = store.Git(repo, "-c", "core.editor=true", "rebase", "--continue")
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
