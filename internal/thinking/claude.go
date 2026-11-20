package thinking

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// claudeReply is the object that claude -p --output-format json prints.
type claudeReply struct {
	IsError bool            `json:"is_error"`
	Subtype string          `json:"subtype"`
	Cost    float64         `json:"total_cost_usd"`
	Output  json.RawMessage `json:"structured_output"`
	Result  string          `json:"result"`
	Errors  []string        `json:"errors"`
}

// commandFor returns the command of one call of Claude Code, with its arguments and its environment.
// The session has no tools, no settings, no context of its own and no transcript, so it only reads the input.
func commandFor(req Request, environ []string) (name string, args, env []string) {
	args = []string{
		"-p", req.Prompt,
		"--output-format", "json",
		"--json-schema", req.Schema,
		"--no-session-persistence",
		"--tools", "",
		"--safe-mode",
		"--setting-sources", "",
		"--strict-mcp-config",
	}
	if req.Budget > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(req.Budget, 'f', 4, 64))
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	// The session belongs to no session of an agent, and memcore knows it as its own.
	drop := map[string]bool{"CLAUDECODE": true, "CLAUDE_CODE_SESSION_ID": true, "MEMCORE_INTERNAL": true, "CLAUDE_CODE_DISABLE_AUTO_MEMORY": true}
	for _, kv := range environ {
		if key, _, _ := strings.Cut(kv, "="); !drop[key] {
			env = append(env, kv)
		}
	}
	env = append(env, "MEMCORE_INTERNAL=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
	return "claude", args, env
}

// lookPath finds the executable of the agent. A test points it at a script.
var lookPath = exec.LookPath

// runClaude runs one call of Claude Code in a new empty folder, so it loads no file of a project.
func runClaude(req Request) (Reply, error) {
	name, args, env := commandFor(req, os.Environ())
	path, err := lookPath(name)
	if err != nil {
		return Reply{}, fmt.Errorf("run the agent: claude is not on the PATH: install Claude Code, or set the PATH: %w", err)
	}
	dir, err := os.MkdirTemp("", "memcore-thinking-")
	if err != nil {
		return Reply{}, fmt.Errorf("make the folder of the agent: %w", err)
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command(path, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdin = strings.NewReader(req.Input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run() // before the buffers are read
	return finish(stdout.Bytes(), stderr.String(), runErr)
}

// finish turns the output and the exit of Claude Code into a reply. A call that stops at its budget, or that
// reports an error, exits with a status and still prints its reply. So the reply names the cause and the cost.
func finish(stdout []byte, stderr string, runErr error) (Reply, error) {
	reply, err := parseReply(stdout)
	if runErr == nil || errors.Is(err, ErrBudget) || errors.Is(err, errReported) {
		return reply, err
	}
	// The exit is an error even when the output parses.
	return Reply{Cost: reply.Cost}, fmt.Errorf("run the agent: %w: %s", runErr, firstLine(stderr))
}

// errReported marks an error that the reply of the agent states itself.
var errReported = errors.New("the agent reports an error")

// parseReply reads the object that Claude Code printed.
func parseReply(out []byte) (Reply, error) {
	var r claudeReply
	if err := json.Unmarshal(bytes.TrimSpace(out), &r); err != nil {
		return Reply{}, fmt.Errorf("read the reply of the agent: %w", err)
	}
	reply := Reply{Cost: r.Cost}
	switch {
	case r.Subtype == "error_max_budget_usd":
		return reply, ErrBudget
	case r.IsError:
		cause := strings.Join(r.Errors, "; ")
		if cause == "" {
			cause = firstLine(r.Result)
		}
		if cause == "" {
			cause = r.Subtype
		}
		return reply, fmt.Errorf("run the agent: %w: %s: check that claude is logged in", errReported, cause)
	case len(r.Output) == 0 || string(r.Output) == "null":
		return reply, errors.New("read the reply of the agent: it holds no structured output")
	}
	reply.Output = r.Output
	return reply, nil
}

// firstLine returns the first line of a text that is not blank.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
