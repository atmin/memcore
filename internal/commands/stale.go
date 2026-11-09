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
			if index := path.Join(folder, "README.md"); store.IsTopicIndex(index) {
				if _, err := os.Stat(filepath.Join(f, "README.md")); err != nil {
					return nil // a folder without an index is no topic folder
				}
				if old, err := topicStale(repo, folder); err != nil {
					return err
				} else if old {
					stale = append(stale, index)
				}
			}
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

// topicFolders returns the topic folders of the repo that hold an index, deepest first.
func topicFolders(repo string) ([]string, error) {
	var topics []string
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
		if !store.IsTopicIndex(path.Join(folder, "README.md")) {
			return nil
		}
		if _, err := os.Stat(filepath.Join(f, "README.md")); err == nil {
			topics = append(topics, folder)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list the topic folders: %w", err)
	}
	deepestFirst(topics)
	return topics, nil
}

// deepestFirst sorts folders so that a folder comes before its parent.
func deepestFirst(folders []string) {
	slices.SortStableFunc(folders, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
}

// topicEmpty reports whether a topic folder holds no file but its own index.
func topicEmpty(repo, folder string) (bool, error) {
	dir := filepath.Join(repo, filepath.FromSlash(folder))
	empty := true
	err := filepath.WalkDir(dir, func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && f != filepath.Join(dir, "README.md") {
			empty = false
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("check whether %s is empty: %w", folder, err)
	}
	return empty, nil
}

// topicStale reports whether the index of a topic folder differs from what the folder generates. An index
// whose frontmatter does not parse counts as stale, so push names it.
func topicStale(repo, folder string) (bool, error) {
	ix, err := store.TopicIndex(repo, folder)
	if err != nil {
		return false, err
	}
	want, err := store.RenderTopic(repo, ix)
	if err != nil {
		return true, nil
	}
	have, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(folder), "README.md"))
	if err != nil {
		return false, fmt.Errorf("read the index of %s: %w", folder, err)
	}
	return string(have) != want, nil
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
		if err := noSymlink(repo, index); err != nil {
			return made, err
		}
		if store.IsTopicIndex(index) {
			if err := store.WriteTopicIndex(repo, folder); err != nil {
				return made, err
			}
		} else {
			level, _ := store.LevelOf(folder)
			if err := store.WriteIndex(repo, level); err != nil {
				return made, err
			}
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
