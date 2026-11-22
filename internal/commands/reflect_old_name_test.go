package commands

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// TestReflectOldName proves that memcore ignores the old name of reflect: the command sleep, and its file of
// records, sleep.jsonl. It is the only file of the repo that names them.
func TestReflectOldName(t *testing.T) {
	home, repo, _ := newMemory(t)

	t.Run("the command sleep is unknown", func(t *testing.T) {
		var out strings.Builder
		err := Run(Env{Dir: home, In: strings.NewReader(""), Out: &out, Err: &out}, "sleep", []string{"--list"})
		if !errors.Is(err, ErrUnknown) {
			t.Errorf("sleep --list: %v, want ErrUnknown\n%s", err, out.String())
		}
	})

	t.Run("reflect reads no sleep.jsonl", func(t *testing.T) {
		c := reflectCorrection{Time: "2026-11-18T10:05:00Z", Quote: "Use small commits.", Summary: "Commit small.", Verdict: verdictCapture, Reason: "No memory holds it."}
		line, err := json.Marshal(reflectRecord{Session: "s-old", File: "claude/p/s-old.jsonl", Blob: "b1", Level: "~", Start: "2026-11-18T10:00:00Z", Corrections: []reflectCorrection{c}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(store.StateDir(repo), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store.StateDir(repo), "sleep.jsonl"), append(line, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := run(t, home, "", "reflect", "--list")
		if err != nil || out != "No reflect pass has judged a session yet. The person runs one with memcore reflect.\n" {
			t.Errorf("reflect --list: %v\n%q", err, out)
		}
	})
}
