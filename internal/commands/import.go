package commands

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/readers"
	"github.com/atmin/memcore/internal/store"
)

// importRecord is the record of the import of auto-memory, .git/memcore/import.json.
type importRecord struct {
	Files   map[string]importedFile   `json:"files"`             // each source file, by its absolute path
	Pending map[string]pendingProject `json:"pending,omitempty"` // each project folder whose commit may have run
}

// importedFile is the outcome of one source file at its last import.
type importedFile struct {
	Hash     string   `json:"hash"`               // the SHA-256 of the source file then
	Path     string   `json:"path,omitempty"`     // the memory in the repo, when the file imported
	Reason   string   `json:"reason,omitempty"`   // why the file stayed out, redacted
	Accepted bool     `json:"accepted,omitempty"` // the person accepted the file as it is
	Names    []string `json:"names,omitempty"`    // the names by which a link of its project may name it
}

// pendingProject is a project whose import commit may exist: the token in its message, and the outcome of each file.
type pendingProject struct {
	Token string                  `json:"token"`
	Files map[string]importedFile `json:"files"`
}

// candidate is one source file that the import tries.
type candidate struct {
	file, stem, hash string
	content          []byte
	memory           store.Memory
	keys             []string // the names by which a link of the project may name it
	name             string   // its final name
}

var wikiLinkRE = regexp.MustCompile(`\[\[([^\[\]\n]+)\]\]`)

// importAutoMemory imports the auto-memory of Claude Code into the memory repo, one commit per project, by the
// rules of docs/onboarding.md. It returns whether the import is complete: every source file is imported as it is
// now, or the person accepted it.
func importAutoMemory(env Env, repo, claude string) (bool, error) {
	h, err := home()
	if err != nil {
		return false, err
	}
	rec, err := readImportRecord(repo)
	if err != nil {
		return false, err
	}
	unlock, err := lockWrite(repo)
	if err != nil {
		return false, err
	}
	err = settlePending(repo, &rec)
	unlock()
	if err != nil {
		return false, err
	}
	projects := filepath.Join(claude, "projects")
	folders, err := os.ReadDir(projects)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read the projects of Claude Code: %w", err)
	}
	for _, f := range folders {
		if f.IsDir() {
			if err := importProject(env, repo, h, &rec, projects, f.Name()); err != nil {
				return false, err
			}
		}
	}
	blockers, err := importBlockers(rec, claude)
	if err != nil {
		return false, err
	}
	for _, b := range blockers {
		if b.changed {
			fmt.Fprintf(env.Out, "%s: %s.\n", tilde(b.file), b.reason)
		}
	}
	return len(blockers) == 0, nil
}

