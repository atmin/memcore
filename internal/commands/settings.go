package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// jsonNode is one JSON value that keeps the order of its keys and each number as written. So an edit of the
// settings leaves every other line as it was.
type jsonNode struct {
	kind  byte        // '{' an object, '[' an array, '"' a string, '0' a number, 't' true, 'f' false, 'n' null
	keys  []string    // the keys of an object, in order
	items []*jsonNode // the values of an object, one for each key, or the items of an array
	text  string      // a string, or a number as written
}

// parseSettings reads the settings of Claude Code, a JSON object. No input reads as an empty object.
func parseSettings(b []byte) (*jsonNode, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return &jsonNode{kind: '{'}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	n, err := decodeNode(dec)
	if err != nil {
		return nil, fmt.Errorf("read the settings: %w", err)
	}
	if n.kind != '{' {
		return nil, errors.New("read the settings: they are not a JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("read the settings: text follows the JSON object")
	}
	return n, nil
}

// readSettings reads a settings file, or an empty object when the file does not exist.
func readSettings(p string) (*jsonNode, error) {
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read the settings %s: %w", p, err)
	}
	return parseSettings(b)
}

func decodeNode(dec *json.Decoder) (*jsonNode, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		n := &jsonNode{kind: byte(t)}
		if t != '{' && t != '[' {
			return nil, fmt.Errorf("unexpected %q", t)
		}
		for dec.More() {
			if t == '{' {
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("a key that is not a string: %v", k)
				}
				n.keys = append(n.keys, key)
			}
			v, err := decodeNode(dec)
			if err != nil {
				return nil, err
			}
			n.items = append(n.items, v)
		}
		if _, err := dec.Token(); err != nil { // the closing delimiter
			return nil, err
		}
		return n, nil
	case string:
		return &jsonNode{kind: '"', text: t}, nil
	case json.Number:
		return &jsonNode{kind: '0', text: t.String()}, nil
	case bool:
		if t {
			return &jsonNode{kind: 't'}, nil
		}
		return &jsonNode{kind: 'f'}, nil
	default:
		return &jsonNode{kind: 'n'}, nil
	}
}

// encode writes the value as JSON.stringify(value, null, 2) does, with a newline at the end, as Claude Code writes it.
func (n *jsonNode) encode() []byte {
	var b bytes.Buffer
	n.write(&b, "")
	b.WriteByte('\n')
	return b.Bytes()
}

func (n *jsonNode) write(b *bytes.Buffer, indent string) {
	switch n.kind {
	case '{', '[':
		closing := map[byte]string{'{': "}", '[': "]"}[n.kind]
		if len(n.items) == 0 {
			b.WriteString(string(n.kind) + closing)
			return
		}
		b.WriteString(string(n.kind) + "\n")
		for i, item := range n.items {
			b.WriteString(indent + "  ")
			if n.kind == '{' {
				b.WriteString(quoteJSON(n.keys[i]) + ": ")
			}
			item.write(b, indent+"  ")
			if i < len(n.items)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + closing)
	case '"':
		b.WriteString(quoteJSON(n.text))
	case '0':
		b.WriteString(n.text)
	case 't':
		b.WriteString("true")
	case 'f':
		b.WriteString("false")
	default:
		b.WriteString("null")
	}
}

// compact writes the value as JSON without spaces, to compare two values.
func (n *jsonNode) compact() string {
	var b bytes.Buffer
	json.Compact(&b, n.encode())
	return b.String()
}

