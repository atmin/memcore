package commands

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// runRevise replaces a document with the one on standard input, checks it, and commits it with the reason.
func runRevise(env Env, args []string) error {
	flags := flag.NewFlagSet("revise", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	reason := flags.String("reason", "", "why the document changes")
	pos, err := parseInterleaved(flags, args)
	if err != nil {
		return usageErr(err.Error())
	}
	if len(pos) != 1 || strings.TrimSpace(*reason) == "" {
		return usageErr("usage: memcore revise <path or name> --reason <text> < document")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	input, err := io.ReadAll(env.In)
	if err != nil {
		return fmt.Errorf("read standard input: %w", err)
	}
	unlock, err := lockWrite(repo)
	if err != nil {
		return err
	}
	defer unlock()
	rel, err := resolve(repo, pos[0])
	if err != nil {
		return err
	}
	env.event().Paths, env.event().Reason = []string{rel}, *reason
	full := filepath.Join(repo, filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}

	content := input
	var warnings []string
	switch {
	case docKind(rel) == "memories" && path.Base(path.Dir(rel)) == "memories" && strings.HasSuffix(rel, ".md"):
		m, err := store.ParseMemory(strings.NewReader(string(input)))
		if err != nil {
			return err
		}
		env.event().input(m.Name, m.Description, m.Type)
		if want := strings.TrimSuffix(path.Base(rel), ".md"); m.Name != want {
			return inputErrf("the name of %s is %s: a new name is memcore forget, then memcore remember", rel, want)
		}
		old, err := os.Open(full)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		was, err := store.ParseMemory(old)
		old.Close()
		if err != nil {
			return fmt.Errorf("read the sessions of %s: %w", rel, err)
		}
		m.Sessions = was.Sessions
		if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" && !slices.Contains(m.Sessions, "claude:"+id) {
			m.Sessions = append(m.Sessions, "claude:"+id)
		}
		content, warnings = m.Bytes(), m.Warnings()
	case store.HasFrontmatter(string(input)):
		fm, body, err := store.ParseFrontmatter(strings.NewReader(string(input)))
		if err != nil {
			return err
		}
		content = append(fm.Bytes(), []byte("\n"+body)...)
	}

	if found := guards.Secrets(string(content)); len(found) > 0 {
		for _, f := range found {
			fmt.Fprintf(env.Out, "%s:%d: %s\n", rel, f.Line, f.Message)
		}
		return inputErrf("the document holds a likely secret, so revise wrote nothing: remove the secret, then revise again")
	}
	if strings.HasSuffix(rel, ".md") {
		report, err := guards.VerifyContent(repo, rel, content)
		if err != nil {
			return err
		}
		for _, f := range report.Faults {
			fmt.Fprintf(env.Out, "%s:%d: fault: %s\n", f.Path, f.Line, f.Message)
		}
		if len(report.Faults) > 0 {
			return inputErrf("%d faults, so revise wrote nothing", len(report.Faults))
		}
		for _, f := range report.Warnings {
			if !slices.Contains(warnings, f.Message) {
				fmt.Fprintf(env.Out, "%s:%d: warning: %s\n", f.Path, f.Line, f.Message)
			}
		}
	}
	for _, w := range warnings {
		fmt.Fprintf(env.Out, "warning: %s\n", w)
	}

	msg, err := commitMessage(repo, rel, "revise", *reason)
	if err != nil {
		return err
	}
	made, err := apply(repo, change{writes: []file{{path: rel, content: content, mode: info.Mode().Perm()}}, message: msg})
	if errors.Is(err, errNoChange) {
		fmt.Fprintf(env.Out, "Nothing to revise: %s already holds this text.\n", rel)
		return nil
	}
	if err != nil {
		return err
	}
	env.event().Made = []string{made}
	fmt.Fprintf(env.Out, "Revised %s.\n", rel)
	return nil
}
