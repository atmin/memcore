package commands

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// helps holds the help of each command: its input and format, with one minimal valid input.
var helps = map[string]string{
	"orient": `memcore orient

Takes no input. It prints the commands, the voice skill to follow, and the indexes
from the working directory up to the root. Run it at the start of every session.
`,
	"show": `memcore show <path>

Prints one document by its path in the memory repo, as orient lists it.
A skill prints as the person accepted it. For example:

  memcore show dev/memories/tests-before-code.md
`,
	"remember": `memcore remember [--new] [--at <dir>] < memory.md

Reads one memory from standard input: a frontmatter between two --- lines, then the body.

  name         the topic, in lower case with hyphens, unique in the memory repo
  description  one line that says when to read the memory, such as "Read before you ..."
  type         user, feedback, project or reference
  sessions     leave it out: memcore fills it from the session of the agent

The body states one fact. A feedback memory ends with **Why:** and **How to apply:**.
Link another memory with [[name]].

memcore places the memory at the level of the working directory. --at places it at the
level of a directory on the path up to home: --at ~ for a fact about the person, in every
project. When memories on the path share most words of the description, memcore lists
them and writes nothing, unless --new.

Example:

memcore remember <<'EOF'
---
name: tests-before-code
description: Read before you write code: the tests of a step come first.
type: feedback
---

Write the tests of a step before its code.

**Why:** A test written after the code proves what the code does, not what it should do.
**How to apply:** Name the tests in the plan, and commit them in the step with their code.
EOF
`,
	"revise": `memcore revise <path or name> --reason <text> < document

Replaces one document with the whole new version on standard input, and records the reason.
Name a memory by its name, and any other document by its path, as orient lists it.

For a memory, the input has the frontmatter of memcore remember, with the same name: a new
name is memcore forget, then memcore remember. memcore keeps the sessions and adds this one.
A plan, a note or a skill keeps its frontmatter, and a script keeps its mode.

When the last change of the document is not pushed yet, the revision folds into that change
at the next push. Example:

memcore revise tests-before-code --reason "The tests go in the same commit." <<'EOF'
---
name: tests-before-code
description: Read before you write code: the tests of a step come first, in its commit.
type: feedback
---

Write the tests of a step before its code, and commit them with it.
EOF
`,
	"forget": `memcore forget <path or name> --reason <text>

Removes one document, and records the reason. Name a memory by its name, and any other
document by its path, as orient lists it. A skill goes by its folder or its SKILL.md, with
everything in the folder.

forget refuses while another document links to the one it removes, and names that document:
revise it first. A level left with no document goes too. When the document was never pushed,
the removal folds into the commit that added it at the next push, so neither remains.
For example:

  memcore forget tests-before-code --reason "The plan template now says it."
`,
	"draft": `memcore draft <kind> [<topic>] [--description <text>] [--at <dir>] [--exec]

Starts a document from its template at the level of the working directory, or of --at,
and commits it. Then memcore revise fills it. The kinds:

  plan <topic>         plans/<topic>.md, from the plan template; needs --description
  note <topic>         notes/<topic>.md, a dated investigation; needs --description
  script <name>        bin/<name>, executable; --description is its comment line
  helper <name>        bin/<name>, a file beside the scripts, not executable
  skill <name>         skills/<name>/SKILL.md; needs --description
  skill <name>/<file>  a file inside an existing skill; --exec makes it executable
  conventions          conventions.md, the rules of the repo
  glossary             glossary.md, the terms of the level

A topic is lower case with hyphens. A script name is unique in the memory repo, and no
command on PATH may hold it. A description says when to read, or when to run. For example:

  memcore draft note slow-search --description "Read when search is slow."
`,
	"verify": `memcore verify <path>

Checks a document, or every document under a path, for dead references and forbidden
words, and warns on a description that names no moment to read. For example:

  memcore verify dev/memories/tests-before-code.md
`,
	"snapshot": `memcore snapshot [--from <dir>]

Takes no input. It commits the new transcripts of Claude Code to the transcripts branch of
the memory repo, trimmed to the conversation and redacted. memcore never pushes that branch.
--from reads another folder in the layout of ~/.claude/projects, such as an old copy:

  memcore snapshot --from ~/old-projects
`,
	"inbox": `memcore inbox

Takes no input. It fetches, then lists the unpushed commits, which wait for the review
of the person.
`,
	"push": `memcore push

Only the person runs it, at a terminal. It refuses uncommitted changes, fetches, and folds
each fixup into the commit it corrects. Then it asks, and pushes the unpushed commits.
`,
	"recover": `memcore recover

Only the person runs it, at a terminal, after a fetch finds that the remote no longer holds
the accepted history. It lists the accepted commits that the remote lost, and the queue.
After the person confirms, it drops the lost commits here and moves the queue onto the remote.
`,
	"drop": `memcore drop <commit> [--reason <text>]

Only the person runs it, at a terminal. It removes one commit of the review queue, with the
fixups that fold into it, and records the reason, so a later sleep pass learns from it.
It refuses an accepted commit, and a drop that leaves a reference dead. memcore inbox lists
the commits. For example:

  memcore drop 1a2b3c4 --reason "Not a fact: a status of one ticket."
`,
	"help": `memcore help [<command>]

Lists the commands, or shows the input of one command, with an example. For example:

  memcore help remember
`,
	"init": `memcore init <remote>

Only the person runs it. It creates the memory repo when the remote is empty, and clones
the remote otherwise. For example:

  memcore init git@example.com:you/memory.git
`,
}

// help returns the help of a command.
func help(c Command) string {
	if h, ok := helps[c.Name]; ok {
		return h
	}
	return fmt.Sprintf("memcore %s\n\n%s\n", strings.TrimSpace(c.Name+" "+c.Args), c.Does)
}

// runHelp lists the commands, or prints the help of one.
func runHelp(env Env, args []string) error {
	if len(args) > 1 {
		return usageErr("usage: memcore help [<command>]")
	}
	if len(args) == 1 {
		for _, c := range Table() {
			if c.Name == args[0] {
				_, err := io.WriteString(env.Out, help(c))
				return err
			}
		}
	}
	for _, c := range Table() {
		if !c.Hidden {
			fmt.Fprintf(env.Out, "  memcore %-20s %s\n", strings.TrimSpace(c.Name+" "+c.Args), c.Does)
		}
	}
	fmt.Fprintln(env.Out, "\nmemcore help <command> shows the input of a command, with an example.")
	if len(args) == 1 {
		return usageErr(fmt.Sprintf("no command %q", args[0]))
	}
	return nil
}

// inputError is a wrong argument or a wrong input, which Run ends with the pointer to the help.
type inputError struct{ err error }

func (e *inputError) Error() string { return e.err.Error() }
func (e *inputError) Unwrap() error { return e.err }

// usageErr returns the error of a wrong argument.
func usageErr(msg string) error {
	return &inputError{errors.New(msg)}
}

// inputErrf returns an input error with a formatted message.
func inputErrf(format string, a ...any) error {
	return &inputError{fmt.Errorf(format, a...)}
}

// isInput reports whether an error comes from a wrong argument or a wrong input.
func isInput(err error) bool {
	if err == nil {
		return false
	}
	var ie *inputError
	var se *store.InputError
	return errors.As(err, &ie) || errors.As(err, &se)
}
