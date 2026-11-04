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
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Source    string `json:"source"`
	Prompt    string `json:"prompt"`
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

// runHook runs a hook of Claude Code: session-start, prompt or session-end. It reads the hook input on standard input.
func runHook(env Env, args []string) error {
	if len(args) != 1 || !slices.Contains([]string{"session-start", "prompt", "session-end"}, args[0]) {
		return usageErr("usage: memcore hook session-start|prompt|session-end < input.json")
	}
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
