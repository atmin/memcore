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

// runForget removes a document, or the folder of a skill, and commits the removal with the reason.
func runForget(env Env, args []string) error {
	flags := flag.NewFlagSet("forget", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	reason := flags.String("reason", "", "why the document goes")
	pos, err := parseInterleaved(flags, args)
	if err != nil {
		return usageErr(err.Error())
	}
	if len(pos) != 1 || strings.TrimSpace(*reason) == "" {
		return usageErr("usage: memcore forget <path or name> --reason <text>")
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
	target, files, err := forgetTarget(repo, pos[0])
	if err != nil {
		return err
	}
	env.event().Paths, env.event().Reason = []string{target}, *reason

	before, err := guards.Faults(repo, nil)
	if err != nil {
		return err
	}
	var prune []string
	if target != files[0] {
		prune = []string{target}
	}
	emptied, err := wouldPrune(repo, files, prune)
	if err != nil {
		return err
	}
	after, err := guards.Faults(repo, append([]string{target}, emptied...))
	if err != nil {
		return err
	}
	var linked []string
	for _, f := range after {
		if !slices.Contains(before, f) {
			fmt.Fprintf(env.Out, "%s:%d: fault: %s\n", f.Path, f.Line, f.Message)
			if !slices.Contains(linked, f.Path) {
				linked = append(linked, f.Path)
			}
		}
	}
	if len(linked) > 0 {
		return inputErrf("%s links to %s, so forget removed nothing: revise %s first", strings.Join(linked, ", "), target, series(linked))
	}

	msg, err := forgetMessage(repo, target, target != files[0], *reason)
	if err != nil {
		return err
	}
	c := change{removes: files, message: msg}
	if target != files[0] {
		c.prune = []string{target} // the folder of a skill, with any empty folder left inside it
	}
	made, err := apply(repo, c)
	if err != nil {
		return err
	}
	env.event().Made = []string{made}
	fmt.Fprintf(env.Out, "Forgot %s.\n", target)
	return nil
}

// forgetTarget returns what forget removes: a document, named by its path or, for a memory, by its name,
// or the folder of a skill, named by the folder or its SKILL.md. It returns the files of the target too.
func forgetTarget(repo, arg string) (string, []string, error) {
	if !strings.Contains(arg, "/") {
		found, err := findMemory(repo, arg)
		if err != nil {
			return "", nil, err
		}
		if found != "" {
			return found, []string{found}, nil
		}
	}
	p, err := cleanPath(arg)
	if err != nil {
		return "", nil, err
	}
	if err := noSymlink(repo, p); err != nil {
		return "", nil, err
	}
	info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(p)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, inputErrf("no document at %s: memcore orient lists the documents", arg)
	}
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", p, err)
	}
	if err := exactCase(repo, p); err != nil {
		return "", nil, err
	}
	dir := ""
	switch {
	case info.IsDir() && path.Base(path.Dir(p)) == "skills":
		dir = p
	case info.IsDir():
		return "", nil, inputErrf("%s is a folder: forget removes a document, or the folder of a skill", p)
	case path.Base(p) == "SKILL.md" && path.Base(path.Dir(path.Dir(p))) == "skills":
		dir = path.Dir(p)
	default:
		return p, []string{p}, nil
	}
	var files []string
	err = filepath.WalkDir(filepath.Join(repo, filepath.FromSlash(dir)), func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s holds a symlink, which memcore refuses", dir)
		}
		if !d.IsDir() {
			rel, err := filepath.Rel(repo, f)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("list the skill %s: %w", dir, err)
	}
	return dir, files, nil
}

