package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBackend answers every read with a fixed value, and records the actions.
type fakeBackend struct {
	mu     sync.Mutex
	head   string
	pushed []string
}

func (f *fakeBackend) Head(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.head, nil
}

func (f *fakeBackend) setHead(h string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.head = h
}

func (f *fakeBackend) Queue(context.Context) (Queue, error) {
	return Queue{Main: f.head}, nil
}

func (f *fakeBackend) Tree(context.Context) (Tree, error) {
	return Tree{Main: f.head, Docs: []TreeDoc{{Path: "a.md"}, {Path: "b/a.md"}, {Path: "c.md"}}}, nil
}

func (f *fakeBackend) Doc(_ context.Context, path string) (Doc, error) {
	switch path {
	case "bad":
		return Doc{}, fmt.Errorf("read %s: %w", path, ErrBadInput)
	case "missing":
		return Doc{}, fmt.Errorf("read %s: %w", path, ErrNotFound)
	}
	return Doc{Main: f.head, Path: path, Text: "text"}, nil
}

func (f *fakeBackend) Search(_ context.Context, q string) (Hits, error) {
	return Hits{Main: f.head, Hits: []Hit{{Path: "a.md", Line: 1, Text: q}}}, nil
}

func (f *fakeBackend) Push(_ context.Context, head string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushed = append(f.pushed, head)
	return Result{Output: "Pushed 1 commit."}, nil
}

func (f *fakeBackend) Drop(_ context.Context, commit, reason string) (Result, error) {
	return Result{Output: "dropped " + commit + ": " + reason}, nil
}

// served runs a server on a free port of loopback until the test ends. It returns the server, its port, and a
// channel that Serve's error arrives on.
func served(t *testing.T, s *Server) (port int, done chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	done = make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- s.Serve(ln)
		close(finished)
	}()
	t.Cleanup(func() {
		ln.Close()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after the listener closed")
		}
	})
	return port, done
}

// newServer returns a server with a fake backend, which a test can stop only by its lifetime.
func newServer() (*Server, *fakeBackend) {
	b := &fakeBackend{head: "aaa"}
	return &Server{Backend: b, Token: "secret", Grace: time.Hour, Start: time.Hour, Poll: 10 * time.Millisecond}, b
}

