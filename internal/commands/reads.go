package commands

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// toolReads returns the documents of the memory repo that a tool call reads, by their paths in the repo, sorted.
// It reads a Bash command as a shell does, in a simple form, and keeps no text of the call.
// A document is a regular file of the working tree, outside .git, with no symlink on its path.
func toolReads(in hookInput, repo, home string) []string {
	r := &reader{repo: filepath.Clean(repo), home: home, found: map[string]bool{}}
	switch in.ToolName {
	case "Bash":
		r.bash(in.ToolInput.Command, in.Cwd)
	case "Read":
		r.file(in.ToolInput.FilePath, in.Cwd)
	case "Grep":
		r.file(in.ToolInput.Path, in.Cwd)
	}
	if len(r.found) == 0 {
		return nil
	}
	paths := make([]string, 0, len(r.found))
	for p := range r.found {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// options are the options of a command that reads files, as getopt parses them.
type options struct {
	flags   string // the letters that take no argument
	args    string // the letters that take an argument
	digits  bool   // a dash with digits, as -20, is an option
	pattern bool   // without -e or -f, the first operand is the pattern or the script. -f names a file that the command reads
}

// readingCommands are the commands whose operands name the files that they read. Any other command reads nothing.
var readingCommands = map[string]options{
	"cat":  {flags: "n"},
	"head": {flags: "q", args: "nc", digits: true},
	"tail": {flags: "q", args: "nc", digits: true},
	"wc":   {flags: "lwcm"},
	"sed":  {flags: "nEr", args: "ef", pattern: true},
	"awk":  {args: "Fvf", pattern: true},
	"grep": {flags: "nivwxclohHsqEFr", args: "efABCm", pattern: true},
	"rg":   {flags: "nivwxcloHsqF", args: "efABCm", pattern: true},
}

// char is one character of a shell word. plain is true when no quote or backslash protects the character.
// Then it keeps its meaning to the shell, as * and ~ do. A $ in double quotes stays plain.
// quote numbers the quotes or the backslash that protect the character, and is 0 outside them.
// So the name of a variable ends where its quotes end.
type char struct {
	r     rune
	plain bool
	quote int
}

type word []char

// text returns the word without its quotes.
func (w word) text() string {
	var b strings.Builder
	for _, c := range w {
		b.WriteRune(c.r)
	}
	return b.String()
}

// allPlain reports whether no quote or backslash protects any character of the word.
func (w word) allPlain() bool {
	for _, c := range w {
		if !c.plain {
			return false
		}
	}
	return true
}

// token is a word, when op is "", or an operator: a separator or a redirection.
type token struct {
	op   string
	word word
}

// redirections are the operators that take the next word. Only the word after < names a file that the command reads.
var redirections = map[string]bool{"<": true, "<<": true, "<<-": true, "<<<": true, ">": true, ">>": true, ">&": true, "<&": true, "&>": true, "&>>": true}

// heredoc is a here-document whose lines start after the current line.
type heredoc struct {
	end  string
	tabs bool // <<- strips the leading tabs of each line
}

// lex splits a command into words and operators. It drops comments and the lines of heredocs.
func lex(command string) []token {
	s := []rune(command)
	var toks []token
	var cur word
	started := false // a word has started, maybe as empty quotes
	var docs []heredoc
	quote := 0 // the number of the last quote or backslash
	endWord := func() {
		if !started {
			return
		}
		if n := len(toks); n > 0 && (toks[n-1].op == "<<" || toks[n-1].op == "<<-") {
			docs = append(docs, heredoc{end: cur.text(), tabs: toks[n-1].op == "<<-"})
		}
		toks = append(toks, token{word: cur})
		cur, started = nil, false
	}
	operator := func(op string) {
		if redirections[op] && len(cur) > 0 && cur.allPlain() && strings.Trim(cur.text(), "0123456789") == "" {
			cur, started = nil, false // the number of a file descriptor, as 2 in 2>/dev/null
		}
		endWord()
		toks = append(toks, token{op: op})
	}
	at := func(i int, prefix string) bool {
		return strings.HasPrefix(string(s[i:min(len(s), i+len(prefix))]), prefix)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			switch {
			case i+1 < len(s) && s[i+1] == '\n':
				i++ // a line that continues
			case i+1 < len(s):
				quote++
				cur, started = append(cur, char{s[i+1], false, quote}), true
				i++
			}
		case c == '\'':
			started = true
			quote++
			for i++; i < len(s) && s[i] != '\''; i++ {
				cur = append(cur, char{s[i], false, quote})
			}
		case c == '"':
			started = true
			quote++
			in := quote
			for i++; i < len(s) && s[i] != '"'; i++ {
				switch {
				case s[i] == '\\' && i+1 < len(s) && s[i+1] == '\n':
					i++
				case s[i] == '\\' && i+1 < len(s):
					quote++
					cur = append(cur, char{s[i+1], false, quote})
					i++
				default:
					cur = append(cur, char{s[i], s[i] == '$', in})
				}
			}
		case c == '#' && !started:
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
		case c == ' ' || c == '\t':
			endWord()
		case c == '\n':
			endWord()
			toks = append(toks, token{op: "\n"})
			for _, d := range docs {
				for i+1 < len(s) {
					j := i + 1
					for j < len(s) && s[j] != '\n' {
						j++
					}
					line := string(s[i+1 : j])
					i = j
					if d.tabs {
						line = strings.TrimLeft(line, "\t")
					}
					if line == d.end {
						break
					}
				}
			}
			docs = nil
		case c == ';' || c == '(' || c == ')' || c == '`':
			operator(string(c))
		case c == '&':
			switch {
			case at(i, "&&"):
				operator("&&")
				i++
			case at(i, "&>>"):
				operator("&>>")
				i += 2
			case at(i, "&>"):
				operator("&>")
				i++
			default:
				operator("&")
			}
		case c == '|':
			if at(i, "||") {
				operator("||")
			} else {
				operator("|")
			}
			if at(i, "||") || at(i, "|&") {
				i++
			}
		case c == '<':
			op := "<"
			for _, o := range []string{"<<<", "<<-", "<<", "<&"} {
				if at(i, o) {
					op = o
					break
				}
			}
			operator(op)
			i += len(op) - 1
		case c == '>':
			op := ">"
			for _, o := range []string{">>", ">&", ">|"} {
				if at(i, o) {
					op = o
					break
				}
			}
			i += len(op) - 1
			if op == ">|" {
				op = ">"
			}
			operator(op)
		default:
			cur, started = append(cur, char{c, true, 0}), true
		}
	}
	endWord()
	return toks
}

// reader collects the documents that one tool call reads.
type reader struct {
	repo, home string
	found      map[string]bool
}

// segment is one simple command: its words, and the words after <, which it reads too.
type segment struct {
	words  []word
	inputs []word
}

// bash finds the documents that a command reads. The folder of relative words starts at cwd,
// and "" stands for a folder that the reader does not know.
func (r *reader) bash(command, cwd string) {
	type frame struct{ dir, start string }
	var stack []frame
	dir := cwd
	start := dir // the folder at the start of the current list
	var seg segment
	pending := "" // the redirection that takes the next word
	piped := false
	backtick := false
	end := func(sep string) {
		r.run(seg, &dir, piped || sep == "|")
		seg, pending, piped = segment{}, "", sep == "|"
		switch sep {
		case "&":
			dir = start // the list runs in the background, so the folder after it is the folder before it
		case "(":
			stack = append(stack, frame{dir, start})
		case ")":
			if n := len(stack); n > 0 {
				dir, start = stack[n-1].dir, stack[n-1].start
				stack = stack[:n-1]
				return
			}
		case "`": // a pair of backticks runs its command in a subshell, as a parenthesis does
			backtick = !backtick
			if backtick {
				stack = append(stack, frame{dir, start})
			} else if n := len(stack); n > 0 {
				dir, start = stack[n-1].dir, stack[n-1].start
				stack = stack[:n-1]
				return
			}
		case "&&", "||", "|":
			return
		}
		start = dir
	}
	for _, t := range lex(command) {
		switch {
		case t.op == "" && pending == "":
			seg.words = append(seg.words, t.word)
		case t.op == "":
			if pending == "<" {
				seg.inputs = append(seg.inputs, t.word)
			}
			pending = ""
		case redirections[t.op]:
			pending = t.op
		default:
			end(t.op)
		}
	}
	end("")
}

// run applies one segment. A cd changes the folder, unless it runs next to a |. A reader reads its files.
func (r *reader) run(seg segment, dir *string, piped bool) {
	words := seg.words
	for len(words) > 0 && (assignment(words[0]) || keyword(words[0])) {
		words = words[1:]
	}
	if len(words) == 0 {
		return
	}
	name := words[0].text()
	if name == "cd" {
		if !piped {
			*dir = r.cd(words[1:], *dir)
		}
		return
	}
	opts, ok := readingCommands[path.Base(name)]
	if !ok {
		return
	}
	files, ok := operands(opts, words[1:])
	if !ok {
		return
	}
	for _, w := range append(files, seg.inputs...) {
		if p, ok := r.resolve(w, *dir); ok {
			r.add(p)
		}
	}
}

// assignment reports whether a word is NAME=value.
func assignment(w word) bool {
	for i, c := range w {
		switch {
		case !c.plain:
			return false
		case c.r == '=':
			return i > 0
		case c.r == '_' || 'a' <= c.r && c.r <= 'z' || 'A' <= c.r && c.r <= 'Z':
		case '0' <= c.r && c.r <= '9' && i > 0:
		default:
			return false
		}
	}
	return false
}

// keyword reports whether a word is do, then or else, which a command word follows.
func keyword(w word) bool {
	t := w.text()
	return w.allPlain() && (t == "do" || t == "then" || t == "else")
}

// cd returns the folder after cd: home without a word, and "" when the folder does not resolve.
func (r *reader) cd(args []word, dir string) string {
	switch {
	case len(args) == 0:
		return r.home
	case len(args) > 1 || args[0].text() == "-":
		return ""
	}
	p, ok := r.resolve(args[0], dir)
	if !ok {
		return ""
	}
	if p, ok = logical(p); !ok {
		return "" // cd fails, and the reader does not follow the folder that the shell keeps
	}
	return p
}

// operands returns the words that name files that a command reads, by its options.
// It returns false when a long option or a letter outside the table makes the command unknown.
func operands(opts options, args []word) ([]word, bool) {
	var files, rest []word
	script, ended := false, false
	for i := 0; i < len(args); i++ {
		a := args[i].text()
		switch {
		case ended || a == "-" || !strings.HasPrefix(a, "-"):
			rest = append(rest, args[i])
			continue
		case a == "--":
			ended = true
			continue
		case strings.HasPrefix(a, "--"):
			return nil, false
		case opts.digits && strings.Trim(a[1:], "0123456789") == "":
			continue
		}
		letters := args[i][1:]
		for j, l := range letters {
			if strings.ContainsRune(opts.flags, l.r) {
				continue
			}
			if !strings.ContainsRune(opts.args, l.r) {
				return nil, false
			}
			// The letter takes the rest of its word as its argument, or else the next word.
			arg := append(word{}, letters[j+1:]...)
			if len(arg) > 0 {
				arg[0].plain = arg[0].plain && arg[0].r != '~' // no ~ stands for home inside a word
			} else if i+1 < len(args) {
				i++
				arg = args[i]
			} else {
				return nil, false
			}
			if opts.pattern && (l.r == 'e' || l.r == 'f') {
				script = true
				if l.r == 'f' {
					files = append(files, arg)
				}
			}
			break
		}
	}
	if opts.pattern && !script && len(rest) > 0 {
		rest = rest[1:]
	}
	return append(files, rest...), true
}

// resolve returns the absolute path that a word names, or false when the reader cannot know it.
// The path keeps its . and .. parts, which add checks.
func (r *reader) resolve(w word, dir string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(w); i++ {
		c := w[i]
		if !c.plain {
			b.WriteRune(c.r)
			continue
		}
		switch c.r {
		case '*', '?', '[':
			return "", false
		case '~':
			if i > 0 {
				b.WriteRune(c.r)
				continue
			}
			if r.home == "" || len(w) > 1 && !(w[1].r == '/' && w[1].plain) {
				return "", false
			}
			b.WriteString(r.home)
		case '$':
			var name strings.Builder // the text after $, up to the end of its quotes
			for _, n := range w[i+1:] {
				if n.quote != c.quote {
					break
				}
				name.WriteRune(n.r)
			}
			rest := name.String()
			switch {
			case r.home == "":
				return "", false
			case strings.HasPrefix(rest, "{HOME}"):
				i += len("{HOME}")
			case strings.HasPrefix(rest, "HOME") && (len(rest) == 4 || !nameChar(rest[4])):
				i += len("HOME")
			default:
				return "", false
			}
			b.WriteString(r.home)
		default:
			b.WriteRune(c.r)
		}
	}
	p := b.String()
	if p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		if dir == "" {
			return "", false
		}
		p = dir + string(filepath.Separator) + p
	}
	return p, true
}

