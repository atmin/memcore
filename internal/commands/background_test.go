package commands

import (
	"errors"
	"os"
	"path/filepath"
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
	}{
		{"no snapshot yet", 0, "orient", true, false, 1},
		{"a snapshot of five hours", 5 * time.Hour, "orient", true, false, 0},
		{"a snapshot of seven hours", 7 * time.Hour, "orient", true, false, 1},
		{"snapshot itself", 0, "snapshot", true, false, 0},
		{"init", 0, "init", true, false, 0},
		{"help", 0, "help", true, false, 0},
		{"no projects folder", 0, "orient", false, false, 0},
		{"a command that started one itself", 0, "hook", true, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			if tt.projects {
				mkdirT(t, filepath.Join(home, ".claude", "projects"))
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
