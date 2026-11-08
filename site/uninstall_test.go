package site

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// uninstallScript returns the uninstall script.
func uninstallScript(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("uninstall")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// uninstallHome makes a home with memcore in ~/.local/bin, a memory repo, and a link of an accepted script.
// The fake memcore records its arguments in $HOME/memcore-calls, and fails for the command failOn. A memcore
// on the returned PATH fails always, so a call through PATH shows.
func uninstallHome(t *testing.T, failOn string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	accepted := filepath.Join(home, "memory", ".git", "memcore", "accepted", "bin")
	for _, d := range []string{bin, accepted} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/memcore-calls\"\n"
	if failOn != "" {
		fake += fmt.Sprintf("[ \"$1\" != %s ] || { echo 'memcore %s: it failed' >&2; exit 1; }\n", failOn, failOn)
	}
	write(t, filepath.Join(bin, "memcore"), fake, 0o755)
	write(t, filepath.Join(home, "memory", "notes.md"), "a memory\n", 0o644)
	write(t, filepath.Join(accepted, "hello"), "#!/bin/sh\necho hello\n", 0o755)
	if err := os.Symlink(filepath.Join(accepted, "hello"), filepath.Join(bin, "hello")); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	write(t, filepath.Join(other, "memcore"), "#!/bin/sh\necho 'the memcore on PATH ran' >> \"$HOME/memcore-calls\"\nexit 1\n", 0o755)
	return home, other + ":" + os.Getenv("PATH")
}

func write(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

// uninstall pipes the script into the shell, with only the environment that the test sets.
func uninstall(shell string, text []byte, home, path string, env ...string) (string, error) {
	cmd := exec.Command(shell)
	cmd.Stdin = bytes.NewReader(text)
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + path}, env...)
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		cmd.Env = append(cmd.Env, "TMPDIR="+tmp)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// kept checks that the memory repo and the link of the accepted script stay.
func kept(t *testing.T, home string) {
	t.Helper()
	if b, err := os.ReadFile(filepath.Join(home, "memory", "notes.md")); err != nil || string(b) != "a memory\n" {
		t.Errorf("the memory repo changed: %q, %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "hello")); err != nil {
		t.Errorf("the link of the accepted script is gone: %v", err)
	}
}

func shells() []string {
	s := []string{"sh"}
	if _, err := exec.LookPath("dash"); err == nil {
		s = append(s, "dash")
	}
	return s
}

func TestUninstall(t *testing.T) {
	for _, shell := range shells() {
		for _, tt := range []struct{ name, repo, want string }{
			{"the memory repo in ~/memory", "", "~/memory"},
			{"MEMCORE_REPO under home", "memories/main", "~/memories/main"},
			{"MEMCORE_REPO elsewhere", "/srv/memory", "/srv/memory"},
		} {
			t.Run(shell+" "+tt.name, func(t *testing.T) {
				t.Parallel()
				home, path := uninstallHome(t, "")
				var env []string
				if tt.repo != "" {
					repo := tt.repo
					if !filepath.IsAbs(repo) {
						repo = filepath.Join(home, repo)
					}
					env = append(env, "MEMCORE_REPO="+repo)
				}
				out, err := uninstall(shell, uninstallScript(t), home, path, env...)
				if err != nil {
					t.Fatalf("uninstall: %v\n%s", err, out)
				}
				if got := calls(t, home); got != "init --undo\nui --uninstall\n" {
					t.Errorf("memcore ran with %q, want init --undo, then ui --uninstall", got)
				}
				if _, err := os.Stat(filepath.Join(home, ".local", "bin", "memcore")); err == nil {
					t.Error("~/.local/bin/memcore stays")
				}
				want := "Removed memcore. The memory repo stays in " + tt.want + ": remove it yourself when you no longer need it.\n"
				if !strings.HasSuffix(out, want) {
					t.Errorf("output =\n%s\nwant it to end with\n%s", out, want)
				}
				kept(t, home)
			})
		}
	}
}

func TestUninstallStops(t *testing.T) {
	for _, tt := range []struct{ failOn, calls, says string }{
		{"init", "init --undo\n", "the settings of Claude Code still run memcore"},
		{"ui", "init --undo\nui --uninstall\n", "the application of the window stays"},
	} {
		t.Run(tt.failOn, func(t *testing.T) {
			t.Parallel()
			home, path := uninstallHome(t, tt.failOn)
			out, err := uninstall("sh", uninstallScript(t), home, path)
			if err == nil {
				t.Fatalf("uninstall succeeded:\n%s", out)
			}
			if !strings.Contains(out, "memcore uninstall: "+tt.says) || !strings.Contains(out, "memcore "+tt.failOn+": it failed") {
				t.Errorf("output =\n%s\nwant the failure of %s and the reason", out, tt.failOn)
			}
			if got := calls(t, home); got != tt.calls {
				t.Errorf("memcore ran with %q, want %q", got, tt.calls)
			}
			if _, err := os.Stat(filepath.Join(home, ".local", "bin", "memcore")); err != nil {
				t.Errorf("memcore is gone: %v", err)
			}
			kept(t, home)
		})
	}
}

func TestUninstallMissing(t *testing.T) {
	home := t.TempDir()
	out, err := uninstall("sh", uninstallScript(t), home, os.Getenv("PATH"))
	if err == nil || !strings.Contains(out, "memcore uninstall: ~/.local/bin/memcore does not exist, so there is nothing to uninstall") {
		t.Errorf("uninstall without memcore: %v\n%s", err, out)
	}
}

func TestUninstallPrefixes(t *testing.T) {
	text := uninstallScript(t)
	end := bytes.LastIndex(text, []byte("\n}"))
	if end < 0 {
		t.Fatal("the script has no closing } line")
	}
	end++ // the length of the longest prefix without the last }
	home, path := uninstallHome(t, "")

	type job struct {
		shell string
		n     int
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				uninstall(j.shell, text[:j.n], home, path)
			}
		}()
	}
	for _, shell := range shells() {
		for n := 1; n <= end; n++ {
			jobs <- job{shell, n}
		}
	}
	close(jobs)
	wg.Wait()

	if got := calls(t, home); got != "" {
		t.Errorf("prefixes ran memcore: %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "memcore")); err != nil {
		t.Errorf("a prefix removed memcore: %v", err)
	}
	kept(t, home)
}
