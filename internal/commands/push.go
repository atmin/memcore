package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// runPush fetches, rebases the review queue, asks the person at the terminal, and pushes the queue.
// It pushes the tip that it counted, so a commit that lands while it asks waits for the next push.
func runPush(env Env, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: memcore push")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	tip, base, queue, made, folded, err := prepare(repo)
	env.event().Made, env.event().Folded = made, folded
	if err != nil {
		return err
	}
	if len(queue) == 0 {
		fmt.Fprintln(env.Out, "Nothing to push.")
		return nil
	}
	confirm := env.Confirm
	if confirm == nil {
		confirm = guards.Confirm
	}
	for _, c := range queue {
		fmt.Fprintf(env.Out, "  %s\n", c)
	}
	ok, err := confirm(fmt.Sprintf("Push %s to origin? [y/N] ", commits(len(queue))))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(env.Out, "Pushed nothing.")
		return nil
	}
	// The person may answer late, so push fetches again under the lock, and pushes only the queue that it counted.
	unlock, err := store.Lock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := Fetch(repo); err != nil { // Fetch refuses while the remote is rewritten
		return err
	}
	if now, _ := revParse(repo, acceptedRef); now != base || headCommit(repo) != tip {
		return errors.New("the remote or the queue changed while the person answered, so push pushed nothing: run memcore push again")
	}
	pushed, err := revList(repo, tip)
	if err != nil {
		return err
	}
	if _, err := store.Git(repo, "push", "-q", "origin", tip+":refs/heads/main"); err != nil {
		return fmt.Errorf("push to origin: %w", err)
	}
	env.event().Pushed = pushed
	if _, err := store.Git(repo, "update-ref", acceptedRef, tip); err != nil {
		return fmt.Errorf("move the accepted history to the pushed tip: %w", err)
	}
	fmt.Fprintf(env.Out, "Pushed %s.\n", commits(len(queue)))
	return nil
}

// revList returns the commits of the queue up to tip, oldest first.
func revList(repo, tip string) ([]string, error) {
	rng := tip
	if hasAccepted(repo) {
		rng = acceptedRef + ".." + tip
	}
	out, err := store.Git(repo, "rev-list", "--reverse", rng)
	if err != nil {
		return nil, fmt.Errorf("list the review queue: %w", err)
	}
	return strings.Fields(out), nil
}

// prepare readies the queue for a push under the write lock. It refuses a working tree with
// uncommitted changes, fetches, regenerates the stale indexes, and folds the fixups. It returns the tip,
// the accepted commit, the queue, newest first, the commits of the indexes and the commits that the fold removed.
func prepare(repo string) (tip, base string, queue, made, folded []string, err error) {
	unlock, err := store.Lock(repo)
	if err != nil {
		return "", "", nil, nil, nil, err
	}
	defer unlock()
	if err := cleanTree(repo, "push pushed nothing"); err != nil {
		return "", "", nil, nil, nil, err
	}
	if err := Fetch(repo); err != nil {
		return "", "", nil, nil, nil, err
	}
	if made, err = regenerate(repo); err != nil {
		return "", "", nil, made, nil, err
	}
	if folded, err = fold(repo); err != nil {
		return "", "", nil, made, nil, err
	}
	tip, queue, err = queueOf(repo)
	if err != nil {
		return "", "", nil, made, folded, err
	}
	base, _ = revParse(repo, acceptedRef)
	return tip, base, queue, made, folded, nil
}

// cleanTree refuses a working tree with changes that are not committed, and names them.
func cleanTree(repo, consequence string) error {
	out, err := store.Git(repo, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("check the working tree: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("the working tree of the memory repo has changes that are not committed, so %s: the person commits or discards them:\n%s", consequence, strings.TrimRight(out, "\n"))
	}
	return nil
}
