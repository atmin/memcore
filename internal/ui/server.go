// Package ui serves the window of memcore: a local page to review, push, browse and search the memory repo.
// It listens only on loopback, and every request of the API carries the token of its run. The package imports
// nothing of the commands: a Backend gives it the data, and runs the actions through the commands.
package ui

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// CSP is the Content Security Policy of every response. A document can hold hostile text, and a script in the
// page could push, so the page runs only its own scripts.
const CSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// ErrBadInput marks an error of the backend that the request caused, such as a path with "..". The server
// answers it with 400.
var ErrBadInput = errors.New("bad input")

// ErrNotFound marks an error of the backend for a document that does not exist. The server answers it with 404.
var ErrNotFound = errors.New("not found")

//go:embed static
var static embed.FS

// Backend gives the server the data of the memory repo, and runs the actions of the person.
type Backend interface {
	// Head returns the tip of main. The event stream sends queue when it changes.
	Head(ctx context.Context) (string, error)
	Queue(ctx context.Context) (Queue, error)
	Tree(ctx context.Context) (Tree, error)
	Doc(ctx context.Context, path string) (Doc, error)
	Search(ctx context.Context, query string) (Hits, error)
	// Push pushes the queue when main is at head. A refusal of the command is in the Result, not the error.
	Push(ctx context.Context, head string) (Result, error)
	// Drop drops a commit of the queue with its fixups. A refusal of the command is in the Result, not the error.
	Drop(ctx context.Context, commit, reason string) (Result, error)
}

// Queue is the review queue as the fold at push leaves it, newest first.
type Queue struct {
	Main     string  `json:"main"`     // the tip of main that the entries come from
	Accepted string  `json:"accepted"` // the accepted commit, or "" before the first push
	Warning  string  `json:"warning,omitempty"`
	Folded   bool    `json:"folded"` // false when the preview failed, and the entries are the queue as it is
	Entries  []Entry `json:"entries"`
	Dropped  int     `json:"dropped"` // the commits that the fold drops, because they leave every document as it was
}

// Entry is a commit of the queue as the fold leaves it.
type Entry struct {
	Commit  string   `json:"commit"` // the commit of the queue that the fold keeps, or "" for an index that push adds
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	Fixups  []string `json:"fixups"`
	Files   []File   `json:"files"`
}

// File is a file that an entry changes, with its diffs and its texts.
type File struct {
	Status string  `json:"status"` // the status letter of git diff-tree
	Path   string  `json:"path"`
	Kind   string  `json:"kind"`   // the kind of the document, such as memories or skills, or ""
	Patch  string  `json:"patch"`  // the unified diff
	Words  string  `json:"words"`  // the word diff, in the porcelain format of git
	Before *string `json:"before"` // the text before the commit, or nil where the file is missing
	After  *string `json:"after"`  // the text after the commit, or nil where the file is missing
}

// Tree lists the documents of main.
type Tree struct {
	Main     string    `json:"main"`
	Accepted string    `json:"accepted"`
	Docs     []TreeDoc `json:"docs"`
}

// TreeDoc is a file of main.
type TreeDoc struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Unreviewed bool   `json:"unreviewed"` // the queue changes it
}

// Doc is a document of main.
type Doc struct {
	Main       string `json:"main"`
	Path       string `json:"path"`
	Text       string `json:"text"`
	Unreviewed bool   `json:"unreviewed"`
}

// Hits are the lines of main that hold a query.
type Hits struct {
	Main string `json:"main"`
	Hits []Hit  `json:"hits"`
	More bool   `json:"more"` // the search stopped at its limit
}

