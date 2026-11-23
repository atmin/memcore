package commands

import "testing"

func TestAgentSession(t *testing.T) {
	tests := []struct {
		name          string
		codex, claude string
		agent, id     string
	}{
		{"neither variable", "", "", "", ""},
		{"only Codex", "t1", "", "codex", "t1"},
		{"only Claude Code", "", "s1", "claude", "s1"},
		{"both, so Codex wins", "t1", "s1", "codex", "t1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CODEX_THREAD_ID", tt.codex)
			t.Setenv("CLAUDE_CODE_SESSION_ID", tt.claude)
			agent, id := agentSession()
			if agent != tt.agent || id != tt.id {
				t.Errorf("agentSession() = %q, %q, want %q, %q", agent, id, tt.agent, tt.id)
			}
		})
	}
}
