package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// said returns a line of a Claude Code transcript in which the person says text, in dir.
func said(session, dir, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "user", "timestamp": "2026-11-04T10:00:00Z", "sessionId": session, "cwd": dir,
		"message": map[string]any{"role": "user", "content": text},
	})
	return string(b) + "\n"
}

// branchFile returns a file of the transcripts branch, or "" when the branch lacks it.
func branchFile(t *testing.T, repo, path string) string {
	t.Helper()
	out, err := store.Git(repo, "show", "transcripts:"+path)
	if err != nil {
		return ""
	}
	return out
}

func TestSnapshot(t *testing.T) {
	home, repo, remote := newMemory(t)
	projects := filepath.Join(home, ".claude", "projects")
	folder := "-home-dev-shop"
	main := filepath.Join(projects, folder, "s1.jsonl")
	// The token is built from parts, so no scanner takes this file for a leak.
	token := "Bearer\t" + "Abcd" + "1234efgh"
	writeT(t, projects, folder+"/s1.jsonl", said("s1", "/home/dev/shop", "Use small commits.")+said("s1", "/home/dev/shop", "the header is "+token))
	writeT(t, projects, folder+"/s1/subagents/agent-a1.jsonl",
		`{"type":"user","timestamp":"t","isSidechain":true,"sessionId":"s1","agentId":"a1","cwd":"/home/dev/shop","message":{"role":"user","content":"Find the config."}}`+"\n")
	writeT(t, projects, folder+"/s1/subagents/agent-a1.meta.json", `{"agentType":"explore"}`)
	head := gitT(t, repo, "rev-parse", "HEAD")

	t.Run("the first snapshot", func(t *testing.T) {
		if out, err := run(t, home, "", "snapshot"); err != nil {
			t.Fatalf("snapshot: %v\n%s", err, out)
		}
		got := branchFile(t, repo, "claude/"+digest(folder)+"/s1.jsonl")
		if !strings.Contains(got, `"who":"you","text":"Use small commits."`) {
			t.Errorf("the trimmed transcript =\n%s\nwant the text of the person", got)
		}
		if strings.Contains(got, "1234efgh") || !strings.Contains(got, "[secret:bearer-token]") {
			t.Errorf("the trimmed transcript =\n%s\nwant the token behind a JSON escape redacted", got)
		}
		if sub := branchFile(t, repo, "claude/"+digest(folder)+"/s1/subagents/agent-a1.jsonl"); !strings.Contains(sub, `"parent":"s1"`) {
			t.Errorf("the subagent file =\n%s\nwant it under its session, with its parent", sub)
		}
		files := gitT(t, repo, "ls-tree", "-r", "--name-only", "transcripts")
		if strings.Contains(files, "meta.json") {
			t.Errorf("the branch holds %s, want no meta.json", files)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != head {
			t.Error("the snapshot moved HEAD")
		}
		if got := gitT(t, repo, "symbolic-ref", "HEAD"); strings.TrimSpace(got) != "refs/heads/main" {
			t.Errorf("HEAD is %s, want main", got)
		}
		if got := gitT(t, repo, "status", "--porcelain"); got != "" {
			t.Errorf("status = %q, want a clean tree and index", got)
		}
		if got := gitT(t, repo, "log", "-1", "--format=%s", "transcripts"); strings.TrimSpace(got) != "snapshot: 2 transcripts" {
			t.Errorf("subject = %q", got)
		}
	})

	t.Run("a second run makes no commit", func(t *testing.T) {
		before := gitT(t, repo, "rev-parse", "transcripts")
		if out, err := run(t, home, "", "snapshot"); err != nil || !strings.Contains(out, "No new transcripts") {
			t.Fatalf("snapshot: %v\n%s", err, out)
		}
		if got := gitT(t, repo, "rev-parse", "transcripts"); got != before {
			t.Error("the second snapshot made a commit")
		}
	})

	t.Run("a broken line is named", func(t *testing.T) {
		writeT(t, projects, folder+"/s2.jsonl", said("s2", "/home/dev/shop", "one")+"{\"type\":\n"+said("s2", "/home/dev/shop", "two"))
		out, err := run(t, home, "", "snapshot")
		if err != nil || !strings.Contains(out, "s2.jsonl") || !strings.Contains(out, "1 broken line") {
			t.Errorf("snapshot: %v\n%s\nwant the file and its broken line", err, out)
		}
	})

	t.Run("a partial line waits, and a file that stays partial is cut off", func(t *testing.T) {
		partial := strings.TrimSuffix(said("s3", "/home/dev/shop", "two"), "\n")
		writeT(t, projects, folder+"/s3.jsonl", said("s3", "/home/dev/shop", "one")+partial[:20])
		writeT(t, projects, folder+"/s4.jsonl", said("s4", "/home/dev/shop", "one")+partial[:20])
		if out, err := run(t, home, "", "snapshot"); err != nil {
			t.Fatalf("snapshot: %v\n%s", err, out)
		}
		if got := branchFile(t, repo, "claude/"+digest(folder)+"/s3.jsonl"); strings.Contains(got, `"text":"two"`) {
			t.Errorf("the partial line landed:\n%s", got)
		}
		writeT(t, projects, folder+"/s3.jsonl", said("s3", "/home/dev/shop", "one")+said("s3", "/home/dev/shop", "two"))
		out, err := run(t, home, "", "snapshot")
		if err != nil {
			t.Fatalf("snapshot: %v\n%s", err, out)
		}
		if got := branchFile(t, repo, "claude/"+digest(folder)+"/s3.jsonl"); !strings.Contains(got, `"text":"two"`) {
			t.Errorf("the completed line is missing:\n%s", got)
		}
		if !strings.Contains(out, "s4.jsonl") || !strings.Contains(out, "cut off") {
			t.Errorf("snapshot =\n%s\nwant s4.jsonl named as cut off", out)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "s3.jsonl") && strings.Contains(line, "cut off") {
				t.Errorf("snapshot =\n%s\nwant only s4.jsonl cut off", out)
			}
		}
	})

	t.Run("a grown file replaces its version", func(t *testing.T) {
		f, err := os.OpenFile(main, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(said("s1", "/home/dev/shop", "Squash before you push."))
		f.Close()
		if out, err := run(t, home, "", "snapshot"); err != nil {
			t.Fatalf("snapshot: %v\n%s", err, out)
		}
		if got := branchFile(t, repo, "claude/"+digest(folder)+"/s1.jsonl"); !strings.Contains(got, "Squash before you push.") || !strings.Contains(got, "Use small commits.") {
			t.Errorf("the grown transcript =\n%s", got)
		}
	})

	t.Run("a shorter copy keeps the longer one, and a diverging copy goes beside it", func(t *testing.T) {
		old := t.TempDir()
		writeT(t, old, folder+"/s1.jsonl", said("s1", "/home/dev/shop", "Use small commits."))
		writeT(t, old, folder+"/s5.jsonl", said("s5", "/home/dev/shop", "first"))
		if out, err := run(t, home, "", "snapshot", "--from", old); err != nil {
			t.Fatalf("snapshot --from: %v\n%s", err, out)
		}
		if got := branchFile(t, repo, "claude/"+digest(folder)+"/s1.jsonl"); !strings.Contains(got, "Squash before you push.") {
			t.Errorf("a shorter copy replaced the transcript:\n%s", got)
		}
		other := t.TempDir()
		writeT(t, other, folder+"/s5.jsonl", said("s5", "/home/dev/shop", "second"))
		if out, err := run(t, home, "", "snapshot", "--from", other); err != nil {
			t.Fatalf("snapshot --from: %v\n%s", err, out)
		}
		files := gitT(t, repo, "ls-tree", "-r", "--name-only", "transcripts", "claude/"+digest(folder)+"/")
		if !strings.Contains(files, "s5.jsonl\n") || strings.Count(files, "s5.") != 2 {
			t.Errorf("files =\n%s\nwant s5.jsonl and one diverging copy beside it", files)
		}
	})

	t.Run("a removed source stays", func(t *testing.T) {
		if err := os.Remove(main); err != nil {
			t.Fatal(err)
		}
		if out, err := run(t, home, "", "snapshot"); err != nil {
			t.Fatalf("snapshot: %v\n%s", err, out)
		}
		if got := branchFile(t, repo, "claude/"+digest(folder)+"/s1.jsonl"); got == "" {
			t.Error("the transcript of a removed source left the branch")
		}
	})

	t.Run("push leaves the branch home", func(t *testing.T) {
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "small-commits", "Read before you commit: one change per commit.")
		pushYes(t, home)
		if refs := gitT(t, remote, "for-each-ref", "--format=%(refname)"); strings.Contains(refs, "transcripts") {
			t.Errorf("the remote holds %s", refs)
		}
	})
}

