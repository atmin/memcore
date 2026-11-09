package store

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// ErrOutsideHome means that a directory has no level, because it is outside home.
var ErrOutsideHome = errors.New("the directory is outside home, so it has no level")

// Level returns the level of a working directory, as a path relative to home.
// The level is the toplevel of the git repo, and a worktree maps to its main checkout.
// A directory in no repo maps to itself, and home maps to "".
func Level(cwd, home string) (string, error) {
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve the working directory: %w", err)
	}
	if _, err := relHome(home, cwd); err != nil {
		return "", fmt.Errorf("%s: %w", cwd, err)
	}
	dir := cwd
	out, err := Git(cwd, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	switch {
	case err == nil:
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			return "", fmt.Errorf("find the repo of %s: unexpected output %q", cwd, out)
		}
		dir = lines[0]
		// The main checkout holds the common git directory. A repo with a separate
		// git directory keeps no record of its main checkout, so its worktrees map to themselves.
		if filepath.Base(lines[1]) == ".git" {
			dir = filepath.Dir(lines[1])
		}
	case strings.Contains(err.Error(), "not a git repository"):
	default:
		return "", fmt.Errorf("find the repo of %s: %w", cwd, err)
	}
	rel, err := relHome(home, dir)
	if err != nil {
		return "", fmt.Errorf("%s: %w", cwd, err)
	}
	return rel, nil
}

// relHome returns dir relative to home, with slashes, or ErrOutsideHome.
func relHome(home, dir string) (string, error) {
	rel, err := filepath.Rel(home, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrOutsideHome
	}
	if rel == "." {
		return "", nil
	}
	return filepath.ToSlash(rel), nil
}

// Path returns the levels from level up to the root, which is "".
func Path(level string) []string {
	var levels []string
	for level != "" && level != "." {
		levels = append(levels, level)
		level = path.Dir(level)
	}
	return append(levels, "")
}

// kindOf splits a path into its level, its kind folder and the parts after the kind folder. The kind
// folder is the first part that names a kind, after a valid level. A file inside a skill has the kind skills.
func kindOf(p string) (level, kind string, rest []string, ok bool) {
	parts := strings.Split(p, "/")
	for i, part := range parts[:len(parts)-1] {
		if !slices.Contains([]string{"memories", "skills", "bin", "plans", "notes"}, part) {
			continue
		}
		level, ok := LevelOf(strings.Join(parts[:i], "/"))
		if !ok {
			return "", "", nil, false
		}
		return level, part, parts[i+1:], true
	}
	return "", "", nil, false
}

// Kind returns the kind folder of a document: memories, skills, bin, plans or notes. It returns "" for a
// path in no kind folder of a level, such as an index, a conventions file or a glossary.
func Kind(p string) string {
	_, kind, _, _ := kindOf(p)
	return kind
}

// IsMemory reports whether a path is a memory: a markdown file in the memories folder of a level, at any
// depth of topic folders. The README.md of a topic folder is its index, and a file inside a skill is part of
// the skill.
func IsMemory(p string) bool {
	_, kind, rest, ok := kindOf(p)
	return ok && kind == "memories" && path.Ext(p) == ".md" && path.Base(p) != "README.md" && len(rest) > 0
}

// IsTopicIndex reports whether a path is the index of a topic folder: the README.md of a folder inside the
// memories or notes folder of a level.
func IsTopicIndex(p string) bool {
	_, kind, rest, ok := kindOf(p)
	return ok && (kind == "memories" || kind == "notes") && len(rest) >= 2 && path.Base(p) == "README.md"
}
