package commands

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotLeavesTheWorkAlone(t *testing.T) {
	home, repo, _ := newMemory(t)
	projects := filepath.Join(home, ".claude", "projects")
	writeT(t, projects, "-home-dev-shop/s1.jsonl", said("s1", "/home/dev/shop", "Use small commits."))
	// The person has work in progress: a staged file, an unstaged change and an untracked file.
	writeT(t, repo, "staged.md", "staged\n")
	gitT(t, repo, "add", "staged.md")
	writeT(t, repo, "README.md", readT(t, filepath.Join(repo, "README.md"))+"unstaged\n")
	writeT(t, repo, "untracked.txt", "untracked\n")
	index, cached, unstaged := gitT(t, repo, "ls-files", "-s"), gitT(t, repo, "diff", "--cached"), gitT(t, repo, "diff")

	if out, err := run(t, home, "", "snapshot"); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	if gitT(t, repo, "ls-files", "-s") != index || gitT(t, repo, "diff", "--cached") != cached || gitT(t, repo, "diff") != unstaged {
		t.Error("the snapshot changed the index or the working tree")
	}
	if readT(t, filepath.Join(repo, "untracked.txt")) != "untracked\n" {
		t.Error("the snapshot changed an untracked file")
	}
}

func TestSnapshotHashesFolders(t *testing.T) {
	home, repo, _ := newMemory(t)
	projects := filepath.Join(home, ".claude", "projects")
	// The tokens are built from parts, so no scanner takes this file for a leak.
	tail := strings.Repeat("a1B2", 9)
	answer := `{"type":"assistant","timestamp":"2026-11-04T10:00:00Z","sessionId":"s1","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}` + "\n"
	// Claude Code writes each character that is not a letter or a digit as -, so no check of the name finds
	// every secret. Some transcripts name the directory, some name another one, and some none.
	folders := map[string]string{
		"-home-x-ghp-" + tail:           said("s1", "/home/x/ghp_"+tail, "Use small commits."),
		"-home-y-ghp-" + tail:           said("s1", "/home", "Use small commits."),
		"-home-z-ghp-" + tail:           "{\"cwd\":\n" + answer,
		"-home-w-ghp-" + tail:           answer,
		"-home-x-Bearer-Abcd-" + "1234": answer,
		"-home-sk-00000000000000166626": said("s1", "/home", "The same words."),
		"-home-sk-00000000000000178827": said("s1", "/home", "The same words."),
	}
	for folder, content := range folders {
		writeT(t, projects, folder+"/s1.jsonl", content)
	}
	if out, err := run(t, home, "", "snapshot"); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	files := gitT(t, repo, "ls-tree", "-r", "--name-only", "transcripts")
	if strings.Contains(files, "home") {
		t.Errorf("the branch holds a path with a folder name:\n%s", files)
	}
	for folder := range folders {
		if !strings.Contains(files, "claude/"+digest(folder)+"/s1.jsonl\n") {
			t.Errorf("files =\n%s\nwant the transcript of %s under the hash of its folder", files, folder)
		}
	}
}

func TestSnapshotSafeFileNames(t *testing.T) {
	home, repo, _ := newMemory(t)
	projects := filepath.Join(home, ".claude", "projects")
	token := "ghp_" + strings.Repeat("a1B2", 9)
	writeT(t, projects, "-home/"+token+".jsonl", said(token, "/home", "Use small commits."))
	writeT(t, projects, "-home/"+token+"x.jsonl", said(token+"x", "/home", "Use small commits."))
	if out, err := run(t, home, "", "snapshot"); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	files := gitT(t, repo, "ls-tree", "-r", "--name-only", "transcripts")
	if strings.Contains(files, token) || strings.Count(files, "/redacted-") != 2 {
		t.Errorf("files =\n%s\nwant two transcripts with redacted names", files)
	}
}
