package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// runVerify checks the documents at a path, relative to the working directory or to the memory repo.
func runVerify(env Env, args []string) error {
	if len(args) != 1 {
		return usageErr("usage: memcore verify <path>")
	}
	target := args[0]
	if !filepath.IsAbs(target) {
		target = filepath.Join(env.Dir, target)
	}
	if _, err := os.Stat(target); err != nil {
		if repo, rerr := memoryRepo(); rerr == nil {
			if _, aerr := os.Stat(filepath.Join(repo, args[0])); aerr == nil {
				target = filepath.Join(repo, args[0])
			}
		}
	}
	target, err := filepath.EvalSymlinks(target)
	if err != nil {
		return inputErrf("find %s: %w", args[0], err)
	}
	repo, err := toplevel(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(repo, target)
	if err != nil {
		return fmt.Errorf("find %s in %s: %w", target, repo, err)
	}
	env.event().Paths = []string{filepath.ToSlash(rel)}
	r, err := guards.Verify(repo, filepath.ToSlash(rel))
	if err != nil {
		return err
	}
	for _, f := range r.Faults {
		fmt.Fprintf(env.Out, "%s:%d: fault: %s\n", f.Path, f.Line, f.Message)
	}
	for _, f := range r.Warnings {
		fmt.Fprintf(env.Out, "%s:%d: warning: %s\n", f.Path, f.Line, f.Message)
	}
	if len(r.Faults) > 0 {
		return fmt.Errorf("%d faults", len(r.Faults))
	}
	noun := "warnings"
	if len(r.Warnings) == 1 {
		noun = "warning"
	}
	fmt.Fprintf(env.Out, "No faults, %d %s.\n", len(r.Warnings), noun)
	return nil
}

// toplevel returns the root of the git repo that holds a path, or the folder of the path in no repo.
func toplevel(target string) (string, error) {
	dir := target
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		dir = filepath.Dir(target)
	}
	out, err := store.Git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return dir, nil
		}
		return "", fmt.Errorf("find the repo of %s: %w", target, err)
	}
	return strings.TrimSpace(out), nil
}
