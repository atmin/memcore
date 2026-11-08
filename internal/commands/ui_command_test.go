package commands

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// quietUI makes the server of the window short-lived for one test, with a window that opens nothing.
func quietUI(t *testing.T) *atomic.Int32 {
	t.Helper()
	var opened atomic.Int32
	g, s, w, wait := uiGrace, uiStart, uiWindow, uiWait
	uiGrace, uiStart, uiWait = 100*time.Millisecond, 500*time.Millisecond, 5*time.Second
	uiWindow = func(string) (func(), error) {
		opened.Add(1)
		return nil, nil
	}
	t.Cleanup(func() { uiGrace, uiStart, uiWindow, uiWait = g, s, w, wait })
	return &opened
}

func TestUIRefusesAgent(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, _, _ := newMemory(t)
	calls := recordStarts(t)
	t.Setenv("CLAUDECODE", "1")
	for _, args := range [][]string{{"ui"}, {"ui", "--serve"}, {"ui", "--install"}, {"ui", "--uninstall"}} {
		if _, err := run(t, home, "", args...); err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
			t.Errorf("%q by an agent: %v, want a refusal", args, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("an agent started %q", *calls)
	}
}

func TestUIStartsServer(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	opened := quietUI(t)
	var served sync.WaitGroup
	var started [][]string
	prev := startBackground
	startBackground = func(r string, args ...string) error {
		if args[0] != "ui" {
			return nil // a snapshot after the command
		}
		started = append(started, args)
		served.Add(1)
		go func() {
			defer served.Done()
			if out, err := run(t, home, "", args...); err != nil {
				t.Errorf("memcore %q: %v\n%s", args, err, out)
			}
		}()
		return nil
	}
	t.Cleanup(func() { startBackground = prev })

	out, err := run(t, home, "", "ui")
	if err != nil {
		t.Fatalf("ui: %v\n%s", err, out)
	}
	if len(started) != 1 || len(started[0]) != 3 || strings.Join(started[0][:2], " ") != "ui --serve" || !strings.HasPrefix(started[0][2], "--nonce=") {
		t.Errorf("started %q, want ui --serve with a nonce", started)
	}
	state := filepath.Join(store.StateDir(repo), "ui.json")
	info, err := os.Stat(state)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("ui.json: %v, %v, want mode 0600", info, err)
	}
	var s uiState
	data, _ := os.ReadFile(state)
	if err := json.Unmarshal(data, &s); err != nil || len(s.Token) != 64 || !strings.Contains(out, s.url()) {
		t.Errorf("ui.json = %s, output %q", data, out)
	}
	served.Wait() // no event stream opens, so the server stops after its start period
	if opened.Load() != 1 {
		t.Errorf("the server opened %d windows, want 1", opened.Load())
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Errorf("ui.json remains after the server stopped: %v", err)
	}
}

func TestUIReusesServer(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	calls := recordStarts(t)
	var windows atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Memcore-Token") != "secret" {
			http.Error(w, "no token", http.StatusForbidden)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/window" && r.Header.Get("Origin") == "http://"+r.Host && r.Header.Get("Content-Type") == "application/json" {
			windows.Add(1)
		}
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	if err := writeState(filepath.Join(store.StateDir(repo), "ui.json"), uiState{PID: 1, Port: port, Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, home, "", "ui")
	if err != nil {
		t.Fatalf("ui: %v\n%s", err, out)
	}
	if windows.Load() != 1 || len(*calls) != 0 || !strings.Contains(out, "on the running server") {
		t.Errorf("windows %d, starts %q, output %q, want one window on the running server", windows.Load(), *calls, out)
	}

	srv.Close() // a server that no longer answers counts as none
	uiWait = 200 * time.Millisecond
	defer func() { uiWait = 5 * time.Second }()
	if _, err := run(t, home, "", "ui"); err == nil || len(*calls) != 1 {
		t.Errorf("ui with a dead server: %v, starts %q, want a start and a timeout", err, *calls)
	}
}

func TestUIOneServer(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	quietUI(t)
	uiStart = 2 * time.Second
	state := filepath.Join(store.StateDir(repo), "ui.json")
	first := make(chan error, 1)
	go func() {
		_, err := run(t, home, "", "ui", "--serve")
		first <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !fileExists(state) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("the first server wrote no ui.json: %v", err)
	}
	if out, err := run(t, home, "", "ui", "--serve"); err != nil {
		t.Fatalf("the second server: %v\n%s", err, out)
	}
	if after, err := os.ReadFile(state); err != nil || string(after) != string(before) {
		t.Errorf("the second server changed ui.json: %s, %v", after, err)
	}
	select {
	case err := <-first:
		t.Fatalf("the first server stopped with the second: %v", err)
	default:
	}
	if err := <-first; err != nil {
		t.Errorf("the first server: %v", err)
	}
	if fileExists(state) {
		t.Error("ui.json remains after the first server stopped")
	}
}

// serveInProcess replaces startBackground with one that runs each memcore ui --serve in a goroutine, and
// returns the group that waits for them.
func serveInProcess(t *testing.T, home string) *sync.WaitGroup {
	t.Helper()
	var served sync.WaitGroup
	prev := startBackground
	startBackground = func(r string, args ...string) error {
		if args[0] != "ui" {
			return nil // a snapshot after the command
		}
		served.Add(1)
		go func() {
			defer served.Done()
			run(t, home, "", args...)
		}()
		return nil
	}
	t.Cleanup(func() { startBackground = prev })
	return &served
}

func TestUIConcurrentStarts(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, _, _ := newMemory(t)
	opened := quietUI(t)
	served := serveInProcess(t, home)
	var calls sync.WaitGroup
	outs := make([]string, 2)
	for i := range outs {
		calls.Add(1)
		go func() {
			defer calls.Done()
			out, err := run(t, home, "", "ui")
			if err != nil {
				t.Errorf("ui: %v\n%s", err, out)
			}
			outs[i] = out
		}()
	}
	calls.Wait()
	served.Wait()
	if opened.Load() != 2 {
		t.Errorf("two memcore ui opened %d windows, want 2; outputs %q", opened.Load(), outs)
	}
}

func TestUIWindowFails(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	quietUI(t)
	uiWait = time.Second
	uiWindow = func(string) (func(), error) {
		time.Sleep(200 * time.Millisecond)
		return nil, errors.New("no browser")
	}
	served := serveInProcess(t, home)
	if out, err := run(t, home, "", "ui"); err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Errorf("ui with a failing window: %v\n%s, want an error", err, out)
	}
	served.Wait()
	if fileExists(filepath.Join(store.StateDir(repo), "ui.json")) {
		t.Error("ui.json exists although the window failed")
	}
}

// fileExists reports whether a file exists.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
