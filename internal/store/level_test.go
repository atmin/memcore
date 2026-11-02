package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// gitT runs git in dir and fails the test on an error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mkdirT(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLevel(t *testing.T) {
	home := t.TempDir()
	repo := mkdirT(t, filepath.Join(home, "dev", "app"))
	gitT(t, repo, "init", "-b", "main")
	gitT(t, repo, "commit", "--allow-empty", "-m", "first")
	mkdirT(t, filepath.Join(repo, "internal", "store"))
	worktree := filepath.Join(home, "dev", "app-feature")
	gitT(t, repo, "worktree", "add", "-b", "feature", worktree)
	mkdirT(t, filepath.Join(worktree, "internal"))
	plain := mkdirT(t, filepath.Join(home, "notes", "trips"))
	outsideWorktree := filepath.Join(t.TempDir(), "app-outside")
	gitT(t, repo, "worktree", "add", "-b", "outside", outsideWorktree)
	separate := mkdirT(t, filepath.Join(home, "dev", "separate"))
	gitT(t, separate, "init", "-b", "main", "--separate-git-dir", filepath.Join(mkdirT(t, filepath.Join(home, "gitdirs")), "separate"))
	mkdirT(t, filepath.Join(separate, "internal"))

	tests := []struct {
		name    string
		cwd     string
		want    string
		wantErr error
	}{
		{"repo", repo, "dev/app", nil},
		{"subdirectory of a repo", filepath.Join(repo, "internal", "store"), "dev/app", nil},
		{"worktree", worktree, "dev/app", nil},
		{"subdirectory of a worktree", filepath.Join(worktree, "internal"), "dev/app", nil},
		{"directory in no repo", plain, "notes/trips", nil},
		{"home", home, "", nil},
		{"outside home", t.TempDir(), "", ErrOutsideHome},
		{"worktree outside home", outsideWorktree, "", ErrOutsideHome},
		{"repo with a separate git directory", separate, "dev/separate", nil},
		{"subdirectory of a repo with a separate git directory", filepath.Join(separate, "internal"), "dev/separate", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Level(tt.cwd, home)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Level = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPath(t *testing.T) {
	tests := []struct {
		level string
		want  []string
	}{
		{"", []string{""}},
		{"dev", []string{"dev", ""}},
		{"dev/app/web", []string{"dev/app/web", "dev/app", "dev", ""}},
	}
	for _, tt := range tests {
		if got := Path(tt.level); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Path(%q) = %q, want %q", tt.level, got, tt.want)
		}
	}
}
