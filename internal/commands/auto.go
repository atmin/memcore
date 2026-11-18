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
	"syscall"
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
	// A fixup that leads a chain found no commit to fold into. Only the person sends such a subject.
	if _, orphan := fixupTarget(ch.target.subject); orphan {
		v.reason = "a fixup with no commit to fold into"
		return v, nil
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

// autoRef keeps the built tip of an automatic push in the memory repo, until its removal from the queue ends.
const autoRef = "refs/memcore/auto"

// pending is the record of an automatic push whose chains may still be in the review queue.
type pending struct {
	Tip      string   `json:"tip"`                // the built tip that the automatic push sent
	Commits  []string `json:"commits"`            // each commit of the built chains in the queue
	Open     bool     `json:"open,omitempty"`     // a push of the tip ended with an error that was no plain no
	Removing bool     `json:"removing,omitempty"` // the removal of the commits from the queue started
	Base     string   `json:"base,omitempty"`     // the accepted commit that the removal moves the queue onto
}

// pushLock is the file of the local state that an automatic push locks while its git process pushes. The git
// process holds the lock itself. So the lock also stays when memcore dies during the push and git goes on.
const pushLock = "autopush.pushing"

// lockPush takes the lock of a running automatic push, and returns its open file. It returns false when
// another push holds the lock. The lock goes when every process that holds the file closes it.
func lockPush(repo string) (*os.File, bool, error) {
	f, err := os.OpenFile(filepath.Join(store.StateDir(repo), pushLock), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("open the lock of the automatic push: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("take the lock of the automatic push: %w", err)
	}
	return f, true, nil
}

// refusePushing returns an error while the push of a recorded automatic push still runs. Until it ends, no
// fetch can tell whether the remote gets its commits, so nothing may rewrite the queue. A free lock proves only
// that the git process of this machine ended: finishDrop keeps the record while the remote can still get the tip.
func refusePushing(repo string) error {
	if _, waits, err := readPending(repo); err != nil || !waits {
		return err
	}
	f, free, err := lockPush(repo)
	if err != nil {
		return err
	}
	if !free {
		return errors.New("an automatic push still runs, so memcore leaves the review queue as it is: run the command again when the push ends")
	}
	return f.Close()
}

func pendingPath(repo string) string {
	return filepath.Join(store.StateDir(repo), "autopush.pending")
}

// readPending reads the record of an automatic push, and returns false when there is none.
func readPending(repo string) (pending, bool, error) {
	var rec pending
	b, err := os.ReadFile(pendingPath(repo))
	if errors.Is(err, fs.ErrNotExist) {
		return rec, false, nil
	}
	if err != nil {
		return rec, false, fmt.Errorf("read the record of the automatic push: %w", err)
	}
	if err := json.Unmarshal(b, &rec); err != nil || rec.Tip == "" {
		return rec, false, fmt.Errorf("read the record of the automatic push: the file %s is damaged, so the person removes the pushed commits from the review queue by hand, and then removes the file", pendingPath(repo))
	}
	return rec, true, nil
}

// inPending reports whether the record of an automatic push names the commit sha of the review queue, or the
// target of its chain. Such a commit leaves the queue when that push ends.
func inPending(repo, sha string) (bool, error) {
	rec, waits, err := readPending(repo)
	if err != nil || !waits {
		return false, err
	}
	if slices.Contains(rec.Commits, sha) {
		return true, nil
	}
	chains, _, err := chainsOf(repo)
	if err != nil {
		return false, err
	}
	for _, ch := range chains {
		for _, f := range ch.fixups {
			if f.sha == sha {
				return slices.Contains(rec.Commits, ch.target.sha), nil
			}
		}
	}
	return false, nil
}

// writePending writes the record of an automatic push, whole, through a temporary file and a rename.
func writePending(repo string, rec pending) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode the record of the automatic push: %w", err)
	}
	tmp, err := os.CreateTemp(store.StateDir(repo), "autopush-*.pending")
	if err != nil {
		return fmt.Errorf("write the record of the automatic push: %w", err)
	}
	defer os.Remove(tmp.Name()) // after the rename, this removes nothing
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write the record of the automatic push: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the record of the automatic push: %w", err)
	}
	if err := os.Rename(tmp.Name(), pendingPath(repo)); err != nil {
		return fmt.Errorf("write the record of the automatic push: %w", err)
	}
	return nil
}

