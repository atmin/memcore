package commands

import (
	"strings"
	"testing"
)

func TestHooksKeepTheRepo(t *testing.T) {
	for _, tt := range []struct {
		repo, prefix string
	}{
		{"/home/a/memory", "'" + exe + "' hook "},
		{"/srv/memory", "MEMCORE_REPO='/srv/memory' '" + exe + "' hook "},
	} {
		tree, _ := parseSettings(nil)
		claudeChanges(tree, tt.repo, "/home/a", exe, false)
		got := string(tree.encode())
		for _, arg := range []string{"session-start", "prompt", "session-end", "tool"} {
			if !strings.Contains(got, `"command": "`+tt.prefix+arg+`"`) {
				t.Errorf("repo %s: settings =\n%s\nwant the hook %q", tt.repo, got, tt.prefix+arg)
			}
		}
	}
}
