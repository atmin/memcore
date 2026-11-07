package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/atmin/memcore/internal/store"
)

// staleIndexes returns the indexes that differ from what their levels generate, as paths in the repo. An
// example is an index after a commit by hand. A level with documents and no index counts as well.
func staleIndexes(repo string) ([]string, error) {
	var stale []string
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
			return nil // a kind folder, or a folder that no level maps to
		}
		ix, err := store.Lines(repo, level)
		if err != nil {
			return err
		}
		index := path.Join(folder, "README.md")
		have, err := os.ReadFile(filepath.Join(f, "README.md"))
		if errors.Is(err, fs.ErrNotExist) && !holdsFile(f) {
			return nil // an empty folder needs no index
		}
		if string(have) != store.Render(ix) {
			stale = append(stale, index)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("check the indexes: %w", err)
	}
	slices.Sort(stale)
	return stale, nil
}

// regenerate writes each stale index again and commits it alone, as "index: <path>".
// The caller holds the write lock, and the working tree is clean.
func regenerate(repo string) ([]string, error) {
	stale, err := staleIndexes(repo)
	if err != nil {
		return nil, err
	}
	var made []string
	for _, index := range stale {
		folder := path.Dir(index)
		if folder == "." {
			folder = ""
		}
		level, _ := store.LevelOf(folder)
		if err := noSymlink(repo, index); err != nil {
			return made, err
		}
		if err := store.WriteIndex(repo, level); err != nil {
			return made, err
		}
		if err := store.Commit(repo, []string{index}, "index: "+index); err != nil {
			restore(repo, index)
			return made, err
		}
		made = append(made, headCommit(repo))
	}
	return made, nil
}

// holdsFile reports whether a folder holds a file anywhere inside it.
func holdsFile(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(f string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// restore puts a path back as HEAD holds it, or removes it when HEAD has no such path.
func restore(repo, p string) {
	store.Git(repo, "--literal-pathspecs", "reset", "-q", "--", p)
	if _, err := store.Git(repo, "cat-file", "-e", "HEAD:"+p); err == nil {
		store.Git(repo, "--literal-pathspecs", "checkout", "HEAD", "--", p)
		return
	}
	os.Remove(filepath.Join(repo, filepath.FromSlash(p)))
}