// importProject imports the auto-memory of one project folder in one commit.
func importProject(env Env, repo, h string, rec *importRecord, projects, folder string) error {
	dir := filepath.Join(projects, folder, "memory")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the auto-memory of %s: %w", folder, err)
	}
	var todo []*candidate
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "MEMORY.md" {
			continue
		}
		file := filepath.Join(dir, e.Name())
		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read the auto-memory %s: %w", file, err)
		}
		sum := sha256.Sum256(content)
		hash := hex.EncodeToString(sum[:])
		prev, seen := rec.Files[file]
		// An imported file, or one that the person accepted, needs nothing. A file imported before and changed
		// since does not import twice: importBlockers names it.
		if seen && (prev.Path != "" || (prev.Accepted && prev.Hash == hash)) {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".md")
		todo = append(todo, &candidate{file: file, stem: stem, hash: hash, content: content, keys: []string{stem}})
	}
	if len(todo) == 0 {
		return nil
	}
	outcome := map[string]importedFile{}
	var notes []string
	leave := func(c *candidate, reason string) {
		reason = guards.Redact(reason) // a message may quote the file
		outcome[c.file] = importedFile{Hash: c.hash, Reason: reason, Names: c.keys}
		notes = append(notes, fmt.Sprintf("Left out %s: %s.", filepath.Base(c.file), reason))
	}
	finish := func() error {
		for f, o := range outcome {
			rec.Files[f] = o
		}
		for _, n := range notes {
			if strings.HasPrefix(n, "Left out") {
				fmt.Fprintf(env.Out, "%s: %s\n", folder, n)
			}
		}
		return writeImportRecord(repo, *rec)
	}

	where, reason := projectLevel(folder, h)
	if reason != "" {
		for _, c := range todo {
			leave(c, reason)
		}
		return finish()
	}
	projectDir, level := where[0], where[1]

	unlock, err := lockWrite(repo)
	if err != nil {
		return err
	}
	defer unlock()
	inRepo, err := memoryNames(repo)
	if err != nil {
		return err
	}

	var set []*candidate
	for _, c := range todo {
		// The names come first, so a link to a file that stays out never reaches another memory of that name.
		// A file that fails to parse still gives the names read before the failure.
		m, err := readers.AutoMemory(bytes.NewReader(c.content))
		if m.Name != "" {
			c.keys = append(c.keys, m.Name, normalizeName(m.Name))
		}
		// The raw file and every decoded field come next, so no message below can quote a secret. An escape of
		// YAML, such as \u0009, can build a secret that the raw file does not show.
		decoded := strings.Join(append([]string{m.Name, m.Description, m.Type, m.Body}, m.Sessions...), "\n")
		if len(guards.Secrets(string(c.content))) > 0 || len(guards.Secrets(decoded)) > 0 {
			leave(c, "it holds a likely secret")
			continue
		}
		switch {
		case err != nil:
			leave(c, "it does not parse: "+err.Error())
			continue
		case !slices.Contains(store.Types, m.Type):
			leave(c, fmt.Sprintf("its type %q is not one of %s", m.Type, strings.Join(store.Types, ", ")))
			continue
		case strings.TrimSpace(m.Description) == "":
			leave(c, "it has no description")
			continue
		}
		name := normalizeName(m.Name)
		if name == "" {
			name = normalizeName(c.stem)
		}
		if name == "" {
			leave(c, "neither its name nor its file name holds a letter or a digit")
			continue
		}
		c.memory, c.name = m, name
		c.keys = []string{m.Name, c.stem, name}
		set = append(set, c)
	}

	// Each name is unique in the repo, so a name that the repo or this import holds takes a suffix.
	taken := map[string]bool{}
	for n := range inRepo {
		taken[n] = true
	}
	segment := normalizeName(path.Base(level))
	if level == "" {
		segment = ""
	}
	for _, c := range set {
		c.name = freeName(c.name, segment, taken)
		taken[c.name] = true
		if c.name != c.memory.Name {
			notes = append(notes, fmt.Sprintf("Renamed %s to %s.", c.memory.Name, c.name))
		}
	}

	// The links follow the final names. A file with a fault leaves, and the links to it lose their brackets,
	// so the check repeats until no file leaves.
	var files map[string][]byte
	for {
		// A link names a file of its project by any of its names: the files imported before, and this set.
		// A link to a file left out loses its brackets, even when another project holds that name.
		final, excluded := map[string]string{}, map[string]bool{}
		for f, o := range rec.Files {
			if filepath.Dir(f) != dir {
				continue
			}
			names := append([]string{strings.TrimSuffix(filepath.Base(f), ".md")}, o.Names...)
			for _, n := range names {
				if o.Path != "" {
					final[n] = strings.TrimSuffix(path.Base(o.Path), ".md")
				} else {
					excluded[n] = true
				}
			}
		}
		for _, o := range outcome {
			for _, n := range o.Names {
				excluded[n] = true
			}
		}
		for _, c := range set {
			for _, k := range c.keys {
				final[k] = c.name
			}
		}
		files = map[string][]byte{}
		var unlinked []string
		for _, c := range set {
			body := wikiLinkRE.ReplaceAllStringFunc(c.memory.Body, func(link string) string {
				x := strings.TrimSpace(link[2 : len(link)-2])
				if f, ok := final[x]; ok {
					return "[[" + f + "]]"
				}
				if inRepo[x] && !excluded[x] {
					return link
				}
				unlinked = append(unlinked, fmt.Sprintf("Removed the brackets of [[%s]] in %s: no memory has that name.", x, c.name))
				return x
			})
			m := store.Memory{Name: c.name, Description: c.memory.Description, Type: c.memory.Type, Sessions: c.memory.Sessions, Body: body}
			files[memoryPath(level, c.name)] = m.Bytes()
		}
		reports, err := guards.VerifySet(repo, files)
		if err != nil {
			return err
		}
		var kept []*candidate
		for _, c := range set {
			p := memoryPath(level, c.name)
			if r := reports[p]; len(r.Faults) > 0 {
				leave(c, r.Faults[0].Message)
				continue
			}
			// An escape of YAML, such as \u0020, can build a secret that the raw file did not show.
			if len(guards.Secrets(string(files[p]))) > 0 {
				leave(c, "it holds a likely secret once its escapes are read")
				continue
			}
			kept = append(kept, c)
		}
		if len(kept) == len(set) {
			notes = append(notes, unlinked...)
			break
		}
		set = kept
	}
	if len(set) == 0 {
		return finish()
	}

	// The record of the project comes before its commit, so a rerun after a crash finds the commit by its token.
	token, err := newToken()
	if err != nil {
		return err
	}
	planned := map[string]importedFile{}
	for f, o := range outcome {
		planned[f] = o
	}
	var writes []file
	for _, c := range set {
		p := memoryPath(level, c.name)
		planned[c.file] = importedFile{Hash: c.hash, Path: p, Names: c.keys}
		writes = append(writes, file{path: p, content: files[p], mode: 0o644})
	}
	if rec.Pending == nil {
		rec.Pending = map[string]pendingProject{}
	}
	rec.Pending[folder] = pendingProject{Token: token, Files: planned}
	if err := writeImportRecord(repo, *rec); err != nil {
		return err
	}
	msg := "init: import the auto-memory of " + tilde(projectDir)
	if len(notes) > 0 {
		msg += "\n\n" + guards.Redact(strings.Join(notes, "\n")) // a note may quote a name or a link
	}
	msg += "\n\nImport: " + token
	made, err := apply(repo, change{writes: writes, message: msg})
	if err != nil {
		delete(rec.Pending, folder)
		writeImportRecord(repo, *rec)
		return err
	}
	env.event().Made = append(env.event().Made, made)
	delete(rec.Pending, folder)
	outcome = planned
	fmt.Fprintf(env.Out, "Imported %s from %s.\n", count(len(set), "memory"), tilde(projectDir))
	return finish()
}

