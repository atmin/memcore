package commands

import (
	"errors"
	"fmt"
	"path"
	"slices"
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
	if err := foreignNames(repo); err != nil { // a file may have taken the name while the person answered
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
	if err := syncAccepted(repo); err != nil {
		return err
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
	if err := foreignNames(repo); err != nil {
		return "", "", nil, made, folded, err
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

// personReason returns "" when an agent may push a commit, and otherwise the reason that only the person may:
// "a merge commit", or the paths that the commit changes and that are not memories, notes, plans or indexes, sorted.
// Only the person accepts skills and scripts, because they run from the accepted history.
func personReason(repo, sha string) (string, error) {
	// rev-list prints the commit, then its parents, and no setting of git log adds a line to it.
	out, err := store.Git(repo, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return "", fmt.Errorf("read the parents of %s: %w", short(sha), err)
	}
	if len(strings.Fields(out)) > 2 {
		return "a merge commit", nil
	}
	// -z keeps each path exact, and --ignore-submodules=none lets no setting of a submodule hide its change.
	out, err = store.Git(repo, "diff-tree", "-r", "-z", "--root", "--no-commit-id", "--raw", "--no-renames", "--ignore-submodules=none", sha)
	if err != nil {
		return "", fmt.Errorf("list the files of %s: %w", short(sha), err)
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var paths []string
	for i := 0; i+1 < len(fields); i += 2 {
		// A record is ":<old mode> <new mode> <old blob> <new blob> <status>", then the path.
		meta := strings.Fields(fields[i])
		if len(meta) != 5 {
			return "", fmt.Errorf("read the files of %s: git printed %q", short(sha), fields[i])
		}
		if !agentFile(fields[i+1], meta[1]) {
			paths = append(paths, fields[i+1])
		}
	}
	slices.Sort(paths)
	return strings.Join(paths, ", "), nil
}

// agentFile reports whether an agent may push a change that leaves a path with mode, 000000 for a deleted path.
// It may push a memory, a note, a plan or an index outside skills and bin, when the change deletes it or leaves
// it a regular file.
func agentFile(p, mode string) bool {
	if mode != "000000" && mode != "100644" {
		return false
	}
	parts := strings.Split(p, "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.EqualFold(part, "skills") || strings.EqualFold(part, "bin") {
			return false
		}
	}
	switch docKind(p) {
	case "memories", "notes", "plans":
		return true
	}
	return path.Base(p) == "README.md"
}
