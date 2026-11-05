package commands

import (
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// runOrient prints the commands, the pinned parts of the voice skill, and the indexes from the working directory to the root.
func runOrient(env Env, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: memcore orient")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	h, err := home()
	if err != nil {
		return err
	}
	levels := []string{""}
	level, err := store.Level(env.Dir, h)
	switch {
	case err == nil:
		levels = store.Path(level)
	case !errors.Is(err, store.ErrOutsideHome):
		return err
	}
	changed, all, err := unreviewed(repo)
	if err != nil {
		return err
	}

	var w strings.Builder
	words, counts := map[string]int{}, map[string]int{}
	fmt.Fprintf(&w, "memcore keeps the long-term memory of your agents, in %s. Touch it only through these commands:\n", tilde(repo))
	fmt.Fprintln(&w)
	var yours []string
	for _, c := range Table() {
		if c.Hidden {
			continue
		}
		fmt.Fprintf(&w, "  memcore %-20s %s\n", strings.TrimSpace(c.Name+" "+c.Args), c.Does)
		if c.Yours {
			yours = append(yours, c.Name)
		}
	}
	fmt.Fprintf(&w, "\nOnly the person runs %s.\n", series(yours))
	fmt.Fprintln(&w, "Before you first use a command, run memcore help <command>: it shows the input, with an example.")
	fmt.Fprintln(&w)

	if voice, ok := accepted(repo, voicePath); ok {
		fmt.Fprintln(&w, "Follow the voice skill in every reply and every document:")
		for _, s := range []string{"Always", "Talking"} {
			if text := section(voice, s); text != "" {
				fmt.Fprintf(&w, "\n%s\n", text)
			}
		}
		fmt.Fprintf(&w, "\nBefore you write a document, read the Writing part: memcore show %s\n", voicePath)
	} else {
		fmt.Fprintf(&w, "No accepted voice skill exists yet: the person has not pushed %s.\n", voicePath)
	}

	fmt.Fprintln(&w, "\nThe indexes, from the working directory to the root. Each line says when to read a document, and memcore show <path> prints it.")
	words["header"] = len(strings.Fields(w.String()))
	for _, l := range levels {
		ix, err := store.Lines(repo, l)
		if err != nil {
			return err
		}
		var lines []string
		for _, kind := range []string{store.KindMemories, store.KindSkills, store.KindScripts, store.KindConventions, store.KindGlossary} {
			for _, line := range ix.Of(kind) {
				label := ""
				if all || isChanged(changed, line) {
					label = " [unreviewed]"
				}
				lines = append(lines, fmt.Sprintf("- %s: %s%s", line.Path, line.Text, label))
			}
		}
		if counts := countLine(len(ix.Of(store.KindPlans)), len(ix.Of(store.KindNotes))); counts != "" {
			lines = append(lines, fmt.Sprintf("%s: memcore show %s lists them.", counts, path.Join(store.Folder(l), "README.md")))
		}
		if len(lines) == 0 {
			continue
		}
		block := fmt.Sprintf("\n%s\n%s\n", title(l), strings.Join(lines, "\n"))
		words[title(l)] = len(strings.Fields(block))
		counts[title(l)] = budgetLines(ix)
		w.WriteString(block)
	}
	env.event().Words, env.event().Lines = words, counts
	_, err = io.WriteString(env.Out, w.String())
	return err
}

// unreviewed returns the paths that a commit after the accepted history changed.
// all is true when there is no accepted history, so every document is unreviewed.
func unreviewed(repo string) (changed map[string]bool, all bool, err error) {
	if !hasAccepted(repo) {
		return nil, true, nil
	}
	out, err := store.Git(repo, "log", "-z", "--format=", "--name-only", acceptedRef+"..HEAD")
	if err != nil {
		return nil, false, fmt.Errorf("list the review queue: %w", err)
	}
	changed = map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p = strings.Trim(p, "\n"); p != "" {
			changed[p] = true
		}
	}
	return changed, false, nil
}

// isChanged reports whether the review queue changed the document of a line, or any file of a skill.
func isChanged(changed map[string]bool, line store.Line) bool {
	if changed[line.Path] {
		return true
	}
	if line.Kind == store.KindSkills {
		dir := path.Dir(line.Path) + "/"
		for p := range changed {
			if strings.HasPrefix(p, dir) {
				return true
			}
		}
	}
	return false
}

// section returns a "## " section of a markdown document, with its heading, or "".
func section(doc, heading string) string {
	start := strings.Index(doc, "\n## "+heading+"\n")
	if start < 0 {
		return ""
	}
	rest := doc[start+1:]
	if end := strings.Index(rest[3:], "\n## "); end >= 0 {
		rest = rest[:end+3]
	}
	return strings.TrimSpace(rest)
}

// countLine says how many plans and notes a level holds, or "" when it holds none.
func countLine(plans, notes int) string {
	var parts []string
	for _, c := range []struct {
		n    int
		noun string
	}{{plans, "plan"}, {notes, "note"}} {
		switch {
		case c.n == 1:
			parts = append(parts, "1 "+c.noun)
		case c.n > 1:
			parts = append(parts, fmt.Sprintf("%d %ss", c.n, c.noun))
		}
	}
	return strings.Join(parts, " and ")
}

// series joins words as a list in a sentence: "a", "a and b", "a, b and c".
func series(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}
