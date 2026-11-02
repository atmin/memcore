// Package commands holds the memcore command line.
package commands

import "io"

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
	return []Command{}
}
