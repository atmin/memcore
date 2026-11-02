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

// acceptedRef is the remote-tracking branch that holds the accepted history.
const acceptedRef = "refs/remotes/origin/main"

// openRepo returns the memory repo, or an error that says how to create it.
func openRepo() (string, error) {
	repo, err := memoryRepo()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return "", fmt.Errorf("the memory repo %s does not exist: the person runs memcore init <remote>", repo)
	}
	return repo, nil
}

// hasAccepted reports whether the memory repo has an accepted history.
func hasAccepted(repo string) bool {
	_, err := store.Git(repo, "rev-parse", "--verify", "-q", acceptedRef)
	return err == nil
}

// accepted returns a document as the accepted history holds it, and false when it holds none.
func accepted(repo, p string) (string, bool) {
	if !hasAccepted(repo) {
		return "", false
	}
	out, err := store.Git(repo, "show", acceptedRef+":"+p)
	if err != nil {
		return "", false
	}
	return out, true
}

// isSkill reports whether a path in the memory repo belongs to a skill.
// It ignores case, because a file system that ignores case serves SKILLS/ from skills/.
func isSkill(p string) bool {
	parts := strings.Split(p, "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.EqualFold(part, "skills") {
			return true
		}
	}
	return false
}

// runShow prints a document by its path in the memory repo. It prints a skill from the accepted history.
func runShow(env Env, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: memcore show <path>")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	p := path.Clean(filepath.ToSlash(args[0]))
	if path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || p == ".git" || strings.HasPrefix(p, ".git/") {
		return fmt.Errorf("%s is not a path in the memory repo", args[0])
	}
	full := filepath.Join(repo, filepath.FromSlash(p))

	if isSkill(p) {
		content, ok := accepted(repo, p)
		if !ok {
			return fmt.Errorf("no accepted version of %s exists yet: the person has not pushed it", p)
		}
		fmt.Fprint(env.Out, content)
		if work, err := os.ReadFile(full); err != nil || string(work) != content {
			fmt.Fprintf(env.Err, "\nNote: this is the accepted version of %s. The review queue changes it, and the change applies after the person pushes it.\n", p)
		}
		return nil
	}

	info, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no document at %s: memcore orient lists the documents", p)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a folder: memcore show %s prints its index", p, path.Join(p, "README.md"))
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	_, err = env.Out.Write(content)
	return err
}
