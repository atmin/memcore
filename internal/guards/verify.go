// Package guards holds the checks that stand between an agent and the memory repo.
package guards

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// Finding is one fault or warning, at a line of a document.
type Finding struct {
	Path    string
	Line    int
	Message string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.Path, f.Line, f.Message)
}

// Report holds the faults, which block a write, and the warnings, which do not.
type Report struct {
	Faults   []Finding
	Warnings []Finding
}

var (
	linkRE   = regexp.MustCompile(`!?\[(?:\\.|[^\]\\\n])*\]\(`)
	refDefRE = regexp.MustCompile(`(?m)^ {0,3}\[[^\]\n]+\]:[ \t]*(<[^>\n]*>|\S+)`)
	listRE   = regexp.MustCompile(`^(?:[-*+]|\d+[.)])(?:\s|$)`)
	wikiRE   = regexp.MustCompile(`\[\[([^\[\]\n]+)\]\]`)
	autoRE   = regexp.MustCompile(`<[a-zA-Z][a-zA-Z0-9+.-]*:[^>\s]*>|https?://[^\s)>\]]+`)
	schemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	notRE    = regexp.MustCompile(`\bNot:\s+([^\n]+?)\.(?:\s|$)`)
)

// verifier checks documents of one repo, and keeps what they share.
type verifier struct {
	repo       string
	gone       []string               // paths that count as removed: files, or folders with all they hold
	pending    []string               // paths that count as present: files of a set that is not on disk yet
	names      map[string]bool        // the names of the memories in the repo
	former     map[string]string      // each name that history gives a memory, with its path now or ""
	formerRead bool                   // whether former holds the history, or stays empty on purpose
	glossaries map[string][]forbidden // the forbidden words of each glossary, by its path
}

type forbidden struct {
	phrase   string
	glossary string
	re       *regexp.Regexp
}

func newVerifier(repo string) (*verifier, error) {
	v := &verifier{repo: repo, names: map[string]bool{}, glossaries: map[string][]forbidden{}}
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			if rel, err := filepath.Rel(repo, p); err == nil && store.IsMemory(filepath.ToSlash(rel)) {
				v.names[strings.TrimSuffix(d.Name(), ".md")] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list the memories: %w", err)
	}
	return v, nil
}

// Faults returns the faults of every markdown document in the repo except the indexes.
// It checks the repo as if the removed paths were gone. A removed path is a file, or a folder
// with everything in it. Only a removed memory file takes its name out of the [[name]] targets.
// A command compares the faults with and without a removal, so only the faults that the removal adds stop it.
func Faults(repo string, removed []string) ([]Finding, error) {
	return FaultsWith(repo, nil, removed)
}

