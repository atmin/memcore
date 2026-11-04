package guards

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Agent refuses a command of the person when an agent runs it: when CLAUDECODE is set.
// It stops an agent's mistake, and nothing more.
func Agent() error {
	if os.Getenv("CLAUDECODE") != "" {
		return errors.New("an agent runs this command, since CLAUDECODE is set: only the person runs it, at a terminal")
	}
	return nil
}

// Confirm asks a yes-or-no question at the controlling terminal, /dev/tty.
// It refuses when it cannot open the terminal, or when CLAUDECODE is set.
// These checks stop an agent's mistake, and nothing more: an agent can fake a terminal.
func Confirm(prompt string) (bool, error) {
	if err := Agent(); err != nil {
		return false, err
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, fmt.Errorf("open the terminal, where only the person confirms: %w", err)
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
