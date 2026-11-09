package commands

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// runMove renames a memory, a note or a plan, and rewrites every link to it in the same commit.
func runMove(env Env, args []string) error {
	flags := flag.NewFlagSet("move", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	reason := flags.String("reason", "", "why the document moves")
	pos, err := parseInterleaved(flags, args)
	if err != nil {
		return usageErr(err.Error())
	}
	if len(pos) != 2 || strings.TrimSpace(*reason) == "" {
		return usageErr("usage: memcore move <path or name> <new name or path> --reason <text>")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	unlock, err := lockWrite(repo)
	if err != nil {
		return err
	}
	defer unlock()
	from, err := resolve(repo, pos[0])
	if err != nil {
		return err
	}
	env.event().Paths, env.event().Reason = []string{from}, *reason
	kind := store.Kind(from)
	if !slices.Contains([]string{"memories", "notes", "plans"}, kind) || !strings.HasSuffix(from, ".md") || path.Base(from) == "README.md" {
		return inputErrf("%s is no memory, note or plan: move moves a memory, a note or a plan", from)
	}
	memory := store.IsMemory(from)
	oldName := strings.TrimSuffix(path.Base(from), ".md")

	to, err := moveTarget(repo, from, pos[1])
	if err != nil {
		return err
	}
	newName := strings.TrimSuffix(path.Base(to), ".md")
	if to == from {
		return inputErrf("%s and %s are the same path: move needs a new name or a new folder", from, to)
	}
	if newName != oldName {
		if !docNameRE.MatchString(newName) {
			return inputErrf("the name %s is not lower case with hyphens", newName)
		}
		if memory {
			taken, err := findMemory(repo, newName)
			if err != nil {
				return err
			}
			if taken != "" {
				return inputErrf("the name %s is taken by %s: choose another name", newName, taken)
			}
		}
	}
	if err := noSymlink(repo, to); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(to))); err == nil {
		return inputErrf("a file exists at %s: choose another name", to)
	}

	relinked, err := guards.Relink(repo, from, to, oldName, newName, memory)
	if err != nil {
		return err
	}
	content, ok := relinked[from]
	if !ok {
		if content, err = os.ReadFile(filepath.Join(repo, filepath.FromSlash(from))); err != nil {
			return fmt.Errorf("read %s: %w", from, err)
		}
	}
	delete(relinked, from)
	content, err = renamed(content, newName)
	if err != nil {
		return err
	}
	writes := map[string][]byte{to: content}
	for p, c := range relinked {
		writes[p] = c
	}

	before, err := guards.Faults(repo, nil)
	if err != nil {
		return err
	}
	// The removal of from can empty its topic folders, and apply prunes them. Their indexes count as removed.
	emptied, err := wouldPrune(repo, []string{from}, nil)
	if err != nil {
		return err
	}
	removed := []string{from}
	for _, d := range emptied {
		if !store.IsTopicIndex(path.Join(d, "README.md")) || strings.HasPrefix(to, d+"/") {
			continue
		}
		removed = append(removed, d)
	}
	after, err := guards.FaultsWith(repo, writes, removed)
	if err != nil {
		return err
	}
	// A fault keeps its identity across the move: its document, its line, and for a dead link the path that
	// the link resolves to. So a dead link that the move only rewrites counts as the same fault.
	had := map[string]bool{}
	for _, f := range before {
		had[faultKey(f, from, to)] = true
	}
	var added []guards.Finding
	for _, f := range after {
		if !had[faultKey(f, from, to)] {
			added = append(added, f)
		}
	}
	if len(added) > 0 {
		for _, f := range added {
			fmt.Fprintf(env.Out, "%s:%d: fault: %s\n", f.Path, f.Line, f.Message)
		}
		return inputErrf("%d faults, so move moved nothing", len(added))
	}

	c := change{removes: []string{from}, message: "move: " + from + " to " + to + "\n\n" + *reason}
	for _, p := range slices.Sorted(maps.Keys(writes)) {
		info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(p)))
		mode := os.FileMode(0o644)
		if err == nil {
			mode = info.Mode().Perm()
		}
		c.writes = append(c.writes, file{path: p, content: writes[p], mode: mode})
	}
	made, err := apply(repo, c)
	if err != nil {
		return err
	}
	env.event().Paths, env.event().Made = []string{from, to}, []string{made}
	fmt.Fprintf(env.Out, "Moved %s to %s.\n", from, to)
	return nil
}

