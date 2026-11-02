// Command memcore is long-term memory for agents, kept as plain markdown in git.
package main

import (
	"fmt"
	"os"

	"github.com/atmin/memcore/internal/commands"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name, args := os.Args[1], os.Args[2:]
	for _, c := range commands.Table() {
		if c.Name != name {
			continue
		}
		dir, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "memcore: read the working directory: %v\n", err)
			os.Exit(1)
		}
		env := commands.Env{Dir: dir, In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
		if err := c.Run(env, args); err != nil {
			fmt.Fprintf(os.Stderr, "memcore %s: %v\n", name, err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "memcore: unknown command %q\n", name)
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: memcore <command> [arguments]")
	for _, c := range commands.Table() {
		fmt.Fprintf(os.Stderr, "  %-18s %s\n", c.Name+" "+c.Args, c.Does)
	}
}
