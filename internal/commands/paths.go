package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// noSymlink refuses a path in the repo whose folders or file include a symlink,
// so a read or a write stays inside the repo and a link cannot serve an unaccepted skill.
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
// Without at, that is the level of dir. With at, it is the level of that directory, which must lie
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