func TestSnapshotFrom(t *testing.T) {
	home, _, _ := newMemory(t)
	if _, err := run(t, home, "", "snapshot", "--from", filepath.Join(home, "missing")); err == nil {
		t.Error("snapshot --from a missing folder succeeded")
	}
	if out, err := run(t, home, "", "snapshot"); err != nil || !strings.Contains(out, "No transcripts") {
		t.Errorf("snapshot without a projects folder: %v\n%s", err, out)
	}
}

func TestPlaceCollision(t *testing.T) {
	_, repo, _ := newMemory(t)
	hash := func(content string) string {
		return strings.TrimSpace(gitStdinT(t, repo, content, "hash-object", "-w", "--stdin"))
	}
	first, second := "first\n", "second\n"
	p := "claude/f/s.jsonl"
	b := hash(second)
	// Another transcript holds the short name of the diverging copy.
	existing := map[string]string{p: hash(first), "claude/f/s." + b[:8] + ".jsonl": hash("other\n")}
	got, ok, err := place(repo, existing, p, second)
	if err != nil || !ok {
		t.Fatalf("place: %v, %v", ok, err)
	}
	if got == "claude/f/s."+b[:8]+".jsonl" || !strings.HasPrefix(got, "claude/f/s."+b[:8]) {
		t.Errorf("place = %s, want a longer name than the taken one", got)
	}
	existing[got] = b
	if _, ok, _ := place(repo, existing, p, second); ok {
		t.Error("place of the same diverging copy again = true, want false")
	}
}

func TestSnapshotSHA256(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "--object-format=sha256")
	mkdirT(t, store.StateDir(repo))
	blob := strings.TrimSpace(gitStdinT(t, repo, "hi\n", "hash-object", "-w", "--stdin"))
	if err := commitSnapshot(repo, "", []string{"100644 " + blob + "\tclaude/f/s.jsonl"}); err != nil {
		t.Fatalf("commitSnapshot in a SHA-256 repo: %v", err)
	}
	if _, ok := revParse(repo, transcriptsRef); !ok {
		t.Error("no transcripts branch")
	}
}

// gitStdinT runs git in dir with stdin, and fails the test on an error.
func gitStdinT(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	out, err := store.GitStdin(dir, stdin, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
