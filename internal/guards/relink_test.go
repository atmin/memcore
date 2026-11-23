package guards

import (
	"maps"
	"slices"
	"testing"
)

func TestRelinkSkipsIgnored(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "-b", "main")
	writeT(t, repo, ".gitignore", "*.draft.md\n")
	writeT(t, repo, "dev/memories/old-name.md", "---\nname: old-name\ndescription: \"Read when you test.\"\n---\n\nA fact.\n")
	writeT(t, repo, "dev/notes/n.md", "See [old](../memories/old-name.md) and [[old-name]].\n")
	writeT(t, repo, "dev/notes/x.draft.md", "See [old](../memories/old-name.md) and [[old-name]].\n")

	got, err := Relink(repo, "dev/memories/old-name.md", "dev/memories/new-name.md", "old-name", "new-name", true)
	if err != nil {
		t.Fatal(err)
	}
	if changed, want := slices.Sorted(maps.Keys(got)), []string{"dev/notes/n.md"}; !slices.Equal(changed, want) {
		t.Errorf("Relink changes %q, want %q", changed, want)
	}
}
