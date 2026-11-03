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

// file is one file that a change writes: its path in the repo, its content and its mode.
type file struct {
	path    string
	content []byte
	mode    fs.FileMode
}

// change is one write of the memory repo: the files to write or remove, and the message of its commit.
type change struct {
	writes  []file
	removes []string // files, relative to the repo
	message string   // the subject, then a blank line and the body
}

// lockWrite takes the write lock and refuses while the remote is rewritten. Every write holds the
// lock from its first check to its commit, so a command calls it before its checks and apply after them.
func lockWrite(repo string) (func(), error) {
	unlock, err := store.Lock(repo)
	if err != nil {
		return nil, err
	}
	if err := refuseRewritten(repo); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// apply writes and removes the files of a change, regenerates the index of each level on their paths,
// and commits them all, then returns the new commit. It refuses a symlink on any path and a path with
// uncommitted changes. On a failure it restores every path from HEAD. The caller holds the lock of lockWrite.
func apply(repo string, c change) (string, error) {
	var docs []string
	for _, f := range c.writes {
		docs = append(docs, f.path)
	}
	docs = append(docs, c.removes...)
	paths := slices.Clone(docs)
	var levels []string
	for _, d := range docs {
		level, ok := docLevel(d)
		if !ok {
			return "", fmt.Errorf("%s belongs to no level", d)
		}
		for _, l := range store.Path(level) {
			if !slices.Contains(levels, l) {
				levels = append(levels, l)
				paths = append(paths, path.Join(store.Folder(l), "README.md"))
			}
		}
	}
	for _, l := range levels {
		if _, err := store.Lines(repo, l); err != nil {
			return "", fmt.Errorf("read the index of the path: %w", err)
		}
	}
	for _, p := range paths {
		if err := noSymlink(repo, p); err != nil {
			return "", err
		}
	}
	dirty, err := store.Git(repo, append([]string{"--literal-pathspecs", "status", "--porcelain", "--"}, paths...)...)
	if err != nil {
		return "", fmt.Errorf("check the paths of the write: %w", err)
	}
	if strings.TrimSpace(dirty) != "" {
		return "", fmt.Errorf("the working tree of the memory repo has changes that are not committed, so memcore wrote nothing: the person commits or discards them:\n%s", strings.TrimRight(dirty, "\n"))
	}

	var created []string // the folders that the change creates
	err = func() error {
		for _, f := range c.writes {
			full := filepath.Join(repo, filepath.FromSlash(f.path))
			for dir := filepath.Dir(full); dir != repo; dir = filepath.Dir(dir) {
				if _, err := os.Stat(dir); err == nil {
					break
				}
				created = append(created, dir)
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return fmt.Errorf("create the folder of %s: %w", f.path, err)
			}
			if err := os.WriteFile(full, f.content, f.mode); err != nil {
				return fmt.Errorf("write %s: %w", f.path, err)
			}
			if err := os.Chmod(full, f.mode); err != nil {
				return fmt.Errorf("set the mode of %s: %w", f.path, err)
			}
		}
		for _, r := range c.removes {
			if err := os.Remove(filepath.Join(repo, filepath.FromSlash(r))); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", r, err)
			}
		}
		for _, l := range levels {
			if err := store.WriteIndex(repo, l); err != nil {
				return err
			}
		}
		return store.Commit(repo, paths, c.message)
	}()
	if err == nil {
		return headCommit(repo), nil
	}
	store.Git(repo, append([]string{"--literal-pathspecs", "reset", "-q", "--"}, paths...)...)
	for _, p := range paths {
		if _, gerr := store.Git(repo, "cat-file", "-e", "HEAD:"+p); gerr == nil {
			store.Git(repo, "--literal-pathspecs", "checkout", "HEAD", "--", p)
		} else {
			os.Remove(filepath.Join(repo, filepath.FromSlash(p)))
		}
	}
	// Remove the deepest folders first, since one change may create folders for several files.
	slices.SortFunc(created, func(a, b string) int { return len(b) - len(a) })
	for _, dir := range created {
		os.Remove(dir)
	}
	return "", err
}

// docLevel returns the level of a document by its path in the repo: the folders before its kind folder,
// or before its file when the file is a conventions file, a glossary or an index.
func docLevel(p string) (string, bool) {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		last := i == len(parts)-1
		kind := !last && slices.Contains([]string{"memories", "skills", "bin", "plans", "notes"}, part)
		if kind || (last && slices.Contains([]string{"conventions.md", "glossary.md", "README.md"}, part)) {
			return store.LevelOf(strings.Join(parts[:i], "/"))
		}
	}
	return "", false
}
