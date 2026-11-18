package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
