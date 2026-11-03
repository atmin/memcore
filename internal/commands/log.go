package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// ErrUnknown means that the table holds no command of that name.
var ErrUnknown = errors.New("unknown command")

// Event is one line of the use log, .git/memcore/log.jsonl. No field holds standard input.
type Event struct {
	Time     string         `json:"time"`
	Session  string         `json:"session,omitempty"`
	Agent    string         `json:"agent,omitempty"`
	Commit   string         `json:"commit,omitempty"`   // the commit of the memory repo when the command started
	Accepted string         `json:"accepted,omitempty"` // the accepted commit then, which serves skills and the voice
	Level    string         `json:"level,omitempty"`    // the level of the working directory, as ~ or ~/<level>
	Command  string         `json:"command"`
	Paths    []string       `json:"paths,omitempty"`
	Outcome  string         `json:"outcome"` // ok or error
	Error    string         `json:"error,omitempty"`
	Made     []string       `json:"made,omitempty"`
	Dropped  []string       `json:"dropped,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Folded   []string       `json:"folded,omitempty"`
	Pushed   []string       `json:"pushed,omitempty"`
	Words    map[string]int `json:"words,omitempty"` // the words that orient printed: the header, then each level

	inputs []string // text from standard input, which redact removes from the error
}

// Run runs one command of the table, then appends its event to the use log of the memory repo.
// A command outside a memory repo, such as a failed init, logs nothing.
func Run(env Env, name string, args []string) error {
	var cmd *Command
	for _, c := range Table() {
		if c.Name == name {
			cmd = &c
			break
		}
	}
	e := &Event{Command: name}
	if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		e.Session, e.Agent = id, "claude"
	}
	if h, err := home(); err == nil {
		if level, err := store.Level(env.Dir, h); err == nil {
			e.Level = title(level)
		}
	}
	repo, rerr := memoryRepo()
	if rerr == nil {
		e.Commit, e.Accepted = headCommit(repo), acceptedCommit(repo)
	}
	env.Event = e
	var err error
	switch {
	case cmd == nil:
		err = fmt.Errorf("%w %q", ErrUnknown, name)
	case slices.Contains(args, "--help") || slices.Contains(args, "-h"):
		_, err = io.WriteString(env.Out, help(*cmd))
	default:
		err = cmd.Run(env, args)
	}
	if isInput(err) {
		err = fmt.Errorf("%w; memcore help %s shows the input", err, name)
	}
	e.Time = time.Now().UTC().Format(time.RFC3339)
	e.Outcome = "ok"
	if err != nil {
		e.Outcome, e.Error = "error", redact(err, e.inputs)
	}
	if rerr == nil {
		if e.Commit == "" {
			e.Commit = headCommit(repo) // init made the first commit
		}
		if lerr := appendEvent(repo, e); lerr != nil {
			fmt.Fprintf(env.Err, "warning: %v\n", lerr)
		}
		if kerr := keep(repo, e); kerr != nil {
			fmt.Fprintf(env.Err, "warning: %v\n", kerr)
		}
	}
	return err
}

// title writes a level as orient titles it: ~ for the root, ~/<level> otherwise.
func title(level string) string {
	if level == "" {
		return "~"
	}
	return "~/" + level
}

// headCommit returns the commit at HEAD of the memory repo, or "" when it has none.
func headCommit(repo string) string {
	out, err := store.Git(repo, "rev-parse", "-q", "--verify", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// acceptedCommit returns the accepted commit of the memory repo, or "" when it has none.
func acceptedCommit(repo string) string {
	out, err := store.Git(repo, "rev-parse", "-q", "--verify", acceptedRef)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// input marks text that came from standard input, so the log never holds it, even inside an error.
func (e *Event) input(texts ...string) {
	for _, t := range texts {
		if t != "" {
			e.inputs = append(e.inputs, t)
		}
	}
}

// redact returns the message of an error without the parts of the input that it quotes,
// and without any text that the command marked as input.
func redact(err error, inputs []string) string {
	msg := err.Error()
	var ie *store.InputError
	if errors.As(err, &ie) {
		msg = strings.Replace(msg, ie.Error(), ie.Redacted(), 1)
	}
	sort.Slice(inputs, func(i, j int) bool { return len(inputs[i]) > len(inputs[j]) })
	for _, t := range inputs {
		msg = strings.ReplaceAll(msg, t, "[input]")
	}
	return msg
}

// appendEvent appends one event to the use log, when the memory repo exists.
func appendEvent(repo string, e *Event) error {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return nil
	}
	if err := os.MkdirAll(store.StateDir(repo), 0o755); err != nil {
		return fmt.Errorf("create the local state: %w", err)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode the event: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(store.StateDir(repo), "log.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open the use log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append to the use log: %w", err)
	}
	return nil
}

// keep makes a ref under refs/memcore/seen/ for each commit of an event, accepted or not,
// so no rewrite and no garbage collection loses a version that a session saw. push never sends these refs.
func keep(repo string, e *Event) error {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return nil
	}
	var b strings.Builder
	seen := map[string]bool{}
	for _, list := range [][]string{{e.Commit, e.Accepted}, e.Made, e.Dropped, e.Folded, e.Pushed} {
		for _, sha := range list {
			if sha != "" && !seen[sha] {
				seen[sha] = true
				fmt.Fprintf(&b, "update refs/memcore/seen/%s %s\n", sha, sha)
			}
		}
	}
	if b.Len() == 0 {
		return nil
	}
	if _, err := store.GitStdin(repo, b.String(), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("keep the logged commits: %w", err)
	}
	return nil
}
