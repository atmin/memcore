package readers

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// AutoMemory reads one file of the auto-memory of Claude Code, in the flat form or the metadata: form.
// It takes name, description, type and originSessionId from either level, and ignores every other key.
// It refuses a map below metadata:. It checks no value: the import does.
// On an error, the memory holds the values read before it, so the import keeps the name as an alias.
func AutoMemory(r io.Reader) (m store.Memory, err error) {
	values := map[string]string{}
	defer func() {
		if err != nil {
			m = store.Memory{Name: values["name"], Description: values["description"], Type: values["type"]}
		}
	}()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	if !sc.Scan() || strings.TrimRight(sc.Text(), " \r") != "---" {
		if err := sc.Err(); err != nil {
			return m, fmt.Errorf("read the auto-memory: %w", err)
		}
		return m, fmt.Errorf("read the auto-memory: no frontmatter, the first line is not ---")
	}
	block := ""  // the key without a value, whose indented lines follow
	last := ""   // the last key at the top level
	indent := -1 // the indent of the first line in metadata:
	closed := false
	for n := 2; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), " \r")
		if line == "---" {
			closed = true
			break
		}
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		depth := len(line) - len(trimmed)
		if depth > 0 {
			if block == "" && used(last) {
				return m, fmt.Errorf("read the auto-memory: line %d: %s holds a value over several lines, which memcore does not read", n, last)
			}
			if block != "metadata" || strings.HasPrefix(trimmed, "- ") {
				continue // a line of a key that memcore ignores
			}
			if indent < 0 {
				indent = depth
			}
			key, raw, _ := strings.Cut(trimmed, ":")
			if depth > indent || strings.TrimSpace(raw) == "" {
				return m, fmt.Errorf("read the auto-memory: line %d: a map below metadata:, which memcore does not read", n)
			}
			if err := keep(values, key, raw, n); err != nil {
				return m, err
			}
			continue
		}
		key, raw, ok := strings.Cut(line, ":")
		if !ok {
			return m, fmt.Errorf("read the auto-memory: line %d: want key: value", n)
		}
		block, last, indent = "", key, -1
		if strings.TrimSpace(raw) == "" {
			block = key
			continue
		}
		if err := keep(values, key, raw, n); err != nil {
			return m, err
		}
	}
	if err := sc.Err(); err != nil {
		return m, fmt.Errorf("read the auto-memory: %w", err)
	}
	if !closed {
		return m, fmt.Errorf("read the auto-memory: the frontmatter has no closing ---")
	}
	var body strings.Builder
	for sc.Scan() {
		body.WriteString(sc.Text())
		body.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return m, fmt.Errorf("read the auto-memory: %w", err)
	}
	m.Name, m.Description, m.Type = values["name"], values["description"], values["type"]
	if id := values["originSessionId"]; id != "" {
		m.Sessions = []string{"claude:" + id}
	}
	m.Body = strings.TrimLeft(body.String(), "\n")
	return m, nil
}

// used reports whether the import uses a key.
func used(key string) bool {
	return key == "name" || key == "description" || key == "type" || key == "originSessionId"
}

// keep reads the value of a key that the import uses, and skips any other key.
func keep(values map[string]string, key, raw string, n int) error {
	if used(key) {
		v, err := store.ParseValue(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("read the auto-memory: line %d: %w", n, err)
		}
		values[key] = v
	}
	return nil
}
