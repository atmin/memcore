package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// hookJSON returns the input of a hook of Claude Code, as JSON.
func hookJSON(fields map[string]string) string {
	b, _ := json.Marshal(fields)
	return string(b)
}

// toolJSON returns the input of a hook after a tool call, as JSON. fields hold any other field, such as error.
func toolJSON(event, cwd, tool string, input map[string]string, fields map[string]any) string {
	all := map[string]any{"session_id": "s9", "cwd": cwd, "hook_event_name": event, "tool_name": tool, "tool_input": input}
	for k, v := range fields {
		all[k] = v
	}
	b, _ := json.Marshal(all)
	return string(b)
}

// logLines returns the lines of the use log, as text.
func logLines(t *testing.T, repo string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(store.StateDir(repo), "log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func TestHook(t *testing.T) {
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "small-commits", "Read before you commit: one change per commit.")

	t.Run("session-start prints orient and logs the start", func(t *testing.T) {
		in := hookJSON(map[string]string{"session_id": "s9", "cwd": dev, "hook_event_name": "SessionStart", "source": "startup"})
		out, err := run(t, home, in, "hook", "session-start")
		if err != nil {
			t.Fatalf("hook: %v\n%s", err, out)
		}
		if !strings.Contains(out, "memcore keeps the long-term memory") || !strings.Contains(out, "small-commits") {
			t.Errorf("output =\n%s\nwant orient of ~/dev", out)
		}
		e := last(t, repo)
		if e.Command != "hook" || e.Hook != "session-start" || e.Session != "s9" || e.Agent != "claude" || e.Dir != "~/dev" || e.Source != "startup" || e.Level != "~/dev" {
			t.Errorf("event = %+v", e)
		}
		if e.Commit == "" || len(e.Words) == 0 {
			t.Errorf("event = %+v, want the commit and the words of orient", e)
		}
	})

	marks := []struct {
		name   string
		prompt string
		want   []string
	}{
		{"one mark", "That is #wrong, see the docs", []string{"wrong"}},
		{"again", "#again you forgot the tests", []string{"again"}},
		{"stale", "the deploy note is #stale", []string{"stale"}},
		{"two marks, in capitals and before punctuation", "#AGAIN and (#stale).", []string{"again", "stale"}},
		{"a mark twice", "#wrong #wrong", []string{"wrong"}},
		{"near matches", "#wrongly a#wrong #again2 x", nil},
		{"letters beyond ASCII next to a mark", "#wrongé é#again #stale_x", nil},
		{"marks side by side", "#wrong,#stale", []string{"wrong", "stale"}},
		{"no mark", "please run the tests", nil},
	}
	for _, tt := range marks {
		t.Run("prompt: "+tt.name, func(t *testing.T) {
			in := hookJSON(map[string]string{"session_id": "s9", "cwd": dev, "hook_event_name": "UserPromptSubmit", "prompt": tt.prompt})
			out, err := run(t, home, in, "hook", "prompt")
			if err != nil {
				t.Fatalf("hook: %v\n%s", err, out)
			}
			e := last(t, repo)
			if !slices.Equal(e.Marks, tt.want) || e.Session != "s9" || e.Hook != "prompt" {
				t.Errorf("event = %+v, want the marks %v", e, tt.want)
			}
			if got := strings.Count(out, "The person marked"); got != len(tt.want) {
				t.Errorf("output =\n%s\nwant %d lines", out, len(tt.want))
			}
			for _, m := range tt.want {
				if !strings.Contains(out, "#"+m) {
					t.Errorf("output =\n%s\nwant a line for #%s", out, m)
				}
			}
		})
	}

	t.Run("no prompt reaches the log", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join(store.StateDir(repo), "log.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, word := range []string{"forgot", "deploy", "please", "docs"} {
			if strings.Contains(string(b), word) {
				t.Errorf("the log holds %q", word)
			}
		}
	})

	t.Run("session-end starts one snapshot", func(t *testing.T) {
		mkdirT(t, filepath.Join(home, ".claude", "projects"))
		calls := recordStarts(t)
		in := hookJSON(map[string]string{"session_id": "s9", "cwd": dev, "hook_event_name": "SessionEnd", "reason": "prompt_input_exit"})
		if out, err := run(t, home, in, "hook", "session-end"); err != nil {
			t.Fatalf("hook: %v\n%s", err, out)
		}
		if len(*calls) != 1 || !slices.Equal((*calls)[0], []string{"snapshot"}) {
			t.Errorf("starts = %v, want one snapshot", *calls)
		}
		if e := last(t, repo); e.Hook != "session-end" {
			t.Errorf("event = %+v, want the hook session-end", e)
		}
	})

	memory := "dev/memories/small-commits.md"
	tools := []struct {
		name  string
		input string
		want  []string // the paths of the new line, or nil for no new line
	}{
		{"tool: a cat of a memory", toolJSON("PostToolUse", dev, "Bash", map[string]string{"command": "cat ~/memory/" + memory}, nil), []string{memory}},
		{"tool: a call that reads no document", toolJSON("PostToolUse", dev, "Bash", map[string]string{"command": "ls ~/memory/dev/memories"}, nil), nil},
		{"tool: a response", toolJSON("PostToolUse", dev, "Bash", map[string]string{"command": "head -n 3 ~/memory/" + memory, "description": "Peek-token-6620 at the memory"},
			map[string]any{"tool_response": map[string]string{"stdout": "response-token-7731", "stderr": ""}}), []string{memory}},
		{"tool: a command that exited 1", toolJSON("PostToolUseFailure", dev, "Bash", map[string]string{"command": "grep -n missing-token-4471 ~/memory/" + memory},
			map[string]any{"error": "Exit code 1\nerror-token-8812", "is_interrupt": false}), []string{memory}},
		{"tool: a Bash failure without an exit code", toolJSON("PostToolUseFailure", dev, "Bash", map[string]string{"command": "cat ~/memory/" + memory},
			map[string]any{"error": "Command timed out after 2m", "is_interrupt": false}), nil},
		{"tool: a failed Read", toolJSON("PostToolUseFailure", dev, "Read", map[string]string{"file_path": filepath.Join(repo, memory)},
			map[string]any{"error": "Exit code 1", "is_interrupt": false}), nil},
		{"tool: a Read", toolJSON("PostToolUse", dev, "Read", map[string]string{"file_path": filepath.Join(repo, memory)}, nil), []string{memory}},
		{"tool: another event", toolJSON("PreToolUse", dev, "Bash", map[string]string{"command": "cat ~/memory/" + memory}, nil), nil},
	}
	for _, tt := range tools {
		t.Run(tt.name, func(t *testing.T) {
			before := len(logLines(t, repo))
			out, err := run(t, home, tt.input, "hook", "tool")
			if err != nil || out != "" {
				t.Fatalf("hook tool: error %v, output %q, want neither", err, out)
			}
			lines := logLines(t, repo)
			if tt.want == nil {
				if len(lines) != before {
					t.Errorf("the log gained %d lines, want none:\n%s", len(lines)-before, strings.Join(lines[before:], "\n"))
				}
				return
			}
			if len(lines) != before+1 {
				t.Fatalf("the log gained %d lines, want one", len(lines)-before)
			}
			e := last(t, repo)
			if e.Command != "hook" || e.Hook != "tool" || e.Session != "s9" || e.Outcome != "ok" || !slices.Equal(e.Paths, tt.want) {
				t.Errorf("event = %+v, want a tool hook of s9 with the paths %v", e, tt.want)
			}
			if head := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD")); e.Level != "~/dev" || e.Commit != head || e.Accepted != acceptedCommit(repo) {
				t.Errorf("level %q, commit %q, accepted %q, want ~/dev, %s and %q", e.Level, e.Commit, e.Accepted, head, acceptedCommit(repo))
			}
			for _, word := range []string{"cat", "head", "grep", "-n", "Peek-token-6620", "response-token-7731", "missing-token-4471", "error-token-8812", "Exit code"} {
				if strings.Contains(lines[before], word) {
					t.Errorf("the line holds %q: %s", word, lines[before])
				}
			}
		})
	}

	t.Run("tool: input that is not JSON", func(t *testing.T) {
		out, err := run(t, home, "zebra-token-5150 quokka", "hook", "tool")
		if err != nil || out != "" {
			t.Fatalf("hook tool: error %v, output %q, want neither", err, out)
		}
		e := last(t, repo)
		if e.Hook != "tool" || e.Outcome != "error" || e.Error == "" {
			t.Errorf("event = %+v, want a failed tool hook", e)
		}
		if line := logLines(t, repo); strings.Contains(line[len(line)-1], "zebra") || strings.Contains(line[len(line)-1], "quokka") {
			t.Errorf("the line holds the input: %s", line[len(line)-1])
		}
	})

	t.Run("wrong input", func(t *testing.T) {
		if _, err := run(t, home, "not json", "hook", "prompt"); err == nil {
			t.Error("hook with input that is not JSON succeeded")
		}
		if _, err := run(t, home, "{}", "hook", "sleep"); err == nil {
			t.Error("hook with an unknown event succeeded")
		}
	})

	t.Run("hook is hidden", func(t *testing.T) {
		out, err := run(t, dev, "", "orient")
		if err != nil || strings.Contains(out, "memcore hook") {
			t.Errorf("orient: %v, output lists hook:\n%s", err, out)
		}
		out, _ = run(t, dev, "", "help")
		if strings.Contains(out, "memcore hook") {
			t.Errorf("help lists hook:\n%s", out)
		}
	})
}

