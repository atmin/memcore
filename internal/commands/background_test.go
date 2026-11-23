package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// recordStarts replaces startBackground with a recorder for one test, and returns what it records.
func recordStarts(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	prev := startBackground
	startBackground = func(repo string, args ...string) error {
		calls = append(calls, args)
		return nil
	}
	t.Cleanup(func() { startBackground = prev })
	return &calls
}

func TestAutoSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		age      time.Duration // of the last snapshot, or 0 for none
		command  string
		projects bool
		started  bool
		want     int
		codex    string // a folder to create in the folder of Codex, or "" for none
	}{
		{"no snapshot yet", 0, "orient", true, false, 1, ""},
		{"a snapshot of five hours", 5 * time.Hour, "orient", true, false, 0, ""},
		{"a snapshot of seven hours", 7 * time.Hour, "orient", true, false, 1, ""},
		{"snapshot itself", 0, "snapshot", true, false, 0, ""},
		{"init", 0, "init", true, false, 0, ""},
		{"help", 0, "help", true, false, 0, ""},
		{"no projects folder", 0, "orient", false, false, 0, ""},
		{"a command that started one itself", 0, "hook", true, true, 0, ""},
		{"only the sessions of Codex", 0, "orient", false, false, 1, "sessions"},
		{"only the archived sessions of Codex", 0, "orient", false, false, 1, "archived_sessions"},
		{"a folder of Codex without sessions", 0, "orient", false, false, 0, "cache"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			if tt.projects {
				mkdirT(t, filepath.Join(home, ".claude", "projects"))
			}
			if tt.codex != "" {
				mkdirT(t, filepath.Join(home, ".codex", tt.codex))
			}
			if tt.age > 0 {
				state := snapshotState{Time: time.Now().Add(-tt.age).UTC().Format(time.RFC3339), Files: map[string]sourceState{}}
				if err := writeSnapshotState(repo, state); err != nil {
					t.Fatal(err)
				}
			}
			calls := recordStarts(t)
			autoSnapshot(repo, tt.command, tt.started)
			if len(*calls) != tt.want {
				t.Fatalf("starts = %v, want %d", *calls, tt.want)
			}
			if tt.want == 1 && (len((*calls)[0]) != 1 || (*calls)[0][0] != "snapshot") {
				t.Errorf("start = %v, want snapshot", (*calls)[0])
			}
		})
	}
}

func TestAutoSnapshotOnce(t *testing.T) {
	home, repo, _ := newMemory(t)
	mkdirT(t, filepath.Join(home, ".claude", "projects"))
	calls := recordStarts(t)
	// A first snapshot may take a while, so the calls during it start no second one.
	for i := 0; i < 3; i++ {
		if out, err := run(t, home, "", "orient"); err != nil {
			t.Fatalf("orient: %v\n%s", err, out)
		}
	}
	if len(*calls) != 1 {
		t.Errorf("starts = %v, want one", *calls)
	}
	if _, err := os.Stat(filepath.Join(store.StateDir(repo), "snapshot.started")); err != nil {
		t.Errorf("no mark of the start: %v", err)
	}
}

func TestAutoSnapshotFailure(t *testing.T) {
	home, repo, _ := newMemory(t)
	mkdirT(t, filepath.Join(home, ".claude", "projects"))
	prev := startBackground
	t.Cleanup(func() { startBackground = prev })
	startBackground = func(string, ...string) error { return errors.New("no executable") }
	if err := autoSnapshot(repo, "orient", false); err == nil {
		t.Error("autoSnapshot with a failed start returned no error")
	}
	if _, err := os.Stat(filepath.Join(store.StateDir(repo), "snapshot.started")); err == nil {
		t.Error("a failed start left its mark, which would hold back the next start for six hours")
	}
	calls := recordStarts(t)
	autoSnapshot(repo, "orient", false)
	if len(*calls) != 1 {
		t.Errorf("starts after a failed one = %v, want one", *calls)
	}
}

func TestAutoSnapshotLocked(t *testing.T) {
	home, repo, _ := newMemory(t)
	mkdirT(t, filepath.Join(home, ".claude", "projects"))
	unlock, ok, err := store.TryLockFile(repo, "snapshot.schedule")
	if err != nil || !ok {
		t.Fatalf("TryLockFile: %v, %v", ok, err)
	}
	defer unlock()
	calls := recordStarts(t)
	autoSnapshot(repo, "orient", false)
	if len(*calls) != 0 {
		t.Errorf("starts while another call schedules = %v, want none", *calls)
	}
}

