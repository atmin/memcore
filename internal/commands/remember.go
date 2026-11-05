package commands

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// budget is the number of lines that orient prints for a level before remember warns.
const budget = 40

// runRemember places, checks, indexes and commits a memory that it reads from standard input.
func runRemember(env Env, args []string) error {
	flags := flag.NewFlagSet("remember", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	isNew := flags.Bool("new", false, "write the memory even when near-duplicates exist")
	at := flags.String("at", "", "place the memory at the level of this directory, on the path to home")
	if err := flags.Parse(args); err != nil {
		return usageErr(err.Error())
	}
	if flags.NArg() != 0 {
		return usageErr("usage: memcore remember [--new] [--at <dir>] < memory.md")
	}
	repo, err := memoryRepo()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return fmt.Errorf("the memory repo %s does not exist: the person runs memcore init <remote>", repo)
	}
	m, err := store.ParseMemory(env.In)
	if err != nil {
		return err
	}
	env.event().input(m.Name, m.Description, m.Type)
	m.Sessions = nil
	if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		m.Sessions = []string{"claude:" + id}
	}
	h, err := home()
	if err != nil {
		return err
	}
	level, err := placeAt(env.Dir, *at, h)
	if err != nil {
		return err
	}
	working, err := store.Level(env.Dir, h)
	if err != nil {
		return fmt.Errorf("place the memory: %w", err)
	}
	rel := path.Join(store.Folder(level), "memories", m.Name+".md")

	unlock, err := lockWrite(repo)
	if err != nil {
		return err
	}
	defer unlock()

	taken, err := findMemory(repo, m.Name)
	if err != nil {
		return err
	}
	if taken != "" {
		return inputErrf("the name %s is taken by %s: choose another name", m.Name, taken)
	}
	if !*isNew {
		// The working directory sees every level on its path, so near-duplicates come from there,
		// even when --at places the memory higher.
		dups, err := nearDuplicates(repo, working, m.Description)
		if err != nil {
			return err
		}
		if len(dups) > 0 {
			fmt.Fprintln(env.Out, "These memories on the path share most words of the description:")
			for _, d := range dups {
				fmt.Fprintf(env.Out, "- %s: %s\n", d.Path, d.Text)
			}
			return usageErr("near-duplicates exist, so remember wrote nothing: if the fact is new, run memcore remember --new")
		}
	}

	content := m.Bytes()
	if found := guards.Secrets(string(content)); len(found) > 0 {
		for _, f := range found {
			fmt.Fprintf(env.Out, "%s:%d: %s\n", rel, f.Line, f.Message)
		}
		return usageErr("the memory holds a likely secret, so remember wrote nothing: remove the secret, then remember again")
	}
	report, err := guards.VerifyContent(repo, rel, content)
	if err != nil {
		return err
	}
	if len(report.Faults) > 0 {
		for _, f := range report.Faults {
			fmt.Fprintf(env.Out, "%s:%d: fault: %s\n", f.Path, f.Line, f.Message)
		}
		return inputErrf("%d faults, so remember wrote nothing", len(report.Faults))
	}
	made, err := apply(repo, change{writes: []file{{path: rel, content: content, mode: 0o644}}, message: "remember: " + rel})
	if err != nil {
		return err
	}
	env.event().Paths, env.event().Made = []string{rel}, []string{made}

	for _, w := range m.Warnings() {
		fmt.Fprintf(env.Out, "warning: %s\n", w)
	}
	for _, f := range report.Warnings {
		if !slices.Contains(m.Warnings(), f.Message) {
			fmt.Fprintf(env.Out, "%s:%d: warning: %s\n", f.Path, f.Line, f.Message)
		}
	}
	ix, err := store.Lines(repo, level)
	if err != nil {
		return err
	}
	if w := budgetWarning(level, budgetLines(ix)); w != "" {
		fmt.Fprintln(env.Out, w)
	}
	fmt.Fprintf(env.Out, "Remembered %s.\n", rel)
	return nil
}

// budgetLines returns the lines of a level that orient prints and the budget counts:
// one for each memory, skill, script, conventions file and glossary.
func budgetLines(ix store.Index) int {
	n := 0
	for _, kind := range []string{store.KindMemories, store.KindSkills, store.KindScripts, store.KindConventions, store.KindGlossary} {
		n += len(ix.Of(kind))
	}
	return n
}

// budgetWarning says that a level has more lines in orient than the budget, or "" when it has not.
func budgetWarning(level string, n int) string {
	if n <= budget {
		return ""
	}
	title := "~"
	if level != "" {
		title += "/" + level
	}
	return fmt.Sprintf("warning: %s has %d lines in orient, over the budget of %d: a sleep pass consolidates it", title, n, budget)
}

// findMemory returns the path of the memory with a name anywhere in the repo, or "".
func findMemory(repo, name string) (string, error) {
	found := ""
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == name+".md" && filepath.Base(filepath.Dir(p)) == "memories" {
			rel, err := filepath.Rel(repo, p)
			if err != nil {
				return err
			}
			if !store.IsMemory(filepath.ToSlash(rel)) {
				return nil // a file in a skill, such as an example of a memory
			}
			found = filepath.ToSlash(rel)
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("look up the name %s: %w", name, err)
	}
	return found, nil
}

// nearDuplicates returns the memories on the path whose description holds more than half of the words of description.
func nearDuplicates(repo, level, description string) ([]store.Line, error) {
	want := words(description)
	if len(want) == 0 {
		return nil, nil
	}
	var dups []store.Line
	for _, l := range store.Path(level) {
		ix, err := store.Lines(repo, l)
		if err != nil {
			return nil, err
		}
		for _, line := range ix.Of(store.KindMemories) {
			have := words(line.Text)
			shared := 0
			for w := range want {
				if have[w] {
					shared++
				}
			}
			if 2*shared > len(want) {
				dups = append(dups, line)
			}
		}
	}
	return dups, nil
}

// common holds the words that start most descriptions, so they say nothing about the fact.
var common = map[string]bool{"read": true, "when": true, "before": true, "you": true}

// words returns the case-folded words of three letters or longer, without the common words.
func words(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 3 && !common[w] {
			set[w] = true
		}
	}
	return set
}
