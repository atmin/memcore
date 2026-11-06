package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// previewed is a commit of the review queue as the fold at push leaves it.
type previewed struct {
	subject, body string
	files         []fileStatus
	target        string   // This is the commit of the memory repo that the fold keeps. It is "" for an index that push commits.
	fixups        []string // These are the commits of the memory repo that fold into the target, oldest first.
}

// pick is a pick line of the todo of a fold, with the fixup lines after it.
type pick struct {
	sha    string
	fixups []string
}

// preview returns the review queue as push would leave it, oldest first. It runs regenerate, fold and regenerate
// again, as prepare does, in a scratch clone of main, and removes the clone before it returns. So it changes nothing
// in the memory repo, and runs no git hook and no file system monitor. The caller holds the write lock.
func preview(repo string) ([]previewed, error) {
	// The clone lacks the ignored files, and an index of push might list them.
	out, err := store.Git(repo, "--no-optional-locks", "-c", "core.fsmonitor=false", "status", "--porcelain", "--ignored", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("list the ignored files: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "!! "); ok {
			return nil, fmt.Errorf("preview the fold at push: the working tree holds the ignored file %s, which a clone lacks", p)
		}
	}
	queue, err := revList(repo, "refs/heads/main")
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "memcore-preview-")
	if err != nil {
		return nil, fmt.Errorf("create the directory of the preview: %w", err)
	}
	defer os.RemoveAll(tmp)
	clone, err := scratchClone(repo, tmp)
	if err != nil {
		return nil, err
	}
	if _, err := regenerate(clone); err != nil {
		return nil, fmt.Errorf("push would stop: %w", err)
	}
	if _, err := fold(clone); err != nil {
		return nil, fmt.Errorf("the fold at push would stop: %w", err)
	}
	// A chain that the fold drops may have held the change of a stale index, so push regenerates again.
	again, err := regenerate(clone)
	if err != nil {
		return nil, fmt.Errorf("push would stop: %w", err)
	}
	all, err := revList(clone, "refs/heads/main")
	if err != nil {
		return nil, err
	}
	commits := make([]previewed, len(all))
	keys := make([]string, len(all))
	for i, sha := range all {
		if commits[i], keys[i], err = describe(clone, sha); err != nil {
			return nil, err
		}
	}
	// The index commits after the fold come last and are no picks of the todo, so they keep no target and no fixups.
	shas := all[:len(all)-len(again)]
	matched, err := readTodo(clone)
	if err != nil {
		return nil, err
	}
	if matched == nil {
		// fold folded nothing and wrote no todo, so each commit keeps its sha.
		matched = make([]pick, len(shas))
		for i, sha := range shas {
			matched[i] = pick{sha: sha}
		}
	} else if matched, err = match(clone, keys[:len(shas)], matched); err != nil {
		return nil, err
	}
	inQueue := map[string]bool{}
	for _, sha := range queue {
		inQueue[sha] = true
	}
	for i, p := range matched {
		if inQueue[p.sha] {
			commits[i].target = p.sha // a commit that regenerate made in the clone has no target
		}
		commits[i].fixups = p.fixups
	}
	return commits, nil
}

// scratchClone clones the branch main of the repo into tmp for regenerate and fold, and returns the path of the clone.
// No hook, file system monitor or signing helper runs in the clone. Its accepted commit is the one of the repo.
// It takes the settings of the repo that shape a commit message, so its messages match those of push.
// It holds every folder of the working tree, since an index lists the empty folders of its level.
func scratchClone(repo, tmp string) (string, error) {
	hooks, clone := filepath.Join(tmp, "hooks"), filepath.Join(tmp, "clone")
	if err := os.Mkdir(hooks, 0o755); err != nil {
		return "", fmt.Errorf("create the empty hooks of the preview: %w", err)
	}
	if _, err := store.Git(tmp, "-c", "core.hooksPath="+hooks, "-c", "core.fsmonitor=false", "clone", "--quiet", "--shared", "--no-tags",
		"--single-branch", "--branch", "main", "--template=", "--no-checkout", repo, clone); err != nil {
		return "", fmt.Errorf("clone the memory repo for the preview: %w", err)
	}
	config := [][2]string{{"core.hooksPath", hooks}, {"core.fsmonitor", "false"}, {"commit.gpgSign", "false"}, {"log.showSignature", "false"}}
	for _, key := range []string{"user.name", "user.email", "commit.cleanup", "core.commentChar", "core.commentString"} {
		if v, err := store.Git(repo, "config", "--get", key); err == nil {
			config = append(config, [2]string{key, strings.TrimSpace(v)})
		}
	}
	for _, c := range config {
		if _, err := store.Git(clone, "config", c[0], c[1]); err != nil {
			return "", fmt.Errorf("configure the clone of the preview: %w", err)
		}
	}
	if _, err := store.Git(clone, "checkout", "-q", "main"); err != nil {
		return "", fmt.Errorf("check out the clone of the preview: %w", err)
	}
	args := []string{"update-ref", "-d", acceptedRef}
	if base, ok := revParse(repo, acceptedRef); ok {
		args = []string{"update-ref", acceptedRef, base}
	}
	if _, err := store.Git(clone, args...); err != nil {
		return "", fmt.Errorf("set the accepted commit of the clone: %w", err)
	}
	if err := os.MkdirAll(store.StateDir(clone), 0o755); err != nil {
		return "", fmt.Errorf("create the local state of the clone: %w", err)
	}
	err := filepath.WalkDir(repo, func(f string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(repo, f)
		if err != nil || rel == "." {
			return err
		}
		// main may hold a symlink where the working tree holds a folder, and a write must stay in the clone.
		if err := noSymlink(clone, filepath.ToSlash(rel)); err != nil {
			return err
		}
		return os.MkdirAll(filepath.Join(clone, rel), 0o755)
	})
	if err != nil {
		return "", fmt.Errorf("copy the folders of the working tree to the clone: %w", err)
	}
	return clone, nil
}