func TestAutoPush(t *testing.T) {
	pushStarts := func(calls [][]string) int {
		n := 0
		for _, c := range calls {
			if len(c) == 2 && c[0] == "push" && c[1] == "--auto" {
				n++
			}
		}
		return n
	}
	tests := []struct {
		name    string
		setting string        // the value of memcore.autopush, or "" for none
		age     time.Duration // of the mark of the last start, or 0 for none
		command string
		want    int
	}{
		{"the setting off", "", 0, "orient", 0},
		{"the first call", "24h", 0, "orient", 1},
		{"a call within the hour", "24h", 59 * time.Minute, "orient", 0},
		{"a call after the hour", "24h", 61 * time.Minute, "orient", 1},
		{"push itself", "24h", 0, "push", 0},
		{"init", "24h", 0, "init", 0},
		{"help", "24h", 0, "help", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, repo, _ := newMemory(t)
			if tt.setting != "" {
				gitT(t, repo, "config", "memcore.autopush", tt.setting)
			}
			mark := filepath.Join(store.StateDir(repo), "autopush.started")
			if tt.age > 0 {
				writeT(t, store.StateDir(repo), "autopush.started", "")
				at := time.Now().Add(-tt.age)
				if err := os.Chtimes(mark, at, at); err != nil {
					t.Fatal(err)
				}
			}
			calls := recordStarts(t)
			if err := autoPush(repo, tt.command); err != nil {
				t.Fatal(err)
			}
			if got := pushStarts(*calls); got != tt.want || len(*calls) != tt.want {
				t.Fatalf("starts = %v, want %d of push --auto", *calls, tt.want)
			}
			if _, err := os.Stat(mark); tt.setting == "" && err == nil {
				t.Error("a call with the setting off left a mark")
			}
		})
	}

	t.Run("a second call starts no second push", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		gitT(t, repo, "config", "memcore.autopush", "24h")
		calls := recordStarts(t)
		for i := 0; i < 3; i++ {
			if out, err := run(t, home, "", "orient"); err != nil {
				t.Fatalf("orient: %v\n%s", err, out)
			}
		}
		if got := pushStarts(*calls); got != 1 {
			t.Errorf("starts = %v, want one push --auto", *calls)
		}
	})

	t.Run("a setting that memcore cannot read", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		gitT(t, repo, "config", "memcore.autopush", "soon")
		calls := recordStarts(t)
		out, err := run(t, home, "", "orient")
		if err != nil || !strings.Contains(out, "warning: read the setting memcore.autopush") {
			t.Errorf("orient = %v\n%s\nwant a warning that names the setting", err, out)
		}
		if got := pushStarts(*calls); got != 0 {
			t.Errorf("starts = %v, want none", *calls)
		}
	})

	t.Run("a start that fails leaves no mark", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		gitT(t, repo, "config", "memcore.autopush", "24h")
		prev := startBackground
		t.Cleanup(func() { startBackground = prev })
		startBackground = func(string, ...string) error { return errors.New("no executable") }
		if err := autoPush(repo, "orient"); err == nil {
			t.Error("autoPush with a failed start returned no error")
		}
		if _, err := os.Stat(filepath.Join(store.StateDir(repo), "autopush.started")); err == nil {
			t.Error("a failed start left its mark, which would hold back the next start for an hour")
		}
	})

	t.Run("another call schedules", func(t *testing.T) {
		_, repo, _ := newMemory(t)
		gitT(t, repo, "config", "memcore.autopush", "24h")
		unlock, ok, err := store.TryLockFile(repo, "autopush.schedule")
		if err != nil || !ok {
			t.Fatalf("TryLockFile: %v, %v", ok, err)
		}
		defer unlock()
		calls := recordStarts(t)
		if err := autoPush(repo, "orient"); err != nil || len(*calls) != 0 {
			t.Errorf("autoPush while another call schedules = %v, starts %v; want none", err, *calls)
		}
	})
}

func TestBackgroundEnv(t *testing.T) {
	in := []string{"PATH=/bin", "CLAUDE_CODE_SESSION_ID=s1", "HOME=/home/me", "CLAUDECODE=1", "CODEX_THREAD_ID=t1", "CODEX_HOME=/codex"}
	want := "PATH=/bin HOME=/home/me CODEX_HOME=/codex"
	if got := strings.Join(backgroundEnv(in), " "); got != want {
		t.Errorf("backgroundEnv = %q, want %q", got, want)
	}
}
