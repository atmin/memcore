package store

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Memory is one file that holds one fact. See docs/memory.md.
type Memory struct {
	Name        string
	Description string
	Type        string
	Sessions    []string
	Body        string
}

// Types are the four types of a memory.
var Types = []string{"user", "feedback", "project", "reference"}

var (
	nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	keyRE  = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

// Frontmatter is the header of a document: key: value lines, and lists of plain or quoted items.
type Frontmatter struct {
	Values map[string]string
	Lists  map[string][]string
}

// ParseFrontmatter reads the frontmatter of a document and returns it with the body.
// It accepts a plain or a double-quoted value and refuses any other YAML.
func ParseFrontmatter(r io.Reader) (Frontmatter, string, error) {
	fm := Frontmatter{Values: map[string]string{}, Lists: map[string][]string{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	if !sc.Scan() || strings.TrimRight(sc.Text(), " \r") != "---" {
		if err := sc.Err(); err != nil {
			return fm, "", fmt.Errorf("read the frontmatter: %w", err)
		}
		return fm, "", errors.New("no frontmatter: the first line is not ---")
	}
	list := "" // the key whose list the next items extend
	closed := false
	for n := 2; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), " \r")
		if line == "---" {
			closed = true
			break
		}
		if line == "" {
			continue
		}
		if item, ok := strings.CutPrefix(strings.TrimLeft(line, " "), "- "); ok && list != "" {
			v, err := parseValue(item)
			if err != nil {
				return fm, "", fmt.Errorf("frontmatter line %d: %w", n, err)
			}
			fm.Lists[list] = append(fm.Lists[list], v)
			continue
		}
		key, raw, ok := strings.Cut(line, ":")
		if !ok || !keyRE.MatchString(key) {
			return fm, "", fmt.Errorf("frontmatter line %d: want key: value, got %q", n, line)
		}
		if _, dup := fm.Values[key]; dup {
			return fm, "", fmt.Errorf("frontmatter line %d: key %q appears twice", n, key)
		}
		if _, dup := fm.Lists[key]; dup {
			return fm, "", fmt.Errorf("frontmatter line %d: key %q appears twice", n, key)
		}
		raw = strings.TrimSpace(raw)
		list = ""
		switch raw {
		case "":
			fm.Lists[key] = nil
			list = key
		case "[]":
			fm.Lists[key] = nil
		default:
			v, err := parseValue(raw)
			if err != nil {
				return fm, "", fmt.Errorf("frontmatter line %d: key %q: %w", n, key, err)
			}
			fm.Values[key] = v
		}
	}
	if err := sc.Err(); err != nil {
		return fm, "", fmt.Errorf("read the frontmatter: %w", err)
	}
	if !closed {
		return fm, "", errors.New("the frontmatter has no closing ---")
	}
	var body strings.Builder
	for sc.Scan() {
		body.WriteString(sc.Text())
		body.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return fm, "", fmt.Errorf("read the body: %w", err)
	}
	return fm, strings.TrimLeft(body.String(), "\n"), nil
}

// parseValue reads a plain or a double-quoted value.
func parseValue(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty value")
	}
	if strings.HasPrefix(raw, `"`) {
		v, err := unquote(raw)
		if err != nil {
			return "", fmt.Errorf("bad double-quoted value %s: %w", raw, err)
		}
		return v, nil
	}
	indicator := strings.ContainsAny(raw[:1], `'[]{}|>&*!%@#`+"`") ||
		slices.Contains([]string{"-", "?", ":"}, raw) ||
		strings.HasPrefix(raw, "- ") || strings.HasPrefix(raw, "? ") || strings.HasPrefix(raw, ": ")
	if indicator || strings.Contains(raw, ": ") || strings.Contains(raw, " #") || strings.HasSuffix(raw, ":") {
		return "", fmt.Errorf("value %q is not plain: put it in double quotes", raw)
	}
	return raw, nil
}

