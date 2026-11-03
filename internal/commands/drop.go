package commands

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// runDrop drops a commit of the review queue and its fixups, after the person confirms at the terminal.
func runDrop(env Env, args []string) error {
	flags := flag.NewFlagSet("drop", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	reason := flags.String("reason", "", "why the commit goes")
	pos, err := parseInterleaved(flags, args)
	if err != nil {
		return usageErr(err.Error())
	}
	if len(pos) != 1 {
		return usageErr("usage: memcore drop <commit> [--reason <text>]")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	unlock, err := lockWrite(repo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := cleanTree(repo, "drop dropped nothing"); err != nil {
		return err
	}
	if rebasing(repo) {
		return errors.New("a rebase is in progress in the memory repo: the person finishes it or aborts it first")
	}
	if out, err := store.Git(repo, "symbolic-ref", "-q", "HEAD"); err != nil || strings.TrimSpace(out) != "refs/heads/main" {
		return fmt.Errorf("the memory repo has %q checked out: the person checks out main first", strings.TrimSpace(out))
	}
	target, ok := revParse(repo, pos[0])
	if !ok {
		return inputErrf("no commit %s: memcore inbox lists the queue", pos[0])
	}
	if accepted, ok := revParse(repo, acceptedRef); ok {
		pushed, err := isAncestor(repo, target, accepted)
		if err != nil {
			return err
		}
		if pushed {
			return inputErrf("%s is accepted: drop removes only commits of the review queue", short(target))
		}
	}
	chains, base, err := chainsOf(repo)
	if err != nil {
		return err
	}
	var dropped []commit
	for i, ch := range chains {
		if ch.target.sha == target {
			if i == 0 && base == "" {
				return inputErrf("%s is the first commit of the memory repo, which drop keeps", short(target))
			}
			dropped = append([]commit{ch.target}, ch.fixups...)
		}
		for i, f := range ch.fixups {
			if f.sha == target {
				// A later fixup of the chain builds on this one, so it goes too.
				dropped = slices.Clone(ch.fixups[i:])
			}
		}
	}
	if len(dropped) == 0 {
		return inputErrf("%s is not in the review queue: memcore inbox lists the queue", short(target))
	}

	fmt.Fprintf(env.Out, "drop removes these commits from the review queue:\n")
	for _, c := range dropped {
		fmt.Fprintf(env.Out, "  %s %s\n", short(c.sha), c.subject)
	}
	confirm := env.Confirm
	if confirm == nil {
		confirm = guards.Confirm
	}
	yes, err := confirm(fmt.Sprintf("Drop %s? [y/N] ", commits(len(dropped))))
	if err != nil {
		return err
	}
	if !yes {
		fmt.Fprintln(env.Out, "Dropped nothing.")
		return nil
	}

	before, err := guards.Faults(repo, nil)
	if err != nil {
		return err
	}
	orphansBefore, err := orphanSkills(repo)
	if err != nil {
		return err
	}
	old := headCommit(repo)
	// The todo keeps the order of the queue, so drop moves no commit that it keeps.
	rng := "HEAD"
	if base != "" {
		rng = base + "..HEAD"
	}
	order, err := store.Git(repo, "rev-list", "--reverse", rng)
	if err != nil {
		return fmt.Errorf("list the review queue: %w", err)
	}
	var todo strings.Builder
	for _, sha := range strings.Fields(order) {
		if slices.ContainsFunc(dropped, func(d commit) bool { return d.sha == sha }) {
			fmt.Fprintf(&todo, "drop %s\n", sha)
		} else {
			fmt.Fprintf(&todo, "pick %s\nexec false\n", sha) // the stop regenerates the indexes
		}
	}
	todoPath := filepath.Join(store.StateDir(repo), "todo")
	if err := os.WriteFile(todoPath, []byte(todo.String()), 0o644); err != nil {
		return fmt.Errorf("write the todo of the drop: %w", err)
	}
	onto := "--root"
	if base != "" {
		onto = base
	}
	editor := "cp '" + strings.ReplaceAll(todoPath, "'", `'\''`) + "'"
	if err := rebaseEditor(repo, editor, "-q", "-i", onto); err != nil {
		return fmt.Errorf("drop the commits: %w", err)
	}
	after, err := guards.Faults(repo, nil)
	if err != nil {
		if _, rerr := store.Git(repo, "reset", "-q", "--hard", old); rerr != nil {
			return fmt.Errorf("%w; put the queue back: %v", err, rerr)
		}
		return fmt.Errorf("check the queue after the drop, so drop dropped nothing: %w", err)
	}
	var broken []string
	for _, f := range after {
		if !slices.Contains(before, f) {
			fmt.Fprintf(env.Out, "%s:%d: fault: %s\n", f.Path, f.Line, f.Message)
			if !slices.Contains(broken, f.Path) {
				broken = append(broken, f.Path)
			}
		}
	}
	orphansAfter, err := orphanSkills(repo)
	var orphans []string
	for _, o := range orphansAfter {
		if !slices.Contains(orphansBefore, o) {
			orphans = append(orphans, o)
		}
	}
	if err != nil {
		if _, rerr := store.Git(repo, "reset", "-q", "--hard", old); rerr != nil {
			return fmt.Errorf("%w; put the queue back: %v", err, rerr)
		}
		return err
	}
	if len(orphans) > 0 {
		if _, err := store.Git(repo, "reset", "-q", "--hard", old); err != nil {
			return fmt.Errorf("put the queue back after a refused drop: %w", err)
		}
		return inputErrf("the drop leaves %s without its SKILL.md, so drop dropped nothing: drop the other commits of the skill too", strings.Join(orphans, ", "))
	}
	if len(broken) > 0 {
		if _, err := store.Git(repo, "reset", "-q", "--hard", old); err != nil {
			return fmt.Errorf("put the queue back after a refused drop: %w", err)
		}
		return inputErrf("the drop leaves %s with a dead reference, so drop dropped nothing: revise %s first", strings.Join(broken, ", "), series(broken))
	}
	for _, c := range dropped {
		env.event().Dropped = append(env.event().Dropped, c.sha)
	}
	env.event().Reason = *reason
	fmt.Fprintf(env.Out, "Dropped %s.\n", commits(len(dropped)))
	return nil
}

// orphanSkills returns the folders of skills that hold files but no SKILL.md. An empty folder is no skill.
func orphanSkills(repo string) ([]string, error) {
	var orphans []string
	err := filepath.WalkDir(repo, func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(repo, f)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if path.Base(path.Dir(rel)) != "skills" {
			return nil
		}
		folder := path.Dir(path.Dir(rel))
		if folder == "." {
			folder = ""
		}
		if _, isLevel := store.LevelOf(folder); !isLevel {
			return nil
		}
		if _, err := os.Stat(filepath.Join(f, "SKILL.md")); err != nil && holdsFile(f) {
			orphans = append(orphans, rel)
		}
		return filepath.SkipDir
	})
	if err != nil {
		return nil, fmt.Errorf("check the skills: %w", err)
	}
	return orphans, nil
}