// describe returns a commit with its subject, body and files, and its key.
func describe(repo, sha string) (previewed, string, error) {
	subject, key, err := keyOf(repo, sha)
	if err != nil {
		return previewed{}, "", err
	}
	body, err := store.Git(repo, "show", "-s", "--format=%b", sha)
	if err != nil {
		return previewed{}, "", fmt.Errorf("read the commit %s: %w", short(sha), err)
	}
	files, err := filesOf(repo, sha)
	if err != nil {
		return previewed{}, "", err
	}
	return previewed{subject: subject, body: strings.TrimSpace(body), files: files}, key, nil
}

// keyOf returns the subject of a commit, and its key: the subject and the author date, which a rebase keeps.
func keyOf(repo, sha string) (subject, key string, err error) {
	out, err := store.Git(repo, "show", "-s", "--date=raw", "--format=%s%x00%ad", sha)
	if err != nil {
		return "", "", fmt.Errorf("read the commit %s: %w", short(sha), err)
	}
	key = strings.TrimSuffix(out, "\n")
	subject, _, _ = strings.Cut(key, "\x00")
	return subject, key, nil
}

// filesOf returns the files that a commit changes, with their status letters, as git diff-tree prints them.
func filesOf(repo, sha string) ([]fileStatus, error) {
	out, err := store.Git(repo, "diff-tree", "--no-commit-id", "--name-status", "-r", "--root", sha)
	if err != nil {
		return nil, fmt.Errorf("list the files of %s: %w", short(sha), err)
	}
	var files []fileStatus
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if status, p, ok := strings.Cut(line, "\t"); ok {
			files = append(files, fileStatus{status, p})
		}
	}
	return files, nil
}

// readTodo returns the picks of the todo that fold wrote, in order, or nil when fold wrote none.
// The drop lines are the chains that the fold drops, so no pick holds them.
func readTodo(repo string) ([]pick, error) {
	b, err := os.ReadFile(filepath.Join(store.StateDir(repo), "todo"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the todo of the fold: %w", err)
	}
	picks := []pick{}
	for _, line := range strings.Split(string(b), "\n") {
		verb, sha, _ := strings.Cut(line, " ")
		switch {
		case verb == "pick":
			picks = append(picks, pick{sha: sha})
		case verb == "fixup" && len(picks) > 0:
			picks[len(picks)-1].fixups = append(picks[len(picks)-1].fixups, sha)
		}
	}
	return picks, nil
}

// match returns the pick of each folded commit, in the order of keys. The rebase keeps the subject and the author
// date of a pick. When it dropped no pick, the commits follow the picks in order. Otherwise it dropped a pick that
// became empty, and each commit matches the one pick with its key.
func match(clone string, keys []string, picks []pick) ([]pick, error) {
	subjects, pickKeys := make([]string, len(picks)), make([]string, len(picks))
	for i, p := range picks {
		var err error
		if subjects[i], pickKeys[i], err = keyOf(clone, p.sha); err != nil {
			return nil, err
		}
		if len(keys) == len(picks) && pickKeys[i] != keys[i] {
			return nil, fmt.Errorf("preview the fold at push: the folded commits do not follow the todo at %q", subjects[i])
		}
	}
	if len(keys) == len(picks) {
		return picks, nil
	}
	matched := make([]pick, len(keys))
	used := make([]bool, len(picks))
	for i, key := range keys {
		var found []int
		for j, k := range pickKeys {
			if k == key {
				found = append(found, j)
			}
		}
		subject, _, _ := strings.Cut(key, "\x00")
		switch {
		case len(found) != 1:
			return nil, fmt.Errorf("preview the fold at push: the rebase drops a commit that becomes empty, and the folded commit %q matches %d commits of the queue", subject, len(found))
		case used[found[0]]:
			return nil, fmt.Errorf("preview the fold at push: two folded commits match the commit %q of the queue", subject)
		}
		used[found[0]] = true
		matched[i] = picks[found[0]]
	}
	for j, p := range picks {
		if !used[j] && len(p.fixups) > 0 {
			return nil, fmt.Errorf("preview the fold at push: the rebase drops %q as empty, and git folds its fixups into the commit before it", subjects[j])
		}
	}
	return matched, nil
}
