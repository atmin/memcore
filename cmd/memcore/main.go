// Command memcore is long-term memory for agents, kept as plain markdown in git.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atmin/memcore/internal/commands"
	"github.com/atmin/memcore/internal/guards"
)

// init locks the main goroutine to the main thread, where the native windows of memcore ui must live.
func init() { runtime.LockOSThread() }

func main() {
	if exe, err := os.Executable(); err == nil {
		if args, ok := bundleArgs(exe, os.Args[1:]); ok {
			os.Args = append(os.Args[:1], args...)
			logToFile()
		}
	}
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

// bundleArgs returns the arguments of memcore.app: ui --app, when memcore runs inside a bundle with no command,
// or with only the -psn_ argument of an old macOS.
func bundleArgs(exe string, args []string) ([]string, bool) {
	if !strings.Contains(exe, ".app/Contents/MacOS/") {
		return nil, false
	}
	if len(args) == 0 || (len(args) == 1 && strings.HasPrefix(args[0], "-psn_")) {
		return []string{"ui", "--app"}, true
	}
	return nil, false
}

// logToFile sends standard error to ~/Library/Logs/memcore.log, because an application has no terminal.
func logToFile() {
	h, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(h, "Library", "Logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "memcore.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	if err := redirectStderr(f); err != nil {
		fmt.Fprintf(f, "memcore: point standard error at the log: %v\n", err)
	}
	os.Stderr = f
}