// nameChar reports whether a byte can continue the name of a shell variable.
func nameChar(b byte) bool {
	return b == '_' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}

// file adds the path of a Read or a Grep call. A relative path starts at cwd.
func (r *reader) file(p, cwd string) {
	if p == "" {
		return
	}
	if !filepath.IsAbs(p) {
		if cwd == "" {
			return
		}
		p = cwd + string(filepath.Separator) + p
	}
	r.add(p)
}

// physical cleans an absolute path as the system resolves it. It returns false when a .. leaves a symlink.
// The reason is that the parent of the target of the symlink is not the folder that the path names.
// It also returns false when a /, a . or a .. follows a part that is no folder. An example is x.md/.
// The system then opens nothing.
func physical(p string) (string, bool) {
	return resolveDots(p, os.Lstat) // a symlink is no folder to Lstat
}

// logical cleans an absolute path as cd -L does. So a .. leaves a symlink to a folder for the folder that holds
// the symlink. It returns false when a /, a . or a .. follows a part that is no folder.
// An example is x.md/.., where cd fails.
func logical(p string) (string, bool) {
	return resolveDots(p, os.Stat)
}

// resolveDots removes the empty, . and .. parts of an absolute path. Each must follow a folder, and before a ..
// the parent function decides what a folder is.
func resolveDots(p string, parent func(string) (os.FileInfo, error)) (string, bool) {
	cur := string(filepath.Separator)
	for i, part := range strings.Split(p, string(filepath.Separator)) {
		switch part {
		case "", ".":
			if i == 0 {
				continue // the root before the first separator
			}
			if info, err := os.Stat(cur); err != nil || !info.IsDir() {
				return "", false
			}
		case "..":
			if info, err := parent(cur); err != nil || !info.IsDir() {
				return "", false
			}
			cur = filepath.Dir(cur)
		default:
			cur = filepath.Join(cur, part)
		}
	}
	return cur, true
}