// clearPending removes the record of an automatic push, and the ref of its built tip.
func clearPending(repo string) error {
	if err := os.Remove(pendingPath(repo)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the record of the automatic push: %w", err)
	}
	if _, ok := revParse(repo, autoRef); ok {
		if _, err := store.Git(repo, "update-ref", "-d", autoRef); err != nil {
			return fmt.Errorf("remove the ref of the automatic push: %w", err)
		}
	}
	return nil
}

// pushAuto is the automatic push: memcore push --auto. It asks nothing. It pushes the chains of the review
// queue that sortChains gives the state push, as the commits that buildAside builds in a scratch clone.
// Its fetch rebases the queue as every fetch does. Nothing else changes the queue until the remote accepts the
// push. After that, finishDrop removes only the pushed chains. It holds the write lock for the whole run.
// When the record of an earlier automatic push stays after the fetch, the remote lacks that built tip and can
// still get it. Then it sends the same tip again, and builds nothing, also with the setting off. It never
// removes such a record itself: a push that lands late would then meet a queue that changed.
func pushAuto(env Env, repo string) error {
	delay, on, err := autoPushDelay(repo)
	if err != nil {
		return err
	}
	// A record is of a push that started with the setting on. The remote may hold its commits already, so that
	// push ends also with the setting off.
	if _, waits, err := readPending(repo); err != nil {
		return err
	} else if !on && !waits {
		fmt.Fprintln(env.Out, "The automatic push is off.")
		return nil
	}
	unlock, err := store.Lock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := cleanTree(repo, "the automatic push pushed nothing"); err != nil {
		return err
	}
	// A rebase turns a merge into plain commits, so the automatic push cannot keep it.
	if merge, err := queueHasMerge(repo); err != nil {
		return err
	} else if merge {
		fmt.Fprintln(env.Out, "The queue holds a merge commit, so the person pushes.")
		return nil
	}
	// With a record, the fetch below finishes the removal of the last automatic push, even with nothing ripe.
	if _, waits, err := readPending(repo); err != nil {
		return err
	} else if !waits {
		ripe, err := ripeChains(repo, delay)
		if err != nil {
			return err
		}
		if len(ripe) == 0 {
			fmt.Fprintln(env.Out, "Nothing is ripe.")
			return nil // no fetch, so the window and push --expect see a stable queue
		}
	}
	if err := Fetch(repo); err != nil {
		return err
	}
	// A record that stays after the fetch is of a built tip that the remote lacks and can still get. Only the
	// remote can end that: this push sends the same tip again, and builds nothing.
	if rec, waits, err := readPending(repo); err != nil {
		return err
	} else if waits {
		base, ok := revParse(repo, acceptedRef)
		if !ok {
			return errors.New("the record of an automatic push stays, and the memory repo has no accepted history")
		}
		return sendBuilt(env, repo, base, rec)
	}
	if !on {
		fmt.Fprintln(env.Out, "The automatic push is off.")
		return nil
	}
	base, ok := revParse(repo, acceptedRef)
	if !ok {
		fmt.Fprintln(env.Out, "The remote holds no accepted history, so the person pushes first.")
		return nil
	}
	// The fetch gives each commit of the queue a new ID, and it may drop a commit.
	ripe, err := ripeChains(repo, delay)
	if err != nil {
		return err
	}
	if len(ripe) == 0 {
		fmt.Fprintln(env.Out, "Nothing is ripe.")
		return nil
	}
	tmp, err := os.MkdirTemp("", "memcore-autopush-")
	if err != nil {
		return fmt.Errorf("create the directory of the automatic push: %w", err)
	}
	defer os.RemoveAll(tmp)
	tip, built, err := buildAside(repo, tmp, ripe)
	if err != nil {
		return err
	}
	if tip == "" {
		fmt.Fprintln(env.Out, "Nothing is ripe.")
		return nil
	}
	// The built commits come into the memory repo under a ref of their own. No branch moves.
	if _, err := store.Git(repo, "fetch", "-q", "--no-tags", "--no-write-fetch-head", "--refmap=", filepath.Join(tmp, "clone"), "HEAD"); err != nil {
		return fmt.Errorf("fetch the built commits from the clone: %w", err)
	}
	if _, err := store.Git(repo, "update-ref", autoRef, tip); err != nil {
		return fmt.Errorf("keep the built commits: %w", err)
	}
	rec := pending{Tip: tip}
	for _, ch := range built {
		for _, c := range append([]commit{ch.target}, ch.fixups...) {
			rec.Commits = append(rec.Commits, c.sha)
		}
	}
	return sendBuilt(env, repo, base, rec)
}

