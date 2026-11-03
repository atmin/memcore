// Package commands holds the memcore command line.
package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Env is what a command reads and writes besides the memory repo.
type Env struct {
	Dir     string // the working directory
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Confirm func(prompt string) (bool, error)
	Event   *Event // the line of the use log that Run appends after the command
}

// Command is one row of the command table.
type Command struct {
	Name  string
	Args  string
	Does  string
	Yours bool   // only the person runs it
	Help  string // the input and the format, with one minimal valid input; see help.go
	Run   func(env Env, args []string) error
}

// Table returns the commands in the order that the usage and orient print them.
func Table() []Command {
	return []Command{
		{Name: "orient", Does: "Shows these commands, the voice and the indexes from the working directory up to the root.", Run: runOrient},
		{Name: "show", Args: "<path>", Does: "Prints a document by its path in the memory repo. It prints a skill as the person accepted it.", Run: runShow},
		{Name: "remember", Args: "[--new] [--at <dir>]", Does: "Places, checks, indexes and commits a memory that it reads from standard input. It lists near-duplicates and writes nothing, unless --new.", Run: runRemember},
		{Name: "revise", Args: "<path or name> --reason <text>", Does: "Replaces a document with the one on standard input, checks it, and commits it with the reason.", Run: runRevise},
		{Name: "forget", Args: "<path or name> --reason <text>", Does: "Removes a document, or the folder of a skill, and commits the removal with the reason.", Run: runForget},
		{Name: "draft", Args: "<kind> [<topic>]", Does: "Starts a plan, a note, a script, a helper, a skill, a conventions file or a glossary from its template, and commits it.", Run: runDraft},
		{Name: "verify", Args: "<path>", Does: "Checks a document, or every document under a path, for dead references and forbidden words.", Run: runVerify},
		{Name: "inbox", Does: "Fetches, then lists the unpushed commits, which wait for the review of the person.", Run: runInbox},
		{Name: "push", Does: "Fetches, asks the person at the terminal, then pushes the unpushed commits.", Yours: true, Run: runPush},
		{Name: "recover", Does: "Drops the accepted commits that the remote lost, and moves the queue onto the remote, after the person confirms.", Yours: true, Run: runRecover},
		{Name: "help", Args: "[<command>]", Does: "Shows the input of a command, with an example.", Run: runHelp},
		{Name: "init", Args: "<remote>", Does: "Creates the memory repo, ~/memory or $MEMCORE_REPO, when the remote is empty, and clones the remote otherwise.", Yours: true, Run: runInit},
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

// memoryRepo returns the path of the memory repo: $MEMCORE_REPO when it is set, and ~/memory otherwise.
func memoryRepo() (string, error) {
	if r := os.Getenv("MEMCORE_REPO"); r != "" {
		if !filepath.IsAbs(r) {
			return "", fmt.Errorf("MEMCORE_REPO is %q, not an absolute path", r)
		}
		return filepath.Clean(r), nil
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "memory"), nil
}

// tilde writes a path under home with ~ in front, for a message.
func tilde(p string) string {
	h, err := home()
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(h, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	if rel == "." {
		return "~"
	}
	return "~/" + filepath.ToSlash(rel)
}

// event returns the event of the command, or a throwaway one when no Run logs the call.
func (env Env) event() *Event {
	if env.Event != nil {
		return env.Event
	}
	return &Event{}
}
