package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atmin/memcore"
	"github.com/atmin/memcore/internal/store"
)

// voicePath is the path of the voice skill in the memory repo and in memcore.Skills.
const voicePath = "skills/voice/SKILL.md"

// initMessage is the message of the first commit of every memory repo, which init makes.
const initMessage = "init: create the memory repo"

// runInit sets up the memory repo: it creates it when the remote is empty, and clones the remote otherwise.
func runInit(env Env, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: memcore init <remote>")
	}
	remote := args[0]
	if !filepath.IsAbs(remote) {
		if _, err := os.Stat(filepath.Join(env.Dir, remote)); err == nil {
			remote = filepath.Join(env.Dir, remote)
		}
	}
	repo, err := memoryRepo()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		return fmt.Errorf("create the folder of the memory repo: %w", err)
	}
	unlock, err := store.LockDir(filepath.Dir(repo))
	if err != nil {
		return err
	}
	defer unlock()

	ok, err := isMemoryRepo(repo)
	if err != nil {
		return err
	}
	if ok {
		if err := os.MkdirAll(store.StateDir(repo), 0o755); err != nil {
			return fmt.Errorf("create the local state: %w", err)
		}
		fmt.Fprintf(env.Out, "%s is already set up, so init changed nothing.\n", repo)
		return nil
	}
	if !isEmpty(repo) {
		return fmt.Errorf("%s exists and is not a memory repo of memcore: move it away, then run init again", repo)
	}

	// init builds the repo beside its final path and renames it into place, so a failure leaves
	// that path as it was, and the rename refuses a directory that something filled meanwhile.
	tmp, err := os.MkdirTemp(filepath.Dir(repo), ".memory-init-")
	if err != nil {
		return fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(tmp)

	out, err := store.Git(env.Dir, "ls-remote", remote)
	if err != nil {
		return fmt.Errorf("read the remote: %w", err)
	}
	cloned := strings.TrimSpace(out) != ""
	if cloned {
		if _, err := store.Git(env.Dir, "clone", "-q", "--branch", "main", remote, tmp); err != nil {
			return fmt.Errorf("clone the remote: %w", err)
		}
		if err := os.MkdirAll(store.StateDir(tmp), 0o755); err != nil {
			return fmt.Errorf("create the local state: %w", err)
		}
	} else if err := create(tmp, remote); err != nil {
		return err
	}
	if err := os.Rename(tmp, repo); err != nil {
		return fmt.Errorf("move the new memory repo into place: %w", err)
	}
	if cloned {
		fmt.Fprintf(env.Out, "Cloned %s into %s.\n", remote, repo)
	} else {
		fmt.Fprintf(env.Out, "Created %s with the voice skill. The person accepts it with memcore push.\n", repo)
	}
	return nil
}

// create makes a new memory repo with the voice skill and the root index, and commits them.
func create(repo, remote string) error {
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return fmt.Errorf("create the memory repo: %w", err)
	}
	if _, err := store.Git(repo, "init", "-q", "-b", "main"); err != nil {
		return fmt.Errorf("create the memory repo: %w", err)
	}
	if _, err := store.Git(repo, "remote", "add", "origin", remote); err != nil {
		return fmt.Errorf("add the remote: %w", err)
	}
	unlock, err := store.Lock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	voice, err := memcore.Skills.ReadFile(voicePath)
	if err != nil {
		return fmt.Errorf("read the shipped voice skill: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(voicePath)), 0o755); err != nil {
		return fmt.Errorf("seed the voice skill: %w", err)
	}
	if err := os.WriteFile(filepath.Join(repo, voicePath), voice, 0o644); err != nil {
		return fmt.Errorf("seed the voice skill: %w", err)
	}
	if err := store.WriteIndex(repo, ""); err != nil {
		return err
	}
	return store.Commit(repo, []string{"README.md", voicePath}, initMessage)
}

// isMemoryRepo reports whether repo is a git repo whose first commit is the one that init makes.
// The indexes may sit behind a commit by hand, so they say nothing about who made the repo.
func isMemoryRepo(repo string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repo, ".git")); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	out, err := store.Git(repo, "log", "--format=%s", "--max-parents=0", "HEAD")
	if err != nil {
		return false, nil // not a repo, or one without a commit
	}
	return slices.Contains(strings.Split(strings.TrimSpace(out), "\n"), initMessage), nil
}

// isEmpty reports whether dir is absent or an empty directory.
func isEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return err == nil && len(entries) == 0
}
