package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// snapshotEvery is the time between two snapshots that a call of memcore starts in the background.
const snapshotEvery = 6 * time.Hour

// startBackground runs memcore with args in its own session, detached, and does not wait for it.
// Its output goes to .git/memcore/background.log. Tests replace it.
var startBackground = func(repo string, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find the executable of memcore: %w", err)
	}
	log, err := os.OpenFile(filepath.Join(store.StateDir(repo), "background.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open the log of background work: %w", err)
	}
	defer log.Close()
	// The child keeps the working directory of the caller, so a relative CLAUDE_CONFIG_DIR means the same.
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// The work belongs to no session of an agent, so the use log does not count it as one.
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); name != "CLAUDE_CODE_SESSION_ID" && name != "CLAUDECODE" {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start memcore %s in the background: %w", strings.Join(args, " "), err)
	}
	return cmd.Process.Release()
}

// autoSnapshot starts a snapshot in the background after a command, when both of these hold:
//   - The projects folder of Claude Code exists.
//   - The last snapshot ran six hours ago or more, by lastSnapshot.
//
// It skips snapshot, init and help, and a command that started a snapshot itself.
func autoSnapshot(repo, command string, started bool) error {
	if started || slices.Contains([]string{"snapshot", "init", "help"}, command) {
		return nil
	}
	dir, err := claudeDir()
	if err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "projects")); err != nil {
		return nil
	}
	// One call at a time checks and marks. A call that finds another call at it leaves the start to that call.
	unlock, ok, err := store.TryLockFile(repo, "snapshot.schedule")
	if err != nil || !ok {
		return err
	}
	defer unlock()
	if last, ok := lastSnapshot(repo); ok && time.Since(last) < snapshotEvery {
		return nil
	}
	// The mark comes first, so the calls during a long snapshot start no second one.
	mark := filepath.Join(store.StateDir(repo), "snapshot.started")
	if err := os.WriteFile(mark, nil, 0o644); err != nil {
		return fmt.Errorf("mark the start of a snapshot: %w", err)
	}
	now := time.Now()
	if err := os.Chtimes(mark, now, now); err != nil {
		return fmt.Errorf("mark the start of a snapshot: %w", err)
	}
	if err := startBackground(repo, "snapshot"); err != nil {
		os.Remove(mark) // a start that failed holds back no later one
		return err
	}
	return nil
}

// lastSnapshot returns when the last snapshot ended or started, whichever is later, and false when none ran.
func lastSnapshot(repo string) (time.Time, bool) {
	var last time.Time
	if state, err := readSnapshotState(repo); err == nil && state.Time != "" {
		if t, err := time.Parse(time.RFC3339, state.Time); err == nil {
			last = t
		}
	}
	info, err := os.Stat(filepath.Join(store.StateDir(repo), "snapshot.started"))
	if err == nil && info.ModTime().After(last) {
		last = info.ModTime()
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, false
	}
	return last, !last.IsZero()
}