// FaultsWith returns the faults of every markdown document in the repo except the indexes, as if the writes
// were made and the removed paths were gone. A write replaces a document on disk, or adds one. A command
// compares the faults before and after a change, so only the faults that the change adds stop it.
func FaultsWith(repo string, writes map[string][]byte, removed []string) ([]Finding, error) {
	v, err := newVerifier(repo)
	if err != nil {
		return nil, err
	}
	v.gone = removed
	v.formerRead = true // a former name only warns, and faults need no history
	for p := range writes {
		v.pending = append(v.pending, path.Clean(p))
	}
	for name := range v.names {
		for _, g := range removed {
			if store.IsMemory(g) && path.Base(g) == name+".md" {
				delete(v.names, name)
			}
		}
	}
	for p := range writes {
		if store.IsMemory(path.Clean(p)) {
			v.names[strings.TrimSuffix(path.Base(p), ".md")] = true
		}
	}
	var faults []Finding
	err = filepath.WalkDir(repo, func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(f, ".md") {
			return nil
		}
		rel, err := filepath.Rel(repo, f)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if v.isGone(rel) || isIndex(rel) {
			return nil
		}
		if _, ok := writes[rel]; ok {
			return nil // checked below, with its new content
		}
		content, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		r, err := v.check(rel, content)
		if err != nil {
			return err
		}
		faults = append(faults, r.Faults...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("check the documents: %w", err)
	}
	written := slices.Sorted(maps.Keys(writes))
	for _, k := range written {
		p := path.Clean(k)
		if !strings.HasSuffix(p, ".md") || v.isGone(p) || isIndex(p) {
			continue
		}
		r, err := v.check(p, writes[k])
		if err != nil {
			return nil, err
		}
		faults = append(faults, r.Faults...)
	}
	return faults, nil
}

// isGone reports whether a path counts as removed.
func (v *verifier) isGone(p string) bool {
	for _, g := range v.gone {
		if p == g || strings.HasPrefix(p, g+"/") {
			return true
		}
	}
	return false
}

// VerifyContent checks one document as it would sit at path, a path relative to repo.
// References and glossaries resolve from that path, which need not exist yet.
func VerifyContent(repo, p string, content []byte) (Report, error) {
	v, err := newVerifier(repo)
	if err != nil {
		return Report{}, err
	}
	return v.check(path.Clean(p), content)
}

// VerifySet checks documents that are not on disk yet as one set, by their paths relative to repo.
// Each document sees the repo and the other documents of the set.
// A link or a [[name]] to another document of the set holds.
func VerifySet(repo string, files map[string][]byte) (map[string]Report, error) {
	v, err := newVerifier(repo)
	if err != nil {
		return nil, err
	}
	for p := range files {
		p = path.Clean(p)
		v.pending = append(v.pending, p)
		if store.IsMemory(p) {
			v.names[strings.TrimSuffix(path.Base(p), ".md")] = true
		}
	}
	reports := map[string]Report{}
	for p, content := range files {
		r, err := v.check(path.Clean(p), content)
		if err != nil {
			return nil, err
		}
		reports[p] = r
	}
	return reports, nil
}

// Verify checks one document, or every markdown document under a path relative to repo.
func Verify(repo, p string) (Report, error) {
	p = path.Clean(p)
	full := filepath.Join(repo, filepath.FromSlash(p))
	info, err := os.Stat(full)
	if err != nil {
		return Report{}, fmt.Errorf("verify %s: %w", p, err)
	}
	v, err := newVerifier(repo)
	if err != nil {
		return Report{}, err
	}
	if !info.IsDir() {
		content, err := os.ReadFile(full)
		if err != nil {
			return Report{}, fmt.Errorf("verify %s: %w", p, err)
		}
		return v.check(p, content)
	}
	var all Report
	err = filepath.WalkDir(full, func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(f, ".md") {
			return nil
		}
		rel, err := filepath.Rel(repo, f)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		r, err := v.check(filepath.ToSlash(rel), content)
		if err != nil {
			return err
		}
		all.Faults = append(all.Faults, r.Faults...)
		all.Warnings = append(all.Warnings, r.Warnings...)
		return nil
	})
	if err != nil {
		return all, fmt.Errorf("verify %s: %w", p, err)
	}
	return all, nil
}

