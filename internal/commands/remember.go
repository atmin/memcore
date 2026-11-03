package commands

import (
	"errors"
	"flag"
	"fmt"
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
	flags.SetOutput(env.Err)
	isNew := flags.Bool("new", false, "write the memory even when near-duplicates exist")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: memcore remember [--new] < memory.md")
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
	level, err := store.Level(env.Dir, h)
	if err != nil {
		return fmt.Errorf("place the memory: %w", err)
	}
	rel := path.Join(store.Folder(level), "memories", m.Name+".md")

	unlock, err := store.Lock(repo)
	if err != nil {
		return err
	}
	defer unlock()

	taken, err := findMemory(repo, m.Name)
	if err != nil {
		return err
	}
	if taken != "" {
		return fmt.Errorf("the name %s is taken by %s: choose another name", m.Name, taken)
	}
	if !*isNew {
		dups, err := nearDuplicates(repo, level, m.Description)
		if err != nil {
			return err
		}
		if len(dups) > 0 {
			fmt.Fprintln(env.Out, "These memories on the path share most words of the description:")
			for _, d := range dups {
				fmt.Fprintf(env.Out, "- %s: %s\n", d.Path, d.Text)
			}
			return errors.New("near-duplicates exist, so remember wrote nothing: if the fact is new, run memcore remember --new")
		}
	}

	content := m.Bytes()
	if found := guards.Secrets(string(content)); len(found) > 0 {
		for _, f := range found {
			fmt.Fprintf(env.Out, "%s:%d: %s\n", rel, f.Line, f.Message)
		}
		return errors.New("the memory holds a likely secret, so remember wrote nothing: remove the secret, then remember again")
	}
	report, err := guards.VerifyContent(repo, rel, content)
	if err != nil {
		return err
	}
	if len(report.Faults) > 0 {
		for _, f := range report.Faults {
			fmt.Fprintf(env.Out, "%s:%d: fault: %s\n", f.Path, f.Line, f.Message)
		}
		return fmt.Errorf("%d faults, so remember wrote nothing", len(report.Faults))
	}
	levels := store.Path(level)
	for _, l := range levels {
		if _, err := store.Lines(repo, l); err != nil {
			return fmt.Errorf("read the index of the path: %w", err)
		}
	}

	paths := []string{rel}
	for _, l := range levels {
		paths = append(paths, path.Join(store.Folder(l), "README.md"))
	}
	for _, p := range paths {
		if err := noSymlink(repo, p); err != nil {
			return err
		}
	}
	dirty, err := store.Git(repo, append([]string{"--literal-pathspecs", "status", "--porcelain", "--"}, paths...)...)
	if err != nil {
		return fmt.Errorf("check the paths of the write: %w", err)
	}
	if strings.TrimSpace(dirty) != "" {
		return fmt.Errorf("the working tree of the memory repo has changes that are not committed, so remember wrote nothing: the person commits or discards them:\n%s", strings.TrimRight(dirty, "\n"))
	}

	if err := write(repo, rel, content, paths); err != nil {
		return err
	}
	env.event().Paths, env.event().Made = []string{rel}, []string{headCommit(repo)}

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
	n := 0
	for _, kind := range []string{store.KindMemories, store.KindSkills, store.KindScripts, store.KindConventions, store.KindGlossary} {
		n += len(ix.Of(kind))
	}
	if w := budgetWarning(level, n); w != "" {
		fmt.Fprintln(env.Out, w)
	}
	fmt.Fprintf(env.Out, "Remembered %s.\n", rel)
	return nil
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

// noSymlink refuses a path in the repo whose folders or file include a symlink, so a write stays inside the repo.
func noSymlink(repo, p string) error {
	cur := repo
	for _, part := range strings.Split(p, "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("check %s: %w", p, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s in the memory repo is a symlink, so remember wrote nothing: the person replaces it with a folder or a file", strings.TrimPrefix(cur, repo+string(filepath.Separator)))
		}
	}
	return nil
}

// write writes the memory at rel, regenerates the indexes, and commits the paths: rel and the index of each level.
// The paths have no changes before the write, so on a failure it restores them from HEAD.
func write(repo, rel string, content []byte, paths []string) error {
	full := filepath.Join(repo, filepath.FromSlash(rel))
	var created []string // the folders that the write creates, deepest first
	for dir := filepath.Dir(full); dir != repo; dir = filepath.Dir(dir) {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		created = append(created, dir)
	}
	err := func() error {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("create the level: %w", err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return fmt.Errorf("write the memory: %w", err)
		}
		for _, p := range paths[1:] {
			folder := path.Dir(p)
			if folder == "." {
				folder = "" // the root
			}
			level, ok := store.LevelOf(folder)
			if !ok {
				return fmt.Errorf("index %s: the folder maps to no level", folder)
			}
			if err := store.WriteIndex(repo, level); err != nil {
				return err
			}
		}
		return store.Commit(repo, paths, "remember: "+rel)
	}()
	if err == nil {
		return nil
	}
	store.Git(repo, append([]string{"--literal-pathspecs", "reset", "-q", "--"}, paths...)...)
	for _, p := range paths {
		if _, gerr := store.Git(repo, "cat-file", "-e", "HEAD:"+p); gerr == nil {
			store.Git(repo, "--literal-pathspecs", "checkout", "HEAD", "--", p)
		} else {
			os.Remove(filepath.Join(repo, filepath.FromSlash(p)))
		}
	}
	for _, dir := range created {
		os.Remove(dir)
	}
	return err
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
