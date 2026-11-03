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

// InputError is an error in the text of an input. Its message quotes parts of the input,
// so Redacted gives the same message with each quoted part replaced.
type InputError struct {
	format string
	args   []any
}

// inputErrorf returns an InputError. Each string argument is a part of the input, and every other argument is not.
func inputErrorf(format string, args ...any) error {
	return &InputError{format: format, args: args}
}

func (e *InputError) Error() string {
	return fmt.Sprintf(e.format, e.args...)
}

// Unwrap returns the error that the message holds, if any.
func (e *InputError) Unwrap() error {
	for _, a := range e.args {
		if err, ok := a.(error); ok {
			return err
		}
	}
	return nil
}

// Redacted returns the message with every part of the input replaced by [input].
func (e *InputError) Redacted() string {
	args := make([]any, len(e.args))
	for i, a := range e.args {
		switch v := a.(type) {
		case *InputError:
			args[i] = v.Redacted()
		case string:
			args[i] = "[input]"
		default:
			args[i] = a
		}
	}
	return fmt.Sprintf(e.format, args...)
}

// Frontmatter is the header of a document: key: value lines, and lists of plain or quoted items.
type Frontmatter struct {
	Keys   []string // the keys in the order of the document
	Values map[string]string
	Lists  map[string][]string
}

// ParseFrontmatter reads the frontmatter of a document and returns it with the body.
// It reads leniently: a plain value may hold ": " or " #", and a value may be single-quoted.
// It reads a block list and a flow list, and refuses any other YAML, such as a nested map.
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
		indented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		if item, ok := strings.CutPrefix(strings.TrimLeft(line, " "), "- "); ok && list != "" {
			v, err := parseValue(item)
			if err != nil {
				return fm, "", fmt.Errorf("frontmatter line %d: %w", n, err)
			}
			fm.Lists[list] = append(fm.Lists[list], v)
			continue
		}
		if indented && list != "" {
			return fm, "", inputErrorf("frontmatter line %d: a nested map under %q, which memcore does not read", n, list)
		}
		key, raw, ok := strings.Cut(line, ":")
		if !ok || !keyRE.MatchString(key) {
			return fm, "", inputErrorf("frontmatter line %d: want key: value, got %q", n, line)
		}
		if _, dup := fm.Values[key]; dup {
			return fm, "", inputErrorf("frontmatter line %d: key %q appears twice", n, key)
		}
		if _, dup := fm.Lists[key]; dup {
			return fm, "", inputErrorf("frontmatter line %d: key %q appears twice", n, key)
		}
		raw = strings.TrimSpace(raw)
		list = ""
		fm.Keys = append(fm.Keys, key)
		switch {
		case raw == "":
			fm.Lists[key] = nil
			list = key
		case raw == "[]":
			fm.Lists[key] = nil
		case strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]"):
			items, err := parseFlow(raw)
			if err != nil {
				return fm, "", inputErrorf("frontmatter line %d: key %q: %s", n, key, err)
			}
			fm.Lists[key] = items
		default:
			v, err := parseValue(raw)
			if err != nil {
				return fm, "", inputErrorf("frontmatter line %d: key %q: %s", n, key, err)
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

// parseValue reads a value: plain, double-quoted or single-quoted.
// A plain value may hold ": " or " #", because memcore reads input leniently and writes it back canonically.
func parseValue(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty value")
	}
	if strings.HasPrefix(raw, `"`) {
		v, err := unquote(raw)
		if err != nil {
			return "", inputErrorf("bad double-quoted value %s: %s", raw, err)
		}
		return v, nil
	}
	if strings.HasPrefix(raw, "'") {
		v, err := unquoteSingle(raw)
		if err != nil {
			return "", inputErrorf("bad single-quoted value %s: %s", raw, err)
		}
		return v, nil
	}
	if indicator(raw) {
		return "", inputErrorf("value %q is not plain: put it in double quotes", raw)
	}
	return raw, nil
}

// indicator reports whether a value starts like YAML that is not a scalar, such as a list or a map.
func indicator(v string) bool {
	return strings.ContainsAny(v[:1], `[]{}|>&*!%@#`+"`") ||
		slices.Contains([]string{"-", "?", ":"}, v) ||
		strings.HasPrefix(v, "- ") || strings.HasPrefix(v, "? ") || strings.HasPrefix(v, ": ")
}