func (v *verifier) check(p string, content []byte) (Report, error) {
	var r Report
	text := string(content)
	lineOf := func(offset int) int { return strings.Count(text[:offset], "\n") + 1 }
	masked := maskCode(text)

	spans := links(masked)
	for _, l := range spans {
		target := masked[l.target[0]:l.target[1]]
		if v.deadLink(p, target) {
			r.Faults = append(r.Faults, Finding{p, lineOf(l.start), fmt.Sprintf("dead link to %q", strings.Trim(target, "<>"))})
		}
	}
	own := ""
	if store.IsMemory(p) {
		own = strings.TrimSuffix(path.Base(p), ".md")
	}
	for _, m := range wikiRE.FindAllStringSubmatchIndex(masked, -1) {
		name := strings.TrimSpace(masked[m[2]:m[3]])
		if name != own && !v.names[name] {
			r.Faults = append(r.Faults, Finding{p, lineOf(m[0]), fmt.Sprintf("dead reference [[%s]]: no memory has that name", name)})
		}
	}
	if store.Kind(p) != "notes" && !isIndex(p) {
		warnings, err := v.formerNames(p, text, spans)
		if err != nil {
			return r, err
		}
		r.Warnings = append(r.Warnings, warnings...)
	}

	if path.Base(p) != "glossary.md" {
		b := []byte(masked)
		for _, l := range spans {
			copy(b[l.start:l.end], blank(masked[l.start:l.end]))
		}
		prose := string(b)
		for _, re := range []*regexp.Regexp{wikiRE, autoRE} {
			prose = re.ReplaceAllStringFunc(prose, blank)
		}
		words, err := v.forbiddenOn(p)
		if err != nil {
			return r, err
		}
		for _, f := range words {
			for _, m := range f.re.FindAllStringSubmatchIndex(prose, -1) {
				r.Faults = append(r.Faults, Finding{p, lineOf(m[2]), fmt.Sprintf("forbidden word %q: %s forbids it", f.phrase, f.glossary)})
			}
		}
	}

	// An indexed document needs a description, because its index line is the description.
	indexed := path.Base(p) == "SKILL.md" || store.IsMemory(p) || store.IsTopicIndex(p) || slices.Contains([]string{"plans", "notes"}, store.Kind(p))
	if !strings.HasPrefix(text, "---") {
		if indexed {
			r.Warnings = append(r.Warnings, Finding{p, 1, "the document has no frontmatter: add one with a description, so the index says when to read it"})
		}
		return r, nil
	}
	fm, _, err := store.ParseFrontmatter(strings.NewReader(text))
	if err != nil {
		r.Warnings = append(r.Warnings, Finding{p, 1, fmt.Sprintf("cannot read the frontmatter: %v", err)})
	} else if d, ok := fm.Values["description"]; ok {
		line := 1
		if i := strings.Index(text, "\ndescription:"); i >= 0 {
			line = lineOf(i + 1)
		}
		for _, w := range store.DescriptionWarnings(d) {
			r.Warnings = append(r.Warnings, Finding{p, line, w})
		}
	} else if indexed {
		r.Warnings = append(r.Warnings, Finding{p, 1, "the frontmatter has no description: add one, so the index says when to read it"})
	}
	return r, nil
}

// link is an inline link or a reference definition: its whole span and the span of its target.
type link struct {
	start, end int
	target     [2]int
}

// links finds the inline links and the reference definitions in s.
// A target may hold balanced parentheses and escaped characters.
func links(s string) []link {
	var found []link
	for _, m := range linkRE.FindAllStringIndex(s, -1) {
		i := m[1]
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		ts := i
		if i < len(s) && s[i] == '<' {
			end := strings.IndexAny(s[i:], ">\n")
			if end < 0 || s[i+end] != '>' {
				continue
			}
			i += end + 1
		} else {
			depth := 0
		scan:
			for ; i < len(s); i++ {
				switch c := s[i]; {
				case c == '\\':
					i++
				case c == '(':
					depth++
				case c == ')':
					if depth == 0 {
						break scan
					}
					depth--
				case c == ' ' || c == '\t' || c == '\n':
					break scan
				}
			}
		}
		te := min(i, len(s))
		closing := strings.IndexAny(s[te:], ")\n")
		if closing < 0 || s[te+closing] != ')' {
			continue
		}
		found = append(found, link{start: m[0], end: te + closing + 1, target: [2]int{ts, te}})
	}
	for _, m := range refDefRE.FindAllStringSubmatchIndex(s, -1) {
		found = append(found, link{start: m[0], end: m[1], target: [2]int{m[2], m[3]}})
	}
	return found
}

// deadLink reports whether a link target is a path that does not exist.
func (v *verifier) deadLink(p, target string) bool {
	t := strings.TrimSuffix(strings.TrimPrefix(target, "<"), ">")
	if t == "" || strings.HasPrefix(t, "#") || schemeRE.MatchString(t) {
		return false
	}
	if i := strings.IndexAny(t, "#?"); i >= 0 {
		t = t[:i]
	}
	if u, err := url.PathUnescape(t); err == nil {
		t = u
	}
	full := filepath.Join(v.repo, filepath.FromSlash(path.Dir(p)), filepath.FromSlash(t))
	if strings.HasPrefix(t, "/") {
		full = filepath.Join(v.repo, filepath.FromSlash(t))
	}
	if rel, err := filepath.Rel(v.repo, full); err == nil {
		rel = filepath.ToSlash(rel)
		if v.isGone(rel) {
			return true
		}
		for _, p := range v.pending {
			if p == rel || strings.HasPrefix(p, rel+"/") {
				return false
			}
		}
	}
	_, err := os.Stat(full)
	return err != nil
}