// quoteJSON writes a string as JSON, and leaves <, > and & as they are, as JSON.stringify does.
func quoteJSON(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// get returns the value of a key of an object, or nil.
func (n *jsonNode) get(key string) *jsonNode {
	if i := slices.Index(n.keys, key); i >= 0 {
		return n.items[i]
	}
	return nil
}

// set gives a key of an object a value, in place, or at the end when the key is new.
func (n *jsonNode) set(key string, v *jsonNode) {
	if i := slices.Index(n.keys, key); i >= 0 {
		n.items[i] = v
		return
	}
	n.keys = append(n.keys, key)
	n.items = append(n.items, v)
}

// child returns the value of a key, and creates it with kind when it is missing or of another kind.
func (n *jsonNode) child(key string, kind byte) *jsonNode {
	if c := n.get(key); c != nil && c.kind == kind {
		return c
	}
	c := &jsonNode{kind: kind}
	n.set(key, c)
	return c
}

// object builds an object from keys and values in turn.
func object(kv ...any) *jsonNode {
	n := &jsonNode{kind: '{'}
	for i := 0; i < len(kv); i += 2 {
		n.keys = append(n.keys, kv[i].(string))
		n.items = append(n.items, kv[i+1].(*jsonNode))
	}
	return n
}

func str(s string) *jsonNode { return &jsonNode{kind: '"', text: s} }

// initRecord is what init added to the settings of Claude Code, .git/memcore/init.json. --undo removes it.
type initRecord struct {
	Hooks      []hookAddition    `json:"hooks,omitempty"`
	Deny       []string          `json:"deny,omitempty"`
	AutoMemory *autoMemoryChange `json:"autoMemory,omitempty"`
	Created    []string          `json:"created,omitempty"` // the objects and arrays that init created, as hooks.SessionStart
}

// hookAddition is one matcher group that init added to the hooks of an event.
type hookAddition struct {
	Event string `json:"event"`
	Entry string `json:"entry"` // the group, as compact JSON
}

// autoMemoryChange is the value of autoMemoryEnabled before init set it to false.
type autoMemoryChange struct {
	Absent bool   `json:"absent,omitempty"` // the setting did not exist
	Before string `json:"before,omitempty"` // its value, as JSON
}

// hookEvent is a hook that init adds. It holds these values:
//   - an event of Claude Code
//   - the argument of memcore hook
//   - the matcher of its group
//
// An empty matcher runs the hook for every source of the event. A matcher of names and | runs it for those
// tools only.
type hookEvent struct {
	event, arg, matcher string
}

// hookEvents are the hooks that init adds.
var hookEvents = []hookEvent{
	{"SessionStart", "session-start", ""},
	{"UserPromptSubmit", "prompt", ""},
	{"SessionEnd", "session-end", ""},
	{"PostToolUse", "tool", "Bash|Read|Grep"},
	{"PostToolUseFailure", "tool", "Bash|Read|Grep"},
}

// claudeChanges adds the hooks, the deny rules, and autoMemoryEnabled false when autoOff, to the settings in place.
// It adds nothing that the settings hold already, and returns what it added.
func claudeChanges(tree *jsonNode, repo, home, exe string, autoOff bool) initRecord {
	var add initRecord
	// child creates what is missing, so the record notes each container before.
	created := func(parent *jsonNode, key, name string) {
		if parent.get(key) == nil {
			add.Created = append(add.Created, name)
		}
	}
	// A hook runs without the environment of init, so a memory repo other than ~/memory goes into its command.
	prefix := ""
	if filepath.Clean(repo) != filepath.Join(home, "memory") {
		prefix = "MEMCORE_REPO=" + shellQuote(repo) + " "
	}
	created(tree, "hooks", "hooks")
	hooks := tree.child("hooks", '{')
	for _, h := range hookEvents {
		command := prefix + "'" + exe + "' hook " + h.arg
		created(hooks, h.event, "hooks."+h.event)
		groups := hooks.child(h.event, '[')
		if hasCommand(groups, command, h.matcher) {
			continue
		}
		list := &jsonNode{kind: '[', items: []*jsonNode{object("type", str("command"), "command", str(command))}}
		group := object("hooks", list)
		if h.matcher != "" {
			group = object("matcher", str(h.matcher), "hooks", list)
		}
		groups.items = append(groups.items, group)
		add.Hooks = append(add.Hooks, hookAddition{Event: h.event, Entry: group.compact()})
	}
	created(tree, "permissions", "permissions")
	perms := tree.child("permissions", '{')
	created(perms, "deny", "permissions.deny")
	deny := perms.child("deny", '[')
	for _, rule := range denyRules(repo, home) {
		if !slices.ContainsFunc(deny.items, func(n *jsonNode) bool { return n.kind == '"' && n.text == rule }) {
			deny.items = append(deny.items, str(rule))
			add.Deny = append(add.Deny, rule)
		}
	}
	if autoOff {
		if cur := tree.get("autoMemoryEnabled"); cur == nil || cur.kind != 'f' {
			add.AutoMemory = &autoMemoryChange{Absent: cur == nil}
			if cur != nil {
				add.AutoMemory.Before = cur.compact()
			}
			tree.set("autoMemoryEnabled", &jsonNode{kind: 'f'})
		}
	}
	return add
}

// hasCommand reports whether a matcher group with the matcher runs command. A group with another matcher, such as
// startup or Bash, runs for other sources or tools. So it does not count.
func hasCommand(groups *jsonNode, command, matcher string) bool {
	for _, g := range groups.items {
		if g.kind != '{' || !sameMatcher(g.get("matcher"), matcher) {
			continue
		}
		if hs := g.get("hooks"); hs != nil {
			for _, h := range hs.items {
				if c := h.get("command"); h.kind == '{' && c != nil && c.text == command {
					return true
				}
			}
		}
	}
	return false
}

// sameMatcher reports whether the matcher of a group equals a matcher of hookEvents.
// An empty matcher equals an absent one, "" and "*", which all run the hook for every source.
func sameMatcher(m *jsonNode, matcher string) bool {
	switch {
	case m == nil:
		return matcher == ""
	case m.kind != '"':
		return false
	case m.text == "" || m.text == "*":
		return matcher == ""
	}
	return m.text == matcher
}

// denyRules are the permissions that keep an agent away from the memory repo and from the commands of the person.
// init gets no rule, because a rule cannot tell --dry-run apart. And memcore refuses an agent for the rest of init.
// push gets no rule, because an agent pushes memories, notes and plans when the person asks.
func denyRules(repo, home string) []string {
	edit := "Edit(/" + filepath.ToSlash(repo) + "/**)"
	if rel, err := filepath.Rel(home, repo); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		edit = "Edit(~/" + filepath.ToSlash(rel) + "/**)"
	}
	return []string{edit, "Bash(memcore drop *)", "Bash(memcore recover *)"}
}

