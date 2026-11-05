package commands

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// readsFixture builds a memory repo under a new home, with plain files and no git.
func readsFixture(t *testing.T) (home, repo string) {
	t.Helper()
	home = t.TempDir()
	repo = filepath.Join(home, "memory")
	for _, p := range []string{
		".git/memcore/log.jsonl",
		"dev/memories/x.md",
		"dev/memories/y.md",
		"dev/app/memories/a.md",
		"my café/memories/z.md",
	} {
		writeT(t, repo, p, "---\nname: x\n---\n")
	}
	writeT(t, home, "notes.md", "outside the repo\n")
	writeT(t, home, "elsewhere/dev/memories/x.md", "outside the repo\n")
	mkdirT(t, filepath.Join(home, "elsewhere", "deep"))
	if err := os.Symlink(filepath.Join(home, "elsewhere", "deep"), filepath.Join(repo, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dev", filepath.Join(repo, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../.git/memcore/log.jsonl", filepath.Join(repo, "dev", "memories", "log.md")); err != nil {
		t.Fatal(err)
	}
	return home, repo
}

func TestToolReads(t *testing.T) {
	home, repo := readsFixture(t)
	dev := filepath.Join(repo, "dev")
	const (
		x = "dev/memories/x.md"
		y = "dev/memories/y.md"
		a = "dev/app/memories/a.md"
		z = "my café/memories/z.md"
		p = "~/memory/dev/memories/x.md"
	)
	bash := func(cwd, command string) hookInput {
		return hookInput{Cwd: cwd, ToolName: "Bash", ToolInput: toolInput{Command: command}}
	}
	tests := []struct {
		name string
		in   hookInput
		want []string
	}{
		{"a path in double quotes", bash(home, `cat "$HOME/memory/dev/memories/x.md"`), []string{x}},
		{"a path after <", bash(home, "cat < "+p), []string{x}},
		{"a path right after <", bash(home, "wc -l <"+p), []string{x}},
		{"an assignment before the command word", bash(home, "f=1 cat "+p), []string{x}},
		{"a command word after do", bash(home, "for i in 1 2; do cat "+p+"; done"), []string{x}},
		{"a level with a space, in quotes", bash(home, `cat "$HOME/memory/my café/memories/z.md"`), []string{z}},
		{"a level with a space, escaped", bash(home, `cat ~/memory/my\ café/memories/z.md`), []string{z}},
		{"cd into a level", bash(home, "cd "+dev+" && sed -n 9p memories/x.md"), []string{x}},
		{"a relative path from cwd", bash(dev, "cat memories/x.md"), []string{x}},
		{"a parenthesis restores the folder", bash(dev, "(cd app && cat memories/a.md); cat memories/y.md"), []string{a, y}},
		{"one memory twice, sorted", bash(home, "cat ~/memory/dev/memories/y.md; head "+p+"; cat ~/memory/dev/memories/y.md"), []string{x, y}},
		{"Read of a memory", hookInput{Cwd: home, ToolName: "Read", ToolInput: toolInput{FilePath: filepath.Join(dev, "memories", "x.md")}}, []string{x}},
		{"Grep of a file", hookInput{Cwd: home, ToolName: "Grep", ToolInput: toolInput{Path: filepath.Join(dev, "memories", "x.md")}}, []string{x}},
		{"Grep of a folder", hookInput{Cwd: home, ToolName: "Grep", ToolInput: toolInput{Path: dev}}, nil},
		{"Grep without a path", hookInput{Cwd: dev, ToolName: "Grep"}, nil},
		{"another tool", hookInput{Cwd: home, ToolName: "Edit", ToolInput: toolInput{FilePath: filepath.Join(dev, "memories", "x.md")}}, nil},

		{"a path under .git", bash(home, "cat ~/memory/.git/memcore/log.jsonl"), nil},
		{"a path under .git in other cases", bash(home, "cat ~/memory/.GIT/memcore/log.jsonl"), nil},
		{"a folder", bash(home, "cat ~/memory/dev"), nil},
		{"a missing file", bash(home, "cat ~/memory/dev/memories/none.md"), nil},
		{"a glob", bash(home, "cat ~/memory/dev/memories/*.md"), nil},
		{"a variable", bash(home, "cat $f"), nil},
		{"a path outside the repo", bash(home, "cat ~/notes.md"), nil},
		{"~ in single quotes", bash(home, "cat '"+p+"'"), nil},
		{"$HOME in single quotes", bash(home, "cat '$HOME/memory/dev/memories/x.md'"), nil},
		{"echo", bash(home, "echo "+p), nil},
		{"memcore show", bash(repo, "memcore show "+x), nil},
		{"a destination after >", bash(home, "cat /tmp/x > "+p), nil},
		{"a destination after >>", bash(home, "cat /tmp/x >> "+p), nil},
		{"sed -i", bash(home, "sed -i s/a/b/ "+p), nil},
		{"text after <<<", bash(home, "cat <<< "+p), nil},
		{"the text of a heredoc", bash(home, "cat <<EOF\ncat "+p+"\nEOF\ncat ~/memory/dev/memories/y.md"), []string{y}},
		{"a heredoc with <<- and a quoted word", bash(home, "cat <<-'END' > /tmp/out\n\tcat "+p+"\n\tEND\nhead ~/memory/dev/memories/y.md"), []string{y}},
		{"rg -g", bash(home, "rg -g '*.md' "+p+" /tmp/log"), nil},
		{"a long option", bash(home, "grep --color=never x "+p), nil},
		{"rg -E", bash(home, "rg -E utf8 "+p+" /tmp/log"), nil},
		{"rg -r", bash(home, "rg -r x y "+p), nil},
		{"rg -h", bash(home, "rg -h "+p), nil},
		{"rg -n", bash(home, "rg -n x "+p), []string{x}},
		{"a symlinked folder in the repo", bash(home, "cat ~/memory/linked/memories/x.md"), nil},
		{"a symlink into .git", bash(home, "cat ~/memory/dev/memories/log.md"), nil},
		{"the pattern of grep -e", bash(home, "grep -F -e "+p+" /tmp/log"), nil},
		{"the pattern of grep", bash(home, "grep "+p+" /tmp/log"), nil},
		{"the file of grep -f", bash(home, "grep -f "+p+" /tmp/log"), []string{x}},
		{"the argument of grep -A", bash(home, "grep -A 1 "+p+" /tmp/log"), nil},
		{"grep --after-context", bash(home, "grep --after-context 1 "+p+" /tmp/log"), nil},
		{"grep -nA1", bash(home, "grep -nA1 x "+p), []string{x}},
		{"head -n 5", bash(home, "head -n 5 "+p), []string{x}},
		{"head -20", bash(home, "head -20 "+p), []string{x}},
		{"awk -F", bash(home, "awk -F: '{print $1}' "+p), []string{x}},
		{"sed -e", bash(home, "sed -e 1p "+p), []string{x}},
		{"awk -f", bash(home, "awk -f "+p+" /tmp/x"), []string{x}},
		{"cat --", bash(home, "cat -- "+p), []string{x}},
		{"cd next to |", bash(dev, "cd app | cat memories/x.md"), []string{x}},
		{"cd in the background", bash(dev, "cd app & cat memories/x.md"), []string{x}},
		{"a list in the background", bash(dev, "cd app && cat memories/a.md & cat memories/y.md"), []string{a, y}},
		{"both sides of ||", bash(home, "true || cat "+p), []string{x}},
		{"cd to a variable", bash(dev, "cd $D && cat memories/x.md"), nil},
		{"cd to home", bash(dev, "cd && cat memory/dev/memories/x.md"), []string{x}},
		{"no cwd", bash("", "cat memories/x.md"), nil},
		{"a comment with a quote", bash(home, "cat "+p+" # don't\ncat ~/memory/dev/memories/y.md"), []string{x, y}},
		{"a path in a comment", bash(home, "ls # cat "+p), nil},
		{"a line that continues", bash(home, "cat ~/memory/dev/memories/\\\nx.md"), []string{x}},
		{"2>&1 runs nothing in the background", bash(dev, "cd app 2>&1; cat memories/a.md"), []string{a}},
		{"a .. from cwd", bash(filepath.Join(dev, "app"), "cat ../memories/x.md"), []string{x}},
		{"a .. that leaves a symlink", bash(home, "cat ~/memory/out/../dev/memories/x.md"), nil},
		{"a / after a file", bash(home, "cat "+p+"/"), nil},
		{"a /. after a file", bash(home, "cat "+p+"/."), nil},
		{"a .. after a file", bash(home, "cat "+p+"/../y.md"), nil},
		{"a // between folders", bash(home, "cat ~/memory/dev//memories/./x.md"), []string{x}},
		{"a cd through a file and .. fails", bash(home, "cd ~/memory/dev/memories/x.md/..; cat y.md"), nil},
		{"a cd through a file and / fails", bash(home, "cd ~/memory/dev/memories/x.md/; cat ../y.md"), nil},
		{"a cd through . and //", bash(home, "cd ~/memory/dev/./memories//; cat x.md"), []string{x}},
		{"a cd through a .. that leaves a symlink", bash(repo, "cd out/.. && cat dev/memories/x.md"), []string{x}},
		{"Read through a .. that leaves a symlink", hookInput{Cwd: home, ToolName: "Read", ToolInput: toolInput{FilePath: filepath.Join(repo, "out") + "/../dev/memories/x.md"}}, nil},
		{"$HOME in a word that ends in quotes", bash(home, `cat "$HOME"/memory/dev/memories/x.md`), []string{x}},
		{"a name that a quote splits", bash(home, "cat $HOM'E'/memory/dev/memories/x.md"), nil},
		{"a name that ends a double quote", bash(home, `cat "$HOM"E/memory/dev/memories/x.md`), nil},
		{"a name that a backslash splits", bash(home, `cat $HOM\E/memory/dev/memories/x.md`), nil},
		{"a brace that a quote splits", bash(home, `cat ${HOME'}'/memory/dev/memories/x.md`), nil},
	}
	for _, cmd := range []string{"cat", "sed -n 1,20p", "head"} {
		for _, form := range []string{"~/memory/", "$HOME/memory/", "${HOME}/memory/", repo + "/"} {
			tests = append(tests, struct {
				name string
				in   hookInput
				want []string
			}{cmd + " " + form, bash(home, cmd+" "+form+x), []string{x}})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolReads(tt.in, repo, home); !slices.Equal(got, tt.want) {
				t.Errorf("toolReads(%q) = %q, want %q", tt.in.ToolInput.Command, got, tt.want)
			}
		})
	}

	t.Run("another casing logs the path as the repo spells it", func(t *testing.T) {
		// A file system that ignores case opens the document by any casing, and one that keeps case opens none.
		_, err := os.Lstat(filepath.Join(repo, "DEV"))
		ignoresCase := err == nil
		for _, c := range []struct {
			name string
			in   hookInput
			want []string
		}{
			{"the folders and the file", bash(home, "cat ~/memory/DEV/Memories/X.md"), []string{x}},
			{"the folder of the repo", bash(home, "cat ~/Memory/dev/memories/x.md"), []string{x}},
			{"a relative path after cd", bash(dev, "cd APP && cat memories/A.md"), []string{a}},
			{"Read", hookInput{Cwd: home, ToolName: "Read", ToolInput: toolInput{FilePath: filepath.Join(repo, "Dev", "memories", "x.md")}}, []string{x}},
		} {
			if !ignoresCase {
				c.want = nil
			}
			if got := toolReads(c.in, repo, home); !slices.Equal(got, c.want) {
				t.Errorf("%s: toolReads = %q, want %q", c.name, got, c.want)
			}
		}
	})

	t.Run("a memory repo outside home", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "memory")
		writeT(t, other, x, "---\nname: x\n---\n")
		in := bash(home, "cat "+filepath.Join(other, "dev", "memories", "x.md")+" "+p)
		if got := toolReads(in, other, home); !slices.Equal(got, []string{x}) {
			t.Errorf("toolReads = %q, want [%s]", got, x)
		}
	})
}