// forbiddenOn returns the forbidden words of the glossaries from the folder of p up to the repo root.
func (v *verifier) forbiddenOn(p string) ([]forbidden, error) {
	var all []forbidden
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		g := path.Join(dir, "glossary.md")
		words, ok := v.glossaries[g]
		if !ok {
			var err error
			words, err = readGlossary(v.repo, g)
			if err != nil {
				return nil, err
			}
			v.glossaries[g] = words
		}
		all = append(all, words...)
		if dir == "." || dir == "/" {
			return all, nil
		}
	}
}

// readGlossary reads the words of the Not: lists of a glossary, which may not exist.
func readGlossary(repo, g string) ([]forbidden, error) {
	content, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(g)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the glossary %s: %w", g, err)
	}
	var words []forbidden
	for _, m := range notRE.FindAllStringSubmatch(string(content), -1) {
		for _, phrase := range strings.Split(m[1], ",") {
			phrase = strings.Trim(strings.TrimSpace(phrase), "`\"")
			if phrase == "" {
				continue
			}
			parts := strings.Fields(phrase)
			for i := range parts {
				parts[i] = regexp.QuoteMeta(parts[i])
			}
			re := regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_-])(` + strings.Join(parts, `\s+`) + `)(?:$|[^\p{L}\p{N}_-])`)
			words = append(words, forbidden{phrase: strings.ToLower(phrase), glossary: g, re: re})
		}
	}
	return words, nil
}

// blank replaces every byte of s but a line break with a space, so offsets and lines stay.
func blank(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
	return string(b)
}

// run counts the bytes c at the start of s.
func run(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

// maskCode blanks fenced code blocks, indented code blocks and inline code spans.
func maskCode(s string) string {
	return maskSpans(maskBlocks(s))
}

// maskBlocks blanks fenced code blocks and indented code blocks, and keeps inline code.
func maskBlocks(s string) string {
	b := []byte(s)
	var fenceChar byte
	fenceLen := 0
	prevBlank, indented, list := true, false, false
	for start := 0; start < len(b); {
		end := start + strings.IndexByte(s[start:], '\n')
		if end < start {
			end = len(b)
		}
		line := s[start:end]
		trimmed := strings.TrimLeft(line, " ")
		isCode := true
		switch {
		case fenceLen > 0:
			if n := run(trimmed, fenceChar); n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "" {
				fenceLen = 0
			}
		case len(line)-len(trimmed) <= 3 && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")):
			fenceChar = trimmed[0]
			fenceLen = run(trimmed, fenceChar)
		case strings.TrimSpace(line) == "":
			isCode = false
			prevBlank = true
		case (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) && (prevBlank || indented) && !list:
			indented = true
			prevBlank = false
		default:
			isCode = false
			if !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "\t") {
				list = listRE.MatchString(trimmed)
			}
			prevBlank, indented = false, false
		}
		if isCode {
			copy(b[start:end], blank(line))
		}
		start = end + 1
	}
	return string(b)
}

// maskSpans blanks inline code spans.
func maskSpans(out string) string {
	b := []byte(out)
	for i := 0; i < len(b); {
		if b[i] != '`' {
			i++
			continue
		}
		n := 0
		for i+n < len(b) && b[i+n] == '`' {
			n++
		}
		closing := -1
		for j := i + n; j < len(b); {
			if b[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(b) && b[j+m] == '`' {
				m++
			}
			if m == n {
				closing = j + m
				break
			}
			j += m
		}
		if closing < 0 {
			i += n
			continue
		}
		copy(b[i:closing], blank(out[i:closing]))
		i = closing
	}
	return string(b)
}

// wordRE matches a run of the characters that a name can hold.
var wordRE = regexp.MustCompile(`[A-Za-z0-9_-]+`)

