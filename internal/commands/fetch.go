package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// Fetch fetches origin, and rebases the review queue onto the accepted history when it exists.
// On a conflict it aborts the rebase and returns an error that names the paths.
// The caller holds the write lock.
func Fetch(repo string) error {
	if rebasing(repo) {
		return errors.New("a rebase is in progress in the memory repo: the person finishes it or aborts it first")
	}
	out, err := store.Git(repo, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil || strings.TrimSpace(out) != "main" {
		return fmt.Errorf("the memory repo has %q checked out: the person checks out main first", strings.TrimSpace(out))
	}
	if _, err := store.Git(repo, "fetch", "-q", "origin"); err != nil {
		return fmt.Errorf("fetch the remote: %w", err)
	}
	if !hasAccepted(repo) {
		return nil
	}
	_, err = store.Git(repo, "rebase", "-q", acceptedRef)
	if err == nil {
		return nil
	}
	if !rebasing(repo) {
		return fmt.Errorf("rebase the review queue: %w", err)
	}
	out, _ = store.Git(repo, "diff", "--name-only", "--diff-filter=U")
	if _, aerr := store.Git(repo, "rebase", "--abort"); aerr != nil {
		return fmt.Errorf("rebase the review queue: %w; abort the rebase: %v", err, aerr)
	}
	if paths := strings.Fields(out); len(paths) > 0 {
		return fmt.Errorf("the review queue and the accepted history change the same documents, so memcore left the queue as it was: %s", strings.Join(paths, ", "))
	}
	return fmt.Errorf("rebase the review queue: %w", err)
}

// rebasing reports whether a rebase is in progress in the repo.
func rebasing(repo string) bool {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		out, err := store.Git(repo, "rev-parse", "--git-path", d)
		if err != nil {
			continue
		}
		p := strings.TrimSpace(out)
		if !filepath.IsAbs(p) {
			p = filepath.Join(repo, p)
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// fetchLocked takes the write lock, fetches, and returns the review queue.
func fetchLocked(repo string) (tip string, queue []string, err error) {
	unlock, err := store.Lock(repo)
	if err != nil {
		return "", nil, err
	}
	defer unlock()
	if err := Fetch(repo); err != nil {
		return "", nil, err
	}
	return queueOf(repo)
}

// queueOf returns the review queue: its tip and its commits, newest first.
func queueOf(repo string) (tip string, queue []string, err error) {
	out, err := store.Git(repo, "rev-parse", "refs/heads/main")
	if err != nil {
		return "", nil, fmt.Errorf("read the tip of the queue: %w", err)
	}
	tip = strings.TrimSpace(out)
	rng := tip
	if hasAccepted(repo) {
		rng = acceptedRef + ".." + tip
	}
	out, err = store.Git(repo, "log", "--oneline", rng)
	if err != nil {
		return "", nil, fmt.Errorf("list the review queue: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			queue = append(queue, line)
		}
	}
	return tip, queue, nil
}

// commits says "1 commit" or "<n> commits", with any words in front of the noun.
func commits(n int, words ...string) string {
	noun := "commit"
	if n != 1 {
		noun = "commits"
	}
	return strings.Join(append(append([]string{strconv.Itoa(n)}, words...), noun), " ")
}
