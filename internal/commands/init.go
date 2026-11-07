package commands

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atmin/memcore"
	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

// voicePath is the path of the voice skill in the memory repo and in memcore.Skills.
const voicePath = "skills/voice/SKILL.md"

// initMessage is the message of the first commit of every memory repo, which init makes.
const initMessage = "init: create the memory repo"

// runInit sets up the memory repo and Claude Code: the repo, the snapshot of the transcripts, the import of
// auto-memory, the settings, then the report of inbox. --dry-run builds the repo at MEMCORE_REPO and only
// prints the change of the settings. --undo removes what init added to the settings.
func runInit(env Env, args []string) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dryRun := flags.Bool("dry-run", false, "build the memory repo at MEMCORE_REPO, and change nothing outside it")
	undo := flags.Bool("undo", false, "remove what init added to the settings of Claude Code")
	pos, err := parseInterleaved(flags, args)
	if err != nil {
		return usageErr(err.Error())
	}
	if *undo {
		if len(pos) != 0 || *dryRun {
			return usageErr("usage: memcore init --undo")
		}
		if err := guards.Agent(); err != nil {
			return err
		}
		repo, err := openRepo()
		if err != nil {
			return err
		}
		claude, err := claudeDir()
		if err != nil {
			return err
		}
		unlock, err := store.LockFile(repo, "init.lock")
		if err != nil {
			return err
		}
		defer unlock()
		return undoSettings(env, repo, filepath.Join(claude, "settings.json"))
	}
	if len(pos) != 1 {
		return usageErr("usage: memcore init [--dry-run] <remote>")
	}
	// A dry run changes nothing outside the memory repo, so an agent may rehearse it.
	if *dryRun {
		if os.Getenv("MEMCORE_REPO") == "" {
			return inputErrf("--dry-run builds the memory repo at MEMCORE_REPO, which is not set: set it to a scratch path")
		}
	} else if err := guards.Agent(); err != nil {
		return err
	}
	repo, err := setupRepo(env, pos[0])
	if err != nil {
		return err
	}
	if err := setupClaude(env, repo, *dryRun); err != nil {
		return err
	}
	// init has fetched or cloned already, and a fetch would link scripts, a change that no diff showed.
	unlock, err := store.Lock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	fmt.Fprintln(env.Out)
	return report(env, repo)
}

// setupRepo creates the memory repo when the remote is empty, and clones the remote otherwise.
// A memory repo that exists stays as it is.
func setupRepo(env Env, remote string) (string, error) {
	if !filepath.IsAbs(remote) {
		if _, err := os.Stat(filepath.Join(env.Dir, remote)); err == nil {
			remote = filepath.Join(env.Dir, remote)
		}
	}
	repo, err := memoryRepo()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		return "", fmt.Errorf("create the folder of the memory repo: %w", err)
	}
	unlock, err := store.LockDir(filepath.Dir(repo))
	if err != nil {
		return "", err
	}
	defer unlock()

	ok, err := isMemoryRepo(repo)
	if err != nil {
		return "", err
	}
	if ok {
		if err := os.MkdirAll(store.StateDir(repo), 0o755); err != nil {
			return "", fmt.Errorf("create the local state: %w", err)
		}
		fmt.Fprintf(env.Out, "%s is already set up.\n", repo)
		return repo, nil
	}
	if !isEmpty(repo) {
		return "", fmt.Errorf("%s exists and is not a memory repo of memcore: move it away, then run init again", repo)
	}

	// init builds the repo beside its final path and renames it into place, so a failure leaves
	// that path as it was, and the rename refuses a directory that something filled meanwhile.
	tmp, err := os.MkdirTemp(filepath.Dir(repo), ".memory-init-")
	if err != nil {
		return "", fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(tmp)

	out, err := store.Git(env.Dir, "ls-remote", remote)
	if err != nil {
		return "", fmt.Errorf("read the remote: %w", err)
	}
	cloned := strings.TrimSpace(out) != ""
	if cloned {
		if _, err := store.Git(env.Dir, "clone", "-q", "--branch", "main", remote, tmp); err != nil {
			return "", fmt.Errorf("clone the remote: %w", err)
		}
		if err := os.MkdirAll(store.StateDir(tmp), 0o755); err != nil {
			return "", fmt.Errorf("create the local state: %w", err)
		}
	} else if err := create(tmp, remote); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, repo); err != nil {
		return "", fmt.Errorf("move the new memory repo into place: %w", err)
	}
	if cloned {
		fmt.Fprintf(env.Out, "Cloned %s into %s.\n", remote, repo)
	} else {
		fmt.Fprintf(env.Out, "Created %s with the voice skill. The person accepts it with memcore push.\n", repo)
	}
	return repo, nil
}

