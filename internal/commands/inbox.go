package commands

import (
	"fmt"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// runInbox fetches, rebases the review queue, and lists the unpushed commits.
// A fetch that fails only warns, so the person sees the queue offline too.
func runInbox(env Env, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: memcore inbox")
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
	if m, ok, err := readMarker(repo); err == nil && ok {
		lost, _ := store.Git(repo, "log", "--oneline", m.fetched+".."+m.accepted)
		fmt.Fprintf(env.Out, "The remote no longer holds these accepted commits. memcore recover drops them here and keeps the queue:\n%s", indent(lost))
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

// indent puts two spaces in front of each line of git output.
func indent(out string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}
