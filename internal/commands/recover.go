package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// runRecover drops the accepted commits that the remote lost, and moves the queue onto the fetched tip,
// after the person confirms at the terminal.
func runRecover(env Env, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: memcore recover")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	unlock, err := store.Lock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	m, ok, err := readMarker(repo)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(env.Out, "Nothing to recover: the remote holds the accepted history.")
		return nil
	}
	if rebasing(repo) {
		return errors.New("a rebase is in progress in the memory repo: the person finishes it or aborts it first")
	}
	lost, err := store.Git(repo, "log", "--oneline", m.fetched+".."+m.accepted)
	if err != nil {
		return fmt.Errorf("list the lost commits: %w", err)
	}
	queue, err := store.Git(repo, "log", "--oneline", m.accepted+"..main")
	if err != nil {
		return fmt.Errorf("list the review queue: %w", err)
	}
	fmt.Fprintf(env.Out, "The remote no longer holds these accepted commits, and recover drops them here:\n%s", indent(lost))
	fmt.Fprintf(env.Out, "The queue keeps these commits, on top of the remote:\n%s", indent(queue))
	confirm := env.Confirm
	if confirm == nil {
		confirm = guards.Confirm
	}
	yes, err := confirm("Drop the lost commits and move the queue? [y/N] ")
	if err != nil {
		return err
	}
	if !yes {
		fmt.Fprintln(env.Out, "Recovered nothing.")
		return nil
	}
	dropped, err := store.Git(repo, "rev-list", m.fetched+".."+m.accepted)
	if err != nil {
		return fmt.Errorf("list the lost commits: %w", err)
	}
	if err := rebase(repo, "-q", "--onto", m.fetched, m.accepted, "main"); err != nil {
		var ce *conflictError
		if errors.As(err, &ce) {
			return fmt.Errorf("the queue and the remote change the same documents, so recover left the queue as it was: %s", strings.Join(ce.paths, ", "))
		}
		return err
	}
	if _, err := store.Git(repo, "update-ref", acceptedRef, m.fetched); err != nil {
		return fmt.Errorf("move the accepted history: %w", err)
	}
	if err := os.Remove(markerPath(repo)); err != nil {
		return fmt.Errorf("remove the marker of a rewritten remote: %w", err)
	}
	env.event().Dropped = strings.Fields(dropped)
	fmt.Fprintln(env.Out, "Recovered: the queue sits on top of the remote.")
	return nil
}