// sendBuilt pushes the built tip of a record, and then removes its chains from the review queue. base is the
// accepted commit that the built commits stand on. The caller holds the write lock.
func sendBuilt(env Env, repo, base string, rec pending) error {
	out, err := store.Git(repo, "rev-list", "--reverse", base+".."+rec.Tip)
	if err != nil {
		return fmt.Errorf("list the built commits: %w", err)
	}
	// The git process of the push holds this lock until it ends, also when memcore dies first. While the lock
	// is held, a fetch refuses, because it cannot tell yet whether the remote gets the commits.
	held, free, err := lockPush(repo)
	if err != nil {
		return err
	}
	if !free {
		return errors.New("another automatic push still runs, so the automatic push pushed nothing")
	}
	defer held.Close()
	if err := writePending(repo, rec); err != nil {
		return err
	}
	// The two settings keep the output of git as refused reads it: one ref, and no colour.
	args := append([]string{"-c", "push.followTags=false", "-c", "color.transport=false"}, pushArgs(rec.Tip)...)
	if err := gitPush(repo, held, args...); err != nil {
		// After a plain no, the remote never gets the built tip from this push. With no earlier push that is
		// still open, the record goes: a record that stayed would hold back every push and drop of the person.
		if refused(err) && !rec.Open {
			if cerr := clearPending(repo); cerr != nil {
				return fmt.Errorf("push to origin: %w; %v", err, cerr)
			}
			return fmt.Errorf("push to origin: %w", err)
		}
		if refused(err) {
			// An earlier push of this tip is still open, and the remote can take it late. So the record stays.
			return fmt.Errorf("push to origin: %w\nAn earlier push of the same commits may still end on the remote, so their record stays. When the remote never takes them, the person removes the file %s", err, pendingPath(repo))
		}
		// Otherwise the remote may have accepted the push, or may accept it later. The record and the ref
		// stay. The next fetch finds out, and the next automatic push sends the same tip again.
		rec.Open = true
		if werr := writePending(repo, rec); werr != nil {
			return fmt.Errorf("push to origin: %w; %v", err, werr)
		}
		return fmt.Errorf("push to origin: %w", err)
	}
	env.event().By, env.event().Pushed = "auto", strings.Fields(out)
	if err := accept(repo, rec.Tip); err != nil {
		return fmt.Errorf("the remote has the automatic push, and the next fetch removes its commits from the review queue: %w", err)
	}
	if err := finishDrop(repo); err != nil {
		return fmt.Errorf("the remote has the automatic push, and the next fetch removes its commits from the review queue: %w", err)
	}
	fmt.Fprintf(env.Out, "Pushed %s automatically.\n", commits(len(env.event().Pushed)))
	return nil
}

// refused reports whether the error of a git push says that the push ended with a plain no for the ref: git
// itself refused to send it, or the remote reported that it rejected it. git prints such a line with the mark
// "!" and the word rejected, and the line names the branch main. A line of the remote starts with "remote:", so no hook can forge one. Every other
// failure is open: the remote may still take the push.
func refused(err error) bool {
	if err == nil {
		return false
	}
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		if (strings.HasPrefix(line, "! [rejected]") || strings.HasPrefix(line, "! [remote rejected]")) && strings.Contains(line, " -> main (") {
			return true
		}
	}
	return false
}

// ripeChains returns the chains of the review queue that the automatic push sends now, oldest first.
func ripeChains(repo string, delay time.Duration) ([]*chain, error) {
	verdicts, err := sortQueue(repo, delay, clock())
	if err != nil {
		return nil, err
	}
	var ripe []*chain
	for _, v := range verdicts {
		if v.state == statePush {
			ripe = append(ripe, v.chain)
		}
	}
	return ripe, nil
}

