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
	if stale, err := staleIndexes(repo); err != nil {
		fmt.Fprintf(env.Err, "warning: %v\n", err)
	} else if len(stale) > 0 {
		fmt.Fprintf(env.Out, "These indexes differ from their documents, and memcore push regenerates them:\n%s", indent(strings.Join(stale, "\n")))
	}
	_, queue, err := queueOf(repo)
	if err != nil {
		return err
	}
	if len(queue) == 0 {
		fmt.Fprintln(env.Out, "Nothing waits for review.")
		return nil
	}
	fmt.Fprintf(env.Out, "The review queue holds %s, newest first:\n", commits(len(queue), "unpushed"))
	for _, c := range queue {
		sha, _, _ := strings.Cut(c, " ")
		detail, err := store.Git(repo, "show", "-s", "--format=%b", sha)
		if err != nil {
			return fmt.Errorf("read the commit %s: %w", sha, err)
		}
		files, err := store.Git(repo, "diff-tree", "--no-commit-id", "--name-status", "-r", "--root", sha)
		if err != nil {
			return fmt.Errorf("list the files of %s: %w", sha, err)
		}
		fmt.Fprintf(env.Out, "\n  %s\n", c)
		for _, line := range strings.Split(strings.TrimSpace(detail), "\n") {
			if line != "" {
				fmt.Fprintf(env.Out, "    %s\n", line)
			}
		}
		for _, line := range strings.Split(strings.TrimSpace(files), "\n") {
			if status, p, ok := strings.Cut(line, "\t"); ok {
				fmt.Fprintf(env.Out, "    %s  %s\n", status, p)
			}
		}
	}
	fmt.Fprintln(env.Out, "\nmemcore drop <commit> removes a commit and its fixups. memcore push folds each fixup into its commit.")
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
