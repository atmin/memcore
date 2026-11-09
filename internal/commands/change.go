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
	prune   []string // folders that the removal empties, which go with every empty folder inside them
	message string   // the subject, then a blank line and the body
}

// lockWrite takes the write lock and refuses while the remote is rewritten. Every write holds the
// lock from its first check to its commit. So a command calls lockWrite before its checks, and apply after them.
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

// apply makes a change and returns the new commit. It does these steps:
//   - It writes and removes the files of the change.
//   - It regenerates the index of each level on their paths.
//   - It commits them all.
//
// It refuses a symlink on any path and a path with uncommitted changes. On a failure it restores every path
// from HEAD. The caller holds the lock of lockWrite.
func apply(repo string, c change) (string, error) {
	var docs []string
	for _, f := range c.writes {
		docs = append(docs, f.path)
	}
	docs = append(docs, c.removes...)
	paths := slices.Clone(docs)
	var levels, topics []string
	for _, d := range docs {
		for _, tf := range store.Topics(d) {
			index := path.Join(tf, "README.md")
			if slices.Contains(topics, tf) {
				continue
			}
			if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(index))); err != nil && !slices.Contains(docs, index) {
				continue // a folder without an index is no topic folder
			}
			topics = append(topics, tf)
			if !slices.Contains(paths, index) {
				paths = append(paths, index)
			}
		}
	}
	deepestFirst(topics)
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

	if unchanged(repo, c) {
		return "", errNoChange
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
			full := filepath.Join(repo, filepath.FromSlash(r))
			if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", r, err)
			}
			// Remove the folders that the removal leaves empty, such as the folder of a skill.
			for dir := filepath.Dir(full); dir != repo && os.Remove(dir) == nil; dir = filepath.Dir(dir) {
			}
		}
		for _, p := range c.prune {
			if err := pruneEmpty(filepath.Join(repo, filepath.FromSlash(p))); err != nil {
				return err
			}
		}
		// A removal can leave a topic folder with nothing but its index. That folder goes, deepest first,
		// so its parent index drops it. A draft never prunes, so a new topic folder stays.
		goneTopic := map[string]bool{}
		if len(c.removes) > 0 {
			for _, tf := range topics {
				full := filepath.Join(repo, filepath.FromSlash(tf))
				if _, err := os.Stat(full); err != nil {
					continue
				}
				empty, err := topicEmpty(repo, tf)
				if err != nil {
					return err
				}
				if empty {
					if err := os.RemoveAll(full); err != nil {
						return fmt.Errorf("remove the empty topic folder %s: %w", tf, err)
					}
					goneTopic[tf] = true
				}
			}
		}
		for _, tf := range topics {
			if goneTopic[tf] {
				continue
			}
			if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(tf), "README.md")); err != nil {
				continue // a folder without an index is no topic folder
			}
			if err := store.WriteTopicIndex(repo, tf); err != nil {
				return err
			}
		}
		// A removal can leave a level with nothing but its index. That level goes too, deepest first,
		// so its parent index drops it. The root stays.
		gone := map[string]bool{}
		if len(c.removes) > 0 {
			deep := slices.Clone(levels)
			slices.SortFunc(deep, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
			for _, l := range deep {
				if l == "" {
					continue
				}
				empty, err := levelEmpty(repo, l)
				if err != nil {
					return err
				}
				if empty {
					if err := os.RemoveAll(filepath.Join(repo, filepath.FromSlash(store.Folder(l)))); err != nil {
						return fmt.Errorf("remove the empty level %s: %w", l, err)
					}
					gone[l] = true
				}
			}
		}
		for _, l := range levels {
			if gone[l] {
				continue
			}
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

// docLevel returns the level of a document by its path in the repo. The level is the folders before its
// kind folder. For a conventions file, a glossary or an index, the level is the folders before the file.
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

// commitMessage returns the commit message of a command that changes one document. When the queue holds the
// last change of the document, the message makes a fixup of that change. Push folds the fixup into it.
// Otherwise the message is "<command>: <path>". The reason goes in the body.
func commitMessage(repo, rel, command, reason string) (string, error) {
	plain := command + ": " + rel + "\n\n" + reason
	out, err := store.Git(repo, "--literal-pathspecs", "log", "-1", "--format=%H%x00%s", "--", rel)
	if err != nil {
		return "", fmt.Errorf("find the last change of %s: %w", rel, err)
	}
	sha, subject, ok := strings.Cut(strings.TrimSpace(out), "\x00")
	if !ok {
		return plain, nil
	}
	if accepted, ok := revParse(repo, acceptedRef); ok {
		pushed, err := isAncestor(repo, sha, accepted)
		if err != nil {
			return "", fmt.Errorf("check whether the last change of %s is pushed: %w", rel, err)
		}
		if pushed {
			return plain, nil
		}
	}
	return "fixup! " + subject + "\n\n" + plain, nil
}

// errNoChange means that a change would leave every file as it is, so apply made no commit.
var errNoChange = errors.New("the change leaves every file as it is")

// unchanged reports whether every file of a change already holds its content and mode, and every removal is gone.
func unchanged(repo string, c change) bool {
	for _, f := range c.writes {
		full := filepath.Join(repo, filepath.FromSlash(f.path))
		info, err := os.Stat(full)
		if err != nil || info.Mode().Perm() != f.mode.Perm() {
			return false
		}
		if b, err := os.ReadFile(full); err != nil || string(b) != string(f.content) {
			return false
		}
	}
	for _, r := range c.removes {
		if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(r))); err == nil {
			return false
		}
	}
	return true
}

// docKind returns the kind folder of a document by its path: memories, skills, bin, plans or notes.
// It returns "" for a conventions file, a glossary or an index. A file inside a skill is of kind skills.
func docKind(p string) string {
	parts := strings.Split(p, "/")
	for _, part := range parts[:len(parts)-1] {
		if slices.Contains([]string{"memories", "skills", "bin", "plans", "notes"}, part) {
			return part
		}
	}
	return ""
}

// levelEmpty reports whether the folder of a level holds no file but its own index.
func levelEmpty(repo, level string) (bool, error) {
	dir := filepath.Join(repo, filepath.FromSlash(store.Folder(level)))
	empty := true
	err := filepath.WalkDir(dir, func(f string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipAll
		}
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
		return false, fmt.Errorf("check whether the level %s is empty: %w", level, err)
	}
	return empty, nil
}

// pruneEmpty removes the empty folders inside dir, deepest first, then dir itself when it is empty.
// A folder that holds a file stays.
func pruneEmpty(dir string) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(f string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipAll
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, f)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("list the folders of %s: %w", dir, err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // fails, and keeps the folder, when it holds anything
	}
	return nil
}
