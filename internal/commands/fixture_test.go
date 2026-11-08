package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// fixture is a memory repo and its empty bare remote, which init built once for the whole run.
// newMemory copies it, which is faster than a run of init for each test.
var fixture struct {
	dir   string   // the folder that holds home/ and remote/memory.git, or "" when there is none
	env   []string // the environment of the build, as fixtureEnv gives it
	owner bool     // whether this process built the fixture, and so removes it
}

// filterEnv sorts environ, without HOME and the variables of the test runner. It drops an empty variable of
// Claude Code or memcore, since only memcore reads those, and it reads an empty one as unset. Git tells an
// empty variable from an unset one, so the others stay.
func filterEnv(environ []string) []string {
	var env []string
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if k == "HOME" || strings.HasPrefix(k, "MEMCORE_TEST_") {
			continue
		}
		if v == "" && (strings.HasPrefix(k, "CLAUDE") || strings.HasPrefix(k, "MEMCORE_")) {
			continue
		}
		env = append(env, kv)
	}
	slices.Sort(env)
	return env
}

// fixtureEnv is the environment that decides what init makes.
func fixtureEnv() []string {
	return filterEnv(os.Environ())
}

// buildFixture builds the fixture with build, or takes the one that MEMCORE_TEST_FIXTURE names. The parent of
// the parallel runner builds it, and its children take it. After a failed build, MEMCORE_TEST_FIXTURE is off,
// and each newMemory runs init, so the failing tests show the error.
func buildFixture(build func(home, remote string) error) {
	fixture.dir, fixture.env, fixture.owner = "", fixtureEnv(), false
	switch dir := os.Getenv("MEMCORE_TEST_FIXTURE"); dir {
	case "off":
		return
	case "":
	default:
		fixture.dir = dir
		return
	}
	dir, err := makeFixture(build)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build the fixture of newMemory: %v\n", err)
		os.Setenv("MEMCORE_TEST_FIXTURE", "off")
		return
	}
	fixture.dir, fixture.owner = dir, true
	os.Setenv("MEMCORE_TEST_FIXTURE", dir)
}