// applySettings shows the diff of the settings and asks once. Then it records the additions, keeps the
// original, and writes the new settings. It does nothing, and asks nothing, when init adds nothing.
func applySettings(env Env, repo, settings string, tree *jsonNode, add initRecord) error {
	if len(add.Hooks) == 0 && len(add.Deny) == 0 && add.AutoMemory == nil {
		return nil
	}
	old, err := os.ReadFile(settings)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read the settings %s: %w", settings, err)
	}
	updated := tree.encode()
	diff, err := settingsDiff(old, updated, tilde(settings))
	if err != nil {
		return err
	}
	fmt.Fprint(env.Out, diff)
	if env.Confirm == nil {
		return errors.New("change the settings: no terminal to ask at")
	}
	ok, err := env.Confirm(fmt.Sprintf("Change %s as above? [y/N] ", tilde(settings)))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(env.Out, "%s stays as it was.\n", tilde(settings))
		return nil
	}
	// The person approved the diff of the file as it was. A change meanwhile would be lost, so it stops the write.
	now, err := os.ReadFile(settings)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read the settings %s: %w", settings, err)
	}
	if !bytes.Equal(now, old) {
		return fmt.Errorf("%s changed while you answered, so init changed nothing there: run init again", tilde(settings))
	}
	// The record comes first, so --undo knows each addition even when the write below fails.
	rec, err := readInitRecord(repo)
	if err != nil {
		return err
	}
	rec.merge(add)
	if err := writeInitRecord(repo, rec); err != nil {
		return err
	}
	if old != nil {
		kept := filepath.Join(store.StateDir(repo), "init", "settings-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".json")
		if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
			return fmt.Errorf("keep the original settings: %w", err)
		}
		if err := os.WriteFile(kept, old, 0o600); err != nil { // the settings may be private
			return fmt.Errorf("keep the original settings: %w", err)
		}
	}
	if err := writeAtomic(settings, updated); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Changed %s.\n", tilde(settings))
	return nil
}

// writeAtomic writes a file through a temporary file beside it, which it renames over the file.
func writeAtomic(p string, content []byte) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(p); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	tmp := p + ".memcore-tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}

