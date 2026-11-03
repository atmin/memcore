package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
