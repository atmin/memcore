package commands

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/ui"
)

// commitName matches the name of a commit that the window sends: hex digits, so no name reads as a flag.
var commitName = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// searchLimit is the most hits that a search of the window returns.
const searchLimit = 200

// uiBackend gives the window the data of the memory repo, and runs the actions of the person through Run.
type uiBackend struct {
	repo string // the memory repo
	dir  string // the working directory of memcore ui, which the use log records for an action
}

// uiResolved runs after a read resolves its commits. Tests replace it to move a ref in the middle of a read.
var uiResolved = func() {}

// resolved returns the tip of main and the accepted commit, once for a read. The accepted commit is "" before
// the first push. A rebase moves main only at its end, so a read never sees a commit in its middle.
func (b uiBackend) resolved() (main, accepted string, err error) {
	main, ok := revParse(b.repo, "refs/heads/main")
	if !ok {
		return "", "", fmt.Errorf("read the tip of main in %s", tilde(b.repo))
	}
	accepted, _ = revParse(b.repo, acceptedRef)
	uiResolved()
	return main, accepted, nil
}

// Head returns the tip of main.
func (b uiBackend) Head(context.Context) (string, error) {
	main, _, err := b.resolved()
	return main, err
}

// Queue fetches, and returns the queue as the fold at push leaves it, newest first, as inbox lists it. When the
// preview fails, it returns the warning and each commit of the queue as it is.
func (b uiBackend) Queue(context.Context) (ui.Queue, error) {
	unlock, err := store.Lock(b.repo)
	if err != nil {
		return ui.Queue{}, err
	}
	defer unlock()
	q := ui.Queue{Entries: []ui.Entry{}} // an empty queue is a list, never null, for the page
	if err := Fetch(b.repo); err != nil {
		q.Warning = fmt.Sprintf("%v. The queue may sit behind the accepted history.", err)
	}
	if q.Main, q.Accepted, err = b.resolved(); err != nil {
		return ui.Queue{}, err
	}
	shas, err := revList(b.repo, q.Main)
	if err != nil {
		return ui.Queue{}, err
	}
	entries, err := preview(b.repo, true)
	if err != nil {
		q.Warning = strings.TrimSpace(q.Warning + " " + fmt.Sprintf("%v. The queue lists each commit as it is, without the fold.", err))
		for i := len(shas) - 1; i >= 0; i-- {
			p, _, err := describe(b.repo, shas[i], true)
			if err != nil {
				return ui.Queue{}, err
			}
			p.target = shas[i]
			q.Entries = append(q.Entries, entryOf(p))
		}
		return q, nil
	}
	q.Folded = true
	held := map[string]bool{}
	for i := len(entries) - 1; i >= 0; i-- {
		p := entries[i]
		held[p.target] = true
		for _, f := range p.fixups {
			held[f] = true
		}
		q.Entries = append(q.Entries, entryOf(p))
	}
	for _, sha := range shas {
		if !held[sha] {
			q.Dropped++
		}
	}
	return q, nil
}

// entryOf turns a commit of the preview into an entry of the window.
func entryOf(p previewed) ui.Entry {
	e := ui.Entry{Commit: p.target, Subject: p.subject, Body: p.body, Fixups: p.fixups, Files: []ui.File{}}
	if e.Fixups == nil {
		e.Fixups = []string{}
	}
	for i, f := range p.files {
		file := ui.File{Status: f.status, Path: f.path, Kind: docKind(f.path)}
		if i < len(p.diffs) {
			d := p.diffs[i]
			file.Patch, file.Words, file.Before, file.After = d.patch, d.words, d.before, d.after
		}
		e.Files = append(e.Files, file)
	}
	return e
}

// changed returns the paths that the queue changes, from the accepted commit to main. Before the first push,
// every path counts.
func (b uiBackend) changed(main, accepted string) (map[string]bool, error) {
	paths := map[string]bool{}
	var out string
	var err error
	if accepted == "" {
		out, err = store.Git(b.repo, "ls-tree", "-r", "-z", "--name-only", main)
	} else {
		out, err = store.Git(b.repo, "diff", "-z", "--name-only", "--no-renames", "--no-ext-diff", "--no-textconv", accepted, main)
	}
	if err != nil {
		return nil, fmt.Errorf("list the files that the queue changes: %w", err)
	}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths[p] = true
		}
	}
	return paths, nil
}

// Tree lists each file of main, with its kind and whether the queue changes it.
func (b uiBackend) Tree(context.Context) (ui.Tree, error) {
	main, accepted, err := b.resolved()
	if err != nil {
		return ui.Tree{}, err
	}
	out, err := store.Git(b.repo, "ls-tree", "-r", "-z", "--name-only", main)
	if err != nil {
		return ui.Tree{}, fmt.Errorf("list the files of main: %w", err)
	}
	changed, err := b.changed(main, accepted)
	if err != nil {
		return ui.Tree{}, err
	}
	t := ui.Tree{Main: main, Accepted: accepted, Docs: []ui.TreeDoc{}}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			t.Docs = append(t.Docs, ui.TreeDoc{Path: p, Kind: docKind(p), Unreviewed: changed[p]})
		}
	}
	return t, nil
}

