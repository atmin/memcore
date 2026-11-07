package store

import "fmt"

// Commit stages the paths, relative to repo, and commits them alone with the message.
// A path that does not exist commits as a removal. Git reads each path literally, never as a pattern.
func Commit(repo string, paths []string, message string) error {
	if _, err := Git(repo, append([]string{"--literal-pathspecs", "add", "-A", "--"}, paths...)...); err != nil {
		return fmt.Errorf("stage the change: %w", err)
	}
	if _, err := Git(repo, append([]string{"--literal-pathspecs", "commit", "-q", "-m", message, "--"}, paths...)...); err != nil {
		return fmt.Errorf("commit the change: %w", err)
	}
	return nil
}
