package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/atmin/memcore/internal/store"
)

// hookInput is the input that Claude Code gives a hook on standard input. See its docs, page hooks.
type hookInput struct {
	SessionID string    `json:"session_id"`
	Cwd       string    `json:"cwd"`
	Event     string    `json:"hook_event_name"`
	Source    string    `json:"source"`
	Prompt    string    `json:"prompt"`
	ToolName  string    `json:"tool_name"`
	ToolInput toolInput `json:"tool_input"`
	Error     string    `json:"error"` // the error of a failed tool call, of which only the start counts
}

// toolInput holds the fields of a tool call that name what the call reads: the command of Bash,
// the file of Read and the path of Grep.
type toolInput struct {
	Command  string `json:"command"`
	FilePath string `json:"file_path"`
	Path     string `json:"path"`
}

// marks are the words that the person adds to a prompt, in the order that the hook reports them.
var marks = []string{"again", "wrong", "stale"}

// markRE finds a mark in any case. findMarks then keeps only a mark that stands as a whole word.
var markRE = regexp.MustCompile(`(?i)#(again|wrong|stale)`)

// markLines are what the agent reads for each mark.
var markLines = map[string]string{
	"again": "The person marked #again: you were told this before. Find the memory with memcore orient or memcore show, and follow it. If no memory covers it, remember the fact now.",
	"wrong": "The person marked #wrong: a memory or your action is wrong. Find the memory with memcore orient or memcore show, and revise it. If no memory covers it, remember the right fact now.",
	"stale": "The person marked #stale: a memory is out of date. Find it with memcore orient or memcore show, then revise it or forget it.",
}

// runHook runs a hook of Claude Code: session-start, prompt, session-end or tool. It reads the hook input on standard input.
func runHook(env Env, args []string) error {
	if len(args) != 1 || !slices.Contains([]string{"session-start", "prompt", "session-end", "tool"}, args[0]) {
		return usageErr("usage: memcore hook session-start|prompt|session-end|tool < input.json")
	}
	env.event().Hook = args[0]
	raw, err := io.ReadAll(env.In)
	if err != nil {
		return fmt.Errorf("read the hook input: %w", err)
	}
	env.event().input(string(raw)) // the input holds the prompt, which no line of the log may hold
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("read the hook input: %w", err)
	}
	e := env.event()
	if in.SessionID != "" {
		e.Session, e.Agent = in.SessionID, "claude"
	}
	if args[0] == "tool" {
		return hookTool(e, in)
	}
	if in.Cwd != "" {
		env.Dir = in.Cwd
		e.Dir = tilde(in.Cwd)
		if h, err := home(); err == nil {
			if level, err := store.Level(in.Cwd, h); err == nil {
				e.Level = title(level)
			}
		}
	}

	switch args[0] {
	case "session-start":
		e.Source = in.Source
		// The commands of the agent run without the environment of the hook, so a memory repo other than
		// ~/memory reaches them through the environment file of the session.
		if repo, f := os.Getenv("MEMCORE_REPO"), os.Getenv("CLAUDE_ENV_FILE"); repo != "" && f != "" {
			if err := appendLine(f, "export MEMCORE_REPO="+shellQuote(repo)); err != nil {
				return err
			}
		}
		return runOrient(env, nil)
	case "prompt":
		found := findMarks(in.Prompt)
		for _, m := range marks {
			if found[m] {
				e.Marks = append(e.Marks, m)
				fmt.Fprintln(env.Out, markLines[m])
			}
		}
		return nil
	default: // session-end
		repo, err := openRepo()
		if err != nil {
			return err
		}
		e.started = true
		return startBackground(repo, "snapshot")
	}
}

// hookTool logs the documents that a tool call read. A call that read none is quiet, so Run logs nothing
// and starts no process for it. The level of cwd costs a git process, so only a call that read a document looks it up.
func hookTool(e *Event, in hookInput) error {
	repo, err := memoryRepo()
	if err != nil {
		return err
	}
	h, _ := home() // without a home, ~ and $HOME name nothing
	if ran(in) {
		e.Paths = toolReads(in, repo, h)
	}
	if len(e.Paths) == 0 {
		e.quiet = true
		return nil
	}
	if in.Cwd != "" {
		e.Dir = tilde(in.Cwd)
	}
	if in.Cwd != "" && h != "" {
		if level, err := store.Level(in.Cwd, h); err == nil {
			e.Level = title(level)
		}
	}
	return nil
}

// ran reports whether a tool call ran, so that it read what it names: any call after PostToolUse,
// and a Bash call after PostToolUseFailure whose command ran and exited.
func ran(in hookInput) bool {
	switch in.Event {
	case "PostToolUse":
		return true
	case "PostToolUseFailure":
		return in.ToolName == "Bash" && strings.HasPrefix(in.Error, "Exit code ")
	}
	return false
}

// findMarks returns the marks in a prompt. A mark stands as a whole word: no letter, digit or underscore
// touches it, in any script, and no # comes before it.
func findMarks(prompt string) map[string]bool {
	found := map[string]bool{}
	for _, m := range markRE.FindAllStringSubmatchIndex(prompt, -1) {
		before, _ := utf8.DecodeLastRuneInString(prompt[:m[0]])
		after, _ := utf8.DecodeRuneInString(prompt[m[1]:])
		if (m[0] > 0 && (wordRune(before) || before == '#')) || (m[1] < len(prompt) && wordRune(after)) {
			continue
		}
		found[strings.ToLower(prompt[m[2]:m[3]])] = true
	}
	return found
}

// wordRune reports whether a character belongs to a word: a letter or a digit of any script, or an underscore.
func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// appendLine appends one line to a file, and creates the file when it is missing.
func appendLine(p, line string) error {
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("write the environment of the session: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("write the environment of the session: %w", err)
	}
	return nil
}

// shellQuote quotes a value for a POSIX shell, in single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
