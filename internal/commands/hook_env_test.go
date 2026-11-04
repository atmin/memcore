package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookKeepsTheRepo(t *testing.T) {
	home, repo, _ := newMemory(t)
	envFile := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("CLAUDE_ENV_FILE", envFile)
	in := hookJSON(map[string]string{"session_id": "s9", "cwd": home, "hook_event_name": "SessionStart", "source": "startup"})

	if out, err := run(t, home, in, "hook", "session-start"); err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	if _, err := os.Stat(envFile); err == nil {
		t.Error("the hook wrote the environment of the session without MEMCORE_REPO")
	}

	t.Setenv("MEMCORE_REPO", repo)
	if out, err := run(t, home, in, "hook", "session-start"); err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	if got := readT(t, envFile); !strings.Contains(got, "export MEMCORE_REPO='"+repo+"'\n") {
		t.Errorf("environment of the session =\n%s\nwant MEMCORE_REPO, so the commands of the agent use the same repo", got)
	}
}