// settingsDiff is the diff of two versions of the settings, from git diff --no-index, under one label.
func settingsDiff(old, updated []byte, label string) (string, error) {
	dir, err := os.MkdirTemp("", "memcore-settings-")
	if err != nil {
		return "", fmt.Errorf("compare the settings: %w", err)
	}
	defer os.RemoveAll(dir)
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	if err := os.WriteFile(a, old, 0o600); err != nil {
		return "", fmt.Errorf("compare the settings: %w", err)
	}
	if err := os.WriteFile(b, updated, 0o600); err != nil {
		return "", fmt.Errorf("compare the settings: %w", err)
	}
	// git diff exits 1 when the files differ, so only output without a hunk is an error.
	out, err := store.Git(dir, "diff", "--no-index", "--no-color", "--no-ext-diff", "--no-textconv", "--", a, b)
	i := strings.Index(out, "\n@@")
	if i < 0 {
		if err != nil {
			return "", fmt.Errorf("compare the settings: %w", err)
		}
		return "", nil
	}
	return "--- " + label + "\n+++ " + label + ", after the change\n" + out[i+1:], nil
}

// merge adds the additions to the record, each once. The first value of autoMemoryEnabled stays.
func (rec *initRecord) merge(add initRecord) {
	for _, h := range add.Hooks {
		if !slices.Contains(rec.Hooks, h) {
			rec.Hooks = append(rec.Hooks, h)
		}
	}
	for _, d := range add.Deny {
		if !slices.Contains(rec.Deny, d) {
			rec.Deny = append(rec.Deny, d)
		}
	}
	if rec.AutoMemory == nil {
		rec.AutoMemory = add.AutoMemory
	}
	for _, c := range add.Created {
		if !slices.Contains(rec.Created, c) {
			rec.Created = append(rec.Created, c)
		}
	}
}

// readInitRecord reads .git/memcore/init.json, or an empty record when it does not exist.
func readInitRecord(repo string) (initRecord, error) {
	var rec initRecord
	b, err := os.ReadFile(filepath.Join(store.StateDir(repo), "init.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return rec, fmt.Errorf("read the record of init: %w", err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, fmt.Errorf("read the record of init: %w", err)
	}
	return rec, nil
}

// writeInitRecord writes .git/memcore/init.json through a temporary file.
func writeInitRecord(repo string, rec initRecord) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("write the record of init: %w", err)
	}
	return writeAtomic(filepath.Join(store.StateDir(repo), "init.json"), append(b, '\n'))
}

// del removes a key of an object, when it holds the key.
func (n *jsonNode) del(key string) {
	if i := slices.Index(n.keys, key); i >= 0 {
		n.keys = slices.Delete(n.keys, i, i+1)
		n.items = slices.Delete(n.items, i, i+1)
	}
}

// pruneEmpty removes the key of an object whose value is an empty object or an empty array.
func (n *jsonNode) pruneEmpty(key string) {
	if c := n.get(key); c != nil && (c.kind == '{' || c.kind == '[') && len(c.items) == 0 {
		n.del(key)
	}
}

