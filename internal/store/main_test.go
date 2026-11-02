package store

import (
	"os"
	"testing"
)

// TestMain isolates git from the configuration and the agent of the machine.
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "Test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		os.Setenv(k, v)
	}
	os.Unsetenv("CLAUDECODE")
	os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	os.Exit(m.Run())
}
