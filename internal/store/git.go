// Package store keeps the memory repo: its documents, its indexes and its commits.
package store

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// repoVariables are the variables of `git rev-parse --local-env-vars`.
// A git hook sets them, and they would point git away from dir.
var repoVariables = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE",
	"GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
	"GIT_SHALLOW_FILE", "GIT_COMMON_DIR",
}

// Git runs the installed git in dir and returns its standard output.
// The error holds the arguments and the standard error of git, in English.
func Git(dir string, args ...string) (string, error) {
	return GitStdin(dir, "", args...)
}

// GitStdin runs the installed git in dir with stdin as its standard input, like Git.
func GitStdin(dir, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); !slices.Contains(repoVariables, name) {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