// projectLevel finds the directory of a project folder and its level, or the reason that the folder stays out.
func projectLevel(folder, h string) ([2]string, string) {
	dirs, err := readers.ProjectDirs(folder, string(filepath.Separator))
	switch {
	case err != nil:
		return [2]string{}, "its directory cannot be found: " + err.Error()
	case len(dirs) == 0:
		return [2]string{}, "the folder " + folder + " names no directory"
	case len(dirs) > 1:
		for i := range dirs {
			dirs[i] = tilde(dirs[i])
		}
		return [2]string{}, fmt.Sprintf("the folder %s names %d directories: %s", folder, len(dirs), strings.Join(dirs, ", "))
	}
	level, err := store.Level(dirs[0], h)
	if errors.Is(err, store.ErrOutsideHome) {
		return [2]string{}, "the directory " + dirs[0] + " is outside home"
	}
	if err != nil {
		return [2]string{}, "its level cannot be found: " + err.Error()
	}
	return [2]string{dirs[0], level}, ""
}

// settlePending marks a pending project done when its commit is in the history of HEAD. Otherwise the import
// stopped before its commit, so settlePending puts back each file that it planned, and the indexes on their paths.
// The caller holds the write lock.
func settlePending(repo string, rec *importRecord) error {
	for folder, p := range rec.Pending {
		out, err := store.Git(repo, "log", "--format=%H", "-F", "--grep=Import: "+p.Token, "HEAD")
		if err != nil {
			return fmt.Errorf("look for the import of %s: %w", folder, err)
		}
		if strings.TrimSpace(out) != "" {
			for f, o := range p.Files {
				rec.Files[f] = o
			}
			delete(rec.Pending, folder)
			continue
		}
		for _, o := range p.Files {
			if o.Path == "" {
				continue
			}
			restore(repo, o.Path)
			if level, ok := docLevel(o.Path); ok {
				for _, l := range store.Path(level) {
					restore(repo, path.Join(store.Folder(l), "README.md"))
				}
			}
			for d := path.Dir(o.Path); d != "." && d != "/"; d = path.Dir(d) {
				if os.Remove(filepath.Join(repo, filepath.FromSlash(d))) != nil {
					break // the folder holds more, or is gone
				}
			}
		}
		delete(rec.Pending, folder)
	}
	return writeImportRecord(repo, *rec)
}

