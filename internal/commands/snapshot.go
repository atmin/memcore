package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/readers"
	"github.com/atmin/memcore/internal/store"
)

// transcriptsRef is the branch of the snapshots, which memcore never checks out and never pushes.
const transcriptsRef = "refs/heads/transcripts"

// snapshotState is the state of snapshots, .git/memcore/snapshot.json.
type snapshotState struct {
	Time  string                 `json:"time,omitempty"` // the time of the last snapshot
	Files map[string]sourceState `json:"files"`          // each source file at its last snapshot, by its absolute path
}

// sourceState is a source file as the last snapshot read it.
type sourceState struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"mtime"`             // in nanoseconds
	Partial bool  `json:"partial,omitempty"` // its last line had no newline
}

// source is one transcript to snapshot: its file, and the names of its path on the branch.
type source struct {
	file     string
	folder   string   // the project folder, as Claude Code names it
	rest     []string // the names below the folder: the session file, or the session, subagents and the file
	subagent bool
}

// branchPath is the path of a transcript on the branch. The project folder becomes its digest. Claude Code
// writes each character of a directory that is not a letter or a digit as -, so no check of the name can
// find every secret in it. The header of the transcript keeps the directory, redacted.
func (s source) branchPath() string {
	parts := []string{"claude", digest(s.folder)}
	for _, r := range s.rest {
		if r == "subagents" {
			parts = append(parts, r)
		} else {
			parts = append(parts, safeName(r))
		}
	}
	return path.Join(parts...)
}

// runSnapshot commits the new transcripts of Claude Code, or of --from, to the transcripts branch.
func runSnapshot(env Env, args []string) error {
	flags := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	from := flags.String("from", "", "read another folder in the layout of ~/.claude/projects")
	if err := flags.Parse(args); err != nil {
		return usageErr(err.Error())
	}
	if flags.NArg() != 0 {
		return usageErr("usage: memcore snapshot [--from <dir>]")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	src := *from
	if src == "" {
		dir, err := claudeDir()
		if err != nil {
			return err
		}
		src = filepath.Join(dir, "projects")
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(env.Out, "No transcripts in %s.\n", tilde(src))
			return nil
		}
	} else {
		if !filepath.IsAbs(src) {
			src = filepath.Join(env.Dir, src)
		}
		if info, err := os.Stat(src); err != nil || !info.IsDir() {
			return inputErrf("--from %s is not a folder", *from)
		}
	}
	n, err := snapshot(env.Out, repo, src)
	if err != nil {
		return err
	}
	if n == 0 {
		fmt.Fprintln(env.Out, "No new transcripts.")
		return nil
	}
	fmt.Fprintf(env.Out, "Snapshotted %s onto the transcripts branch.\n", count(n, "transcript"))
	return nil
}

// snapshot commits the changed transcripts under src to the transcripts branch, and returns how many it committed.
// It holds its own lock, so it never blocks a write, and it never touches main, HEAD or the index of the repo.
func snapshot(out io.Writer, repo, src string) (int, error) {
	unlock, err := store.LockFile(repo, "snapshot.lock")
	if err != nil {
		return 0, err
	}
	defer unlock()
	state, err := readSnapshotState(repo)
	if err != nil {
		return 0, err
	}
	parent, _ := revParse(repo, transcriptsRef)
	existing := map[string]string{} // each path on the branch, to its blob
	if parent != "" {
		tree, err := store.Git(repo, "ls-tree", "-r", "-z", parent)
		if err != nil {
			return 0, fmt.Errorf("read the transcripts branch: %w", err)
		}
		for _, entry := range strings.Split(strings.TrimRight(tree, "\x00"), "\x00") {
			meta, p, ok := strings.Cut(entry, "\t")
			if f := strings.Fields(meta); ok && len(f) == 3 {
				existing[p] = f[2]
			}
		}
	}
	sources, err := transcriptFiles(src)
	if err != nil {
		return 0, err
	}

	var entries []string
	for _, s := range sources {
		info, err := os.Stat(s.file)
		if err != nil {
			return 0, fmt.Errorf("read the transcript %s: %w", s.file, err)
		}
		now := sourceState{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		if prev, ok := state.Files[s.file]; ok && prev.Size == now.Size && prev.ModTime == now.ModTime {
			if prev.Partial {
				fmt.Fprintf(out, "%s: cut off, its last line has no end\n", tilde(s.file))
			}
			continue
		}
		f, err := os.Open(s.file)
		if err != nil {
			return 0, fmt.Errorf("read the transcript %s: %w", s.file, err)
		}
		var trimmed bytes.Buffer
		stats, err := readers.Trim(f, &trimmed, readers.TrimOptions{Redact: guards.Redact, Subagent: s.subagent})
		f.Close()
		if err != nil {
			return 0, fmt.Errorf("trim the transcript %s: %w", s.file, err)
		}
		if stats.Broken > 0 {
			fmt.Fprintf(out, "%s: %s left out\n", tilde(s.file), count(stats.Broken, "broken line"))
		}
		now.Partial = stats.Partial
		state.Files[s.file] = now
		if trimmed.Len() == 0 {
			continue
		}
		p, ok, err := place(repo, existing, s.branchPath(), trimmed.String())
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		blob, err := store.GitStdin(repo, trimmed.String(), "hash-object", "-w", "--stdin")
		if err != nil {
			return 0, fmt.Errorf("store the transcript %s: %w", s.file, err)
		}
		existing[p] = strings.TrimSpace(blob)
		entries = append(entries, "100644 "+strings.TrimSpace(blob)+"\t"+p)
	}
	if len(entries) > 0 {
		if err := commitSnapshot(repo, parent, entries); err != nil {
			return 0, err
		}
	}
	state.Time = time.Now().UTC().Format(time.RFC3339)
	return len(entries), writeSnapshotState(repo, state)
}

// place returns where a trimmed transcript goes on the branch, and false when the branch already holds it.
// It replaces a transcript only with one that grows it. A version that diverges goes beside it, named by its blob.
func place(repo string, existing map[string]string, p, content string) (string, bool, error) {
	sha, ok := existing[p]
	if !ok {
		return p, true, nil
	}
	old, err := store.Git(repo, "cat-file", "blob", sha)
	if err != nil {
		return "", false, fmt.Errorf("read %s on the transcripts branch: %w", p, err)
	}
	switch {
	case old == content, strings.HasPrefix(old, content):
		return "", false, nil // the same, or a shorter copy
	case strings.HasPrefix(content, old):
		return p, true, nil
	}
	out, err := store.GitStdin(repo, content, "hash-object", "--stdin")
	if err != nil {
		return "", false, fmt.Errorf("hash a transcript: %w", err)
	}
	blob := strings.TrimSpace(out)
	// The name holds the first 8 digits of the blob, and more when another transcript holds that name.
	for n := 8; ; n += 4 {
		n = min(n, len(blob))
		beside := strings.TrimSuffix(p, ".jsonl") + "." + blob[:n] + ".jsonl"
		switch sha, ok := existing[beside]; {
		case !ok:
			return beside, true, nil
		case sha == blob, n == len(blob):
			return "", false, nil
		}
	}
}

// commitSnapshot commits the entries onto the transcripts branch, through a private index.
func commitSnapshot(repo, parent string, entries []string) error {
	index := filepath.Join(store.StateDir(repo), "transcripts.index")
	base := []string{"read-tree", "--empty"}
	if parent != "" {
		base = []string{"read-tree", parent}
	}
	if _, err := store.GitIndex(repo, index, "", base...); err != nil {
		return fmt.Errorf("read the transcripts branch: %w", err)
	}
	if _, err := store.GitIndex(repo, index, strings.Join(entries, "\n")+"\n", "update-index", "--add", "--index-info"); err != nil {
		return fmt.Errorf("add the transcripts: %w", err)
	}
	tree, err := store.GitIndex(repo, index, "", "write-tree")
	if err != nil {
		return fmt.Errorf("write the tree of the snapshot: %w", err)
	}
	args := []string{"commit-tree", strings.TrimSpace(tree), "-m", "snapshot: " + count(len(entries), "transcript")}
	old := "" // the branch must not exist yet
	if parent != "" {
		args = append(args, "-p", parent)
		old = parent
	}
	commit, err := store.Git(repo, args...)
	if err != nil {
		return fmt.Errorf("commit the snapshot: %w", err)
	}
	if _, err := store.Git(repo, "update-ref", transcriptsRef, strings.TrimSpace(commit), old); err != nil {
		return fmt.Errorf("move the transcripts branch: %w", err)
	}
	return nil
}

// transcriptFiles lists the transcripts under src: <folder>/<session>.jsonl, and <folder>/<session>/subagents/<file>.jsonl.
func transcriptFiles(src string) ([]source, error) {
	folders, err := os.ReadDir(src)
	if err != nil {
		return nil, fmt.Errorf("read the transcripts in %s: %w", src, err)
	}
	var sources []source
	for _, folder := range folders {
		if !folder.IsDir() {
			continue
		}
		dir := filepath.Join(src, folder.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read the transcripts in %s: %w", dir, err)
		}
		for _, e := range entries {
			switch {
			case e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl"):
				sources = append(sources, source{filepath.Join(dir, e.Name()), folder.Name(), []string{e.Name()}, false})
			case e.IsDir():
				subs, err := os.ReadDir(filepath.Join(dir, e.Name(), "subagents"))
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("read the subagents of %s: %w", e.Name(), err)
				}
				for _, sub := range subs {
					if sub.Type().IsRegular() && strings.HasSuffix(sub.Name(), ".jsonl") {
						sources = append(sources, source{
							filepath.Join(dir, e.Name(), "subagents", sub.Name()),
							folder.Name(), []string{e.Name(), "subagents", sub.Name()}, true,
						})
					}
				}
			}
		}
	}
	return sources, nil
}

// safeName returns the name of a file for the transcripts branch. A name that may hold a secret becomes
// redacted- and its digest, so it shows nothing of the secret, and two names stay apart.
func safeName(name string) string {
	if len(guards.Secrets(name)) == 0 {
		return name
	}
	safe := "redacted-" + digest(name)
	if strings.HasSuffix(name, ".jsonl") {
		safe += ".jsonl"
	}
	return safe
}

// digest returns 32 hex digits of the hash of a name.
func digest(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])[:32]
}

// readSnapshotState reads .git/memcore/snapshot.json, or returns an empty state when it does not exist.
func readSnapshotState(repo string) (snapshotState, error) {
	state := snapshotState{Files: map[string]sourceState{}}
	b, err := os.ReadFile(filepath.Join(store.StateDir(repo), "snapshot.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read the state of snapshots: %w", err)
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return state, fmt.Errorf("read the state of snapshots: %w", err)
	}
	if state.Files == nil {
		state.Files = map[string]sourceState{}
	}
	return state, nil
}

// writeSnapshotState writes .git/memcore/snapshot.json through a temporary file.
func writeSnapshotState(repo string, state snapshotState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("write the state of snapshots: %w", err)
	}
	p := filepath.Join(store.StateDir(repo), "snapshot.json")
	if err := os.WriteFile(p+".tmp", append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write the state of snapshots: %w", err)
	}
	if err := os.Rename(p+".tmp", p); err != nil {
		return fmt.Errorf("write the state of snapshots: %w", err)
	}
	return nil
}

// count writes a number with its noun, as "1 transcript", "2 transcripts" or "2 memories".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if stem, ok := strings.CutSuffix(noun, "y"); ok {
		return fmt.Sprintf("%d %sies", n, stem)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
