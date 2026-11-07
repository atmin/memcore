package commands

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUISearchConfig(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	writeT(t, repo, "lab/notes/line\nbreak.md", "A needle here.\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "test: a path with a newline")
	gitT(t, repo, "config", "grep.column", "true")
	gitT(t, repo, "config", "grep.lineNumber", "false")
	h, err := uiBackend{repo: repo, dir: home}.Search(context.Background(), "needle")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Hits) != 1 || h.Hits[0].Path != "lab/notes/line\nbreak.md" || h.Hits[0].Line != 1 || h.Hits[0].Text != "A needle here." {
		t.Errorf("hits = %+v, want the path with its newline, line 1, and the text", h.Hits)
	}
}

func TestUISearchFails(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	gitT(t, repo, "config", "grep.patternType", "nonsense") // git grep exits 128
	if _, err := (uiBackend{repo: repo, dir: home}).Search(context.Background(), "needle"); err == nil {
		t.Error("a search that git refuses returns no error")
	}
}

func TestUIQueueSigned(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("no ssh-keygen to sign a commit")
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	pushYes(t, home)
	key := filepath.Join(t.TempDir(), "key")
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	gitT(t, repo, "config", "gpg.format", "ssh")
	gitT(t, repo, "config", "user.signingkey", key)
	writeT(t, repo, "lab/notes/signed.md", "A signed note.\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-S", "-m", "test: a signed note")
	marker := filepath.Join(t.TempDir(), "ran")
	helper := filepath.Join(t.TempDir(), "verify")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	signers := filepath.Join(t.TempDir(), "allowed")
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signers, append([]byte("test@example.com "), pub...), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "config", "gpg.ssh.allowedSignersFile", signers)
	gitT(t, repo, "config", "gpg.ssh.program", helper)
	gitT(t, repo, "config", "log.showSignature", "true")
	p, _, err := describe(repo, headCommit(repo), true)
	if err != nil || p.subject != "test: a signed note" || len(p.diffs) != 1 {
		t.Fatalf("describe = %+v, %v", p, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("describe ran the helper that checks signatures")
	}
	gitT(t, repo, "show", "-s", "HEAD")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("git show did not run the helper, so the test proves nothing: %v", err)
	}
}

func TestUIQueueEmpty(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	pushYes(t, home)
	q, err := uiBackend{repo: repo, dir: home}.Queue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(q)
	if err != nil || !strings.Contains(string(data), `"entries":[]`) {
		t.Errorf("an empty queue = %s, %v, want an empty list of entries", data, err)
	}
}