// forgetMessage returns the commit message of a removal. When the queue created the target, the removal is
// a fixup of its last change, so the fold leaves no commit of the pair. Otherwise it is "forget: <path>".
// A skill counts as created by the commit that created its SKILL.md, and its removal is a fixup only when
// every queued change of the skill folds into that commit.
func forgetMessage(repo, target string, skill bool, reason string) (string, error) {
	plain := "forget: " + target + "\n\n" + reason
	creation := target
	if skill {
		creation = path.Join(target, "SKILL.md")
	}
	// The newest creation counts, since a document can come back after an earlier forget.
	out, err := store.Git(repo, "--literal-pathspecs", "log", "-1", "--diff-filter=A", "--format=%H", "--", creation)
	if err != nil {
		return "", fmt.Errorf("find the commit that created %s: %w", target, err)
	}
	adds := strings.Fields(out)
	if len(adds) > 0 {
		created := adds[0]
		pushed := false
		if accepted, ok := revParse(repo, acceptedRef); ok {
			if pushed, err = isAncestor(repo, created, accepted); err != nil {
				return "", fmt.Errorf("check whether %s is pushed: %w", target, err)
			}
		}
		if pushed {
			return plain, nil
		}
		if skill {
			subject, err := store.Git(repo, "log", "-1", "--format=%s", created)
			if err != nil {
				return "", fmt.Errorf("read the commit that created %s: %w", target, err)
			}
			rng := "HEAD"
			if accepted, ok := revParse(repo, acceptedRef); ok {
				rng = accepted + "..HEAD"
			}
			queued, err := store.Git(repo, "--literal-pathspecs", "log", "--format=%H%x00%s", rng, "--", target)
			if err != nil {
				return "", fmt.Errorf("list the queued changes of %s: %w", target, err)
			}
			for _, line := range strings.Split(strings.TrimSpace(queued), "\n") {
				sha, want, _ := strings.Cut(line, "\x00")
				isFixup := strings.HasPrefix(want, "fixup! ")
				for strings.HasPrefix(want, "fixup! ") {
					want = strings.TrimPrefix(want, "fixup! ")
				}
				if sha != created && (!isFixup || want != strings.TrimSpace(subject)) {
					return plain, nil // another commit made part of the skill, so no fixup cancels it all
				}
			}
		}
		return commitMessage(repo, target, "forget", reason)
	}
	return plain, nil
}

// wouldPrune returns the folders that a removal of files leaves empty, deepest first, as apply removes them:
// a folder that holds only removed files and emptied folders, and a level that keeps only its index.
// An empty folder inside counts as gone only where apply removes it too: inside a level that empties,
// or inside a pruned folder, such as the folder of a skill. A link into such a folder breaks too.
func wouldPrune(repo string, files, prune []string) ([]string, error) {
	var dirs []string
	for _, f := range files {
		for d := path.Dir(f); d != "."; d = path.Dir(d) {
			if !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	slices.SortFunc(dirs, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
	var emptied []string
	for _, d := range dirs {
		entries, err := os.ReadDir(filepath.Join(repo, filepath.FromSlash(d)))
		if err != nil {
			return nil, fmt.Errorf("check whether %s empties: %w", d, err)
		}
		_, isLevel := store.LevelOf(d)
		empty := true
		for _, e := range entries {
			p := path.Join(d, e.Name())
			switch {
			case slices.Contains(files, p), slices.Contains(emptied, p):
			case isLevel && e.Name() == "README.md":
			case e.IsDir() && !holdsAny(repo, p, files) && (isLevel || inside(d, prune)):
			default:
				empty = false
			}
		}
		if empty {
			emptied = append(emptied, d)
		}
	}
	return emptied, nil
}

// holdsAny reports whether a folder holds a file anywhere inside it, apart from the files that go.
func holdsAny(repo, dir string, gone []string) bool {
	found := false
	filepath.WalkDir(filepath.Join(repo, filepath.FromSlash(dir)), func(f string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if rel, err := filepath.Rel(repo, f); err == nil && slices.Contains(gone, filepath.ToSlash(rel)) {
			return nil
		}
		found = true
		return fs.SkipAll
	})
	return found
}

// inside reports whether a folder is one of the roots, or inside one.
func inside(dir string, roots []string) bool {
	for _, r := range roots {
		if dir == r || strings.HasPrefix(dir, r+"/") {
			return true
		}
	}
	return false
}