// call sends a request with the given host, token, origin and content type. An empty token sends no header.
func call(t *testing.T, port int, method, path, host, token, origin, ctype, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	if token != "" {
		req.Header.Set("X-Memcore-Token", token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestToken(t *testing.T) {
	s, _ := newServer()
	port, _ := served(t, s)
	host := fmt.Sprintf("127.0.0.1:%d", port)
	tests := []struct {
		path, token string
		want        int
	}{
		{"/api/ping", "secret", 200},
		{"/api/ping", "", 403},
		{"/api/ping", "wrong", 403},
		{"/api/queue", "", 403},
		{"/api/tree", "", 403},
		{"/api/doc?path=a.md", "", 403},
		{"/api/search?q=a", "", 403},
		{"/api/find?q=a", "", 403},
		{"/api/doc?path=a.md", "secret", 200},
		{"/api/doc?path=bad", "secret", 400},
		{"/api/doc?path=missing", "secret", 404},
		{"/api/events?token=wrong", "", 403},
		{"/", "", 200},
	}
	for _, tt := range tests {
		resp := call(t, port, "GET", tt.path, host, tt.token, "", "", "")
		if resp.StatusCode != tt.want {
			t.Errorf("GET %s with token %q = %d, want %d", tt.path, tt.token, resp.StatusCode, tt.want)
		}
	}
}

func TestHost(t *testing.T) {
	s, _ := newServer()
	port, _ := served(t, s)
	for _, tt := range []struct {
		host string
		want int
	}{
		{fmt.Sprintf("127.0.0.1:%d", port), 200},
		{fmt.Sprintf("localhost:%d", port), 200},
		{"evil.example:80", 403},
		{fmt.Sprintf("evil.example:%d", port), 403},
		{"127.0.0.1:1", 403},
	} {
		for _, path := range []string{"/", "/api/ping"} {
			resp := call(t, port, "GET", path, tt.host, "secret", "", "", "")
			if resp.StatusCode != tt.want {
				t.Errorf("GET %s with Host %q = %d, want %d", path, tt.host, resp.StatusCode, tt.want)
			}
		}
	}
}

func TestOrigin(t *testing.T) {
	s, b := newServer()
	port, _ := served(t, s)
	host := fmt.Sprintf("127.0.0.1:%d", port)
	for _, tt := range []struct {
		origin string
		want   int
	}{
		{fmt.Sprintf("http://127.0.0.1:%d", port), 200},
		{fmt.Sprintf("http://localhost:%d", port), 200},
		{"", 403},
		{"http://evil.example", 403},
		{fmt.Sprintf("http://127.0.0.1:%d", port+1), 403},
		{"null", 403},
	} {
		resp := call(t, port, "POST", "/api/push", host, "secret", tt.origin, "application/json", `{"head":"aaa"}`)
		if resp.StatusCode != tt.want {
			t.Errorf("POST with Origin %q = %d, want %d", tt.origin, resp.StatusCode, tt.want)
		}
	}
	if len(b.pushed) != 2 || b.pushed[0] != "aaa" {
		t.Errorf("pushes = %q, want the two with a good origin", b.pushed)
	}
}

func TestContentType(t *testing.T) {
	s, _ := newServer()
	port, _ := served(t, s)
	host := fmt.Sprintf("127.0.0.1:%d", port)
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	for _, tt := range []struct {
		ctype string
		want  int
	}{
		{"application/json", 200},
		{"application/json; charset=utf-8", 200},
		{"", 403},
		{"text/plain", 403},
		{"application/x-www-form-urlencoded", 403},
		{"multipart/form-data; boundary=x", 403},
	} {
		resp := call(t, port, "POST", "/api/drop", host, "secret", origin, tt.ctype, `{"commit":"bbb","reason":"wrong"}`)
		if resp.StatusCode != tt.want {
			t.Errorf("POST with Content-Type %q = %d, want %d", tt.ctype, resp.StatusCode, tt.want)
		}
	}
	resp := call(t, port, "GET", "/api/push", host, "secret", origin, "application/json", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/push = %d, want 405", resp.StatusCode)
	}
}

func TestCSP(t *testing.T) {
	s, _ := newServer()
	port, _ := served(t, s)
	host := fmt.Sprintf("127.0.0.1:%d", port)
	for _, tt := range []struct{ path, token, hostname string }{
		{"/", "", host},
		{"/api/ping", "secret", host},
		{"/api/ping", "", host},
		{"/", "", "evil.example:80"},
	} {
		resp := call(t, port, "GET", tt.path, tt.hostname, tt.token, "", "", "")
		if got := resp.Header.Get("Content-Security-Policy"); got != CSP {
			t.Errorf("GET %s: Content-Security-Policy = %q, want %q", tt.path, got, CSP)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s: X-Content-Type-Options = %q", tt.path, got)
		}
	}
}

// stream opens the event stream, reads its first event, and returns the reader and a function that closes it.
func stream(t *testing.T, port int) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/api/events?token=secret", port), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/events = %d", resp.StatusCode)
	}
	return bufio.NewReader(resp.Body), func() { cancel(); resp.Body.Close() }
}

// event reads the next event of a stream, and returns its name and data.
func event(t *testing.T, r *bufio.Reader) (name, data string) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read the stream: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "" && name != "":
			return name, data
		}
	}
}

func TestEvents(t *testing.T) {
	s, b := newServer()
	port, _ := served(t, s)
	r, closeStream := stream(t, port)
	defer closeStream()
	if name, data := event(t, r); name != "head" || data != "aaa" {
		t.Errorf("first event = %s %s, want head aaa", name, data)
	}
	b.setHead("bbb")
	if name, data := event(t, r); name != "queue" || data != "bbb" {
		t.Errorf("event after a commit = %s %s, want queue bbb", name, data)
	}
}

func TestLifetime(t *testing.T) {
	t.Run("no stream opens", func(t *testing.T) {
		s, _ := newServer()
		s.Start = 50 * time.Millisecond
		var stopped atomic.Int32
		s.Window = func(string) (func(), error) { return func() { stopped.Add(1) }, nil }
		_, done := served(t, s)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not return after the start period")
		}
		if stopped.Load() != 1 {
			t.Errorf("the window stopped %d times, want 1", stopped.Load())
		}
	})

	t.Run("the last stream closes", func(t *testing.T) {
		s, _ := newServer()
		s.Start = 50 * time.Millisecond
		s.Grace = 100 * time.Millisecond
		var stopped atomic.Int32
		var opened []string
		s.Window = func(url string) (func(), error) {
			opened = append(opened, url)
			return func() { stopped.Add(1) }, nil
		}
		port, done := served(t, s)
		r1, close1 := stream(t, port)
		event(t, r1)
		time.Sleep(150 * time.Millisecond) // past the start period, with a stream open
		host := fmt.Sprintf("127.0.0.1:%d", port)
		resp := call(t, port, "POST", "/api/window", host, "secret", "http://"+host, "application/json", "{}")
		if resp.StatusCode != 200 {
			t.Fatalf("POST /api/window = %d", resp.StatusCode)
		}
		r2, close2 := stream(t, port)
		event(t, r2)
		close1()
		time.Sleep(200 * time.Millisecond) // past the grace period, with one stream open
		select {
		case <-done:
			t.Fatal("Serve returned while a stream was open")
		default:
		}
		close2()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not return after the last stream closed")
		}
		if stopped.Load() != 2 {
			t.Errorf("the windows stopped %d times, want 2", stopped.Load())
		}
		want := fmt.Sprintf("http://127.0.0.1:%d/#secret", port)
		if len(opened) != 2 || opened[0] != want {
			t.Errorf("opened %q, want two windows at %s", opened, want)
		}
	})

	t.Run("a window that fails", func(t *testing.T) {
		s, _ := newServer()
		s.Window = func(string) (func(), error) { return nil, errors.New("no browser") }
		s.Ready = func() error {
			t.Error("Ready ran after the window failed")
			return nil
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		if err := s.Serve(ln); err == nil || !strings.Contains(err.Error(), "no browser") {
			t.Errorf("Serve = %v, want the error of the window", err)
		}
	})
}

