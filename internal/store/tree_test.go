package store

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"
)

// names returns the names of the entries.
func names(list []fs.DirEntry) []string {
	var out []string
	for _, e := range list {
		out = append(out, e.Name())
	}
	return out
}

func TestTree(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "-b", "main")
	writeT(t, repo, ".gitignore", ".DS_Store\n.idea/\n*.md~\ntracked.log\n", 0o644)
	writeT(t, repo, ".git/info/exclude", "scratch.tmp\n", 0o644)
	writeT(t, repo, "dev/tracked.log", "tracked\n", 0o644)
	gitT(t, repo, "add", ".gitignore")
	gitT(t, repo, "add", "-f", "dev/tracked.log")
	gitT(t, repo, "commit", "-q", "-m", "init")
	writeT(t, repo, "dev/.DS_Store", "finder\n", 0o644)
	writeT(t, repo, "dev/scratch.tmp", "scratch\n", 0o644)
	writeT(t, repo, "dev/a.md~", "backup\n", 0o644)
	writeT(t, repo, "dev/a.md", "visible\n", 0o644)
	writeT(t, repo, "dev/.idea/x/y.xml", "editor\n", 0o644)
	writeT(t, repo, "dev/über notes.md", "visible\n", 0o644)
	writeT(t, repo, "dev/über notes.md~", "backup\n", 0o644)

	tree, err := OpenTree(repo)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		rel     string
		ignored bool
	}{
		{"a file that .gitignore names", "dev/.DS_Store", true},
		{"a file that .git/info/exclude names", "dev/scratch.tmp", true},
		{"a file that a pattern names", "dev/a.md~", true},
		{"an ignored folder", "dev/.idea", true},
		{"a file inside an ignored folder", "dev/.idea/x/y.xml", true},
		{"a folder inside an ignored folder", "dev/.idea/x", true},
		{"a tracked file that a rule matches", "dev/tracked.log", false},
		{"an untracked file that no rule matches", "dev/a.md", false},
		{"a name with a space and a non-ASCII letter", "dev/über notes.md", false},
		{"an ignored name with a space and a non-ASCII letter", "dev/über notes.md~", true},
		{"a folder", "dev", false},
		{"the root", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tree.Ignored(tt.rel); got != tt.ignored {
				t.Errorf("Ignored(%q) = %v, want %v", tt.rel, got, tt.ignored)
			}
			if got := tree.Exists(tt.rel); got == tt.ignored {
				t.Errorf("Exists(%q) = %v, want %v", tt.rel, got, !tt.ignored)
			}
		})
	}

	t.Run("ReadDir skips each ignored path", func(t *testing.T) {
		list, err := tree.ReadDir("dev")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"a.md", "tracked.log", "über notes.md"}
		if got := names(list); !slices.Equal(got, want) {
			t.Errorf("ReadDir(dev) = %q, want %q", got, want)
		}
	})

	t.Run("WalkDir skips each ignored path and never enters an ignored folder", func(t *testing.T) {
		var got []string
		err := tree.WalkDir("dev", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			got = append(got, p)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(repo, "dev")
		want := []string{dir, filepath.Join(dir, "a.md"), filepath.Join(dir, "tracked.log"), filepath.Join(dir, "über notes.md")}
		if !slices.Equal(got, want) {
			t.Errorf("WalkDir(dev) visits %q, want %q", got, want)
		}
	})

	t.Run("a missing folder", func(t *testing.T) {
		if _, err := tree.ReadDir("no/such"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadDir of a missing folder: %v, want fs.ErrNotExist", err)
		}
		if _, err := tree.ReadDir("dev/.idea"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadDir of an ignored folder: %v, want fs.ErrNotExist", err)
		}
		if tree.Exists("no/such") {
			t.Error("Exists(no/such) = true, want false")
		}
	})

	t.Run("a directory with no .git", func(t *testing.T) {
		dir := t.TempDir()
		writeT(t, dir, ".gitignore", ".DS_Store\n", 0o644)
		writeT(t, dir, "dev/.DS_Store", "finder\n", 0o644)
		t.Setenv("PATH", "") // a call of git would fail
		plain, err := OpenTree(dir)
		if err != nil {
			t.Fatalf("OpenTree: %v", err)
		}
		if plain.Ignored("dev/.DS_Store") || !plain.Exists("dev/.DS_Store") {
			t.Error("dev/.DS_Store is ignored, want no path ignored")
		}
		list, err := plain.ReadDir("dev")
		if err != nil {
			t.Fatal(err)
		}
		if got := names(list); !slices.Equal(got, []string{".DS_Store"}) {
			t.Errorf("ReadDir(dev) = %q, want .DS_Store", got)
		}
	})

	if got := tree.Root(); got != repo {
		t.Errorf("Root() = %q, want %q", got, repo)
	}
}