// undoSettings removes what init added to the settings, as the record says: each addition that is present and
// unchanged. It names each addition that changed since, and leaves it. It shows the diff and asks once.
func undoSettings(env Env, repo, settings string) error {
	rec, err := readInitRecord(repo)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(settings)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read the settings %s: %w", settings, err)
	}
	tree, err := parseSettings(old)
	if err != nil {
		return err
	}
	var left initRecord // the conflicts, which stay in the record
	var conflicts []string
	hooks := tree.get("hooks")
	for _, h := range rec.Hooks {
		var groups *jsonNode
		if hooks != nil && hooks.kind == '{' {
			groups = hooks.get(h.Event)
		}
		if groups == nil || groups.kind != '[' {
			continue // you removed it
		}
		if i := slices.IndexFunc(groups.items, func(g *jsonNode) bool { return g.compact() == h.Entry }); i >= 0 {
			groups.items = slices.Delete(groups.items, i, i+1)
			continue
		}
		// A hook that still runs memcore for the same event, by another path or in another form, changed since init.
		arg := hookArg(h.Entry)
		if arg != "" && slices.ContainsFunc(groups.items, func(g *jsonNode) bool { return runsHook(g, arg) }) {
			left.Hooks = append(left.Hooks, h)
			conflicts = append(conflicts, fmt.Sprintf("the %s hook of memcore changed since init", h.Event))
		}
	}
	if perms := tree.get("permissions"); perms != nil && perms.kind == '{' {
		if deny := perms.get("deny"); deny != nil && deny.kind == '[' {
			deny.items = slices.DeleteFunc(deny.items, func(n *jsonNode) bool { return n.kind == '"' && slices.Contains(rec.Deny, n.text) })
			// A rule for the same tool and the same command, written another way, changed since init.
			for _, rule := range rec.Deny {
				key := ruleKey(rule)
				if slices.ContainsFunc(deny.items, func(n *jsonNode) bool { return n.kind == '"' && strings.HasPrefix(n.text, key) }) {
					left.Deny = append(left.Deny, rule)
					conflicts = append(conflicts, fmt.Sprintf("the rule %s changed since init", rule))
				}
			}
		}
	}
	// Only a container that init created goes, and only when it is empty.
	for _, c := range slices.Backward(rec.Created) {
		parent, key := tree, c
		if p, k, ok := strings.Cut(c, "."); ok {
			parent, key = tree.get(p), k
		}
		if parent == nil || parent.kind != '{' {
			continue
		}
		parent.pruneEmpty(key)
		if parent.get(key) != nil {
			left.Created = append([]string{c}, left.Created...)
		}
	}
	if a := rec.AutoMemory; a != nil {
		switch cur := tree.get("autoMemoryEnabled"); {
		case cur == nil:
		case cur.kind == 'f' && a.Absent:
			tree.del("autoMemoryEnabled")
		case cur.kind == 'f':
			before, err := parseValueJSON(a.Before)
			if err != nil {
				return err
			}
			tree.set("autoMemoryEnabled", before)
		default:
			left.AutoMemory = a
			conflicts = append(conflicts, "autoMemoryEnabled changed since init")
		}
	}

	for _, c := range conflicts {
		fmt.Fprintf(env.Out, "%s, so --undo leaves it.\n", c)
	}
	updated := tree.encode()
	if old == nil || bytes.Equal(old, updated) {
		if len(conflicts) == 0 {
			fmt.Fprintf(env.Out, "%s holds nothing that init added.\n", tilde(settings))
		}
		return writeInitRecord(repo, left)
	}
	diff, err := settingsDiff(old, updated, tilde(settings))
	if err != nil {
		return err
	}
	fmt.Fprint(env.Out, diff)
	if env.Confirm == nil {
		return errors.New("undo the settings: no terminal to ask at")
	}
	ok, err := env.Confirm(fmt.Sprintf("Change %s as above? [y/N] ", tilde(settings)))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(env.Out, "%s stays as it was.\n", tilde(settings))
		return nil
	}
	// The person approved the diff of the file as it was. A change meanwhile would be lost, so it stops the write.
	if now, err := os.ReadFile(settings); err != nil || !bytes.Equal(now, old) {
		return fmt.Errorf("%s changed while you answered, so --undo changed nothing there: run it again", tilde(settings))
	}
	if err := writeAtomic(settings, updated); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Removed what init added to %s.\n", tilde(settings))
	return writeInitRecord(repo, left)
}

// parseValueJSON reads one JSON value of the record, such as the value of a setting before init.
func parseValueJSON(s string) (*jsonNode, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	n, err := decodeNode(dec)
	if err != nil {
		return nil, fmt.Errorf("read a value of the record of init: %w", err)
	}
	return n, nil
}

// hookArg returns the argument of memcore hook in a recorded matcher group, such as session-start.
func hookArg(entry string) string {
	g, err := parseSettings([]byte(entry))
	if err != nil {
		return ""
	}
	if hs := g.get("hooks"); hs != nil && len(hs.items) > 0 {
		if c := hs.items[0].get("command"); c != nil {
			if i := strings.LastIndex(c.text, " hook "); i >= 0 {
				return c.text[i+len(" hook "):]
			}
		}
	}
	return ""
}

// runsHook reports whether a matcher group runs memcore hook with the argument, by any path and in any form.
func runsHook(g *jsonNode, arg string) bool {
	if g.kind != '{' {
		return false
	}
	hs := g.get("hooks")
	if hs == nil {
		return false
	}
	for _, h := range hs.items {
		if c := h.get("command"); h.kind == '{' && c != nil && strings.Contains(c.text, "memcore") &&
			(strings.HasSuffix(c.text, " hook "+arg) || strings.Contains(c.text, " hook "+arg+" ")) {
			return true
		}
	}
	return false
}

// ruleKey is a rule without its pattern at the end: the tool and the command, as Bash(memcore drop.
func ruleKey(rule string) string {
	key := strings.TrimSuffix(rule, ")")
	for _, tail := range []string{" *", ":*", "/**"} {
		key = strings.TrimSuffix(key, tail)
	}
	return key
}
