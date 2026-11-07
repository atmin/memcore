// Command memcore is long-term memory for agents, kept as plain markdown in git.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/atmin/memcore/internal/commands"
	"github.com/atmin/memcore/internal/guards"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name, args := os.Args[1], os.Args[2:]
	dir, err := workingDir(name, args, os.Getwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memcore: %v\n", err)
		os.Exit(1)
	}
	env := commands.Env{Dir: dir, In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Confirm: guards.Confirm}
	err = commands.Run(env, name, args)
	if errors.Is(err, commands.ErrUnknown) {
		fmt.Fprintf(os.Stderr, "memcore: unknown command %q\n", name)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "memcore %s: %v\n", name, err)
		os.Exit(1)
	}
}

// workingDir returns the working directory of a command. hook tool takes its folder from its input,
// and it never fails. So hook tool runs with an empty folder when getwd fails.
func workingDir(name string, args []string, getwd func() (string, error)) (string, error) {
	dir, err := getwd()
	if err != nil {
		if commands.Late(name, args) {
			return "", nil
		}
		return "", fmt.Errorf("read the working directory: %w", err)
	}
	return dir, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: memcore <command> [arguments]")
	for _, c := range commands.Table() {
		if !c.Hidden {
			fmt.Fprintf(os.Stderr, "  %-18s %s\n", c.Name+" "+c.Args, c.Does)
		}
	}
}