func TestReady(t *testing.T) {
	s, _ := newServer()
	s.Start = 50 * time.Millisecond
	var order []string
	s.Window = func(string) (func(), error) {
		order = append(order, "window")
		return nil, nil
	}
	s.Ready = func() error {
		order = append(order, "ready")
		return errors.New("no state")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := s.Serve(ln); err == nil || err.Error() != "no state" {
		t.Errorf("Serve = %v, want the error of Ready", err)
	}
	if strings.Join(order, " ") != "window ready" {
		t.Errorf("order = %q, want the window, then Ready", order)
	}
}

func TestStatic(t *testing.T) {
	s, _ := newServer()
	port, _ := served(t, s)
	host := fmt.Sprintf("127.0.0.1:%d", port)
	for _, tt := range []struct {
		path, holds string
		status      int
	}{
		{"/", "<html", 200},
		{"/static/vendor/pele.global.js", "Pele 1.13.0", 200},
		{"/static/vendor/marked.umd.js", "marked v18.1.3", 200},
		{"/static/absent.js", "", 404},
		{"/vendor/pele.global.js", "", 404},
	} {
		resp := call(t, port, "GET", tt.path, host, "", "", "", "")
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != tt.status || !strings.Contains(string(body), tt.holds) {
			t.Errorf("GET %s = %d, want %d and a body with %q", tt.path, resp.StatusCode, tt.status, tt.holds)
		}
	}
}

func TestWindowAfterClose(t *testing.T) {
	s, _ := newServer()
	var stopped atomic.Int32
	s.Window = func(string) (func(), error) { return func() { stopped.Add(1) }, nil }
	if err := s.openWindow(); err != nil {
		t.Fatal(err)
	}
	s.closeWindows()
	if stopped.Load() != 1 {
		t.Fatalf("closeWindows stopped %d windows, want 1", stopped.Load())
	}
	if err := s.openWindow(); err != nil { // a request of /api/window that finishes after the shutdown
		t.Fatal(err)
	}
	if stopped.Load() != 2 {
		t.Errorf("a window that opened after the close stays open")
	}
}

func TestFindAPI(t *testing.T) {
	s, _ := newServer()
	port, _ := served(t, s)
	resp := call(t, port, "GET", "/api/find?q=a", fmt.Sprintf("127.0.0.1:%d", port), "secret", "", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/find = %d, want 200", resp.StatusCode)
	}
	var got Finds
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range got.Found {
		paths = append(paths, f.Path)
	}
	if got.Main != "aaa" || !reflect.DeepEqual(paths, []string{"a.md", "b/a.md"}) {
		t.Errorf("GET /api/find?q=a = %+v, want main aaa with a.md and b/a.md", got)
	}
	resp = call(t, port, "GET", "/api/find?q=zzz", fmt.Sprintf("127.0.0.1:%d", port), "secret", "", "", "")
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"found":[]`) {
		t.Errorf("GET /api/find?q=zzz = %s, want an empty list, not null", body)
	}
}

func TestQuit(t *testing.T) {
	s, _ := newServer()
	s.Start, s.Grace = time.Minute, time.Minute
	quit := make(chan struct{})
	s.Quit = quit
	var stopped atomic.Int32
	s.Window = func(string) (func(), error) { return func() { stopped.Add(1) }, nil }
	_, done := served(t, s)
	select {
	case <-done:
		t.Fatal("Serve returned before the quit")
	case <-time.After(100 * time.Millisecond):
	}
	close(quit)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the quit")
	}
	if stopped.Load() != 1 {
		t.Errorf("the window stopped %d times, want 1", stopped.Load())
	}
}