// Doc returns a file of main. It refuses an empty path, an absolute path and a path with "..".
func (b uiBackend) Doc(_ context.Context, p string) (ui.Doc, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return ui.Doc{}, fmt.Errorf("the path %q is not a path in the memory repo: %w", p, ui.ErrBadInput)
	}
	main, accepted, err := b.resolved()
	if err != nil {
		return ui.Doc{}, err
	}
	text, err := blobAt(b.repo, main, p)
	if err != nil {
		return ui.Doc{}, err
	}
	if text == nil {
		return ui.Doc{}, fmt.Errorf("main holds no file %s: %w", p, ui.ErrNotFound)
	}
	changed, err := b.changed(main, accepted)
	if err != nil {
		return ui.Doc{}, err
	}
	return ui.Doc{Main: main, Path: p, Text: *text, Unreviewed: changed[p]}, nil
}

// Search returns the lines of main that hold the query, ignoring case, at most searchLimit of them.
func (b uiBackend) Search(_ context.Context, query string) (ui.Hits, error) {
	if strings.TrimSpace(query) == "" {
		return ui.Hits{}, fmt.Errorf("the search has no words: %w", ui.ErrBadInput)
	}
	main, _, err := b.resolved()
	if err != nil {
		return ui.Hits{}, err
	}
	h := ui.Hits{Main: main, Hits: []ui.Hit{}}
	out, err := store.Git(b.repo, "grep", "-z", "-n", "--no-column", "-I", "-i", "-F", "--no-textconv", "--no-color", "-e", query, main)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && out == "" {
		return h, nil // git grep exits 1 when no line matches
	}
	if err != nil {
		return ui.Hits{}, fmt.Errorf("search main: %w", err)
	}
	// Each hit is "<commit>:<path>\0<line>\0<text>\n". A path can hold a newline, so the fields end at NUL.
	for rest := out; rest != ""; {
		name, after, ok := strings.Cut(rest, "\x00")
		if !ok {
			break
		}
		num, after, ok := strings.Cut(after, "\x00")
		if !ok {
			break
		}
		text, after, _ := strings.Cut(after, "\n")
		rest = after
		n, err := strconv.Atoi(num)
		if err != nil {
			return ui.Hits{}, fmt.Errorf("search main: git grep printed the line number %q", num)
		}
		if len(h.Hits) == searchLimit {
			h.More = true
			break
		}
		h.Hits = append(h.Hits, ui.Hit{Path: strings.TrimPrefix(name, main+":"), Line: n, Text: text})
	}
	return h, nil
}

// Push runs push --expect head, so it pushes only the queue that the person saw. The person confirmed in the
// window, so the push takes the path of the person.
func (b uiBackend) Push(_ context.Context, head string) (ui.Result, error) {
	if !commitName.MatchString(head) {
		return ui.Result{}, fmt.Errorf("the push names no commit as the tip of main, but %q: %w", head, ui.ErrBadInput)
	}
	return b.action("push", "--expect="+head)
}

// Drop runs drop on a commit of the queue, with the reason of the person, who confirmed in the window.
func (b uiBackend) Drop(_ context.Context, commit, reason string) (ui.Result, error) {
	if !commitName.MatchString(commit) {
		return ui.Result{}, fmt.Errorf("the drop names no commit, but %q: %w", commit, ui.ErrBadInput)
	}
	// A reason such as --help stays one argument, so Run never reads it as a flag.
	args := []string{"drop", commit}
	if reason != "" {
		args = append(args, "--reason="+reason)
	}
	return b.action(args...)
}

// action runs a command through Run, so the use log records it, with a confirmation that says yes. It returns
// the output of the command, and its error in the Result.
func (b uiBackend) action(args ...string) (ui.Result, error) {
	var out bytes.Buffer
	yes := func(string) (bool, error) { return true, nil }
	err := Run(Env{Dir: b.dir, In: strings.NewReader(""), Out: &out, Err: &out, Confirm: yes}, args[0], args[1:])
	r := ui.Result{Output: strings.TrimSpace(out.String())}
	if err != nil {
		r.Error = err.Error()
	}
	return r, nil
}

var _ ui.Backend = uiBackend{}

// These values set the lifetime and the window of the server. Tests replace them.
var (
	uiGrace  = 10 * time.Second
	uiStart  = 60 * time.Second
	uiWindow = ui.Window
	uiWait   = 5 * time.Second // how long memcore ui waits for the server that it started
)

// uiState is the server of the window that runs, as .git/memcore/ui.json records it.
type uiState struct {
	PID   int    `json:"pid"`
	Port  int    `json:"port"`
	Token string `json:"token"`
	Nonce string `json:"nonce"` // the nonce of the memcore ui that started the server
}

// url returns the address of the page, with the token in the fragment.
func (s uiState) url() string { return fmt.Sprintf("http://127.0.0.1:%d/#%s", s.Port, s.Token) }