// formerNames warns on each word of text that equals a name that history gives a memory, which no memory in the
// repo holds now. It reads prose and inline code, but not code blocks, links or [[name]] references. A name of one
// word is too common to check, so a name counts only when it holds a hyphen.
func (v *verifier) formerNames(p, text string, spans []link) ([]Finding, error) {
	if err := v.readFormer(); err != nil {
		return nil, err
	}
	if len(v.former) == 0 {
		return nil, nil
	}
	b := []byte(maskBlocks(text))
	for _, l := range spans {
		copy(b[l.start:l.end], blank(string(b[l.start:l.end])))
	}
	prose := string(b)
	for _, re := range []*regexp.Regexp{wikiRE, autoRE} {
		prose = re.ReplaceAllStringFunc(prose, blank)
	}
	var found []Finding
	for _, m := range wordRE.FindAllStringIndex(prose, -1) {
		word := prose[m[0]:m[1]]
		now, ok := v.former[word]
		if !ok || v.names[word] {
			continue
		}
		line := strings.Count(text[:m[0]], "\n") + 1
		if now == "" {
			found = append(found, Finding{p, line, fmt.Sprintf("the text names %s, a memory that no longer exists", word)})
		} else {
			found = append(found, Finding{p, line, fmt.Sprintf("the text names %s, a memory that moved to %s", word, now)})
		}
	}
	return found, nil
}

// readFormer fills former once from git history: each memory that a commit deleted or renamed, by its name.
// A renamed memory maps to the path of its last rename when a memory sits there now. A repo without a .git
// has no history, so it has no former names.
func (v *verifier) readFormer() error {
	if v.formerRead {
		return nil
	}
	v.formerRead = true
	v.former = map[string]string{}
	if _, err := os.Stat(filepath.Join(v.repo, ".git")); err != nil {
		return nil
	}
	if _, err := store.Git(v.repo, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		return nil // no commit yet
	}
	out, err := store.Git(v.repo, "log", "-z", "--reverse", "-M", "--diff-filter=DR", "--name-status", "--format=", "HEAD")
	if err != nil {
		return fmt.Errorf("read the former memories: %w", err)
	}
	// lives maps each path at the end of the history to the paths that its document held before, oldest first.
	// A rename carries them along, and a deletion ends them.
	lives := map[string][]string{}
	final := map[string]string{} // each former path, with the path of its document now, or "" when it went
	fields := strings.Split(strings.Trim(out, "\x00\n"), "\x00")
	for i := 0; i < len(fields); i++ {
		status := strings.TrimSpace(fields[i])
		switch {
		case strings.HasPrefix(status, "R") && i+2 < len(fields):
			from, to := fields[i+1], fields[i+2]
			lives[to] = append(append(lives[from], from), lives[to]...)
			delete(lives, from)
			i += 2
		case status == "D" && i+1 < len(fields):
			gone := fields[i+1]
			for _, f := range append(lives[gone], gone) {
				final[f] = ""
			}
			delete(lives, gone)
			i++
		}
	}
	for now, before := range lives {
		_, err := os.Stat(filepath.Join(v.repo, filepath.FromSlash(now)))
		for _, f := range before {
			if err == nil && store.IsMemory(now) {
				final[f] = now
			} else {
				final[f] = ""
			}
		}
	}
	for f, now := range final {
		if !store.IsMemory(f) {
			continue
		}
		name := strings.TrimSuffix(path.Base(f), ".md")
		if !strings.Contains(name, "-") {
			continue
		}
		if prev, ok := v.former[name]; ok && prev != "" && now == "" {
			continue // a document that still lives wins over one that went
		}
		v.former[name] = now
	}
	return nil
}

// isIndex reports whether a path is the generated index of a level or of a topic folder, which a write
// regenerates. A README.md inside a skill is a document.
func isIndex(p string) bool {
	if path.Base(p) != "README.md" {
		return false
	}
	if store.IsTopicIndex(p) {
		return true
	}
	folder := path.Dir(p)
	if folder == "." {
		return true
	}
	_, ok := store.LevelOf(folder)
	return ok
}
