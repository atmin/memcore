package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// autoPushKey is the setting of the automatic push, in the git configuration of the memory repo.
const autoPushKey = "memcore.autopush"

// autoPushDelay reads the setting of the automatic push. An unset key means off. A value is the delay before a
// chain is ripe, as time.ParseDuration reads it, such as 24h. 0s means no delay.
func autoPushDelay(repo string) (delay time.Duration, on bool, err error) {
	out, err := store.Git(repo, "config", "--get", autoPushKey)
	if err != nil {
		// Only the exit status 1 of git means that the key is unset.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read the setting %s: %w", autoPushKey, err)
	}
	value := strings.TrimSpace(out)
	delay, err = time.ParseDuration(value)
	if err != nil {
		return 0, false, fmt.Errorf("read the setting %s: the value %q is no duration, such as 24h: %w", autoPushKey, value, err)
	}
	if delay < 0 {
		return 0, false, fmt.Errorf("read the setting %s: the value %q is negative", autoPushKey, value)
	}
	return delay, true, nil
}

// changedPath returns the record of the newest date of each chain, in the local state.
func changedPath(repo string) string {
	return filepath.Join(store.StateDir(repo), "changed.json")
}

// readChanged reads the record of the newest date of each chain. The record maps the key of the target of a
// chain, by keyOf, to the newest author date of the commits of the chain. A missing file reads as an empty
// record. A file that memcore cannot read is an error, because the record may hold the only date of a fixup
// that a fold removed.
func readChanged(repo string) (map[string]time.Time, error) {
	changed := map[string]time.Time{}
	b, err := os.ReadFile(changedPath(repo))
	if errors.Is(err, fs.ErrNotExist) {
		return changed, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the dates of the chains: %w", err)
	}
	if err := json.Unmarshal(b, &changed); err != nil {
		return nil, fmt.Errorf("read the dates of the chains: the file %s is damaged, so the person removes it, and each chain then counts from its newest commit: %w", changedPath(repo), err)
	}
	if changed == nil {
		return nil, fmt.Errorf("read the dates of the chains: the file %s holds no record, so the person removes it", changedPath(repo))
	}
	return changed, nil
}

// recordChanged records the newest author date of each chain before a fold removes the dates of the fixups.
// A fold keeps the subject and the author date of a target, so the folded commit has the same key. Two commits
// can share a key, and they then share the newer date, which only makes a chain wait longer.
// It keeps the newer date of each key, and removes each key that no target of the queue has. It writes the
// file whole, through a temporary file and a rename. The caller holds the write lock.
func recordChanged(repo string, chains []*chain) error {
	old, err := readChanged(repo)
	if err != nil {
		return err
	}
	changed := map[string]time.Time{}
	for _, ch := range chains {
		_, key, err := keyOf(repo, ch.target.sha)
		if err != nil {
			return err
		}
		newest, err := newestDate(repo, ch)
		if err != nil {
			return err
		}
		for _, t := range []time.Time{old[key], changed[key]} {
			if t.After(newest) {
				newest = t
			}
		}
		changed[key] = newest
	}
	b, err := json.Marshal(changed)
	if err != nil {
		return fmt.Errorf("encode the dates of the chains: %w", err)
	}
	if err := os.MkdirAll(store.StateDir(repo), 0o755); err != nil {
		return fmt.Errorf("create the local state: %w", err)
	}
	tmp, err := os.CreateTemp(store.StateDir(repo), "changed-*.json")
	if err != nil {
		return fmt.Errorf("write the dates of the chains: %w", err)
	}
	defer os.Remove(tmp.Name()) // after the rename, this removes nothing
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write the dates of the chains: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the dates of the chains: %w", err)
	}
	if err := os.Rename(tmp.Name(), changedPath(repo)); err != nil {
		return fmt.Errorf("write the dates of the chains: %w", err)
	}
	return nil
}

// newestDate returns the newest author date of the commits of a chain.
func newestDate(repo string, ch *chain) (time.Time, error) {
	var newest time.Time
	for _, c := range append([]commit{ch.target}, ch.fixups...) {
		t, err := authorDate(repo, c.sha)
		if err != nil {
			return time.Time{}, err
		}
		if t.After(newest) {
			newest = t
		}
	}
	return newest, nil
}

// authorDate returns the author date of a commit.
func authorDate(repo, sha string) (time.Time, error) {
	out, err := store.Git(repo, "show", "-s", "--no-show-signature", "--format=%at", sha)
	if err != nil {
		return time.Time{}, fmt.Errorf("read the author date of %s: %w", short(sha), err)
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("read the author date of %s: %w", short(sha), err)
	}
	return time.Unix(sec, 0).UTC(), nil
}

