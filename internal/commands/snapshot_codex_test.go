package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The lines below follow the format of Codex in the plan codex-transcripts. They are written by hand.

// codexMetaLine returns the session_meta line of a Codex session. source is raw JSON.
func codexMetaLine(id, dir, source string) string {
	return `{"timestamp":"2026-11-23T14:37:50.269Z","ordinal":0,"type":"session_meta","payload":{"id":"` + id + `","cwd":"` + dir + `","source":` + source + `}}` + "\n"
}

// codexSaid returns a line of a Codex session in which the person, or the parent of a subagent, says text.
func codexSaid(ordinal int, text string) string {
	b, _ := json.Marshal(map[string]any{
		"timestamp": "2026-11-23T14:38:00.000Z", "ordinal": ordinal, "type": "event_msg",
		"payload": map[string]any{"type": "item_completed", "item": map[string]any{
			"type": "UserMessage", "content": []any{map[string]any{"type": "text", "text": text}},
		}},
	})
	return string(b) + "\n"
}

const codexSpawned = `{"subagent":{"thread_spawn":{"parent_thread_id":"s1","depth":1,"agent_path":"/root/finder"}}}`

// codexPath returns the path of a session file of Codex under the sessions folder.
func codexPath(id string) string {
	return "sessions/2026/11/23/rollout-2026-11-23T14-37-50-" + id + ".jsonl"
}

func TestSnapshotCodex(t *testing.T) {
	_, repo, _ := newMemory(t)
	codex := t.TempDir()
	dir := "/home/dev/shop"
	project := "codex/" + digest(dir)
	// The token is built from parts, so no scanner takes this file for a leak.
	token := "Bearer\t" + "Abcd" + "1234efgh"
	writeT(t, codex, codexPath("s1"), codexMetaLine("s1", dir, `"vscode"`)+codexSaid(1, "Use small commits.")+codexSaid(2, "the header is "+token))
	writeT(t, codex, codexPath("a1"), codexMetaLine("a1", dir, codexSpawned)+codexSaid(1, "Find the config."))
	writeT(t, codex, codexPath("g1"), codexMetaLine("g1", dir, `{"subagent":{"other":"guardian"}}`)+codexSaid(1, "Approve the command?"))
	writeT(t, codex, codexPath("i1"), codexMetaLine("i1", dir, `"vscode"`)+codexSaid(1, "Imported from Claude Code."))
	writeT(t, codex, "external_agent_session_imports.json", `{"records":[{"imported_thread_id":"i1","source":"claude"}]}`)
	snap := func(t *testing.T) (int, string) {
		t.Helper()
		var out strings.Builder
		n, err := snapshot(&out, repo, "", codex)
		if err != nil {
			t.Fatalf("snapshot: %v\n%s", err, out.String())
		}
		return n, out.String()
	}

	t.Run("a main session lands at its path", func(t *testing.T) {
		if n, out := snap(t); n != 2 {
			t.Errorf("snapshot committed %d transcripts, want 2\n%s", n, out)
		}
		got := branchFile(t, repo, project+"/s1.jsonl")
		if !strings.Contains(got, `{"agent":"codex","session":"s1","dir":"/home/dev/shop"}`) || !strings.Contains(got, `"who":"you","text":"Use small commits."`) {
			t.Errorf("the trimmed transcript =\n%s\nwant the header of Codex and the text of the person", got)
		}
	})

	t.Run("a token is redacted", func(t *testing.T) {
		got := branchFile(t, repo, project+"/s1.jsonl")
		if strings.Contains(got, "1234efgh") || !strings.Contains(got, "[secret:bearer-token]") {
			t.Errorf("the trimmed transcript =\n%s\nwant the token redacted", got)
		}
	})

	t.Run("a subagent lands under its parent", func(t *testing.T) {
		got := branchFile(t, repo, project+"/s1/subagents/a1.jsonl")
		if !strings.Contains(got, `"parent":"s1"`) || !strings.Contains(got, `"who":"parent","text":"Find the config."`) {
			t.Errorf("the subagent file =\n%s\nwant it under its parent", got)
		}
	})

	t.Run("a guardian and an import land nowhere", func(t *testing.T) {
		files := gitT(t, repo, "ls-tree", "-r", "--name-only", "transcripts")
		if strings.Contains(files, "g1") || strings.Contains(files, "i1") {
			t.Errorf("files =\n%s\nwant no guardian and no import", files)
		}
		if n, out := snap(t); n != 0 {
			t.Errorf("a second snapshot committed %d transcripts\n%s", n, out)
		}
	})

	t.Run("the same file under archived_sessions makes no entry", func(t *testing.T) {
		before := gitT(t, repo, "rev-parse", "transcripts")
		mkdirT(t, filepath.Join(codex, "archived_sessions"))
		if err := os.Rename(filepath.Join(codex, filepath.FromSlash(codexPath("s1"))), filepath.Join(codex, "archived_sessions", "rollout-2026-11-23T14-37-50-s1.jsonl")); err != nil {
			t.Fatal(err)
		}
		if n, out := snap(t); n != 0 {
			t.Errorf("snapshot committed %d transcripts, want 0\n%s", n, out)
		}
		if got := gitT(t, repo, "rev-parse", "transcripts"); got != before {
			t.Error("the archived session made a commit")
		}
	})

	t.Run("a session that grew replaces its transcript", func(t *testing.T) {
		f, err := os.OpenFile(filepath.Join(codex, "archived_sessions", "rollout-2026-11-23T14-37-50-s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(codexSaid(3, "Squash before you push."))
		f.Close()
		if n, out := snap(t); n != 1 {
			t.Errorf("snapshot committed %d transcripts, want 1\n%s", n, out)
		}
		got := branchFile(t, repo, project+"/s1.jsonl")
		if !strings.Contains(got, "Squash before you push.") || !strings.Contains(got, "Use small commits.") {
			t.Errorf("the grown transcript =\n%s", got)
		}
		files := gitT(t, repo, "ls-tree", "-r", "--name-only", "transcripts", project+"/")
		if strings.Count(files, "s1.") != 1 {
			t.Errorf("files =\n%s\nwant one transcript of s1", files)
		}
	})
}
