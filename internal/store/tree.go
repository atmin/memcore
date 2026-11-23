package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Tree reads the working tree of a repo, and skips each untracked path that git ignores. Every walk of the
// memory repo reads through a Tree, so an ignored file is no document and an ignored folder is no level.
type Tree struct {
	root    string
	files   map[string]bool // the ignored files, relative to the root
	folders map[string]bool // the ignored folders, relative to the root
}

// OpenTree asks git which untracked paths of the working tree at repo it ignores. It reads the ignored paths
// once, so a Tree shows the ignored paths of the moment of the call. In a directory with no .git it runs no
// git, and ignores nothing.
func OpenTree(repo string) (Tree, error) {
	t := Tree{root: repo, files: map[string]bool{}, folders: map[string]bool{}}
	if _, err := os.Lstat(filepath.Join(repo, ".git")); errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	out, err := Git(repo, "--no-optional-locks", "-c", "core.fsmonitor=false",
		"ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return t, fmt.Errorf("list the ignored files of %s: %w", repo, err)
	}
	for _, p := range strings.Split(out, "\x00") {
		switch {
		case p == "":
		case strings.HasSuffix(p, "/"):
			t.folders[strings.TrimSuffix(p, "/")] = true
		default:
			t.files[p] = true
		}
	}
	return t, nil
}

// Root returns the directory of the repo.
func (t Tree) Root() string {
	return t.root
}

// Ignored reports whether git ignores the path rel, or a folder that holds it. rel is relative to the root,
// with slashes.
func (t Tree) Ignored(rel string) bool {
	rel = clean(rel)
	if rel == "" {
		return false
	}
	if t.files[rel] {
		return true
	}
	for p := rel; p != "."; p = path.Dir(p) {
		if t.folders[p] {
			return true
		}
	}
	return false
}

// Exists reports whether the path rel exists, and git does not ignore it.
func (t Tree) Exists(rel string) bool {
	if t.Ignored(rel) {
		return false
	}
	_, err := os.Stat(t.abs(rel))
	return err == nil
}

// ReadDir lists the folder rel as os.ReadDir does, without the ignored paths. An ignored folder does not
// exist.
func (t Tree) ReadDir(rel string) ([]fs.DirEntry, error) {
	if t.Ignored(rel) {
		return nil, &fs.PathError{Op: "open", Path: t.abs(rel), Err: fs.ErrNotExist}
	}
	list, err := os.ReadDir(t.abs(rel))
	var visible []fs.DirEntry
	for _, e := range list {
		if !t.Ignored(path.Join(clean(rel), e.Name())) {
			visible = append(visible, e)
		}
	}
	return visible, err
}

// WalkDir walks the folder rel as filepath.WalkDir does, and fn gets the same absolute paths. It skips each
// ignored path, and never enters an ignored folder. An ignored root does not exist.
func (t Tree) WalkDir(rel string, fn fs.WalkDirFunc) error {
	root := t.abs(rel)
	if t.Ignored(rel) {
		err := fn(root, nil, &fs.PathError{Op: "lstat", Path: root, Err: fs.ErrNotExist})
		if errors.Is(err, filepath.SkipDir) || errors.Is(err, fs.SkipAll) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if r, rerr := filepath.Rel(t.root, p); rerr == nil && d != nil && t.Ignored(filepath.ToSlash(r)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return fn(p, d, err)
	})
}

// onlyIgnored reports whether the folder rel holds files, and git ignores each of them. Git reports such a
// folder as ignored only while its index tracks no file in it, so a removal that is not committed yet hides it.
func (t Tree) onlyIgnored(rel string) bool {
	ignored, seen := false, false
	err := filepath.WalkDir(t.abs(rel), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, err := filepath.Rel(t.root, p)
		if err != nil {
			return err
		}
		switch {
		case t.Ignored(filepath.ToSlash(r)):
			ignored = true
			if d.IsDir() {
				return filepath.SkipDir
			}
		case !d.IsDir():
			seen = true
			return fs.SkipAll
		}
		return nil
	})
	return err == nil && ignored && !seen
}

// abs returns the absolute path of rel.
func (t Tree) abs(rel string) string {
	return filepath.Join(t.root, filepath.FromSlash(clean(rel)))
}

// clean returns rel in its clean form, with "" for the root.
func clean(rel string) string {
	rel = path.Clean(filepath.ToSlash(rel))
	if rel == "." || rel == "/" {
		return ""
	}
	return rel
}