// runUI opens a window on the server of the window, and starts that server when none runs. Only the person
// runs it. With --serve, it is that server: it serves until the last window closes. --install and --uninstall
// write and remove the application of the window, and need no memory repo.
func runUI(env Env, args []string) error {
	flags := flag.NewFlagSet("ui", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	serve := flags.Bool("serve", false, "run the server of the window")
	nonce := flags.String("nonce", "", "the nonce of the memcore ui that starts the server")
	install := flags.Bool("install", false, "write the application of the window")
	uninstall := flags.Bool("uninstall", false, "remove the application of the window")
	pos, err := parseInterleaved(flags, args)
	if err != nil || len(pos) != 0 {
		return usageErr("usage: memcore ui [--install | --uninstall]")
	}
	if (*install || *uninstall) && (len(args) != 1) {
		return usageErr("usage: memcore ui [--install | --uninstall]")
	}
	if err := guards.Agent(); err != nil {
		return err
	}
	if *install {
		return installApp(env)
	}
	if *uninstall {
		return uninstallApp(env)
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	if *serve {
		return serveUI(env, repo, *nonce)
	}
	if s, ok := runningUI(repo); ok {
		if err := s.post("/api/window"); err != nil {
			return err
		}
		fmt.Fprintf(env.Out, "Opened a window on the running server: %s\n", s.url())
		return nil
	}
	// The nonce tells this call whether its own server won. Another memcore ui may start one at the same time,
	// and then only one of the two servers runs.
	mine := make([]byte, 16)
	if _, err := rand.Read(mine); err != nil {
		return fmt.Errorf("make the nonce of the window: %w", err)
	}
	if err := startBackground(repo, "ui", "--serve", "--nonce="+hex.EncodeToString(mine)); err != nil {
		return err
	}
	// The server writes ui.json only after its first window opened, so a failed window never reads as success.
	for deadline := time.Now().Add(uiWait); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		s, ok := runningUI(repo)
		if !ok {
			continue
		}
		if s.Nonce != hex.EncodeToString(mine) {
			if err := s.post("/api/window"); err != nil {
				return err
			}
			fmt.Fprintf(env.Out, "Opened a window on the running server: %s\n", s.url())
			return nil
		}
		fmt.Fprintf(env.Out, "Opened the window: %s\nThe server stops when the last window closes.\n", s.url())
		return nil
	}
	return fmt.Errorf("the server of the window did not start within %s: %s holds its errors", uiWait, tilde(filepath.Join(store.StateDir(repo), "background.log")))
}

// serveUI serves the window until the last one closes. Only one server runs for a memory repo: it holds
// ui.lock for its whole life, and another server exits at once.
func serveUI(env Env, repo, nonce string) error {
	unlock, ok, err := store.TryLockFile(repo, "ui.lock")
	if err != nil {
		return err
	}
	if !ok {
		return nil // another server runs, and memcore ui opens a window on it
	}
	defer unlock()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("make the token of the window: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for the window: %w", err)
	}
	defer ln.Close()
	state := uiState{PID: os.Getpid(), Port: ln.Addr().(*net.TCPAddr).Port, Token: hex.EncodeToString(secret), Nonce: nonce}
	file := filepath.Join(store.StateDir(repo), "ui.json")
	defer os.Remove(file) // the lock makes this server the only one that writes it
	s := &ui.Server{Backend: uiBackend{repo: repo, dir: env.Dir}, Token: state.Token, Grace: uiGrace, Start: uiStart, Window: uiWindow,
		Ready: func() error { return writeState(file, state) }}
	return s.Serve(ln)
}

// writeState writes ui.json with mode 0600 through a temporary file, so a reader never sees half of it.
func writeState(file string, state uiState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode the state of the window: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), "ui-*.json")
	if err != nil {
		return fmt.Errorf("write the state of the window: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write the state of the window: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write the state of the window: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the state of the window: %w", err)
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		return fmt.Errorf("write the state of the window: %w", err)
	}
	return nil
}

// runningUI returns the server that ui.json names, when it answers with its token.
func runningUI(repo string) (uiState, bool) {
	data, err := os.ReadFile(filepath.Join(store.StateDir(repo), "ui.json"))
	if err != nil {
		return uiState{}, false
	}
	var s uiState
	if json.Unmarshal(data, &s) != nil || s.Port == 0 || s.Token == "" {
		return uiState{}, false
	}
	return s, s.get("/api/ping") == nil
}

// get asks the server for a path of its API.
func (s uiState) get(p string) error {
	return s.call("GET", p)
}

// post sends an empty JSON object to a path of the API, from the origin of the server.
func (s uiState) post(p string) error {
	return s.call("POST", p)
}

// call sends one request to the server, with its token, and refuses an answer other than 200.
func (s uiState) call(method, p string) error {
	origin := fmt.Sprintf("http://127.0.0.1:%d", s.Port)
	req, err := http.NewRequest(method, origin+p, strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("ask the server of the window: %w", err)
	}
	req.Header.Set("X-Memcore-Token", s.Token)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ask the server of the window: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("the server of the window answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
