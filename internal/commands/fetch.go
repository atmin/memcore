package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
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
	if err := refuseRewritten(repo); err != nil {
		return err
	}
	// The fetch lands under refs/memcore/fetched/, so origin/main moves only after the check below.
	if _, err := store.Git(repo, "fetch", "-q", "--refmap=", "origin", "+refs/heads/*:refs/memcore/fetched/*"); err != nil {
		return fmt.Errorf("fetch the remote: %w", err)
	}
	fetched, ok := revParse(repo, fetchedRef)
	if !ok {
		return nil // the remote has no main yet
	}
	if old, ok := revParse(repo, acceptedRef); ok && old != fetched {
		contains, err := isAncestor(repo, old, fetched)
		if err != nil {
			return fmt.Errorf("compare the fetched tip with the accepted history: %w", err)
		}
		if !contains {
			m := rewritten{accepted: old, fetched: fetched}
			if err := writeMarker(repo, m); err != nil {
				return err
			}
			return m.refusal()
		}
	}
	if _, err := store.Git(repo, "update-ref", acceptedRef, fetched); err != nil {
		return fmt.Errorf("move the accepted history: %w", err)
	}
	if err := syncAccepted(repo); err != nil {
		return err
	}
	err = rebase(repo, "-q", acceptedRef)
	var ce *conflictError
	if errors.As(err, &ce) {
		return fmt.Errorf("the review queue and the accepted history change the same documents, so memcore left the queue as it was: %s", strings.Join(ce.paths, ", "))
	}
	return err
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

// fetchedRef is where a fetch lands the main branch of the remote, before memcore accepts it.
const fetchedRef = "refs/memcore/fetched/main"

// rewritten is the marker of a remote that lacks the accepted history.
type rewritten struct{ accepted, fetched string }

// refusal is the error of every write while the marker exists.
func (m rewritten) refusal() error {
	return fmt.Errorf("the remote no longer holds the accepted commit %s, so memcore refuses writes until the person runs memcore recover", short(m.accepted))
}

func markerPath(repo string) string {
	return filepath.Join(store.StateDir(repo), "rewritten")
}

// readMarker returns the marker of a rewritten remote, and false when there is none.
func readMarker(repo string) (rewritten, bool, error) {
	b, err := os.ReadFile(markerPath(repo))
	if errors.Is(err, fs.ErrNotExist) {
		return rewritten{}, false, nil
	}
	if err != nil {
		return rewritten{}, false, fmt.Errorf("read the marker of a rewritten remote: %w", err)
	}
	var m rewritten
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "accepted "); ok {
			m.accepted = v
		}
		if v, ok := strings.CutPrefix(line, "fetched "); ok {
			m.fetched = v
		}
	}
	if m.accepted == "" || m.fetched == "" {
		return rewritten{}, false, fmt.Errorf("the marker %s is damaged: the person removes it and fetches again", markerPath(repo))
	}
	return m, true, nil
}

func writeMarker(repo string, m rewritten) error {
	if err := os.MkdirAll(store.StateDir(repo), 0o755); err != nil {
		return fmt.Errorf("create the local state: %w", err)
	}
	if err := os.WriteFile(markerPath(repo), []byte("accepted "+m.accepted+"\nfetched "+m.fetched+"\n"), 0o644); err != nil {
		return fmt.Errorf("write the marker of a rewritten remote: %w", err)
	}
	return nil
}

// refuseRewritten returns the refusal of a write while the marker of a rewritten remote exists.
func refuseRewritten(repo string) error {
	m, ok, err := readMarker(repo)
	if err != nil {
		return err
	}
	if ok {
		return m.refusal()
	}
	return nil
}

// revParse returns the commit of a ref, and false when the ref does not exist.
func revParse(repo, ref string) (string, bool) {
	out, err := store.Git(repo, "rev-parse", "-q", "--verify", ref+"^{commit}")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// short returns the first seven characters of a commit.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// isAncestor reports whether commit a is an ancestor of commit b, or b itself.
// Only the exit status 1 of git means no. Every other failure is an error.
func isAncestor(repo, a, b string) (bool, error) {
	_, err := store.Git(repo, "merge-base", "--is-ancestor", a, b)
	var ee *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return false, nil
	default:
		return false, err
	}
}
