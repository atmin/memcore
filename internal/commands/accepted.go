package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// acceptedDir returns the checkout of the accepted history, a detached worktree under the local state.
func acceptedDir(repo string) string {
	return filepath.Join(store.StateDir(repo), "accepted")
}

// binDir returns the folder that memcore links scripts into, ~/.local/bin.
func binDir() (string, error) {
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "bin"), nil
}

// syncAccepted moves the checkout of the accepted history to origin/main. Then it links each of its scripts
// onto ~/.local/bin, and it removes the links of scripts that are gone. A script runs as the person accepted
// it, whatever the review queue holds. The caller holds the write lock.
func syncAccepted(repo string) error {
	accepted, ok := revParse(repo, acceptedRef)
	if !ok {
		return nil
	}
	dir := acceptedDir(repo)
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, fs.ErrNotExist) {
		store.Git(repo, "worktree", "prune")
		if _, err := store.Git(repo, "worktree", "add", "-q", "--detach", "-f", dir, accepted); err != nil {
			return fmt.Errorf("check out the accepted history: %w", err)
		}
	} else if _, err := store.Git(dir, "checkout", "-q", "--detach", "--force", accepted); err != nil {
		return fmt.Errorf("move the checkout of the accepted history: %w", err)
	}
	bin, err := binDir()
	if err != nil {
		return err
	}
	scripts, err := scriptsIn(repo, accepted)
	if err != nil {
		return err
	}
	if len(scripts) > 0 {
		if err := os.MkdirAll(bin, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", bin, err)
		}
	}
	for name, rel := range scripts {
		link := filepath.Join(bin, name)
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if have, err := os.Readlink(link); err == nil {
			if filepath.Clean(have) == target {
				continue
			}
			if !ours(repo, have) {
				continue // a foreign link stays, and push refuses a new script with its name
			}
			if err := os.Remove(link); err != nil {
				return fmt.Errorf("replace the link of the script %s: %w", name, err)
			}
		} else if _, err := os.Lstat(link); err == nil {
			continue // a foreign file stays
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("link the script %s: %w", name, err)
		}
	}
	entries, err := os.ReadDir(bin)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list %s: %w", bin, err)
	}
	for _, e := range entries {
		link := filepath.Join(bin, e.Name())
		have, err := os.Readlink(link)
		if err != nil || !ours(repo, have) {
			continue
		}
		if rel, ok := scripts[e.Name()]; ok && filepath.Clean(have) == filepath.Join(dir, filepath.FromSlash(rel)) {
			continue
		}
		// The accepted history lacks this script, or holds it elsewhere or as a helper.
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("remove the link %s of a script that the accepted history no longer holds: %w", link, err)
		}
	}
	return nil
}

// ours reports whether a link target points into the accepted checkout of the repo, after any .. resolves.
func ours(repo, target string) bool {
	t := filepath.Clean(target)
	return filepath.IsAbs(t) && strings.HasPrefix(t, acceptedDir(repo)+string(filepath.Separator))
}

// scriptsIn returns the scripts of a commit by name. A script is a regular executable file, mode 100755,
// directly in the bin folder of a level. It reads the tree of git, so no symlink and no untracked file counts.
// A script name is unique in the memory repo. So two scripts with one name are an error that names both.
func scriptsIn(repo, rev string) (map[string]string, error) {
	out, err := store.Git(repo, "ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, fmt.Errorf("list the scripts of %s: %w", rev, err)
	}
	scripts := map[string]string{}
	for _, entry := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(entry, "\t")
		if !ok || !strings.HasPrefix(meta, "100755 ") || path.Base(path.Dir(p)) != "bin" {
			continue
		}
		folder := path.Dir(path.Dir(p))
		if folder == "." {
			folder = ""
		}
		if _, isLevel := store.LevelOf(folder); !isLevel {
			continue // a file in the bin folder of a skill
		}
		name := path.Base(p)
		if other, dup := scripts[name]; dup {
			return nil, fmt.Errorf("two scripts have the name %s, %s and %s: a script name is unique in the memory repo", name, other, p)
		}
		scripts[name] = p
	}
	return scripts, nil
}

// foreignNames refuses a script of the queue that the accepted history lacks, when a foreign file holds its
// name. A foreign file is a file in ~/.local/bin that memcore did not link. It refuses two scripts with one
// name too.
func foreignNames(repo string) error {
	bin, err := binDir()
	if err != nil {
		return err
	}
	now, err := scriptsIn(repo, "HEAD")
	if err != nil {
		return err
	}
	before := map[string]string{}
	if accepted, ok := revParse(repo, acceptedRef); ok {
		if before, err = scriptsIn(repo, accepted); err != nil {
			return err
		}
	}
	for name := range now {
		if _, ok := before[name]; ok {
			continue
		}
		link := filepath.Join(bin, name)
		if _, err := os.Lstat(link); err != nil {
			continue
		}
		if target, err := os.Readlink(link); err == nil && ours(repo, target) {
			continue
		}
		return fmt.Errorf("the new script %s has the name of %s, which memcore did not link, so push pushed nothing: the person removes that file, or the script gets another name", name, link)
	}
	return nil
}
