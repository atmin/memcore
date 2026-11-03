// Package guards holds the checks that stand between an agent and the memory repo.
package guards

import (
	"errors"
	"fmt"
	"io/fs"
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
	names      map[string]bool        // the names of the memories in the repo
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
		if !d.IsDir() && strings.HasSuffix(p, ".md") && filepath.Base(filepath.Dir(p)) == "memories" {
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

// VerifyContent checks one document as it would sit at path, a path relative to repo.
// References and glossaries resolve from that path, which need not exist yet.
func VerifyContent(repo, p string, content []byte) (Report, error) {
	v, err := newVerifier(repo)
	if err != nil {
		return Report{}, err
	}
	return v.check(path.Clean(p), content)
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
	if path.Base(path.Dir(p)) == "memories" {
		own = strings.TrimSuffix(path.Base(p), ".md")
	}
	for _, m := range wikiRE.FindAllStringSubmatchIndex(masked, -1) {
		name := strings.TrimSpace(masked[m[2]:m[3]])
		if name != own && !v.names[name] {
			r.Faults = append(r.Faults, Finding{p, lineOf(m[0]), fmt.Sprintf("dead reference [[%s]]: no memory has that name", name)})
		}
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
	indexed := path.Base(p) == "SKILL.md" || slices.Contains([]string{"memories", "plans", "notes"}, path.Base(path.Dir(p)))
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
	out := string(b)
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
