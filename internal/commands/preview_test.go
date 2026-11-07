package commands

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// pushedCommits returns the commits of the remote main after base, oldest first, as a preview describes them.
// An empty base gives every commit.
func pushedCommits(t *testing.T, remote, base string) []previewed {
	t.Helper()
	rng := "main"
	if base != "" {
		rng = base + "..main"
	}
	var commits []previewed
	for _, sha := range strings.Fields(gitT(t, remote, "rev-list", "--reverse", rng)) {
		files, err := filesOf(remote, sha)
		if err != nil {
			t.Fatal(err)
		}
		commits = append(commits, previewed{
			subject: strings.TrimSpace(gitT(t, remote, "show", "-s", "--format=%s", sha)),
			body:    strings.TrimSpace(gitT(t, remote, "show", "-s", "--format=%b", sha)),
			files:   files,
		})
	}
	return commits
}

func TestPreviewMatchesPush(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	for _, patches := range []bool{false, true} {
		for _, c := range foldCases() {
			t.Run(fmt.Sprintf("%s, patches %v", c.name, patches), func(t *testing.T) {
				home, repo, remote := newMemory(t)
				c.queue(t, home, repo, remote)
				got, err := preview(repo, patches)
				if err != nil {
					t.Fatalf("preview: %v", err)
				}
				if c.preview != nil {
					c.preview(t, repo, got)
				}
				base, _ := revParse(repo, acceptedRef)
				pushYes(t, home)
				want := pushedCommits(t, remote, base)
				var seen []previewed
				for _, p := range got {
					seen = append(seen, previewed{subject: p.subject, body: p.body, files: p.files})
					if patches && len(p.diffs) != len(p.files) {
						t.Errorf("%s has %d diffs for %d files", p.subject, len(p.diffs), len(p.files))
					}
					if !patches && p.diffs != nil {
						t.Errorf("%s has diffs without patches", p.subject)
					}
				}
				if !reflect.DeepEqual(seen, want) {
					t.Errorf("preview:\n%+v\npushed:\n%+v", seen, want)
				}
			})
		}
	}
}