// add records an absolute path when it is a document of the memory repo. It records the path as the repo
// spells it.
func (r *reader) add(p string) {
	p, ok := physical(p)
	if !ok {
		return
	}
	parts, ok := r.below(p)
	if !ok {
		return
	}
	for _, part := range parts {
		if strings.EqualFold(part, ".git") {
			return
		}
	}
	dir := r.repo
	for i, part := range parts {
		if parts[i], ok = spelling(dir, part); !ok {
			return
		}
		dir = filepath.Join(dir, parts[i])
	}
	rel := strings.Join(parts, "/")
	if noSymlink(r.repo, rel) != nil {
		return
	}
	if info, err := os.Lstat(filepath.Join(r.repo, filepath.FromSlash(rel))); err != nil || !info.Mode().IsRegular() {
		return
	}
	r.found[rel] = true
}

// below returns the parts of a clean absolute path below the memory repo. It returns false when the path lies
// outside the repo.
// A file system that ignores case also opens the repo by another casing of its path.
func (r *reader) below(p string) ([]string, bool) {
	sep := string(filepath.Separator)
	if rel, err := filepath.Rel(r.repo, p); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+sep) {
		return strings.Split(rel, sep), true
	}
	n := strings.Count(r.repo, sep) + 1 // the parts of the repo, the empty one before the first separator included
	parts := strings.Split(p, sep)
	if len(parts) <= n {
		return nil, false
	}
	if top := strings.Join(parts[:n], sep); !strings.EqualFold(top, r.repo) || !sameFile(top, r.repo) {
		return nil, false
	}
	return parts[n:], true
}

// spelling returns the name by which a folder holds part. A file system that ignores case opens a file by any
// casing of its name. So the log takes the name of the entry, and names each document one way.
// It returns false when no entry of the folder is part in another casing.
func spelling(dir, part string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	match := ""
	for _, e := range entries {
		switch {
		case e.Name() == part:
			return part, true
		case match == "" && strings.EqualFold(e.Name(), part):
			match = e.Name()
		}
	}
	if match == "" || !sameFile(filepath.Join(dir, match), filepath.Join(dir, part)) {
		return "", false
	}
	return match, true
}

// sameFile reports whether two paths name one file, without following a symlink at their ends.
func sameFile(a, b string) bool {
	ia, err := os.Lstat(a)
	if err != nil {
		return false
	}
	ib, err := os.Lstat(b)
	return err == nil && os.SameFile(ia, ib)
}