// Hit is one line of a document that holds the query.
type Hit struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Result is the outcome of an action: the output of the command, and its error.
type Result struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// Server serves the page and the API on one listener of loopback, for the life of its windows.
type Server struct {
	Backend Backend
	Token   string        // the secret of the run, which every request of the API carries
	Grace   time.Duration // how long the server waits after the last event stream closes
	Start   time.Duration // how long the server waits for the first event stream
	Poll    time.Duration // how often the event stream checks the tip of main; 2 s when it is 0
	// Window opens a window at a URL, and returns the function that closes it. Serve opens the first one.
	Window func(url string) (stop func(), err error)
	// Ready runs once the first window opened. Serve stops with its error.
	Ready func() error

	port    int
	mu      sync.Mutex
	streams int
	changed chan struct{} // a stream opened or closed
	stops   []func()
	closed  bool // the server closed its windows
}

// URL returns the address that a window opens: the page, with the token in the fragment. A fragment never
// reaches a server, a log or a referrer.
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/#%s", s.port, s.Token)
}

// Serve serves on ln, opens the first window, and returns when the lifetime ends. The lifetime ends when no
// event stream opens within Start, or when no stream is open for Grace after the last one closes. Then Serve
// closes every window that it opened.
func (s *Server) Serve(ln net.Listener) error {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("serve the window: %s is no TCP address", ln.Addr())
	}
	s.port = addr.Port
	s.changed = make(chan struct{}, 1)
	if s.Poll == 0 {
		s.Poll = 2 * time.Second
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &http.Server{
		Handler:           s.handler(),
		BaseContext:       func(net.Listener) context.Context { return base },
		ReadHeaderTimeout: 10 * time.Second,
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	defer s.closeWindows()
	if s.Window != nil {
		if err := s.openWindow(); err != nil {
			srv.Close()
			<-served
			return err
		}
	}
	if s.Ready != nil {
		if err := s.Ready(); err != nil {
			srv.Close()
			<-served
			return err
		}
	}
	err := s.wait(served)
	cancel() // the open streams end, so the shutdown waits for no stream
	ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	srv.Shutdown(ctx)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// wait returns when the lifetime ends, or when the listener fails.
func (s *Server) wait(served chan error) error {
	timer := time.NewTimer(s.Start)
	defer timer.Stop()
	for {
		select {
		case err := <-served:
			return err
		case <-timer.C:
			s.mu.Lock()
			open := s.streams
			s.mu.Unlock()
			if open == 0 {
				return nil
			}
		case <-s.changed:
			s.mu.Lock()
			open := s.streams
			s.mu.Unlock()
			if open == 0 {
				timer.Reset(s.Grace)
			} else {
				timer.Stop()
			}
		}
	}
}

// openWindow opens one window, and keeps the function that closes it. A window that finishes opening after
// the server closed its windows closes at once.
func (s *Server) openWindow() error {
	stop, err := s.Window(s.URL())
	if err != nil {
		return fmt.Errorf("open the window: %w", err)
	}
	if stop == nil {
		return nil
	}
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.stops = append(s.stops, stop)
	}
	s.mu.Unlock()
	if closed {
		stop()
	}
	return nil
}

// closeWindows closes every window that the server opened, and each window that opens later.
func (s *Server) closeWindows() {
	s.mu.Lock()
	stops := s.stops
	s.stops = nil
	s.closed = true
	s.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
}

// stream counts an event stream that opens, by +1, or closes, by -1.
func (s *Server) stream(delta int) {
	s.mu.Lock()
	s.streams += delta
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// handler routes the requests, behind the checks of the host, the token, the origin and the content type.
func (s *Server) handler() http.Handler {
	files, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // the embedded folder always exists
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) { reply(w)(map[string]bool{"ok": true}, nil) })
	api.HandleFunc("GET /api/events", s.events)
	api.HandleFunc("GET /api/queue", func(w http.ResponseWriter, r *http.Request) { reply(w)(s.Backend.Queue(r.Context())) })
	api.HandleFunc("GET /api/tree", func(w http.ResponseWriter, r *http.Request) { reply(w)(s.Backend.Tree(r.Context())) })
	api.HandleFunc("GET /api/doc", func(w http.ResponseWriter, r *http.Request) {
		reply(w)(s.Backend.Doc(r.Context(), r.URL.Query().Get("path")))
	})
	api.HandleFunc("GET /api/search", func(w http.ResponseWriter, r *http.Request) {
		reply(w)(s.Backend.Search(r.Context(), r.URL.Query().Get("q")))
	})
	api.HandleFunc("GET /api/find", func(w http.ResponseWriter, r *http.Request) {
		t, err := s.Backend.Tree(r.Context())
		if err != nil {
			reply(w)(nil, err)
			return
		}
		found := Find(t.Docs, r.URL.Query().Get("q"), findLimit)
		if found == nil {
			found = []Found{}
		}
		reply(w)(Finds{Main: t.Main, Found: found}, nil)
	})
	api.HandleFunc("POST /api/push", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Head string }
		if !decode(w, r, &in) {
			return
		}
		reply(w)(s.Backend.Push(r.Context(), in.Head))
	})
	api.HandleFunc("POST /api/drop", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Commit, Reason string }
		if !decode(w, r, &in) {
			return
		}
		reply(w)(s.Backend.Drop(r.Context(), in.Commit, in.Reason))
	})
	api.HandleFunc("POST /api/window", func(w http.ResponseWriter, r *http.Request) {
		if s.Window == nil {
			http.Error(w, "this server opens no window", http.StatusNotFound)
			return
		}
		reply(w)(map[string]bool{"ok": true}, s.openWindow())
	})
	mux := http.NewServeMux()
	mux.Handle("/api/", s.guard(api))
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServerFS(files)))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, files, "index.html")
	})
	return s.headers(mux)
}