// stateOf returns the state of a repo that a preview must leave as it was. That is its refs, HEAD, its
// working tree and .git/memcore/.
func stateOf(t *testing.T, repo string) string {
	t.Helper()
	state := gitT(t, repo, "for-each-ref") + gitT(t, repo, "symbolic-ref", "HEAD") + gitT(t, repo, "rev-parse", "HEAD") +
		gitT(t, repo, "status", "--porcelain", "--ignored", "--untracked-files=all")
	err := filepath.WalkDir(store.StateDir(repo), func(f string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		state += f + "\n" + readT(t, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestPreviewLeavesRepo(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "a-fact", "Read when you test a fact.")
	reviseT(t, dev, memoryText("a-fact", "Read when you test a fact.", "Sharper."), "a-fact", "Sharper.")
	writeT(t, repo, "dev/notes/scratch.md", "An untracked file.\n")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	before := stateOf(t, repo)
	got, err := preview(repo, true)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(got) != 2 || len(got[1].fixups) != 1 {
		t.Errorf("preview = %+v, want init and the remember with its fixup", got)
	}
	if after := stateOf(t, repo); after != before {
		t.Errorf("the repo before the preview:\n%s\nafter:\n%s", before, after)
	}
	if left, err := os.ReadDir(tmp); err != nil || len(left) > 0 {
		t.Errorf("the temporary directory holds %v, %v, want nothing", left, err)
	}
}

func TestPreviewErrors(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	const (
		fact    = "Read when you test a fact."
		sharper = "Read when you test a fact, now sharper."
		twinAt  = "2026-11-01T10:00:00Z"
	)
	// helper commits a file that no index lists, with a message and a date.
	helper := func(t *testing.T, repo, message, date string) {
		writeT(t, repo, "dev/bin/helper.sh", "# A helper, not a script.\n")
		commitAt(t, repo, message, date)
	}
	for _, c := range []struct {
		name  string
		queue func(t *testing.T, home, repo, dev string)
		want  string
	}{
		{
			name: "a conflict stops the fold",
			queue: func(t *testing.T, home, repo, dev string) {
				remember(t, dev, "a-fact", fact)
				writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "B."))
				commitAt(t, repo, "test: an edit by hand", twinAt)
				writeT(t, repo, "dev/memories/a-fact.md", memoryText("a-fact", fact, "C."))
				commitAt(t, repo, "fixup! remember: dev/memories/a-fact.md", twinAt)
			},
			want: "the fold at push would stop: fold the fixups: a rebase stopped on a conflict in dev/memories/a-fact.md",
		},
		{
			name: "twins with one subject and one date, and the rebase drops the first",
			queue: func(t *testing.T, home, repo, dev string) {
				remember(t, dev, "a-fact", fact)
				handIndex(t, repo, sharper, "test: a twin", twinAt)
				helper(t, repo, "test: a twin", twinAt)
				reviseT(t, dev, memoryText("a-fact", sharper, "A fact."), "a-fact", "Sharper.")
			},
			want: `"test: a twin" matches 2 commits of the queue`,
		},
		{
			name: "twins with one subject and one date, and the rebase drops the second",
			queue: func(t *testing.T, home, repo, dev string) {
				remember(t, dev, "a-fact", fact)
				helper(t, repo, "test: a twin", twinAt)
				handIndex(t, repo, sharper, "test: a twin", twinAt)
				reviseT(t, dev, memoryText("a-fact", sharper, "A fact."), "a-fact", "Sharper.")
			},
			want: `"test: a twin" matches 2 commits of the queue`,
		},
		{
			name: "the rebase drops a target with a fixup",
			queue: func(t *testing.T, home, repo, dev string) {
				remember(t, dev, "a-fact", fact)
				handIndex(t, repo, sharper, "test: an index by hand", twinAt)
				helper(t, repo, "fixup! test: an index by hand", twinAt)
				reviseT(t, dev, memoryText("a-fact", sharper, "A fact."), "a-fact", "Sharper.")
			},
			want: `the rebase drops "test: an index by hand" as empty`,
		},
		{
			name: "an ignored file",
			queue: func(t *testing.T, home, repo, dev string) {
				remember(t, dev, "a-fact", fact)
				reviseT(t, dev, memoryText("a-fact", fact, "Sharper."), "a-fact", "Sharper.")
				writeT(t, repo, ".git/info/exclude", "*.tmp\n")
				writeT(t, repo, "dev/scratch.tmp", "An ignored file.\n")
			},
			want: "dev/scratch.tmp",
		},
		{
			name: "an ignored file that the status of the repo hides",
			queue: func(t *testing.T, home, repo, dev string) {
				remember(t, dev, "a-fact", fact)
				gitT(t, repo, "config", "status.showUntrackedFiles", "no")
				writeT(t, repo, ".git/info/exclude", "*.tmp\n")
				writeT(t, repo, "dev/scratch.tmp", "An ignored file.\n")
			},
			want: "dev/scratch.tmp",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			c.queue(t, home, repo, mkdirT(t, filepath.Join(home, "dev")))
			before := stateOf(t, repo)
			got, err := preview(repo, false)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("preview = %+v, error %v, want an error that holds %q", got, err, c.want)
			}
			if after := stateOf(t, repo); after != before {
				t.Errorf("the repo before the preview:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestPreviewRunsNoHook(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "a-fact", "Read when you test a fact.")
	remember(t, dev, "b-fact", "Read before you deploy on Fridays.")
	reviseT(t, dev, memoryText("a-fact", "Read when you test a fact.", "Sharper."), "a-fact", "Sharper.")
	// A signed commit makes git run the signing helper when it shows the signature.
	writeT(t, repo, "dev/bin/helper.sh", "# A helper, not a script.\n")
	gitT(t, repo, "add", "-A")
	signed := strings.TrimSpace(gitStdinT(t, repo, "tree "+strings.TrimSpace(gitT(t, repo, "write-tree"))+"\n"+
		"parent "+strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))+"\n"+
		"author Test <test@example.com> 1793646698 +0000\ncommitter Test <test@example.com> 1793646698 +0000\n"+
		"gpgsig -----BEGIN PGP SIGNATURE-----\n \n c2lnbmF0dXJl\n -----END PGP SIGNATURE-----\n\ntest: a signed commit\n",
		"hash-object", "-t", "commit", "-w", "--stdin"))
	gitT(t, repo, "update-ref", "refs/heads/main", signed)
	clean(t, repo)

	// Each hook and helper writes a file named after it into ran, so the test sees every one that runs.
	ran, hooks := t.TempDir(), t.TempDir()
	script := func(dir, name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\ntouch '"+filepath.Join(ran, name)+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, name := range []string{"post-checkout", "pre-commit", "commit-msg", "post-commit", "post-rewrite"} {
		script(hooks, name)
	}
	helpers := t.TempDir()
	monitor, gpg := script(helpers, "fsmonitor"), script(helpers, "gpg")
	global := filepath.Join(t.TempDir(), "gitconfig")
	writeT(t, filepath.Dir(global), "gitconfig", "[core]\n\thooksPath = "+hooks+"\n\tfsmonitor = "+monitor+"\n"+
		"[commit]\n\tgpgSign = true\n[log]\n\tshowSignature = true\n[gpg]\n\tprogram = "+gpg+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)

	if _, err := preview(repo, false); err != nil {
		t.Fatalf("preview: %v", err)
	}
	if left, err := os.ReadDir(ran); err != nil || len(left) > 0 {
		t.Errorf("hooks ran during the preview: %v, %v", left, err)
	}

	// The hooks and the helpers do run for git with this config, so the check above can fail.
	other := t.TempDir()
	gitT(t, other, "init", "-q")
	gitT(t, other, "status")
	store.Git(other, "commit", "-q", "--allow-empty", "-m", "test: a commit") // the helper signs nothing, so the commit fails
	gitT(t, repo, "show", "-s", signed)
	for _, name := range []string{"fsmonitor", "pre-commit", "gpg"} {
		if _, err := os.Stat(filepath.Join(ran, name)); err != nil {
			t.Errorf("the hook %s did not run outside the preview: %v", name, err)
		}
	}
}

func TestPreviewSymlinkFolder(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	_, repo, _ := newMemory(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, "lab")); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "test: a symlink")
	// The working tree replaces the symlink of main with a folder, so the clone would hold a symlink there.
	if err := os.Remove(filepath.Join(repo, "lab")); err != nil {
		t.Fatal(err)
	}
	mkdirT(t, filepath.Join(repo, "lab", "sub"))
	if got, err := preview(repo, false); err == nil || !strings.Contains(err.Error(), "lab in the memory repo is a symlink") {
		t.Errorf("preview = %+v, error %v, want a refusal of the symlink lab", got, err)
	}
	if left, err := os.ReadDir(outside); err != nil || len(left) > 0 {
		t.Errorf("the preview wrote %v, %v outside the clone, want nothing", left, err)
	}
}

