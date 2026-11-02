// Package commands holds the memcore command line.
package commands

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Env is what a command reads and writes besides the memory repo.
type Env struct {
	Dir     string // the working directory
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Confirm func(prompt string) (bool, error)
}

// Command is one row of the command table.
type Command struct {
	Name  string
	Args  string
	Does  string
	Yours bool // only the person runs it
	Run   func(env Env, args []string) error
}

// Table returns the commands in the order that the usage and orient print them.
func Table() []Command {
	return []Command{
		{Name: "verify", Args: "<path>", Does: "Checks a document, or every document under a path, for dead references and forbidden words.", Run: runVerify},
		{Name: "init", Args: "<remote>", Does: "Creates the memory repo in ~/memory when the remote is empty, and clones the remote otherwise.", Yours: true, Run: runInit},
	}
}

// home returns the home directory, from $HOME.
func home() (string, error) {
	h := os.Getenv("HOME")
	if h == "" {
		return "", errors.New("HOME is not set")
	}
	return h, nil
}

// memoryRepo returns the path of the memory repo, ~/memory.
func memoryRepo() (string, error) {
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "memory"), nil
}