// headers sets the security headers on every response, and refuses a request for another host. The check of
// the host stops a page of another site that a DNS name points at loopback.
func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", CSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if r.Host != s.host("127.0.0.1") && r.Host != s.host("localhost") {
			http.Error(w, "this server answers only on loopback", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// guard refuses a request of the API without the token, and a POST from another origin or with a content type
// that a form can send.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Memcore-Token")
		if r.URL.Path == "/api/events" {
			token = r.URL.Query().Get("token") // EventSource sets no header
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.Token)) != 1 {
			http.Error(w, "the request lacks the token of this run", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			origin := r.Header.Get("Origin")
			if origin != "http://"+s.host("127.0.0.1") && origin != "http://"+s.host("localhost") {
				http.Error(w, "the request comes from another origin", http.StatusForbidden)
				return
			}
			if t, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || t != "application/json" {
				http.Error(w, "the request is not JSON", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// host returns the name with the port of the server.
func (s *Server) host(name string) string {
	return net.JoinHostPort(name, strconv.Itoa(s.port))
}

// events streams the tip of main: head when the stream opens, then queue each time the tip changes. The
// lifetime of the server counts the open streams.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "the response cannot stream", http.StatusInternalServerError)
		return
	}
	s.stream(1)
	defer s.stream(-1)
	w.Header().Set("Content-Type", "text/event-stream")
	head, err := s.Backend.Head(r.Context())
	if err != nil {
		head = ""
	}
	fmt.Fprintf(w, "event: head\ndata: %s\n\n", head)
	flusher.Flush()
	poll := time.NewTicker(s.Poll)
	defer poll.Stop()
	alive := time.NewTicker(15 * time.Second)
	defer alive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-alive.C:
			if _, err := io.WriteString(w, ": alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-poll.C:
			now, err := s.Backend.Head(r.Context())
			if err != nil || now == head {
				continue
			}
			head = now
			if _, err := fmt.Fprintf(w, "event: queue\ndata: %s\n\n", head); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// decode reads a JSON body of at most 64 KiB into v. It answers 400 and returns false when it cannot.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v); err != nil {
		http.Error(w, fmt.Sprintf("read the request: %v", err), http.StatusBadRequest)
		return false
	}
	return true
}

// reply returns a function that writes a value as JSON, or an error with its status.
func reply(w http.ResponseWriter) func(any, error) {
	return func(v any, err error) {
		switch {
		case errors.Is(err, ErrBadInput):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, ErrNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(v)
		}
	}
}
