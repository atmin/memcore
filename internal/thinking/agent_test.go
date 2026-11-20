package thinking

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCommandFor(t *testing.T) {
	environ := []string{"HOME=/home/me", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=abc", "MEMCORE_INTERNAL=0", "PATH=/bin"}
	cases := []struct {
		name    string
		req     Request
		with    []string // each run of arguments must appear, in this order
		without []string
	}{
		{"plain", Request{Prompt: "List them.", Schema: `{"type":"object"}`},
			[]string{"-p", "List them.", "--output-format", "json", "--json-schema", `{"type":"object"}`, "--no-session-persistence", "--tools", "", "--safe-mode", "--setting-sources", "", "--strict-mcp-config"},
			[]string{"--model", "--max-budget-usd"}},
		{"model and budget", Request{Prompt: "p", Schema: "{}", Model: "sonnet", Budget: 1.5},
			[]string{"--max-budget-usd", "1.5000", "--model", "sonnet"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, args, env := commandFor(c.req, environ)
			if name != "claude" {
				t.Errorf("name = %q, want claude", name)
			}
			at := 0
			for _, w := range c.with {
				i := slices.Index(args[at:], w)
				if i < 0 {
					t.Fatalf("args %q lack %q after position %d", args, w, at)
				}
				at += i + 1
			}
			for _, w := range c.without {
				if slices.Contains(args, w) {
					t.Errorf("args %q hold %q", args, w)
				}
			}
			for _, kv := range env {
				if strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "CLAUDE_CODE_SESSION_ID=") || kv == "MEMCORE_INTERNAL=0" {
					t.Errorf("env holds %q", kv)
				}
			}
			for _, w := range []string{"HOME=/home/me", "PATH=/bin", "MEMCORE_INTERNAL=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"} {
				if !slices.Contains(env, w) {
					t.Errorf("env %q lacks %q", env, w)
				}
			}
		})
	}
}

func TestParseReply(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		output  string
		cost    float64
		budget  bool
		wantErr string
	}{
		{"success", `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.25,"structured_output":{"a":[1]}}` + "\n", `{"a":[1]}`, 0.25, false, ""},
		{"no cost", `{"subtype":"success","structured_output":{"a":1}}`, `{"a":1}`, 0, false, ""},
		{"budget", `{"subtype":"error_max_budget_usd","is_error":true,"total_cost_usd":0.5,"errors":["Reached maximum budget ($0.5)"]}`, "", 0.5, true, "budget"},
		{"agent error", `{"subtype":"error_during_execution","is_error":true,"total_cost_usd":0.1,"errors":["Not logged in"]}`, "", 0.1, false, "Not logged in"},
		{"error in the result", `{"subtype":"success","is_error":true,"result":"Invalid API key\nmore"}`, "", 0, false, "Invalid API key"},
		{"no structured output", `{"subtype":"success","is_error":false,"result":"hello"}`, "", 0, false, "no structured output"},
		{"null output", `{"subtype":"success","structured_output":null}`, "", 0, false, "no structured output"},
		{"not JSON", `claude: command failed`, "", 0, false, "read the reply"},
		{"empty", ``, "", 0, false, "read the reply"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reply, err := parseReply([]byte(c.out))
			if c.wantErr == "" && err != nil {
				t.Fatalf("err = %v", err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("err = %v, want one with %q", err, c.wantErr)
			}
			if errors.Is(err, ErrBudget) != c.budget {
				t.Errorf("ErrBudget = %v, want %v", errors.Is(err, ErrBudget), c.budget)
			}
			if string(reply.Output) != c.output || reply.Cost != c.cost {
				t.Errorf("reply = %q, %v; want %q, %v", reply.Output, reply.Cost, c.output, c.cost)
			}
		})
	}
}

func TestFinish(t *testing.T) {
	exit := errors.New("exit status 1")
	ok := `{"subtype":"success","total_cost_usd":0.2,"structured_output":{"a":1}}`
	cases := []struct {
		name    string
		stdout  string
		stderr  string
		runErr  error
		output  string
		cost    float64
		budget  bool
		wantErr string
	}{
		{"success", ok, "", nil, `{"a":1}`, 0.2, false, ""},
		{"budget with an exit status", `{"subtype":"error_max_budget_usd","is_error":true,"total_cost_usd":0.5}`, "", exit, "", 0.5, true, "budget"},
		{"agent error with an exit status", `{"subtype":"error_during_execution","is_error":true,"total_cost_usd":0.1,"errors":["Not logged in"]}`, "", exit, "", 0.1, false, "Not logged in"},
		{"exit status with a good reply", ok, "boom", exit, "", 0.2, false, "exit status 1: boom"},
		{"exit status with no reply", "", "claude: bad flag\nmore", exit, "", 0, false, "exit status 1: claude: bad flag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reply, err := finish([]byte(c.stdout), c.stderr, c.runErr)
			if c.wantErr == "" && err != nil {
				t.Fatalf("err = %v", err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("err = %v, want one with %q", err, c.wantErr)
			}
			if errors.Is(err, ErrBudget) != c.budget {
				t.Errorf("ErrBudget = %v, want %v", errors.Is(err, ErrBudget), c.budget)
			}
			if string(reply.Output) != c.output || reply.Cost != c.cost {
				t.Errorf("reply = %q, %v; want %q, %v", reply.Output, reply.Cost, c.output, c.cost)
			}
		})
	}
}

// TestRunClaude runs runClaude end to end, with a script in place of Claude Code.
func TestRunClaude(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	// The script answers with its input, its folder and its mark, so the test sees what the agent got.
	body := `#!/bin/sh
input=$(cat)
if [ "$input" = fail ]; then echo "claude: not logged in" >&2; exit 3; fi
printf '{"subtype":"success","is_error":false,"total_cost_usd":0.5,"structured_output":{"input":"%s","dir":"%s","internal":"%s","files":"%s"}}\n' "$input" "$(pwd)" "$MEMCORE_INTERNAL" "$(ls -A | wc -l | tr -d ' ')"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := lookPath
	t.Cleanup(func() { lookPath = prev })
	lookPath = func(string) (string, error) { return script, nil }

	reply, err := runClaude(Request{Prompt: "p", Input: "hello", Schema: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	out := string(reply.Output)
	for _, want := range []string{`"input":"hello"`, `"internal":"1"`, `"files":"0"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the reply %s lacks %s", out, want)
		}
	}
	if reply.Cost != 0.5 {
		t.Errorf("cost = %v, want 0.5", reply.Cost)
	}
	if strings.Contains(out, `"dir":"`+dir) {
		t.Errorf("the agent ran in the folder of the caller: %s", out)
	}

	if _, err := runClaude(Request{Prompt: "p", Input: "fail", Schema: "{}"}); err == nil || !strings.Contains(err.Error(), "exit status 3: claude: not logged in") {
		t.Errorf("err = %v, want the exit status and the first line of the error output", err)
	}

	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if _, err := runClaude(Request{Prompt: "p", Schema: "{}"}); err == nil || !strings.Contains(err.Error(), "claude is not on the PATH") {
		t.Errorf("err = %v, want the cause and the fix", err)
	}
}
