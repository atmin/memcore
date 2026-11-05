package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// runInbox fetches, rebases the review queue, prints the summary of the week, and lists the unpushed commits.
// A fetch or a summary that fails only warns, so the person sees the queue offline too.
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
	if text, err := summary(repo, clock().UTC()); err != nil {
		fmt.Fprintf(env.Err, "warning: %v\n", err)
	} else {
		fmt.Fprintf(env.Out, "%s\n", text)
	}
	return report(env, repo)
}

// report prints what waits on the person, without a fetch: a rewritten remote, stale indexes, the files that keep
// auto-memory on, and the queue. The caller holds the write lock.
func report(env Env, repo string) error {
	if m, ok, err := readMarker(repo); err == nil && ok {
		lost, _ := store.Git(repo, "log", "--oneline", m.fetched+".."+m.accepted)
		fmt.Fprintf(env.Out, "The remote no longer holds these accepted commits. memcore recover drops them here and keeps the queue:\n%s", indent(lost))
	}
	if stale, err := staleIndexes(repo); err != nil {
		fmt.Fprintf(env.Err, "warning: %v\n", err)
	} else if len(stale) > 0 {
		fmt.Fprintf(env.Out, "These indexes differ from their documents, and memcore push regenerates them:\n%s", indent(strings.Join(stale, "\n")))
	}
	if err := reportBlockers(env, repo); err != nil {
		fmt.Fprintf(env.Err, "warning: %v\n", err)
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

// reportBlockers lists the files that keep auto-memory on, once an import has run.
func reportBlockers(env Env, repo string) error {
	if _, err := os.Stat(filepath.Join(store.StateDir(repo), "import.json")); err != nil {
		return nil
	}
	claude, err := claudeDir()
	if err != nil {
		return err
	}
	rec, err := readImportRecord(repo)
	if err != nil {
		return err
	}
	blockers, err := importBlockers(rec, claude)
	if err != nil || len(blockers) == 0 {
		return err
	}
	// After the switch, auto-memory is off, and a changed file only waits for the next init.
	if tree, err := readSettings(filepath.Join(claude, "settings.json")); err == nil && tree.get("autoMemoryEnabled") != nil && tree.get("autoMemoryEnabled").kind == 'f' {
		fmt.Fprintln(env.Out, "These files of auto-memory are not in the memory repo as they are now:")
	} else {
		fmt.Fprintln(env.Out, "Auto-memory stays on, because these files are not in the memory repo:")
	}
	for _, b := range blockers {
		fmt.Fprintf(env.Out, "  %s: %s\n", tilde(b.file), b.reason)
	}
	fmt.Fprintln(env.Out, "Fix a file and run memcore init again, or let init leave the files out.")
	return nil
}
