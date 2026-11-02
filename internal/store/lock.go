package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockWait is how long Lock retries before it gives up.
var lockWait = 10 * time.Second

// StateDir returns the local state of the memory repo, which memcore never commits.
func StateDir(repo string) string {
	return filepath.Join(repo, ".git", "memcore")
}

// Lock takes the write lock of the memory repo, an exclusive flock on .git/memcore/lock.
// It retries for ten seconds, and returns the function that releases the lock.
func Lock(repo string) (func(), error) {
	if err := os.MkdirAll(StateDir(repo), 0o755); err != nil {
		return nil, fmt.Errorf("create the local state: %w", err)
	}
	path := filepath.Join(StateDir(repo), "lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open the write lock: %w", err)
	}
	return hold(f, path)
}

// LockDir takes an exclusive flock on a directory itself. init holds it on home,
// because the memory repo and its lock file do not exist yet.
func LockDir(dir string) (func(), error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s to lock it: %w", dir, err)
	}
	return hold(f, dir)
}

// hold takes an exclusive flock on an open file, retries for lockWait, and closes the file on a failure.
func hold(f *os.File, path string) (func(), error) {
	fd := int(f.Fd())
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(fd, syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("take the write lock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("take the write lock %s: another memcore write held it for %s", path, lockWait)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
