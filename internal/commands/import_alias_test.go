package commands

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestImportDecodedSecrets(t *testing.T) {
	home, repo, _ := newMemory(t)
	claude := filepath.Join(home, ".claude")
	remember(t, home, "shared", "Read when you share a fact across projects.")
	// The tokens are built from parts, so no scanner takes this file for a leak.
	tail := strings.Repeat("a1B2", 9)
	p := mkdirT(t, filepath.Join(home, "dev", "p"))
	autoMemory(t, claude, p, "tabbed.md", "---\nname: tabbed\ndescription: \"Read when you send Bearer\\u0009Abcd"+"1234efgh.\"\ntype: user\n---\nBody.\n")
	autoMemory(t, claude, p, "named.md", "---\nname: \"ghp\\u005f"+tail+"\"\ndescription: Read when you test.\ntype: user\n---\nBody.\n")
	autoMemory(t, claude, p, "local.md", flat("shared", "Read when you test shared.", "user", "token ghp_"+tail))
	autoMemory(t, claude, p, "c.md", flat("gamma", "Read when you test gamma.", "user", "See [[shared]]."))
	importNow(t, home, repo, claude)

	all := gitT(t, repo, "log", "-p", "--format=%B")
	for _, s := range []string{"1234efgh", tail, strings.ToLower(tail)} {
		if strings.Contains(all, s) {
			t.Errorf("the history holds %q", s)
		}
	}
	got := readT(t, filepath.Join(repo, "dev", "p", "memories", "gamma.md"))
	if strings.Contains(got, "[[shared]]") {
		t.Errorf("gamma =\n%s\nwant the link to the file left out, local.md named shared, without brackets", got)
	}
}

func TestImportEveryDecodedField(t *testing.T) {
	home, repo, _ := newMemory(t)
	claude := filepath.Join(home, ".claude")
	remember(t, home, "shared", "Read when you share a fact across projects.")
	p := mkdirT(t, filepath.Join(home, "dev", "p"))
	// The tokens are built from parts, so no scanner takes this file for a leak.
	hidden := "\"Bearer\\u0009Abcd" + "1234efgh\""
	autoMemory(t, claude, p, "typed.md", "---\nname: typed\ndescription: Read when you test.\ntype: "+hidden+"\n---\nBody.\n")
	autoMemory(t, claude, p, "session.md", "---\nname: session\ndescription: Read when you test.\ntype: user\noriginSessionId: "+hidden+"\n---\nBody.\n")
	autoMemory(t, claude, p, "local.md", "---\nname: shared\ndescription: \"no closing quote\ntype: user\n---\nBody.\n")
	autoMemory(t, claude, p, "c.md", flat("gamma", "Read when you test gamma.", "user", "See [[shared]]."))
	importNow(t, home, repo, claude)

	if all := gitT(t, repo, "log", "-p", "--format=%B"); strings.Contains(all, "1234efgh") {
		t.Error("the history holds the token")
	}
	if got := readT(t, filepath.Join(repo, "dev", "p", "memories", "gamma.md")); strings.Contains(got, "[[shared]]") {
		t.Errorf("gamma =\n%s\nwant the link to local.md, which fails to parse, without brackets", got)
	}
}