// changedWords returns the lines of a porcelain word diff that add or remove words, without the headers.
func changedWords(words string) []string {
	var lines []string
	for _, line := range strings.Split(words, "\n") {
		if (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")) && !strings.HasPrefix(line, "+++ ") && !strings.HasPrefix(line, "--- ") {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestPreviewPatches(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	remember(t, dev, "gone-fact", "Read when the cache misbehaves.")
	pushYes(t, home)
	remember(t, dev, "deploy-fact", "Read when you deploy the shop.")
	remember(t, dev, "merge-fact", "Read before you merge a branch.")
	merge := filepath.Join(repo, "dev", "memories", "merge-fact.md")
	text := readT(t, merge)
	writeT(t, repo, "dev/memories/merge-fact.md", strings.TrimSuffix(text, "\n")+"   \n")
	gitT(t, repo, "commit", "-q", "-am", "test: whitespace by hand")
	if out, err := run(t, dev, "", "forget", "gone-fact", "--reason", "Gone."); err != nil {
		t.Fatalf("forget: %v\n%s", err, out)
	}
	// A note by hand at a new level leaves its index stale, so push adds an index commit.
	writeT(t, repo, "lab/notes/trial.md", "---\nname: trial\ndescription: \"Read when you try the lab.\"\n---\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "test: a note by hand")

	got, err := preview(repo, true)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	file := func(subject, path string) (fileStatus, fileDiff) {
		t.Helper()
		for _, p := range got {
			if p.subject != subject {
				continue
			}
			for i, f := range p.files {
				if f.path == path {
					return f, p.diffs[i]
				}
			}
		}
		t.Fatalf("no entry %q with the file %s in %+v", subject, path, got)
		return fileStatus{}, fileDiff{}
	}

	f, d := file("remember: dev/memories/deploy-fact.md", "dev/memories/deploy-fact.md")
	if f.status != "A" || d.before != nil || d.after == nil || !strings.Contains(*d.after, "Read when you deploy the shop.") {
		t.Errorf("the added memory: %+v, before %v, after %v", f, d.before, d.after)
	}
	_, deployIndex := file("remember: dev/memories/deploy-fact.md", "dev/README.md")
	_, mergeIndex := file("remember: dev/memories/merge-fact.md", "dev/README.md")
	if !strings.Contains(deployIndex.patch, "+- [deploy-fact]") || strings.Contains(deployIndex.patch, "+- [merge-fact]") {
		t.Errorf("the index in the first entry:\n%s", deployIndex.patch)
	}
	if !strings.Contains(mergeIndex.patch, "+- [merge-fact]") || strings.Contains(mergeIndex.patch, "+- [deploy-fact]") {
		t.Errorf("the index in the second entry:\n%s", mergeIndex.patch)
	}

	_, d = file("test: whitespace by hand", "dev/memories/merge-fact.md")
	if !strings.Contains(d.patch, "\n+") || len(changedWords(d.words)) != 0 {
		t.Errorf("a change of whitespace: patch\n%s\nwords\n%s", d.patch, d.words)
	}

	f, d = file("forget: dev/memories/gone-fact.md", "dev/memories/gone-fact.md")
	if f.status != "D" || d.after != nil || d.before == nil || !strings.Contains(*d.before, "Read when the cache misbehaves.") {
		t.Errorf("the removed memory: %+v, before %v, after %v", f, d.before, d.after)
	}

	index := false
	for _, p := range got {
		if p.target == "" && strings.HasPrefix(p.subject, "index: ") {
			index = true
			if len(p.diffs) != len(p.files) || len(p.diffs) == 0 || p.diffs[0].patch == "" {
				t.Errorf("the index commit of push: %+v", p)
			}
		}
	}
	if !index {
		t.Errorf("preview = %+v, want an index commit of push", got)
	}
}

func TestFilesOfExactPaths(t *testing.T) {
	_, repo, _ := newMemory(t)
	for _, p := range []string{"lab/notes/two words.md", "lab/notes/café.md", "lab/notes/a\"quote.md"} {
		writeT(t, repo, p, "A file.\n")
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "test: odd paths")
	files, err := filesOf(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := []fileStatus{{"A", "lab/notes/a\"quote.md"}, {"A", "lab/notes/café.md"}, {"A", "lab/notes/two words.md"}}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("filesOf = %q, want %q", files, want)
	}
	d, err := diffOf(repo, headCommit(repo), files[1])
	if err != nil || d.after == nil || *d.after != "A file.\n" || !strings.Contains(d.patch, "+A file.") {
		t.Errorf("diffOf café.md = %+v, %v", d, err)
	}
}
