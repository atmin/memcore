package guards

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfirmRefusesAnAgent(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	ok, err := Confirm("Push? [y/N] ")
	if ok || err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("Confirm = %v, %v; want a refusal that names CLAUDECODE", ok, err)
	}
}

func TestConfirmWithoutTerminal(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	os.Unsetenv("CLAUDECODE")
	was := ttyPath
	ttyPath = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { ttyPath = was })
	ok, err := Confirm("Push? [y/N] ")
	if ok || !errors.Is(err, ErrNoPerson) {
		t.Errorf("Confirm without a terminal = %v, %v; want an error that is ErrNoPerson", ok, err)
	}
}

func TestAgent(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	if err := Agent(); err != nil {
		t.Errorf("Agent without CLAUDECODE = %v, want nil", err)
	}
	t.Setenv("CLAUDECODE", "1")
	err := Agent()
	if err == nil || !strings.Contains(err.Error(), "CLAUDECODE") || !errors.Is(err, ErrNoPerson) {
		t.Fatalf("Agent = %v, want a refusal that names CLAUDECODE and is ErrNoPerson", err)
	}
	if want := "an agent runs this command, since CLAUDECODE is set: only the person runs it, at a terminal"; err.Error() != want {
		t.Errorf("Agent = %q, want %q", err, want)
	}
}
