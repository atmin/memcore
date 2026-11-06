package guards

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNoPerson is the refusal of a command of the person when no person answers: an agent runs it,
// or no terminal opens. A command that an agent may run in part checks for it with errors.Is.
var ErrNoPerson = errors.New("only the person runs it, at a terminal")

// ttyPath is the controlling terminal, where Confirm asks. A test points it at a missing file.
var ttyPath = "/dev/tty"

// Agent refuses a command of the person when an agent runs it: when CLAUDECODE is set.
// It stops an agent's mistake, and nothing more.
func Agent() error {
	if os.Getenv("CLAUDECODE") != "" {
		return fmt.Errorf("an agent runs this command, since CLAUDECODE is set: %w", ErrNoPerson)
	}
	return nil
}

// Confirm asks a yes-or-no question at the controlling terminal, /dev/tty.
// It refuses with ErrNoPerson when it cannot open the terminal, or when CLAUDECODE is set.
// These checks stop an agent's mistake, and nothing more: an agent can fake a terminal.
func Confirm(prompt string) (bool, error) {
	if err := Agent(); err != nil {
		return false, err
	}
	tty, err := os.OpenFile(ttyPath, os.O_RDWR, 0)
	if err != nil {
		return false, fmt.Errorf("open the terminal: %w: %w", err, ErrNoPerson)
	}
	defer tty.Close()
	if _, err := fmt.Fprint(tty, prompt); err != nil {
		return false, fmt.Errorf("ask at the terminal: %w", err)
	}
	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && line == "" {
		return false, fmt.Errorf("read the answer at the terminal: %w", err)
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}
