package store

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
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

// walkAllowed lists the functions that may read a directory without a Tree, as path::symbol, with the reason.
var walkAllowed = map[string]string{
	// The reader itself.
	"internal/store/tree.go": "the reader of the working tree",
	// The folders of Claude Code, which lie outside the memory repo.
	"internal/commands/import.go::importAutoMemory":  "lists the project folders of Claude Code",
	"internal/commands/import.go::importProject":     "lists the auto-memory of a project of Claude Code",
	"internal/commands/import.go::importBlockers":    "lists the auto-memory files of Claude Code",
	"internal/commands/snapshot.go::transcriptFiles": "lists the transcripts of Claude Code",
	"internal/readers/claude.go::ProjectDirs":        "finds the directories that a project folder of Claude Code names",
	// Other directories.
	"internal/commands/init.go::isEmpty":          "checks the directory that init clones into",
	"internal/commands/accepted.go::syncAccepted": "reads the checkout of the accepted history",
	// The reads of a named path, which find the spelling of a path that a caller names.
	"internal/commands/reads.go::spelling":  "reads a named path, to find its spelling on disk",
	"internal/commands/paths.go::exactCase": "reads a named path, to check its case on disk",
}

// walk is a call that reads a directory, in a declaration of a file.
type walk struct {
	symbol string // path::name of the declaration that holds the call
	call   string // such as os.ReadDir
	pos    token.Position
}

// walksIn returns the calls that read a directory in each declaration of a file, rel its path in the repo. A
// package-level variable counts as a declaration, by its name.
func walksIn(fset *token.FileSet, file *ast.File, rel string) []walk {
	calls := map[string][]string{"os": {"ReadDir"}, "filepath": {"WalkDir", "Walk", "Glob"}, "fs": {"WalkDir"}}
	var found []walk
	inspect := func(symbol string, node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && slices.Contains(calls[pkg.Name], sel.Sel.Name) {
					found = append(found, walk{rel + "::" + symbol, pkg.Name + "." + sel.Sel.Name, fset.Position(sel.Pos())})
				}
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			inspect(d.Name.Name, d)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if v, ok := spec.(*ast.ValueSpec); ok && len(v.Names) > 0 {
					inspect(v.Names[0].Name, v)
				}
			}
		}
	}
	return found
}

// TestNoWalkWithoutTree keeps every walk of the memory repo on Tree, which skips the ignored paths. A walk
// outside the allowed list fails the test.
func TestNoWalkWithoutTree(t *testing.T) {
	t.Run("a function and a package-level variable", func(t *testing.T) {
		src := "package x\n\nfunc a() { os.ReadDir(\".\") }\n\nvar scan = func(repo string) { filepath.WalkDir(repo, nil) }\n"
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "x.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, w := range walksIn(fset, file, "x.go") {
			got = append(got, w.symbol+" "+w.call)
		}
		if want := []string{"x.go::a os.ReadDir", "x.go::scan filepath.WalkDir"}; !slices.Equal(got, want) {
			t.Errorf("walks = %q, want %q", got, want)
		}
	})

	root := filepath.Join("..", "..")
	used := map[string]bool{}
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			for _, w := range walksIn(fset, file, rel) {
				switch {
				case walkAllowed[rel] != "":
					used[rel] = true
				case walkAllowed[w.symbol] != "":
					used[w.symbol] = true
				default:
					t.Errorf("%s: %s in %s walks without the reader: use store.Tree", w.pos, w.call, w.symbol)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for entry := range walkAllowed {
		if !used[entry] {
			t.Errorf("the allowed list names %s, which walks no directory: remove the entry", entry)
		}
	}
}
