package store

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// The kinds of index lines.
const (
	KindMemories    = "memories"
	KindSkills      = "skills"
	KindScripts     = "scripts"
	KindConventions = "conventions"
	KindGlossary    = "glossary"
	KindPlans       = "plans"
	KindNotes       = "notes"
	KindLevels      = "levels"
)

// Kinds are the kinds of index lines, in the order of a generated index.
var Kinds = []string{KindMemories, KindSkills, KindScripts, KindConventions, KindGlossary, KindPlans, KindNotes, KindLevels}

// Reserved are the names at a level that belong to its documents, so no child level has them.
var Reserved = []string{"memories", "skills", "bin", "plans", "notes", "conventions.md", "glossary.md", "README.md"}

// escape is the prefix of a folder whose directory has a reserved name.
const escape = "+"

// collides reports whether a folder name belongs to the documents of a level or to git.
// It ignores case, because a file system that ignores case serves Bin/ from bin/.
func collides(name string) bool {
	if strings.EqualFold(name, ".git") {
		return true
	}
	for _, r := range Reserved {
		if strings.EqualFold(name, r) {
			return true
		}
	}
	return false
}

// Folder returns the folder of a level in the memory repo. A segment that collides
// with a reserved name, or starts with the escape, gets the escape in front.
func Folder(level string) string {
	if level == "" {
		return ""
	}
	parts := strings.Split(level, "/")
	for i, p := range parts {
		if collides(p) || strings.HasPrefix(p, escape) {
			parts[i] = escape + p
		}
	}
	return strings.Join(parts, "/")
}

// LevelOf returns the level of a folder, and false when Folder returns no such folder.
func LevelOf(folder string) (string, bool) {
	if folder == "" {
		return "", true
	}
	parts := strings.Split(folder, "/")
	for i, p := range parts {
		name, escaped := strings.CutPrefix(p, escape)
		if escaped != (collides(name) || strings.HasPrefix(name, escape)) || name == "" {
			return "", false
		}
		parts[i] = name
	}
	return strings.Join(parts, "/"), true
}

var headings = map[string]string{
	KindMemories:    "Memories",
	KindSkills:      "Skills",
	KindScripts:     "Scripts",
	KindConventions: "Conventions",
	KindGlossary:    "Glossary",
	KindPlans:       "Plans",
	KindNotes:       "Notes",
	KindLevels:      "Levels",
}

// Line is one line of an index.
type Line struct {
	Kind string
	Name string // the link text
	Path string // the path of the document, relative to the repo
	Text string // when to read the document
}

// Index holds the lines of one level, in the order of Kinds and then by name.
type Index struct {
	Level string
	Lines []Line
}

// Of returns the lines of one kind.
func (ix Index) Of(kind string) []Line {
	var lines []Line
	for _, l := range ix.Lines {
		if l.Kind == kind {
			lines = append(lines, l)
		}
	}
	return lines
}

