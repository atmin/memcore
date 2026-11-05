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