// makeFixture builds a fixture in a new temporary folder, and removes the folder when the build fails.
func makeFixture(build func(home, remote string) error) (string, error) {
	dir, err := os.MkdirTemp("", "memcore-fixture-")
	if err != nil {
		return "", fmt.Errorf("create the folder: %w", err)
	}
	home := filepath.Join(dir, "home")
	remote := filepath.Join(dir, "remote", "memory.git")
	err = os.MkdirAll(home, 0o755)
	if err == nil {
		_, err = store.Git(dir, "init", "-q", "--bare", "-b", "main", remote)
	}
	if err == nil {
		prev := os.Getenv("HOME")
		os.Setenv("HOME", home)
		err = build(home, remote)
		os.Setenv("HOME", prev)
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// initFixture runs init, as newMemory did before the fixture.
func initFixture(home, remote string) error {
	var out bytes.Buffer
	if err := Run(Env{Dir: home, In: strings.NewReader(""), Out: &out, Err: &out}, "init", []string{remote}); err != nil {
		return fmt.Errorf("init: %w\n%s", err, out.String())
	}
	return nil
}

// fromFixture copies the fixture into home and remote, and points the origin of the copy at remote. It returns
// false, and copies nothing, when there is no fixture or the environment differs from the one of the build.
func fromFixture(t *testing.T, home, remote string) bool {
	t.Helper()
	if fixture.dir == "" || !slices.Equal(fixtureEnv(), fixture.env) {
		return false
	}
	copyTree(t, filepath.Join(fixture.dir, "remote", "memory.git"), remote)
	copyTree(t, filepath.Join(fixture.dir, "home"), home)
	gitT(t, filepath.Join(home, "memory"), "config", "remote.origin.url", remote)
	return true
}

// copyTree copies the folders and regular files under src to dst, with their modes and modification times.
// dst itself keeps its mode, as HOME keeps its mode through init.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	type dirTime struct {
		path string
		time time.Time
	}
	// A copy into a folder changes its time, so the times of the folders go last, the deepest first.
	var dirs []dirTime
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			dirs = append(dirs, dirTime{to, info.ModTime()})
			return os.MkdirAll(to, info.Mode().Perm())
		case !info.Mode().IsRegular():
			return fmt.Errorf("%s is neither a folder nor a regular file", p)
		}
		if err := copyFile(p, to, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chtimes(to, info.ModTime(), info.ModTime())
	})
	for i := len(dirs) - 1; i >= 0 && err == nil; i-- {
		err = os.Chtimes(dirs[i].path, dirs[i].time, dirs[i].time)
	}
	if err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func TestCopyTree(t *testing.T) {
	src := t.TempDir()
	writeT(t, src, "a/b/file", "text\n")
	if err := os.Chmod(filepath.Join(src, "a", "b", "file"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, rel := range []string{"a/b/file", "a/b", "a"} {
		if err := os.Chtimes(filepath.Join(src, rel), old, old); err != nil {
			t.Fatal(err)
		}
	}
	dst := t.TempDir()
	copyTree(t, src, dst)
	if got := readT(t, filepath.Join(dst, "a", "b", "file")); got != "text\n" {
		t.Errorf("file = %q, want the text", got)
	}
	for rel, mode := range map[string]fs.FileMode{"a/b/file": 0o755, "a/b": 0o700} {
		info, err := os.Stat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("mode of %s = %v, want %v", rel, info.Mode().Perm(), mode)
		}
	}
	for _, rel := range []string{"a/b/file", "a/b", "a"} {
		info, err := os.Stat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(old) {
			t.Errorf("time of %s = %v, want %v", rel, info.ModTime(), old)
		}
	}
}

func TestFixtureEnv(t *testing.T) {
	tests := []struct {
		kv   string
		kept bool
	}{
		{"HOME=/home/someone", false},
		{"MEMCORE_TEST_CHILD=1", false},
		{"MEMCORE_TEST_FIXTURE=off", false},
		{"CLAUDE_CODE_SESSION_ID=", false},
		{"MEMCORE_REPO=", false},
		{"CLAUDE_CODE_SESSION_ID=s1", true},
		{"MEMCORE_REPO=/tmp/memory", true},
		{"GIT_ALLOW_PROTOCOL=", true},
		{"PATH=/usr/bin", true},
	}
	var environ []string
	for _, tt := range tests {
		environ = append(environ, tt.kv)
	}
	got := filterEnv(environ)
	for _, tt := range tests {
		if slices.Contains(got, tt.kv) != tt.kept {
			t.Errorf("%q kept = %v, want %v", tt.kv, !tt.kept, tt.kept)
		}
	}
	if !slices.IsSorted(got) {
		t.Errorf("filterEnv = %q, want it sorted", got)
	}
}

func TestFixtureFallback(t *testing.T) {
	if fixture.dir == "" {
		t.Fatal("TestMain built no fixture")
	}
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"no change", nil, true},
		{"an empty session", map[string]string{"CLAUDE_CODE_SESSION_ID": ""}, true},
		{"a session", map[string]string{"CLAUDE_CODE_SESSION_ID": "s1"}, false},
		{"an author date", map[string]string{"GIT_AUTHOR_DATE": "1700000000 +0000"}, false},
		{"an empty list of git protocols", map[string]string{"GIT_ALLOW_PROTOCOL": ""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			home := newHome(t)
			if got := fromFixture(t, home, filepath.Join(t.TempDir(), "memory.git")); got != tt.want {
				t.Errorf("fromFixture = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFixtureMatchesInit(t *testing.T) {
	copied := newHome(t)
	copiedRemote := filepath.Join(t.TempDir(), "memory.git")
	if !fromFixture(t, copied, copiedRemote) {
		t.Fatal("fromFixture = false, want a copy of the fixture")
	}
	initHome := newHome(t)
	initRemote := bareRemote(t)
	if out, err := run(t, initHome, "", "init", initRemote); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	a, b := filepath.Join(copied, "memory"), filepath.Join(initHome, "memory")

	for _, args := range [][]string{{"ls-tree", "-r", "HEAD"}, {"log", "--format=%s"}, {"status", "--porcelain"}} {
		if x, y := gitT(t, a, args...), gitT(t, b, args...); x != y {
			t.Errorf("git %s: the copy gives\n%s\ninit gives\n%s", strings.Join(args, " "), x, y)
		}
	}
	if out := gitT(t, a, "status", "--porcelain"); out != "" {
		t.Errorf("the copy has changes:\n%s", out)
	}
	if x, y := worktreeFiles(t, a), worktreeFiles(t, b); !slices.Equal(x, y) {
		t.Errorf("files of the copy = %q, of init = %q", x, y)
	}
	if x, y := refsOf(t, a), refsOf(t, b); !slices.Equal(x, y) {
		t.Errorf("refs of the copy = %q, of init = %q", x, y)
	}
	if x, y := logOf(t, a), logOf(t, b); !slices.Equal(x, y) {
		t.Errorf("use log of the copy = %q, of init = %q", x, y)
	}
	if url := strings.TrimSpace(gitT(t, a, "config", "remote.origin.url")); url != copiedRemote {
		t.Errorf("origin of the copy = %s, want %s", url, copiedRemote)
	}
	if refs := gitT(t, copiedRemote, "for-each-ref"); refs != "" {
		t.Errorf("the remote of the copy has refs:\n%s", refs)
	}
}

// worktreeFiles lists the files of repo outside .git, each with its content.
func worktreeFiles(t *testing.T, repo string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			return err
		}
		files = append(files, rel+"\n"+readT(t, p))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// firstCommit returns the sha of the first commit of repo.
func firstCommit(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(gitT(t, repo, "rev-list", "--max-parents=0", "HEAD"))
}

// refsOf lists the refs of repo with their targets, with <sha> in place of the first commit.
func refsOf(t *testing.T, repo string) []string {
	t.Helper()
	out := gitT(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
	return strings.Split(strings.ReplaceAll(strings.TrimSpace(out), firstCommit(t, repo), "<sha>"), "\n")
}

// logOf lists the entries of the use log of repo without their time. Each commit must be the first commit.
func logOf(t *testing.T, repo string) []string {
	t.Helper()
	first := firstCommit(t, repo)
	var entries []string
	for _, line := range strings.Split(strings.TrimSpace(readT(t, filepath.Join(repo, ".git", "memcore", "log.jsonl"))), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("a line of the use log: %v\n%s", err, line)
		}
		if c, ok := e["commit"]; ok && c != first {
			t.Errorf("the use log names commit %v, want the first commit %s", c, first)
		}
		delete(e, "time")
		delete(e, "commit")
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, string(b))
	}
	return entries
}

func TestFixtureCopiesApart(t *testing.T) {
	if fixture.dir == "" {
		t.Fatal("TestMain built no fixture")
	}
	_, first, _ := newMemory(t)
	_, second, _ := newMemory(t)
	fixtureRepo := filepath.Join(fixture.dir, "home", "memory")
	head := gitT(t, second, "rev-parse", "HEAD")
	fixtureHead := gitT(t, fixtureRepo, "rev-parse", "HEAD")
	writeT(t, first, "note.md", "a change\n")
	gitT(t, first, "add", "note.md")
	gitT(t, first, "commit", "-q", "-m", "a change")
	if got := gitT(t, second, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD of the second repo = %s, want %s", got, head)
	}
	if got := gitT(t, fixtureRepo, "rev-parse", "HEAD"); got != fixtureHead {
		t.Errorf("HEAD of the fixture = %s, want %s", got, fixtureHead)
	}
}

func TestBuildFixtureFailure(t *testing.T) {
	saved := fixture
	t.Cleanup(func() { fixture = saved })
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("MEMCORE_TEST_FIXTURE", "")
	builds := 0
	failing := func(home, remote string) error {
		builds++
		return errors.New("init failed")
	}
	buildFixture(failing)
	if fixture.dir != "" || fixture.owner {
		t.Errorf("fixture = %+v after a failed build, want none", fixture)
	}
	if got := os.Getenv("MEMCORE_TEST_FIXTURE"); got != "off" {
		t.Errorf("MEMCORE_TEST_FIXTURE = %q, want off", got)
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Errorf("the temporary folder holds %v, %v after a failed build, want nothing", entries, err)
	}
	buildFixture(failing)
	if builds != 1 || fixture.dir != "" {
		t.Errorf("builds = %d, fixture = %q with MEMCORE_TEST_FIXTURE=off, want 1 build and no fixture", builds, fixture.dir)
	}
}