// setupClaude snapshots the transcripts of Claude Code, imports its auto-memory, and adds the hooks and the
// permissions to its settings. A home without the folder of Claude Code skips it all. A dry run asks nothing and
// prints the change of the settings without making it.
func setupClaude(env Env, repo string, dryRun bool) error {
	claude, err := claudeDir()
	if err != nil {
		return err
	}
	info, err := os.Stat(claude)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(env.Out, "No Claude Code folder at %s, so init sets up nothing for Claude Code.\n", tilde(claude))
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the Claude Code folder %s: %w", tilde(claude), err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder, so init cannot set up Claude Code: move it away", tilde(claude))
	}
	// One init at a time reads and writes the records, so two runs never import twice.
	unlock, err := store.LockFile(repo, "init.lock")
	if err != nil {
		return err
	}
	defer unlock()
	h, err := home()
	if err != nil {
		return err
	}
	// The snapshot comes first, so the transcripts are safe before anything else can fail.
	projects := filepath.Join(claude, "projects")
	if _, err := os.Stat(projects); err == nil {
		n, err := snapshot(env.Out, repo, projects)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Out, "Snapshotted %s onto the transcripts branch.\n", count(n, "transcript"))
	}
	complete, err := importAutoMemory(env, repo, claude)
	if err != nil {
		return err
	}
	if !complete && !dryRun {
		if complete, err = acceptExclusions(env, repo); err != nil {
			return err
		}
	}

	found, err := exec.LookPath("memcore")
	if err != nil {
		return errors.New("memcore is not on PATH, so init adds no hook: put it on PATH, then run init again")
	}
	exe, err := filepath.Abs(found)
	if err != nil {
		return fmt.Errorf("find the path of memcore: %w", err)
	}
	if strings.Contains(exe, "'") {
		return fmt.Errorf("the path of memcore, %s, holds a single quote, which a hook cannot hold: move memcore", exe)
	}
	settings := filepath.Join(claude, "settings.json")
	old, err := os.ReadFile(settings)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read the settings %s: %w", settings, err)
	}
	tree, err := parseSettings(old)
	if err != nil {
		return err
	}
	add := claudeChanges(tree, repo, h, exe, complete)
	if !dryRun {
		if ask := env.Confirm; add.AutoMemory != nil && ask != nil {
			// Auto-memory may write while the person answers, and a new file would stay out of the repo.
			env.Confirm = func(q string) (bool, error) {
				ok, err := ask(q)
				if !ok || err != nil {
					return ok, err
				}
				rec, err := readImportRecord(repo)
				if err != nil {
					return false, err
				}
				blockers, err := importBlockers(rec, claude)
				if err != nil {
					return false, err
				}
				if len(blockers) > 0 {
					return false, errors.New("a file of auto-memory changed while you answered, so init changed nothing there: run init again")
				}
				return true, nil
			}
		}
		return applySettings(env, repo, settings, tree, add)
	}
	diff, err := settingsDiff(old, tree.encode(), tilde(settings))
	if err != nil || diff == "" {
		return err
	}
	fmt.Fprintf(env.Out, "A dry run changes no setting. init would change %s as below:\n%s", tilde(settings), diff)
	return nil
}

// create makes a new memory repo with the voice skill and the root index, and commits them.
func create(repo, remote string) error {
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return fmt.Errorf("create the memory repo: %w", err)
	}
	if _, err := store.Git(repo, "init", "-q", "-b", "main"); err != nil {
		return fmt.Errorf("create the memory repo: %w", err)
	}
	if _, err := store.Git(repo, "remote", "add", "origin", remote); err != nil {
		return fmt.Errorf("add the remote: %w", err)
	}
	unlock, err := store.Lock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	voice, err := memcore.Skills.ReadFile(voicePath)
	if err != nil {
		return fmt.Errorf("read the shipped voice skill: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(voicePath)), 0o755); err != nil {
		return fmt.Errorf("seed the voice skill: %w", err)
	}
	if err := os.WriteFile(filepath.Join(repo, voicePath), voice, 0o644); err != nil {
		return fmt.Errorf("seed the voice skill: %w", err)
	}
	if err := store.WriteIndex(repo, ""); err != nil {
		return err
	}
	return store.Commit(repo, []string{"README.md", voicePath}, initMessage)
}

// isMemoryRepo reports whether repo is a git repo whose first commit is the one that init makes.
// The indexes may sit behind a commit by hand, so they say nothing about who made the repo.
func isMemoryRepo(repo string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repo, ".git")); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	out, err := store.Git(repo, "log", "--format=%s", "--max-parents=0", "HEAD")
	if err != nil {
		return false, nil // not a repo, or one without a commit
	}
	return slices.Contains(strings.Split(strings.TrimSpace(out), "\n"), initMessage), nil
}

// isEmpty reports whether dir is absent or an empty directory.
func isEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return err == nil && len(entries) == 0
}
