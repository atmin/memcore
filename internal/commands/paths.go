package commands

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// noSymlink refuses a path in the repo whose folders or file include a symlink. So a read or a write stays
// inside the repo, and a link cannot serve an unaccepted skill.
func noSymlink(repo, p string) error {
	cur := repo
	for _, part := range strings.Split(p, "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("check %s: %w", p, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s in the memory repo is a symlink, which memcore refuses: the person replaces it with a folder or a file", strings.TrimPrefix(cur, repo+string(filepath.Separator)))
		}
	}
	return nil
}

// placeAt returns the level where a write from the working directory dir places its document.
// Without at, that is the level of dir. With at, it is the level of the directory at. That level must lie
// on the path from the level of dir to the root. at may start with ~, and a relative at starts from dir.
func placeAt(dir, at, home string) (string, error) {
	level, err := store.Level(dir, home)
	if err != nil {
		return "", fmt.Errorf("place the document: %w", err)
	}
	if at == "" {
		return level, nil
	}
	// The target joins without filepath.Join, which would clean a ".." before a symlink resolves.
	target := at
	switch {
	case at == "~":
		target = home
	case strings.HasPrefix(at, "~/"):
		target = home + string(filepath.Separator) + at[2:]
	case !filepath.IsAbs(at):
		target = dir + string(filepath.Separator) + at
	}
	there, err := store.Level(target, home)
	if err != nil {
		return "", inputErrf("place the document at %s: %w", at, err)
	}
	if !slices.Contains(store.Path(level), there) {
		return "", inputErrf("%s is not on the path from %s to ~, so memcore places nothing there", at, title(level))
	}
	return there, nil
}

// resolve returns the path in the memory repo of a document. The document is named by its path or, for a
// memory, by its name. The path rules of show apply, and the document must exist.
func resolve(repo, arg string) (string, error) {
	if !strings.Contains(arg, "/") {
		found, err := findMemory(repo, arg)
		if err != nil {
			return "", err
		}
		if found != "" {
			return found, noSymlink(repo, found)
		}
	}
	p, err := cleanPath(arg)
	if err != nil {
		return "", err
	}
	if err := noSymlink(repo, p); err != nil {
		return "", err
	}
	info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(p)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", inputErrf("no document at %s: memcore orient lists the documents", arg)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	if info.IsDir() {
		return "", inputErrf("%s is a folder, not a document", p)
	}
	return p, exactCase(repo, p)
}

// parseInterleaved parses flags that may come before or after the positional arguments, and returns those.
func parseInterleaved(flags *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		if flags.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, flags.Arg(0))
		args = flags.Args()[1:]
	}
}

// exactCase refuses a path whose spelling differs from the one on disk. An example is a.MD for a.md on a
// file system that ignores case. A write under the other spelling would miss git, and its rollback could
// remove the file.
func exactCase(repo, p string) error {
	dir := repo
	for _, part := range strings.Split(p, "/") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read %s: %w", dir, err)
		}
		found := ""
		for _, e := range entries {
			if e.Name() == part {
				found = part
				break
			}
			if strings.EqualFold(e.Name(), part) {
				found = e.Name()
			}
		}
		if found != part {
			return inputErrf("no document at %s: the repo spells it with other cases, as %s", p, found)
		}
		dir = filepath.Join(dir, part)
	}
	return nil
}

// cleanPath returns a path in the memory repo, cleaned and with slashes. It refuses a path that leaves the
// repo or enters .git.
func cleanPath(arg string) (string, error) {
	p := path.Clean(filepath.ToSlash(arg))
	if path.IsAbs(p) || p == "." || p == ".." || strings.HasPrefix(p, "../") || p == ".git" || strings.HasPrefix(p, ".git/") {
		return "", inputErrf("%s is not a path in the memory repo", arg)
	}
	return p, nil
}