// finishDrop removes from the review queue the chains that an automatic push sent, by its record. Without a
// record it does nothing. The caller holds the write lock, and the accepted ref is as the remote has it.
//   - When the accepted history lacks the built tip, the remote did not get the push yet. The remote can
//     still get it while the built tip and the queue both stand on the accepted commit: a push whose client
//     died can end late, and the next automatic push sends the same tip again. Then the record and the ref
//     stay, and so does the queue. Otherwise the remote rejects the built tip, and it removes only the record
//     and the ref.
//   - When the queue lacks a commit of the record, it changes nothing, and returns an error. The one exception
//     is a removal that ended and left only the record: the removal started, the queue holds no commit of the
//     record, and main stands on the commit that the removal moved the queue onto.
//   - Otherwise it runs one interactive rebase of main onto the accepted commit. Its todo drops each commit of
//     the record by its ID, and picks every other commit of the queue as it is. Then it removes the record
//     and the ref.
//
// When the rebase cannot run or stops on a conflict, the record stays, and the next fetch tries again.
func finishDrop(repo string) error {
	rec, waits, err := readPending(repo)
	if err != nil || !waits {
		return err
	}
	accepted, ok := revParse(repo, acceptedRef)
	_, built := revParse(repo, rec.Tip)
	has := false
	if ok && built {
		if has, err = isAncestor(repo, rec.Tip, accepted); err != nil {
			return fmt.Errorf("look for the automatic push in the accepted history: %w", err)
		}
	}
	if !has {
		if !ok || !built {
			return clearPending(repo)
		}
		// With the accepted commit under the queue too, the rebase of the fetch rewrites no commit of the record.
		for _, rev := range []string{rec.Tip, "HEAD"} {
			if under, err := isAncestor(repo, accepted, rev); err != nil {
				return fmt.Errorf("look for the accepted commit under the automatic push: %w", err)
			} else if !under {
				return clearPending(repo)
			}
		}
		return nil
	}
	// The rebase below rewrites the branch that is checked out, and the queue is the branch main.
	if out, err := store.Git(repo, "symbolic-ref", "-q", "--short", "HEAD"); err != nil || strings.TrimSpace(out) != "main" {
		return fmt.Errorf("the memory repo has %q checked out, so the commits of an automatic push stay in the review queue: the person checks out main first", strings.TrimSpace(out))
	}
	queue, err := revList(repo, "HEAD")
	if err != nil {
		return err
	}
	present, gone := 0, ""
	for _, sha := range rec.Commits {
		if slices.Contains(queue, sha) {
			present++
			continue
		}
		// A built commit can be the commit of the queue itself, with the same ID. Then the accepted history holds it.
		if _, ok := revParse(repo, sha); ok {
			if has, err := isAncestor(repo, sha, accepted); err != nil {
				return fmt.Errorf("look for the commit %s in the accepted history: %w", short(sha), err)
			} else if has {
				continue
			}
		}
		if gone == "" {
			gone = sha
		}
	}
	if gone != "" {
		// A removal that ended leaves main on the commit that it moved the queue onto. The record names that
		// commit, because a later fetch can move the accepted commit on. A queue that the person rewrote does
		// not stand there: only this removal, or a fetch after it, moves the queue onto that commit.
		ended := false
		if _, ok := revParse(repo, rec.Base); rec.Removing && present == 0 && rec.Base != "" && ok {
			if ended, err = isAncestor(repo, rec.Base, "HEAD"); err != nil {
				return fmt.Errorf("look for the base of the removal under the review queue: %w", err)
			}
		}
		if ended {
			return clearPending(repo)
		}
		return fmt.Errorf("the remote has an automatic push, and the review queue no longer holds its commit %s: the person removes the pushed commits from the queue by hand, and then removes the file %s", short(gone), pendingPath(repo))
	}
	if present == 0 {
		// The accepted history holds each commit of the record itself, or a removal that started has ended.
		return clearPending(repo)
	}
	if err := cleanTree(repo, "the commits of an automatic push stay in the review queue until the next fetch"); err != nil {
		return err
	}
	var todo strings.Builder
	for _, sha := range queue {
		if slices.Contains(rec.Commits, sha) {
			fmt.Fprintf(&todo, "drop %s\n", sha)
			continue
		}
		fmt.Fprintf(&todo, "pick %s\nexec false\n", sha) // a stop where rebase regenerates the indexes of this commit
	}
	path := filepath.Join(store.StateDir(repo), "todo.drop")
	if err := os.WriteFile(path, []byte(todo.String()), 0o644); err != nil {
		return fmt.Errorf("write the todo of the removal: %w", err)
	}
	defer os.Remove(path)
	// The record says that the removal started. So a retry tells a removal that ended from a queue that the
	// person changed.
	rec.Removing, rec.Base = true, accepted
	if err := writePending(repo, rec); err != nil {
		return err
	}
	editor := "cp '" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	if err := rebaseEditor(repo, editor, "-q", "-i", accepted); err != nil {
		// When the abort left the queue as it was, the removal did not start after all.
		if !rebasing(repo) && headCommit(repo) == queue[len(queue)-1] {
			rec.Removing, rec.Base = false, ""
			if werr := writePending(repo, rec); werr != nil {
				return fmt.Errorf("remove the commits of an automatic push from the review queue: %w; %v", err, werr)
			}
		}
		return fmt.Errorf("remove the commits of an automatic push from the review queue: %w", err)
	}
	return clearPending(repo)
}
