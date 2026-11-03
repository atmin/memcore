package commands

import (
	"errors"
	"fmt"

	"github.com/atmin/memcore/internal/store"
)

// runInbox fetches, rebases the review queue, and lists the unpushed commits.
// A fetch that fails only warns, so the person sees the queue offline too.
func runInbox(env Env, args []string) error {
	if len(args) != 0 {
		return errors.New("usage: memcore inbox")
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
	if err := Fetch(repo); err != nil {
		fmt.Fprintf(env.Err, "warning: %v\nThe queue below may sit behind the accepted history.\n", err)
	}
	_, queue, err := queueOf(repo)
	if err != nil {
		return err
	}
	if len(queue) == 0 {
		fmt.Fprintln(env.Out, "Nothing waits for review.")
		return nil
	}
	fmt.Fprintf(env.Out, "The review queue holds %s:\n", commits(len(queue), "unpushed"))
	for _, c := range queue {
		fmt.Fprintf(env.Out, "  %s\n", c)
	}
	fmt.Fprintf(env.Out, "Review them with git in %s, then accept them with memcore push.\n", tilde(repo))
	return nil
}