// The states of a chain for the automatic push.
const (
	statePush = "push" // the automatic push sends the chain now
	stateWait = "wait" // the chain gets ripe at a known time
	stateHeld = "held" // only the person pushes the chain
)

// verdict is what the automatic push does with one chain of the review queue.
type verdict struct {
	chain  *chain
	state  string
	reason string    // why a chain is held, or which earlier chain it waits behind
	ripe   time.Time // when a waiting chain gets ripe
}

// changedAt returns the newest change of a chain. It is the newer of two dates: the newest author date of its
// commits, and the date of the key of its target in the record that a fold writes.
func changedAt(repo string, ch *chain, changed map[string]time.Time) (time.Time, error) {
	newest, err := newestDate(repo, ch)
	if err != nil {
		return time.Time{}, err
	}
	_, key, err := keyOf(repo, ch.target.sha)
	if err != nil {
		return time.Time{}, err
	}
	if t := changed[key]; t.After(newest) {
		newest = t
	}
	return newest, nil
}

// queueHasMerge reports whether the review queue holds a merge commit.
func queueHasMerge(repo string) (bool, error) {
	rng := "HEAD"
	if hasAccepted(repo) {
		rng = acceptedRef + "..HEAD"
	}
	out, err := store.Git(repo, "rev-list", "--merges", "-n", "1", rng)
	if err != nil {
		return false, fmt.Errorf("look for a merge in the review queue: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

// sortQueue sorts the chains of the review queue for the automatic push, with the record of the dates.
func sortQueue(repo string, delay time.Duration, now time.Time) ([]verdict, error) {
	chains, _, err := chainsOf(repo)
	if err != nil {
		return nil, err
	}
	changed, err := readChanged(repo)
	if err != nil {
		return nil, err
	}
	return sortChains(repo, chains, changed, delay, now)
}

// sortChains gives each chain of the queue, oldest first, its verdict for the automatic push:
//   - held, when only the person may push a commit of the chain, by personReason;
//   - held, when the commits of the chain cancel each other, since the next push of the person drops it;
//   - held, when a document or a commit message of the chain holds a likely secret;
//   - wait, when the newest change of the chain is younger than the delay;
//   - push otherwise.
//
// A chain that is not held by itself also takes the state of an earlier chain that is not pushed, when both
// chains change one document. It is then held behind a held chain, and it waits for the later time behind a
// chain that waits.
func sortChains(repo string, chains []*chain, changed map[string]time.Time, delay time.Duration, now time.Time) ([]verdict, error) {
	verdicts := make([]verdict, 0, len(chains))
	touched := make([]chainPaths, 0, len(chains))
	for _, ch := range chains {
		v, err := ownVerdict(repo, ch, changed, delay, now)
		if err != nil {
			return nil, err
		}
		paths, err := pathsOf(repo, ch)
		if err != nil {
			return nil, err
		}
		if v.state != stateHeld {
			// A held chain stops this chain for good, so it counts before a chain that only waits. A chain that
			// waits itself takes the later of the two times.
			for i, earlier := range verdicts {
				if earlier.state == statePush || !paths.shares(touched[i]) {
					continue
				}
				switch {
				case earlier.state == stateHeld && v.state != stateHeld:
					v.state, v.ripe = stateHeld, time.Time{}
					v.reason = "it changes a document of the chain " + short(earlier.chain.target.sha) + ", which is held"
				case earlier.state == stateWait && v.state != stateHeld && earlier.ripe.After(v.ripe):
					v.state, v.ripe = stateWait, earlier.ripe
					v.reason = "it changes a document of the chain " + short(earlier.chain.target.sha) + ", which waits"
				}
			}
		}
		verdicts = append(verdicts, v)
		touched = append(touched, paths)
	}
	return verdicts, nil
}

// ownVerdict returns the verdict of a chain by its own commits, without the chains before it.
func ownVerdict(repo string, ch *chain, changed map[string]time.Time, delay time.Duration, now time.Time) (verdict, error) {
	v := verdict{chain: ch, state: stateHeld}
	for _, c := range append([]commit{ch.target}, ch.fixups...) {
		reason, err := personReason(repo, c.sha)
		if err != nil {
			return v, err
		}
		if reason != "" {
			v.reason = reason
			return v, nil
		}
	}
	cancelled, err := cancels(repo, ch)
	if err != nil {
		return v, err
	}
	if cancelled {
		v.reason = "its commits cancel each other"
		return v, nil
	}
	if v.reason, err = secretIn(repo, ch); err != nil || v.reason != "" {
		return v, err
	}
	at, err := changedAt(repo, ch, changed)
	if err != nil {
		return v, err
	}
	if ripe := at.Add(delay); ripe.After(now) {
		v.state, v.ripe = stateWait, ripe
		return v, nil
	}
	v.state = statePush
	return v, nil
}

// chainPaths are the paths that a chain changes, for the question whether two chains change one document.
type chainPaths struct {
	docs   map[string]bool // every path but an index
	topics map[string]bool // each topic index, and whether the chain changes its existence or its frontmatter
}

// shares reports whether two chains change one document. A level index never counts, because memcore
// generates it. A topic index counts when one of the two chains changes its existence or its frontmatter.
func (a chainPaths) shares(b chainPaths) bool {
	for p := range a.docs {
		if b.docs[p] {
			return true
		}
	}
	for p, own := range a.topics {
		if other, ok := b.topics[p]; ok && (own || other) {
			return true
		}
	}
	return false
}

// pathsOf returns the paths that the commits of a chain change.
func pathsOf(repo string, ch *chain) (chainPaths, error) {
	paths := chainPaths{docs: map[string]bool{}, topics: map[string]bool{}}
	for _, c := range append([]commit{ch.target}, ch.fixups...) {
		out, err := store.Git(repo, "diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z", "--root", c.sha)
		if err != nil {
			return paths, fmt.Errorf("list the files of %s: %w", short(c.sha), err)
		}
		parent, hasParent := revParse(repo, c.sha+"^")
		for _, p := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
			switch _, level := indexLevel(p); {
			case p == "" || level:
			case store.IsTopicIndex(p):
				paths.topics[p] = paths.topics[p] || !hasParent || !sameTopicIndex(repo, parent, c.sha, p)
			default:
				paths.docs[p] = true
			}
		}
	}
	return paths, nil
}

// secretIn returns the reason that a chain is held for a likely secret, or "". It scans each commit message of
// the chain, and each path and each blob that a commit of the chain adds or changes. The reason names the
// first path, or the commit message, and never the secret: a secret in a path shows as its placeholder.
func secretIn(repo string, ch *chain) (string, error) {
	inMessage := false
	for _, c := range append([]commit{ch.target}, ch.fixups...) {
		out, err := store.Git(repo, "diff-tree", "-r", "-z", "--root", "--no-commit-id", "--raw", "--no-renames", "--ignore-submodules=none", c.sha)
		if err != nil {
			return "", fmt.Errorf("list the files of %s: %w", short(c.sha), err)
		}
		fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
		for i := 0; i+1 < len(fields); i += 2 {
			// A record is ":<old mode> <new mode> <old blob> <new blob> <status>", then the path.
			meta := strings.Fields(fields[i])
			if len(meta) != 5 {
				return "", fmt.Errorf("read the files of %s: git printed %q", short(c.sha), fields[i])
			}
			if meta[1] != "100644" && meta[1] != "100755" {
				if len(guards.Secrets(fields[i+1])) > 0 {
					return "a likely secret in " + guards.Redact(fields[i+1]), nil
				}
				continue // a removed path, or no regular file
			}
			text, err := store.Git(repo, "cat-file", "blob", meta[3])
			if err != nil {
				return "", fmt.Errorf("read %s at %s: %w", fields[i+1], short(c.sha), err)
			}
			// A path may hold a secret itself, and the remote would get the path too.
			if len(guards.Secrets(text)) > 0 || len(guards.Secrets(fields[i+1])) > 0 {
				return "a likely secret in " + guards.Redact(fields[i+1]), nil
			}
		}
		message, err := store.Git(repo, "show", "-s", "--no-show-signature", "--format=%B", c.sha)
		if err != nil {
			return "", fmt.Errorf("read the message of %s: %w", short(c.sha), err)
		}
		inMessage = inMessage || len(guards.Secrets(message)) > 0
	}
	if inMessage {
		return "a likely secret in the commit message", nil
	}
	return "", nil
}

// buildAside builds the commits of an automatic push in a scratch clone under tmp, and changes nothing in the
// memory repo. It checks out the accepted commit in the clone, detached. Then it does these for each chain,
// oldest first:
//   - It applies the target and then each fixup. When only indexes conflict, it writes them again from the
//     documents, as a rebase of memcore does.
//   - It writes the indexes as a fold does.
//   - It commits once, with the message, the author and the author date of the target.
//
// A chain that leaves no change against the commit before it gets no commit, and is not in built. A conflict
// in any other document is an error that names the chain by its target. tip is the last commit that it built,
// or "" with none. The clone tmp/clone holds the commits, and the caller fetches tip from it.
func buildAside(repo, tmp string, chains []*chain) (tip string, built []*chain, err error) {
	base, ok := revParse(repo, acceptedRef)
	if !ok {
		return "", nil, errors.New("build the automatic push: the memory repo has no accepted history")
	}
	clone, err := scratchClone(repo, tmp)
	if err != nil {
		return "", nil, err
	}
	if _, err := store.Git(clone, "checkout", "-q", "--detach", base); err != nil {
		return "", nil, fmt.Errorf("check out the accepted commit in the clone: %w", err)
	}
	if err := dropQueueFolders(repo, clone); err != nil {
		return "", nil, err
	}
	for _, ch := range chains {
		name := short(ch.target.sha)
		for _, c := range append([]commit{ch.target}, ch.fixups...) {
			if _, err := store.Git(clone, "cherry-pick", "--no-commit", c.sha); err != nil {
				out, derr := store.Git(clone, "diff", "--name-only", "-z", "--diff-filter=U")
				if derr != nil || out == "" {
					return "", nil, fmt.Errorf("apply %s of the chain %s in the clone: %w", short(c.sha), name, err)
				}
				paths := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
				var ce *conflictError
				if err := resolveIndexes(clone, paths); errors.As(err, &ce) {
					return "", nil, fmt.Errorf("the chain %s conflicts with the commits before it in %s, so the automatic push built nothing", name, strings.Join(ce.paths, ", "))
				} else if err != nil {
					return "", nil, fmt.Errorf("apply %s of the chain %s in the clone: %w", short(c.sha), name, err)
				}
			}
		}
		indexes, err := writeIndexes(clone)
		if err != nil {
			return "", nil, fmt.Errorf("write the indexes of the chain %s in the clone: %w", name, err)
		}
		if _, err := store.Git(clone, append([]string{"--literal-pathspecs", "add", "-A", "--"}, indexes...)...); err != nil {
			return "", nil, fmt.Errorf("stage the indexes of the chain %s in the clone: %w", name, err)
		}
		if _, err := store.Git(clone, "diff", "--cached", "--quiet"); err == nil {
			continue // the commit before the chain holds its change
		}
		if err := commitAside(clone, tmp, ch.target.sha); err != nil {
			return "", nil, err
		}
		built = append(built, ch)
	}
	if len(built) > 0 {
		tip = headCommit(clone)
	}
	return tip, built, nil
}

// commitAside commits the staged change of the clone with the message, the author and the author date of a
// target. It passes the message in a file with --cleanup=verbatim, so no setting of git changes the message.
func commitAside(clone, tmp, target string) error {
	out, err := store.Git(clone, "show", "-s", "--no-show-signature", "--date=raw", "--format=%an <%ae>%x00%ad%x00%B", target)
	if err != nil {
		return fmt.Errorf("read the commit %s: %w", short(target), err)
	}
	fields := strings.SplitN(out, "\x00", 3)
	if len(fields) != 3 {
		return fmt.Errorf("read the commit %s: git printed %q", short(target), out)
	}
	file := filepath.Join(tmp, "message")
	if err := os.WriteFile(file, []byte(strings.TrimRight(fields[2], "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write the message of the chain %s: %w", short(target), err)
	}
	// The raw date is "<seconds> <zone>", and @ marks it as that form for every length of the seconds.
	if _, err := store.Git(clone, "commit", "-q", "--cleanup=verbatim", "-F", file, "--author="+fields[0], "--date=@"+fields[1]); err != nil {
		return fmt.Errorf("commit the chain %s in the clone: %w", short(target), err)
	}
	return nil
}

// dropQueueFolders removes from the clone each folder that holds no file there, while the review queue tracks
// a file in it. scratchClone copies every folder of the working tree, since an index lists the empty folders
// of its level. A folder of a chain that is not built is no empty folder: the accepted history lacks it. The
// build of a chain creates the folders of its own files.
func dropQueueFolders(repo, clone string) error {
	out, err := store.Git(repo, "ls-tree", "-r", "-d", "-z", "--name-only", "--full-tree", "HEAD")
	if err != nil {
		return fmt.Errorf("list the folders of the review queue: %w", err)
	}
	folders := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	slices.Sort(folders)
	slices.Reverse(folders) // a folder comes after the folders inside it
	for _, folder := range folders {
		if folder == "" {
			continue
		}
		if err := noSymlink(clone, folder); err != nil {
			return err
		}
		dir := filepath.Join(clone, filepath.FromSlash(folder))
		if _, err := os.Stat(dir); err != nil || holdsFile(dir) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove the folder %s of the clone: %w", folder, err)
		}
	}
	return nil
}
