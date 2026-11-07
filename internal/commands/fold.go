package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// commit is one commit of the review queue.
type commit struct{ sha, subject string }

// chain is a commit with the fixups that fold into it.
type chain struct {
	target commit
	fixups []commit
}

// fold folds the fixups of the review queue into their targets, with one interactive rebase whose todo
// memcore writes itself. A fixup folds into the nearest earlier commit with its subject, as autosquash does.
// A chain may leave every document as it found it, such as a remember and its forget. The fold drops such a
// chain whole, because git stops on a fixup that empties its target. It returns the commits that the fold
// removed.
// The caller holds the write lock, and the working tree is clean.
func fold(repo string) ([]string, error) {
	chains, base, err := chainsOf(repo)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(chains, func(ch *chain) bool { return len(ch.fixups) > 0 }) {
		return nil, nil
	}

	var todo strings.Builder
	var folded []string
	for _, ch := range chains {
		cancelled, err := cancels(repo, ch)
		if err != nil {
			return nil, err
		}
		verb := "fixup"
		if cancelled {
			verb = "drop"
			fmt.Fprintf(&todo, "drop %s\n", ch.target.sha)
			folded = append(folded, ch.target.sha)
		} else {
			fmt.Fprintf(&todo, "pick %s\n", ch.target.sha)
		}
		for _, f := range ch.fixups {
			fmt.Fprintf(&todo, "%s %s\n", verb, f.sha)
			folded = append(folded, f.sha)
		}
		if !cancelled {
			todo.WriteString("exec false\n") // a stop where rebase regenerates the indexes of this commit
		}
	}
	path := filepath.Join(store.StateDir(repo), "todo")
	if err := os.WriteFile(path, []byte(todo.String()), 0o644); err != nil {
		return nil, fmt.Errorf("write the todo of the fold: %w", err)
	}
	onto := "--root"
	if base != "" {
		onto = base
	}
	editor := "cp '" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	if err := rebaseEditor(repo, editor, "-q", "-i", onto); err != nil {
		return nil, fmt.Errorf("fold the fixups: %w", err)
	}
	return folded, nil
}

// chainsOf returns the review queue as chains, oldest first, and the accepted commit, or "" without one.
// A fixup joins the nearest earlier commit with its subject, as autosquash does.
func chainsOf(repo string) ([]*chain, string, error) {
	base, _ := revParse(repo, acceptedRef)
	rng := "HEAD"
	if base != "" {
		rng = base + "..HEAD"
	}
	out, err := store.Git(repo, "log", "--reverse", "--format=%H%x00%s", rng)
	if err != nil {
		return nil, "", fmt.Errorf("list the review queue: %w", err)
	}
	var chains []*chain
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		sha, subject, ok := strings.Cut(line, "\x00")
		if !ok {
			continue
		}
		c := commit{sha: sha, subject: subject}
		if want, isFixup := fixupTarget(subject); isFixup {
			if t := nearest(chains, want); t != nil {
				t.fixups = append(t.fixups, c)
				continue
			}
		}
		chains = append(chains, &chain{target: c})
	}
	return chains, base, nil
}

// fixupTarget returns the subject that a fixup names, without its fixup! prefixes, and whether it is a fixup.
func fixupTarget(subject string) (string, bool) {
	isFixup := false
	for strings.HasPrefix(subject, "fixup! ") {
		subject = strings.TrimPrefix(subject, "fixup! ")
		isFixup = true
	}
	return subject, isFixup
}

// nearest returns the latest chain whose target has the subject, or nil.
func nearest(chains []*chain, subject string) *chain {
	for i := len(chains) - 1; i >= 0; i-- {
		if chains[i].target.subject == subject {
			return chains[i]
		}
	}
	return nil
}

// cancels reports whether a chain with fixups leaves every document, indexes aside, as it was before its target.
func cancels(repo string, ch *chain) (bool, error) {
	if len(ch.fixups) == 0 {
		return false, nil
	}
	parent, ok := revParse(repo, ch.target.sha+"^")
	if !ok {
		return false, nil // the root commit stays
	}
	last := ch.fixups[len(ch.fixups)-1].sha
	docs := map[string]bool{}
	for _, c := range append([]commit{ch.target}, ch.fixups...) {
		out, err := store.Git(repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", c.sha)
		if err != nil {
			return false, fmt.Errorf("list the files of %s: %w", short(c.sha), err)
		}
		for _, p := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
			if _, index := indexLevel(p); p != "" && !index {
				docs[p] = true
			}
		}
	}
	for p := range docs {
		before := objectAt(repo, parent, p)
		after := objectAt(repo, last, p)
		if before != after {
			return false, nil
		}
	}
	return true, nil
}

// objectAt returns the tree entry of a path at a commit, with its mode and its object. It returns "" when
// the commit has no such path. A change of mode alone counts as a change.
func objectAt(repo, rev, p string) string {
	out, err := store.Git(repo, "--literal-pathspecs", "ls-tree", "--full-tree", rev, "--", p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
