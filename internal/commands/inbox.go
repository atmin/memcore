package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// runInbox does these steps:
//   - It fetches, and rebases the review queue.
//   - It prints the summary of the week.
//   - It lists the unpushed commits as the fold at push leaves them.
//
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

// report prints what waits on the person, without a fetch. First come a rewritten remote, the stale indexes and
// the files that keep auto-memory on. Then comes the queue as the fold at push leaves it. The queue includes the
// index commits that push adds, and the header counts them apart. When the preview of the fold fails, report
// warns and lists each commit of the queue as it is. The caller holds the write lock.
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
	// regenerate can add an index commit to an empty queue, so every queue gets a preview.
	entries, err := preview(repo, false)
	if err != nil {
		fmt.Fprintf(env.Err, "warning: %v\nThe queue below lists each commit as it is, without the fold.\n", err)
		return listQueue(env, repo, queue)
	}
	if len(queue) == 0 && len(entries) == 0 {
		fmt.Fprintln(env.Out, "Nothing waits for review.")
		return nil
	}
	shas, err := revList(repo, "refs/heads/main")
	if err != nil {
		return err
	}
	held := map[string]bool{}
	same := len(entries) == len(queue) // the fold leaves each commit as it is
	added := 0                         // the index commits that push adds, which are no commits of the queue
	for _, p := range entries {
		held[p.target] = true
		for _, f := range p.fixups {
			held[f] = true
		}
		if p.target == "" {
			added++
		}
		if p.target == "" || len(p.fixups) > 0 {
			same = false
		}
	}
	kept := len(entries) - added
	left := "none"
	if kept > 0 {
		left = fmt.Sprint(kept)
	}
	switch {
	case same:
		return listQueue(env, repo, queue) // each commit keeps its sha, so the list is the queue as git shows it
	case len(queue) == 0: // every entry is an index commit
		fmt.Fprintf(env.Out, "The review queue holds no unpushed commits. Push adds %s, newest first:\n", plural(added, "index commit"))
	case added == 0 && kept == 0:
		fmt.Fprintf(env.Out, "The review queue holds %s. The fold at push leaves none.\n", commits(len(queue), "unpushed"))
	case added == 0:
		fmt.Fprintf(env.Out, "The review queue holds %s. The fold at push leaves %d, newest first:\n", commits(len(queue), "unpushed"), kept)
	default:
		fmt.Fprintf(env.Out, "The review queue holds %s. The fold at push leaves %s, and push adds %s, newest first:\n",
			commits(len(queue), "unpushed"), left, plural(added, "index commit"))
	}
	for i := len(entries) - 1; i >= 0; i-- {
		p := entries[i]
		name := "new" // an index that push commits
		if p.target != "" {
			name = abbrev(repo, p.target)
		}
		fmt.Fprintf(env.Out, "\n  %s %s\n", name, p.subject)
		writeBody(env, p.body)
		switch len(p.fixups) {
		case 0:
		case 1:
			fmt.Fprintln(env.Out, "    1 fixup folds in.")
		default:
			fmt.Fprintf(env.Out, "    %d fixups fold in.\n", len(p.fixups))
		}
		writeFiles(env, p.files)
	}
	dropped := 0
	for _, sha := range shas {
		if !held[sha] {
			dropped++
		}
	}
	switch dropped {
	case 0:
	case 1:
		fmt.Fprintln(env.Out, "\nThe fold drops 1 commit that leaves every document as it was.")
	default:
		fmt.Fprintf(env.Out, "\nThe fold drops %d commits that leave every document as it was.\n", dropped)
	}
	writeClosing(env, repo)
	return nil
}

// abbrev returns the short name of a commit that git log --oneline prints, or its first seven characters.
func abbrev(repo, sha string) string {
	if out, err := store.Git(repo, "rev-parse", "--short", sha); err == nil {
		return strings.TrimSpace(out)
	}
	return short(sha)
}

// listQueue lists each commit of the queue as it is, newest first, with its body and its files.
func listQueue(env Env, repo string, queue []string) error {
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
		files, err := filesOf(repo, sha)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Out, "\n  %s\n", c)
		writeBody(env, detail)
		writeFiles(env, files)
	}
	writeClosing(env, repo)
	return nil
}

// writeBody writes the lines of a commit body that are not blank, indented under the subject.
func writeBody(env Env, body string) {
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if line != "" {
			fmt.Fprintf(env.Out, "    %s\n", line)
		}
	}
}

// writeFiles writes the files of a commit, each with its status letter, indented under the subject.
func writeFiles(env Env, files []fileStatus) {
	for _, f := range files {
		fmt.Fprintf(env.Out, "    %s  %s\n", f.status, f.path)
	}
}

// writeClosing writes how the person drops, reviews and accepts the queue.
func writeClosing(env Env, repo string) {
	fmt.Fprintln(env.Out, "\nmemcore drop <commit> removes a commit and its fixups. memcore push folds each fixup into its commit.")
	fmt.Fprintf(env.Out, "Review them with git in %s, then accept them with memcore push.\n", tilde(repo))
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
