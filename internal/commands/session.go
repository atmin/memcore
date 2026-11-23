package commands

import "os"

// agentSession returns the agent and the session id of the caller, from the environment that the agent sets.
// Codex comes first, because Claude Code runs codex exec, and Codex passes the environment of its parent on.
// It returns two empty strings when no agent sets an id.
func agentSession() (agent, id string) {
	if id := os.Getenv("CODEX_THREAD_ID"); id != "" {
		return "codex", id
	}
	if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		return "claude", id
	}
	return "", ""
}