// plain reports whether a value reads back the same as a plain value under the strict rules of YAML,
// which memcore writes by. The lenient parser never decides this.
func plain(v string) bool {
	if v == "" || v != strings.TrimSpace(v) || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	if strings.ContainsAny(v[:1], `"'`) || indicator(v) {
		return false
	}
	return !strings.Contains(v, ": ") && !strings.Contains(v, " #") && !strings.HasSuffix(v, ":")
}

// unquoteSingle decodes a YAML single-quoted value on one line, where two single quotes stand for one.
func unquoteSingle(raw string) (string, error) {
	if len(raw) < 2 || raw[len(raw)-1] != '\'' {
		return "", errors.New("no closing single quote")
	}
	s := raw[1 : len(raw)-1]
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\'' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 == len(s) || s[i+1] != '\'' {
			return "", errors.New("a single quote inside the value is not doubled")
		}
		b.WriteByte('\'')
		i++
	}
	return b.String(), nil
}

// parseFlow reads a flow list on one line, such as [a, "b"], into its items.
func parseFlow(raw string) ([]string, error) {
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return nil, nil
	}
	var items []string
	start, quote := 0, byte(0)
	for i := 0; i <= len(inner); i++ {
		if i < len(inner) {
			c := inner[i]
			switch {
			case quote == 0 && (c == '"' || c == '\'') && strings.TrimSpace(inner[start:i]) == "":
				quote = c
			case quote == '"' && c == '\\':
				i++
			case quote == '\'' && c == '\'' && i+1 < len(inner) && inner[i+1] == '\'':
				i++ // a doubled single quote stays inside the item
			case c == quote:
				quote = 0
			}
			if c != ',' || quote != 0 {
				continue
			}
		}
		v, err := parseValue(strings.TrimSpace(inner[start:i]))
		if err != nil {
			return nil, err
		}
		items = append(items, v)
		start = i + 1
	}
	return items, nil
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
			return "", inputErrorf("bad escape \\%s", string(s[i]))
		}
		r, err := strconv.ParseUint(s[i+1:i+1+width], 16, 32)
		if err != nil || !utf8.ValidRune(rune(r)) {
			return "", inputErrorf("bad escape \\%s", s[i:i+1+width])
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

// formatValue writes a value plain when it reads back the same under the strict rules, and in double quotes otherwise.
func formatValue(v string) string {
	if plain(v) {
		return v
	}
	return quote(v)
}

// Bytes writes the frontmatter between its two --- lines: the keys in their order, each value canonical.
func (fm Frontmatter) Bytes() []byte {
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range fm.Keys {
		if items, ok := fm.Lists[k]; ok {
			if len(items) == 0 {
				fmt.Fprintf(&b, "%s: []\n", k)
				continue
			}
			fmt.Fprintf(&b, "%s:\n", k)
			for _, it := range items {
				fmt.Fprintf(&b, "  - %s\n", formatValue(it))
			}
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", k, formatValue(fm.Values[k]))
	}
	b.WriteString("---\n")
	return []byte(b.String())
}

// ParseMemory reads a memory file and checks its frontmatter.
func ParseMemory(r io.Reader) (Memory, error) {
	fm, body, err := ParseFrontmatter(r)
	if err != nil {
		return Memory{}, fmt.Errorf("parse the memory: %w", err)
	}
	for key := range fm.Values {
		if !slices.Contains([]string{"name", "description", "type"}, key) {
			return Memory{}, inputErrorf("parse the memory: unknown key %q", key)
		}
	}
	for key := range fm.Lists {
		if key != "sessions" {
			return Memory{}, inputErrorf("parse the memory: unknown key %q, or a list where a value belongs", key)
		}
	}
	for _, key := range []string{"name", "description", "type"} {
		if fm.Values[key] == "" {
			return Memory{}, fmt.Errorf("parse the memory: missing key %q", key)
		}
	}
	m := Memory{
		Name:        fm.Values["name"],
		Description: fm.Values["description"],
		Type:        fm.Values["type"],
		Sessions:    fm.Lists["sessions"],
		Body:        body,
	}
	if !nameRE.MatchString(m.Name) {
		return Memory{}, inputErrorf("parse the memory: name %q is not lower case with hyphens", m.Name)
	}
	if !slices.Contains(Types, m.Type) {
		return Memory{}, inputErrorf("parse the memory: type %q is not one of "+strings.Join(Types, ", "), m.Type)
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
