package commands

import (
	"errors"
	"fmt"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// runPush fetches, rebases the review queue, asks the person at the terminal, and pushes the queue.
// It pushes the tip that it counted, so a commit that lands while it asks waits for the next push.
func runPush(env Env, args []string) error {
	if len(args) != 0 {
		return errors.New("usage: memcore push")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	tip, queue, err := fetchLocked(repo)
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
	if _, err := store.Git(repo, "push", "-q", "origin", tip+":refs/heads/main"); err != nil {
		return fmt.Errorf("push to origin: %w", err)
	}
	if _, err := store.Git(repo, "fetch", "-q", "origin"); err != nil {
		return fmt.Errorf("fetch after the push: %w", err)
	}
	fmt.Fprintf(env.Out, "Pushed %s.\n", commits(len(queue)))
	return nil
}