// blocker is a source file that keeps auto-memory on, with the reason.
type blocker struct {
	file, reason string
	changed      bool // the file imported, then changed
}

// importBlockers returns the source files that are neither imported as they are now nor accepted by the person.
func importBlockers(rec importRecord, claude string) ([]blocker, error) {
	var blockers []blocker
	files, err := filepath.Glob(filepath.Join(claude, "projects", "*", "memory", "*.md"))
	if err != nil {
		return nil, fmt.Errorf("list the auto-memory: %w", err)
	}
	for _, f := range files {
		if filepath.Base(f) == "MEMORY.md" {
			continue
		}
		content, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read the auto-memory %s: %w", f, err)
		}
		sum := sha256.Sum256(content)
		hash := hex.EncodeToString(sum[:])
		o, seen := rec.Files[f]
		switch {
		case seen && o.Hash == hash && (o.Path != "" || o.Accepted):
		case seen && o.Path != "":
			blockers = append(blockers, blocker{f, "it changed since its import", true})
		case seen && o.Hash == hash && o.Reason != "":
			blockers = append(blockers, blocker{f, o.Reason, false})
		default:
			blockers = append(blockers, blocker{f, "it is not imported yet", false})
		}
	}
	return blockers, nil
}

// memoryPath is the path of a memory at a level.
func memoryPath(level, name string) string {
	return path.Join(store.Folder(level), "memories", name+".md")
}

// normalizeName writes a name in lower case with hyphens: each run of other characters becomes one hyphen.
func normalizeName(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			gap = false
		} else {
			gap = true
		}
	}
	return b.String()
}

// freeName returns name when it is free, then name with the segment of its level, then with -2, -3 and on.
func freeName(name, segment string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	base := name
	if segment != "" {
		base = name + "-" + segment
		if !taken[base] {
			return base
		}
	}
	for n := 2; ; n++ {
		if c := base + "-" + strconv.Itoa(n); !taken[c] {
			return c
		}
	}
}

// memoryNames returns the names of the memories in the repo.
func memoryNames(repo string) (map[string]bool, error) {
	names := map[string]bool{}
	err := filepath.WalkDir(repo, func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if rel, err := filepath.Rel(repo, f); err == nil && !d.IsDir() && store.IsMemory(filepath.ToSlash(rel)) {
			names[strings.TrimSuffix(d.Name(), ".md")] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list the memories: %w", err)
	}
	return names, nil
}

// newToken returns a random token that names one import commit.
func newToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make the token of an import: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// readImportRecord reads .git/memcore/import.json, or returns an empty record when it does not exist.
func readImportRecord(repo string) (importRecord, error) {
	rec := importRecord{Files: map[string]importedFile{}, Pending: map[string]pendingProject{}}
	b, err := os.ReadFile(filepath.Join(store.StateDir(repo), "import.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return rec, fmt.Errorf("read the record of the import: %w", err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, fmt.Errorf("read the record of the import: %w", err)
	}
	if rec.Files == nil {
		rec.Files = map[string]importedFile{}
	}
	if rec.Pending == nil {
		rec.Pending = map[string]pendingProject{}
	}
	return rec, nil
}

// writeImportRecord writes .git/memcore/import.json through a temporary file.
func writeImportRecord(repo string, rec importRecord) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("write the record of the import: %w", err)
	}
	p := filepath.Join(store.StateDir(repo), "import.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("write the record of the import: %w", err)
	}
	if err := os.WriteFile(p+".tmp", append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write the record of the import: %w", err)
	}
	if err := os.Rename(p+".tmp", p); err != nil {
		return fmt.Errorf("write the record of the import: %w", err)
	}
	return nil
}