func TestHookToolStartsNoGit(t *testing.T) {
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "small-commits", "Read before you commit: one change per commit.")
	bin, marker := t.TempDir(), filepath.Join(t.TempDir(), "git-ran")
	writeT(t, bin, "git", "#!/bin/sh\n: > "+shellQuote(marker)+"\nexit 1\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkdirT(t, filepath.Join(home, ".claude", "projects"))
	calls := recordStarts(t)
	t.Setenv("PATH", bin)
	before := len(logLines(t, repo))

	in := toolJSON("PostToolUse", dev, "Bash", map[string]string{"command": "ls ~/memory/dev/memories && cat /tmp/x"}, nil)
	if out, err := run(t, dev, in, "hook", "tool"); err != nil || out != "" {
		t.Fatalf("hook tool: error %v, output %q, want neither", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a call that reads nothing ran git")
	}
	if n := len(logLines(t, repo)); n != before {
		t.Errorf("the log gained %d lines, want none", n-before)
	}
	if len(*calls) != 0 {
		t.Errorf("starts = %v, want none", *calls)
	}

	// A call that reads a memory runs the fake git, so the check above has teeth.
	in = toolJSON("PostToolUse", dev, "Bash", map[string]string{"command": "cat ~/memory/dev/memories/small-commits.md"}, nil)
	if out, err := run(t, dev, in, "hook", "tool"); err != nil || out != "" {
		t.Fatalf("hook tool: error %v, output %q, want neither", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("a call that reads a memory ran no git: %v", err)
	}
	if n := len(logLines(t, repo)); n != before+1 {
		t.Errorf("the log gained %d lines, want one", n-before)
	}
	if len(*calls) != 1 {
		t.Errorf("starts = %v, want the snapshot that a logged call starts", *calls)
	}
}