// faultKey names a fault apart from where the move puts its document. A fault of the moved document counts
// at from. A dead link counts by the path that it resolves to, from the folder of its document.
func faultKey(f guards.Finding, from, to string) string {
	doc := f.Path
	if doc == to {
		doc = from
	}
	msg := f.Message
	if target, ok := strings.CutPrefix(msg, "dead link to "); ok {
		if t, err := strconv.Unquote(target); err == nil {
			if i := strings.IndexAny(t, "#?"); i >= 0 {
				t = t[:i]
			}
			if !strings.HasPrefix(t, "/") {
				t = path.Join(path.Dir(f.Path), t)
			}
			msg = "dead link to " + path.Clean(strings.TrimPrefix(t, "/"))
		}
	}
	return fmt.Sprintf("%s:%d: %s", doc, f.Line, msg)
}

// moveTarget returns the new path of a document from the second argument of move. An argument without a /
// is a new name in the same folder. A path that ends in / names a folder, and keeps the name. A path that ends
// in .md names the new path. The folder is the kind folder of the level of from, or a topic folder in it.
func moveTarget(repo, from, arg string) (string, error) {
	if !strings.Contains(arg, "/") {
		return path.Join(path.Dir(from), strings.TrimSuffix(arg, ".md")+".md"), nil
	}
	var to string
	switch {
	case strings.HasSuffix(arg, "/"):
		to = path.Join(path.Clean(arg), path.Base(from))
	case strings.HasSuffix(arg, ".md"):
		to = path.Clean(arg)
	default:
		return "", inputErrf("%s ends in neither / nor .md: a folder ends in /, and a new path in .md", arg)
	}
	if path.IsAbs(to) || to == ".." || strings.HasPrefix(to, "../") {
		return "", inputErrf("%s is not a path in the memory repo", arg)
	}
	fromLevel, _ := docLevel(from)
	toLevel, ok := docLevel(to)
	if !ok || toLevel != fromLevel {
		return "", inputErrf("%s is not at the level of %s: move stays in one level", arg, from)
	}
	kind := docKind(from)
	if docKind(to) != kind {
		return "", inputErrf("%s is not in the %s folder of %s: move keeps the kind of a document", arg, kind, title(fromLevel))
	}
	dir := path.Dir(to)
	if top := path.Join(store.Folder(fromLevel), kind); dir != top {
		if kind == "plans" {
			return "", inputErrf("%s is a folder inside plans: a plan has no topic folder", dir)
		}
		if err := needTopic(repo, fromLevel, kind, strings.TrimPrefix(dir, top+"/")); err != nil {
			return "", err
		}
	}
	return to, nil
}

// renamed sets the name in the frontmatter of a document. It replaces only the name line, so every other line
// keeps its place. A document without a name keeps its frontmatter.
func renamed(content []byte, name string) ([]byte, error) {
	text := string(content)
	if !store.HasFrontmatter(text) {
		return content, nil
	}
	if _, _, err := store.ParseFrontmatter(strings.NewReader(text)); err != nil {
		return nil, fmt.Errorf("read the frontmatter: %w", err)
	}
	lines := strings.SplitAfter(text, "\n")
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r\n")
		if line == "---" {
			break
		}
		if strings.HasPrefix(line, "name:") {
			lines[i] = "name: " + name + lines[i][len(line):]
			return []byte(strings.Join(lines, "")), nil
		}
	}
	return content, nil
}
