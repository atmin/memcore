package store

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
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

// IsMemory reports whether a path is a memory: a markdown file in the memories folder of a level.
// A file under the memories folder of a skill is part of the skill.
func IsMemory(p string) bool {
	if path.Base(path.Dir(p)) != "memories" || path.Ext(p) != ".md" {
		return false
	}
	folder := path.Dir(path.Dir(p))
	if folder == "." {
		folder = ""
	}
	_, ok := LevelOf(folder)
	return ok
}
