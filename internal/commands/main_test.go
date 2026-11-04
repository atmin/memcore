package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// TestMain isolates git from the configuration and the agent of the machine.
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "Test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		os.Setenv(k, v)
	}
	os.Unsetenv("CLAUDECODE")
	os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	os.Unsetenv("MEMCORE_REPO")
	// No test reads or writes the Claude Code folder of the machine.
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	empty, err := os.MkdirTemp("", "memcore-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", empty)
	// No test starts a process in the background. recordStarts records the starts of one test.
	startBackground = func(string, ...string) error { return nil }
	code := m.Run()
	os.RemoveAll(empty)
	os.Exit(code)
}

// run runs one command in dir and returns what it printed, standard output and standard error together.
func run(t *testing.T, dir, stdin string, args ...string) (string, error) {
	t.Helper()
	return runWith(t, Env{Dir: dir, In: strings.NewReader(stdin)}, args...)
}

func runWith(t *testing.T, env Env, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	env.Out, env.Err = &out, &out
	if env.In == nil {
		env.In = strings.NewReader("")
	}
	err := Run(env, args[0], args[1:])
	if errors.Is(err, ErrUnknown) {
		t.Fatalf("no command %q", args[0])
	}
	return out.String(), err
}

// gitT runs git in dir and fails the test on an error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := store.Git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mkdirT(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeT(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	mkdirT(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newHome sets HOME to a new temporary directory and returns it.
func newHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// bareRemote creates an empty bare repo and returns its path.
func bareRemote(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "memory.git")
	gitT(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", remote)
	return remote
}

// newMemory sets up HOME and a memory repo with an empty bare remote, through init.
func newMemory(t *testing.T) (home, repo, remote string) {
	t.Helper()
	home = newHome(t)
	remote = bareRemote(t)
	if out, err := run(t, home, "", "init", remote); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	return home, filepath.Join(home, "memory"), remote
}
