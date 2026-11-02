package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// TestVerifyCommand uses only this package and the store, because it lands before the helpers of the commands.
func TestVerifyCommand(t *testing.T) {
	dir := t.TempDir()
	if _, err := store.Git(dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"dev/notes/2026-11-03-bare.md": "# A bare note\n",
		"dev/memories/linked.md":       "---\nname: linked\ndescription: \"Read when you test.\"\n---\n\nSee [[gone]].\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	verify := func(cwd, target string) (string, error) {
		var out strings.Builder
		err := runVerify(Env{Dir: cwd, Out: &out, Err: &out}, []string{target})
		return out.String(), err
	}

	out, err := verify(dir, "dev/notes")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	for _, want := range []string{"dev/notes/2026-11-03-bare.md:1: warning: the document has no frontmatter", "No faults, 1 warning.\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want %q", out, want)
		}
	}

	out, err = verify(filepath.Join(dir, "dev"), "memories/linked.md")
	if err == nil || !strings.Contains(out, "dev/memories/linked.md:6: fault: dead reference [[gone]]") {
		t.Errorf("verify of a dead reference = %v, output %q, want a fault", err, out)
	}
}
