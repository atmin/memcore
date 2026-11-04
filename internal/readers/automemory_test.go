package readers

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAutoMemory(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string // name, description, type, then the sessions
		body    string
		wantErr bool
	}{
		{"the flat form", "---\nname: small-commits\ndescription: Read before you commit.\ntype: feedback\n---\n\nOne change per commit.\n",
			[]string{"small-commits", "Read before you commit.", "feedback"}, "One change per commit.\n", false},
		{"the metadata form", "---\nname: deploy_day\ndescription: \"Read when: you deploy\"\nmetadata:\n  node_type: memory\n  type: project\n  originSessionId: d4d1d99a-9439\n  modified: 2026-11-03T15:13:28.279Z\n---\n\nFridays only.\n",
			[]string{"deploy_day", "Read when: you deploy", "project", "claude:d4d1d99a-9439"}, "Fridays only.\n", false},
		{"a session in the flat form", "---\nname: a\ndescription: d\ntype: user\noriginSessionId: s1\n---\nBody\n",
			[]string{"a", "d", "user", "claude:s1"}, "Body\n", false},
		{"no session", "---\nname: a\ndescription: d\nmetadata:\n  type: reference\n---\nBody\n",
			[]string{"a", "d", "reference"}, "Body\n", false},
		{"unknown keys", "---\nname: a\ndescription: d\ntype: user\ntags: [x, y]\nowner:\n  - me\nmetadata:\n  stars: 3\n---\nBody\n",
			[]string{"a", "d", "user"}, "Body\n", false},
		{"a description over two lines", "---\nname: a\ndescription: Read when you\n  deploy on Fridays.\ntype: user\n---\nBody\n", nil, "", true},
		{"a folded description", "---\nname: a\ndescription: >\n  Read when you deploy.\ntype: user\n---\nBody\n", nil, "", true},
		{"a deeper map", "---\nname: a\ndescription: d\nmetadata:\n  type: user\n  extra:\n    deep: x\n---\nBody\n", nil, "", true},
		{"no frontmatter", "Just text.\n", nil, "", true},
		{"no closing line", "---\nname: a\n", nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := AutoMemory(strings.NewReader(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Errorf("AutoMemory = %+v, want an error", m)
				}
				return
			}
			if err != nil {
				t.Fatalf("AutoMemory: %v", err)
			}
			got := append([]string{m.Name, m.Description, m.Type}, m.Sessions...)
			if !slices.Equal(got, tt.want) || m.Body != tt.body {
				t.Errorf("AutoMemory = %q, body %q; want %q, body %q", got, m.Body, tt.want, tt.body)
			}
		})
	}
}

// encode names a directory the way Claude Code names its project folder: one hyphen for each
// character that is not a letter or a digit of ASCII, as the paths of this test hold no character beyond U+FFFF.
func encode(dir string) string {
	var b strings.Builder
	for _, r := range dir {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func TestProjectDirs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"dev/atmin.net", "dev/my-app", "dev/my apps", "dev/café", "dev/a-b", "dev/a/b", "dev/plain"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "target", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "target"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		dir  string // the directory that Claude Code named the folder after
		want []string
	}{
		{"a dot", "dev/atmin.net", []string{"dev/atmin.net"}},
		{"a hyphen", "dev/my-app", []string{"dev/my-app"}},
		{"a space", "dev/my apps", []string{"dev/my apps"}},
		{"a letter beyond ASCII", "dev/café", []string{"dev/café"}},
		{"two candidates", "dev/a-b", []string{"dev/a-b", "dev/a/b"}},
		{"a parent of other folders", "dev", []string{"dev"}},
		{"none", "dev/gone", nil},
		{"through a link to a folder", "link/inner", []string{"link/inner"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ProjectDirs(encode(filepath.Join(root, tt.dir)), root)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, w := range tt.want {
				want = append(want, filepath.Join(root, w))
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("ProjectDirs = %q, want %q", got, want)
			}
		})
	}
}

func TestProjectDirsUnreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every folder")
	}
	root := t.TempDir()
	for _, d := range []string{"a-b", "a/b"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "a"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "a"), 0o755) })
	if got, err := ProjectDirs(encode(filepath.Join(root, "a-b")), root); err == nil {
		t.Errorf("ProjectDirs = %q, want an error: the folder a hides a candidate", got)
	}
}

func TestAutoMemoryKeepsTheNameOnAnError(t *testing.T) {
	m, err := AutoMemory(strings.NewReader("---\nname: shared\ndescription: \"no closing quote\ntype: user\n---\nBody\n"))
	if err == nil {
		t.Fatal("AutoMemory of a broken description succeeded")
	}
	if m.Name != "shared" {
		t.Errorf("name = %q, want shared: a link may name the file by it", m.Name)
	}
}
