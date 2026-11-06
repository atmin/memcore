package commands

import (
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
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
	o, err := orientation(repo, env.Dir)
	if err != nil {
		return err
	}
	text, words, lines := o.full()
	env.event().Words, env.event().Lines = words, lines
	_, err = io.WriteString(env.Out, text)
	return err
}

// orientText is the print of orient in parts: the header, then one part for each level,
// from the working directory to the root. A level with no line and no count line has no part.
type orientText struct {
	header string
	levels []levelPart
}

// levelPart is the part of one level in the print of orient.
type levelPart struct {
	title     string   // ~ or ~/<level>
	lines     []string // one line for each memory, skill, script, conventions file and glossary: - <path>: <text>, with any label
	count     string   // the line with the counts of plans and notes, or ""
	short     []string // the lines without the text: - <path>, with any label
	documents int      // one for each line, plan and note
}

// orientation builds the print of orient for a directory, in parts.
func orientation(repo, dir string) (orientText, error) {
	h, err := home()
	if err != nil {
		return orientText{}, err
	}
	levels := []string{""}
	level, err := store.Level(dir, h)
	switch {
	case err == nil:
		levels = store.Path(level)
	case !errors.Is(err, store.ErrOutsideHome):
		return orientText{}, err
	}
	changed, all, err := unreviewed(repo)
	if err != nil {
		return orientText{}, err
	}

	var w strings.Builder
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
	fmt.Fprintln(&w, "Run push only when the person asks. An agent's push sends only memories, notes, plans and indexes.")
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
	o := orientText{header: w.String()}
	for _, l := range levels {
		ix, err := store.Lines(repo, l)
		if err != nil {
			return orientText{}, err
		}
		part := levelPart{title: title(l)}
		for _, kind := range []string{store.KindMemories, store.KindSkills, store.KindScripts, store.KindConventions, store.KindGlossary} {
			for _, line := range ix.Of(kind) {
				label := ""
				if all || isChanged(changed, line) {
					label = " [unreviewed]"
				}
				part.lines = append(part.lines, fmt.Sprintf("- %s: %s%s", line.Path, line.Text, label))
				part.short = append(part.short, fmt.Sprintf("- %s%s", line.Path, label))
			}
		}
		plans, notes := len(ix.Of(store.KindPlans)), len(ix.Of(store.KindNotes))
		if counts := countLine(plans, notes); counts != "" {
			part.count = fmt.Sprintf("%s: memcore show %s lists them.", counts, path.Join(store.Folder(l), "README.md"))
		}
		part.documents = len(part.lines) + plans + notes
		if len(part.lines) == 0 && part.count == "" {
			continue
		}
		o.levels = append(o.levels, part)
	}
	return o, nil
}

// full returns the whole print of orient. It also returns the words of the header and of each level,
// and the lines of each level that the budget counts.
func (o orientText) full() (text string, words, lines map[string]int) {
	var w strings.Builder
	w.WriteString(o.header)
	words, lines = map[string]int{"header": len(strings.Fields(o.header))}, map[string]int{}
	for _, l := range o.levels {
		block := l.block(l.lines)
		w.WriteString(block)
		words[l.title] = len(strings.Fields(block))
		lines[l.title] = len(l.lines)
	}
	return w.String(), words, lines
}

// block returns the text of a level: a blank line, its title, the given lines, then its count line.
func (l levelPart) block(lines []string) string {
	all := slices.Clone(lines)
	if l.count != "" {
		all = append(all, l.count)
	}
	return fmt.Sprintf("\n%s\n%s\n", l.title, strings.Join(all, "\n"))
}

// hookBytes is the most that the SessionStart hook prints. Claude Code caps the output of a hook at 10,000 characters.
// A UTF-8 byte count never undercounts the characters, and the margin of 500 covers any framing.
const hookBytes = 9500

// hookFallback is the print of the SessionStart hook when even the notice and the header pass the limit.
// It is under 1,000 bytes, so it always fits.
const hookFallback = "Run memcore orient before you act. Its print passes the output limit of Claude Code hooks.\n"

// The forms of a level in a short print.
const (
	formFull     = iota // the title, the lines and the count line
	formShort           // the title, the lines without the text, and the count line
	formReplaced        // one line with the count of the documents
)

// fit returns the print of orient in at most limit bytes. It also returns the words of the header and of each level,
// and the titles of the shortened levels. The print is the first of these forms that fits:
//  1. the full print;
//  2. the notice, the header and the levels, with levels made short one at a time, the largest full block first;
//  3. the same, with short levels replaced one at a time, the largest short block first;
//  4. the fixed text hookFallback.
func (o orientText) fit(limit int) (text string, words map[string]int, short []string) {
	text, words, _ = o.full()
	if len(text) <= limit {
		return text, words, nil
	}
	forms := make([]int, len(o.levels))
	for _, i := range o.bySize(func(l levelPart) string { return l.block(l.lines) }) {
		l := o.levels[i]
		if l.block(l.short) == l.block(l.lines) {
			continue // a level with only a count line has no description to leave out
		}
		forms[i] = formShort
		if text, words, short = o.shortened(forms); len(text) <= limit {
			return text, words, short
		}
	}
	for _, i := range o.bySize(func(l levelPart) string { return l.block(l.short) }) {
		forms[i] = formReplaced
		if text, words, short = o.shortened(forms); len(text) <= limit {
			return text, words, short
		}
	}
	short = nil
	for _, l := range o.levels {
		short = append(short, l.title)
	}
	return hookFallback, map[string]int{"header": len(strings.Fields(hookFallback))}, short
}

// bySize returns the indexes of the levels, the largest block first. Levels with blocks of one size keep their order.
func (o orientText) bySize(block func(levelPart) string) []int {
	order := make([]int, len(o.levels))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return len(block(o.levels[b])) - len(block(o.levels[a]))
	})
	return order
}

// shortened returns a short print, with each level in its form. The first line is the notice,
// which names the shortened levels and tells the agent to run orient. The notice counts as words of the header.
func (o orientText) shortened(forms []int) (text string, words map[string]int, short []string) {
	var blocks strings.Builder
	var described, replaced []string
	words = map[string]int{}
	for i, l := range o.levels {
		var block string
		switch forms[i] {
		case formFull:
			block = l.block(l.lines)
		case formShort:
			block = l.block(l.short)
			described = append(described, l.title)
		default:
			noun := "documents"
			if l.documents == 1 {
				noun = "document"
			}
			block = fmt.Sprintf("\n%s: %d %s, which memcore orient lists.\n", l.title, l.documents, noun)
			replaced = append(replaced, l.title)
		}
		if forms[i] != formFull {
			short = append(short, l.title)
		}
		words[l.title] = len(strings.Fields(block))
		blocks.WriteString(block)
	}
	var left []string
	if len(described) > 0 {
		left = append(left, "the descriptions of "+series(described))
	}
	if len(replaced) > 0 {
		left = append(left, "the lines of "+series(replaced))
	}
	head := fmt.Sprintf("Run memcore orient before you act. This print leaves out %s, to fit the output limit of Claude Code hooks.\n\n%s",
		strings.Join(left, ", and "), o.header)
	words["header"] = len(strings.Fields(head))
	return head + blocks.String(), words, short
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
