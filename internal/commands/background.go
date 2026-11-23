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

// autoPushEvery is the time between two automatic pushes that a call of memcore starts in the background.
const autoPushEvery = time.Hour

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
	cmd.Env = backgroundEnv(os.Environ())
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start memcore %s in the background: %w", strings.Join(args, " "), err)
	}
	return cmd.Process.Release()
}

// backgroundEnv returns the environment of background work: env, a list of NAME=value, without the variables
// that name the session of an agent. The work belongs to no session, so the use log does not count it as one.
func backgroundEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains([]string{"CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CODEX_THREAD_ID"}, name) {
			out = append(out, kv)
		}
	}
	return out
}

// autoSnapshot starts a snapshot in the background after a command, when both of these hold:
//   - The projects folder of Claude Code exists, or the sessions or archived_sessions of Codex.
//   - The last snapshot ran six hours ago or more, by lastSnapshot.
//
// It skips snapshot, init and help, and a command that started a snapshot itself.
func autoSnapshot(repo, command string, started bool) error {
	if started || slices.Contains([]string{"snapshot", "init", "help"}, command) {
		return nil
	}
	claude, err := claudeDir()
	if err != nil {
		return nil
	}
	codex, err := codexDir()
	if err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(claude, "projects")); err != nil && !hasCodexSessions(codex) {
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

// autoPush starts memcore push --auto in the background after a command, at most once in autoPushEvery, and
// only with the setting of the automatic push on. It skips push, init and help.
func autoPush(repo, command string) error {
	if slices.Contains([]string{"push", "init", "help"}, command) {
		return nil
	}
	// One call at a time checks and marks. A call that finds another call at it leaves the start to that call.
	unlock, ok, err := store.TryLockFile(repo, "autopush.schedule")
	if err != nil || !ok {
		return err
	}
	defer unlock()
	mark := filepath.Join(store.StateDir(repo), "autopush.started")
	if info, err := os.Stat(mark); err == nil && time.Since(info.ModTime()) < autoPushEvery {
		return nil
	}
	// The setting is read only now, so a call within the hour of a start runs no git process for it.
	if _, on, err := autoPushDelay(repo); err != nil || !on {
		return err
	}
	// The mark comes first, so the calls during a long push start no second one.
	if err := os.WriteFile(mark, nil, 0o644); err != nil {
		return fmt.Errorf("mark the start of an automatic push: %w", err)
	}
	now := time.Now()
	if err := os.Chtimes(mark, now, now); err != nil {
		return fmt.Errorf("mark the start of an automatic push: %w", err)
	}
	if err := startBackground(repo, "push", "--auto"); err != nil {
		os.Remove(mark) // a start that failed holds back no later one
		return err
	}
	return nil
}