// yamlEscapes are the one-letter escapes of a YAML double-quoted value.
var yamlEscapes = map[byte]string{
	'0': "\x00", 'a': "\a", 'b': "\b", 't': "\t", '\t': "\t", 'n': "\n", 'v': "\v", 'f': "\f",
	'r': "\r", 'e': "\x1b", ' ': " ", '"': `"`, '/': "/", '\\': `\`,
	'N': "\u0085", '_': " ", 'L': " ", 'P': " ",
}

// unquote decodes a YAML double-quoted value on one line.
func unquote(raw string) (string, error) {
	if len(raw) < 2 || raw[len(raw)-1] != '"' {
		return "", errors.New("no closing double quote")
	}
	s := raw[1 : len(raw)-1]
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			return "", errors.New("a double quote inside the value is not escaped")
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i == len(s) {
			return "", errors.New("the value ends with a backslash")
		}
		if e, ok := yamlEscapes[s[i]]; ok {
			b.WriteString(e)
			continue
		}
		width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[s[i]]
		if width == 0 || i+1+width > len(s) {
			return "", fmt.Errorf("bad escape \\%c", s[i])
		}
		r, err := strconv.ParseUint(s[i+1:i+1+width], 16, 32)
		if err != nil || !utf8.ValidRune(rune(r)) {
			return "", fmt.Errorf("bad escape \\%s", s[i:i+1+width])
		}
		b.WriteRune(rune(r))
		i += width
	}
	return b.String(), nil
}

// quote writes a value in YAML double quotes, so any YAML reader reads it back.
func quote(v string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range strings.ToValidUTF8(v, "�") {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f || r == 0x85 || r == 0x2028 || r == 0x2029:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// formatValue writes a value plain when it reads back the same, and in double quotes otherwise.
func formatValue(v string) string {
	plain := v == strings.TrimSpace(v) && !strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f })
	if p, err := parseValue(v); plain && err == nil && p == v {
		return v
	}
	return quote(v)
}

// ParseMemory reads a memory file and checks its frontmatter.
func ParseMemory(r io.Reader) (Memory, error) {
	fm, body, err := ParseFrontmatter(r)
	if err != nil {
		return Memory{}, fmt.Errorf("parse the memory: %w", err)
	}
	for key := range fm.Values {
		if !slices.Contains([]string{"name", "description", "type"}, key) {
			return Memory{}, fmt.Errorf("parse the memory: unknown key %q", key)
		}
	}
	for key := range fm.Lists {
		if key != "sessions" {
			return Memory{}, fmt.Errorf("parse the memory: unknown key %q, or a list where a value belongs", key)
		}
	}
	for _, key := range []string{"name", "description", "type"} {
		if fm.Values[key] == "" {
			return Memory{}, fmt.Errorf("parse the memory: missing key %q", key)
		}
	}
	sessions, ok := fm.Lists["sessions"]
	if !ok {
		return Memory{}, fmt.Errorf("parse the memory: missing key %q", "sessions")
	}
	m := Memory{
		Name:        fm.Values["name"],
		Description: fm.Values["description"],
		Type:        fm.Values["type"],
		Sessions:    sessions,
		Body:        body,
	}
	if !nameRE.MatchString(m.Name) {
		return Memory{}, fmt.Errorf("parse the memory: name %q is not lower case with hyphens", m.Name)
	}
	if !slices.Contains(Types, m.Type) {
		return Memory{}, fmt.Errorf("parse the memory: type %q is not one of %s", m.Type, strings.Join(Types, ", "))
	}
	return m, nil
}

// Bytes returns the memory as a file, with the description in double quotes.
func (m Memory) Bytes() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: %s\ntype: %s\n", m.Name, quote(m.Description), m.Type)
	if len(m.Sessions) == 0 {
		b.WriteString("sessions: []\n")
	} else {
		b.WriteString("sessions:\n")
		for _, s := range m.Sessions {
			fmt.Fprintf(&b, "  - %s\n", formatValue(s))
		}
	}
	b.WriteString("---\n\n")
	b.WriteString(m.Body)
	return []byte(b.String())
}

// Warnings returns the warnings on the description of the memory.
func (m Memory) Warnings() []string {
	return DescriptionWarnings(m.Description)
}

// DescriptionWarnings checks that a description is 25 words at most and names a moment to read.
func DescriptionWarnings(description string) []string {
	var warnings []string
	words := strings.Fields(description)
	if len(words) > 25 {
		warnings = append(warnings, fmt.Sprintf("the description has %d words, over 25", len(words)))
	}
	moment := false
	for _, w := range words {
		w = strings.ToLower(strings.Trim(w, ".,;:!?\"'()"))
		if w == "when" || w == "before" {
			moment = true
		}
	}
	if !moment {
		warnings = append(warnings, `the description names no moment to read: it has no "when" and no "before"`)
	}
	return warnings
}
