package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLock(t *testing.T) {
	repo := t.TempDir()
	log := filepath.Join(repo, "log")
	appendLine := func(s string) {
		f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		fmt.Fprintln(f, s)
	}

	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				unlock, err := Lock(repo)
				if err != nil {
					t.Error(err)
					return
				}
				appendLine(id + " start")
				time.Sleep(time.Millisecond)
				appendLine(id + " end")
				unlock()
			}
		}()
	}
	wg.Wait()

	content, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 80 {
		t.Fatalf("got %d lines, want 80", len(lines))
	}
	for i := 0; i < len(lines); i += 2 {
		id, _, _ := strings.Cut(lines[i], " ")
		if lines[i] != id+" start" || lines[i+1] != id+" end" {
			t.Fatalf("lines %d and %d interleave: %q, %q", i+1, i+2, lines[i], lines[i+1])
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "memcore", "lock")); err != nil {
		t.Errorf("the lock file: %v", err)
	}
}

func TestLockWaits(t *testing.T) {
	repo := t.TempDir()
	old := lockWait
	lockWait = 100 * time.Millisecond
	t.Cleanup(func() { lockWait = old })

	unlock, err := Lock(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(repo); err == nil {
		t.Fatal("a second lock while the first is held: no error")
	}
	unlock()
	unlock2, err := Lock(repo)
	if err != nil {
		t.Fatalf("a lock after the release: %v", err)
	}
	unlock2()
}

func TestTryLockFile(t *testing.T) {
	repo := t.TempDir()
	unlock, ok, err := TryLockFile(repo, "schedule")
	if err != nil || !ok {
		t.Fatalf("TryLockFile: %v, %v", ok, err)
	}
	if _, ok, err := TryLockFile(repo, "schedule"); err != nil || ok {
		t.Errorf("a second TryLockFile = %v, %v, want false at once", ok, err)
	}
	unlock()
	if unlock2, ok, err := TryLockFile(repo, "schedule"); err != nil || !ok {
		t.Errorf("TryLockFile after the release = %v, %v", ok, err)
	} else {
		unlock2()
	}
}