// Lines returns the index lines of a level, read at its folder. A level that does not exist has none.
func Lines(repo, level string) (Index, error) {
	ix := Index{Level: level}
	folder := Folder(level)
	dir := filepath.Join(repo, filepath.FromSlash(folder))
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return ix, nil
	}
	rel := func(p string) string { return path.Join(folder, p) }
	add := func(kind, name, p, text string) {
		ix.Lines = append(ix.Lines, Line{Kind: kind, Name: name, Path: rel(p), Text: text})
	}

	for _, k := range []struct{ kind, folder string }{{KindMemories, "memories"}, {KindPlans, "plans"}, {KindNotes, "notes"}} {
		names, err := entries(dir, k.folder, false)
		if err != nil {
			return ix, err
		}
		for _, n := range names {
			if !strings.HasSuffix(n, ".md") {
				continue
			}
			p := k.folder + "/" + n
			d, err := description(filepath.Join(dir, p))
			if err != nil {
				return ix, fmt.Errorf("index %s: %w", rel(p), err)
			}
			if d == "" {
				d = noDescription(rel(p))
			}
			add(k.kind, strings.TrimSuffix(n, ".md"), p, d)
		}
	}

	skills, err := entries(dir, "skills", true)
	if err != nil {
		return ix, err
	}
	for _, n := range skills {
		p := "skills/" + n + "/SKILL.md"
		d, err := description(filepath.Join(dir, p))
		if err != nil {
			return ix, fmt.Errorf("index %s: %w", rel(p), err)
		}
		if d == "" {
			d = noDescription(rel(p))
		}
		add(KindSkills, n, p, d)
	}

	scripts, err := entries(dir, "bin", false)
	if err != nil {
		return ix, err
	}
	for _, n := range scripts {
		p := "bin/" + n
		info, err := os.Stat(filepath.Join(dir, p))
		if err != nil {
			return ix, fmt.Errorf("index %s: %w", rel(p), err)
		}
		if info.Mode()&0o111 == 0 {
			continue // a helper beside the scripts
		}
		d, err := scriptLine(filepath.Join(dir, p))
		if err != nil {
			return ix, fmt.Errorf("index %s: %w", rel(p), err)
		}
		if d == "" {
			d = "No comment line says when to run it."
		}
		add(KindScripts, n, p, d)
	}

	for _, f := range []struct{ kind, file, text string }{
		{KindConventions, "conventions.md", "Read before you change anything in this repo."},
		{KindGlossary, "glossary.md", "Read before you name anything at this level."},
	} {
		if _, err := os.Stat(filepath.Join(dir, f.file)); err == nil {
			add(f.kind, strings.TrimSuffix(f.file, ".md"), f.file, f.text)
		}
	}

	children, err := os.ReadDir(dir)
	if err != nil {
		return ix, fmt.Errorf("index %s: %w", level, err)
	}
	for _, c := range children {
		if !c.IsDir() || collides(c.Name()) {
			continue
		}
		child, ok := LevelOf(c.Name())
		if !ok {
			continue // a folder that no level maps to
		}
		add(KindLevels, child, c.Name()+"/README.md", "Read when you work under ~/"+path.Join(level, child)+".")
	}

	slices.SortStableFunc(ix.Lines, func(a, b Line) int {
		if d := slices.Index(Kinds, a.Kind) - slices.Index(Kinds, b.Kind); d != 0 {
			return d
		}
		return strings.Compare(a.Name, b.Name)
	})
	return ix, nil
}

// entries lists the names in a folder of a level: directories when dirs is true, files otherwise.
func entries(dir, folder string, dirs bool) ([]string, error) {
	list, err := os.ReadDir(filepath.Join(dir, folder))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", folder, err)
	}
	var names []string
	for _, e := range list {
		if e.IsDir() == dirs && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// description reads the description from the frontmatter of a document.
// It returns "" when the document has no frontmatter or no description, so one bad document never stops a read.
func description(file string) (string, error) {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil // a skill folder without its SKILL.md
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	fm, _, err := ParseFrontmatter(f)
	if err != nil {
		return "", nil
	}
	return fm.Values["description"], nil
}

// noDescription is the index line of a document without a description.
func noDescription(p string) string {
	return "No description. Run memcore verify " + p + "."
}

// scriptLine reads the first comment line of a script, after its shebang, or "" when it has none.
func scriptLine(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#!") || line == "" {
			continue
		}
		if text, ok := strings.CutPrefix(line, "#"); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), nil
		}
		break
	}
	return "", sc.Err()
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`)

// Render returns the index as the text of README.md.
func Render(ix Index) string {
	var b strings.Builder
	b.WriteString("# ~")
	if ix.Level != "" {
		b.WriteString("/" + ix.Level)
	}
	b.WriteString("\n")
	for _, kind := range Kinds {
		lines := ix.Of(kind)
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n", headings[kind])
		folder := Folder(ix.Level)
		for _, l := range lines {
			link := strings.TrimPrefix(l.Path, folder+"/")
			if folder == "" {
				link = l.Path
			}
			fmt.Fprintf(&b, "- [%s](%s): %s\n", labelEscaper.Replace(l.Name), (&url.URL{Path: link}).EscapedPath(), l.Text)
		}
	}
	return b.String()
}

// WriteIndex writes the index of a level as the README.md of its folder.
func WriteIndex(repo, level string) error {
	ix, err := Lines(repo, level)
	if err != nil {
		return err
	}
	dir := filepath.Join(repo, filepath.FromSlash(Folder(level)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create the level %s: %w", level, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(Render(ix)), 0o644); err != nil {
		return fmt.Errorf("write the index of %q: %w", level, err)
	}
	return nil
}
