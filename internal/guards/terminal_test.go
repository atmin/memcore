package guards

import (
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

func TestAgent(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	if err := Agent(); err != nil {
		t.Errorf("Agent without CLAUDECODE = %v, want nil", err)
	}
	t.Setenv("CLAUDECODE", "1")
	if err := Agent(); err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("Agent = %v, want a refusal that names CLAUDECODE", err)
	}
}
