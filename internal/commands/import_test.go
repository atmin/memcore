package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

// projectFolder names a directory the way Claude Code names its project folder.
func projectFolder(dir string) string {
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

// autoMemory writes one file of auto-memory for the project of dir, under the Claude Code folder.
func autoMemory(t *testing.T, claude, dir, file, content string) string {
	t.Helper()
	p := filepath.Join(claude, "projects", projectFolder(dir), "memory", file)
	writeT(t, filepath.Dir(p), filepath.Base(p), content)
	return p
}

// flat and nested write a file of auto-memory in the flat form and in the metadata: form.
func flat(name, description, typ, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\ntype: " + typ + "\n---\n\n" + body + "\n"
}

func nested(name, description, typ, session, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\nmetadata:\n  node_type: memory\n  type: " + typ +
		"\n  originSessionId: " + session + "\n---\n\n" + body + "\n"
}

// importNow runs the import and fails the test on an error.
func importNow(t *testing.T, home, repo, claude string) (bool, string) {
	t.Helper()
	var out strings.Builder
	complete, err := importAutoMemory(Env{Dir: home, Out: &out, Err: &out}, repo, claude)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	return complete, out.String()
}

func readRecord(t *testing.T, repo string) importRecord {
	t.Helper()
	var rec importRecord
	b, err := os.ReadFile(filepath.Join(store.StateDir(repo), "import.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestImport(t *testing.T) {
	home, repo, _ := newMemory(t)
	claude := filepath.Join(home, ".claude")
	shop := mkdirT(t, filepath.Join(home, "dev", "shop"))
	// The projects import in the order of their folder names: ~/---, then ~/dev/shop, then ~/dev/the App.
	app := mkdirT(t, filepath.Join(home, "dev", "the App"))
	dashes := mkdirT(t, filepath.Join(home, "---"))
	commitByHand(t, repo, map[string]string{"dev/glossary.md": "# Glossary\n\n- **memory**: one fact. Not: memo.\n"})
	remember(t, home, "care-rule", "Read before you change anything with care.")

	// The shop holds both forms, links by name and by file name, a cycle, and the files that stay out.
	autoMemory(t, claude, shop, "deploy_day.md", nested("deploy_day", "Read before you deploy.", "project", "s1", "Deploy on Fridays. See [[review_rules]] and [[ghost]]."))
	autoMemory(t, claude, shop, "review_rules.md", flat("review-rules", "Read before you review.", "feedback", "Review small diffs. See [[deploy_day]] and [[skipped]]."))
	autoMemory(t, claude, shop, "skipped.md", flat("skipped", "Read when you test.", "user", "token ghp_"+strings.Repeat("a1B2", 9)))
	autoMemory(t, claude, shop, "broken.md", "No frontmatter.\n")
	autoMemory(t, claude, shop, "spec_link.md", flat("spec-link", "Read when you read the spec.", "reference", "See [the spec](../notes/spec.md)."))
	autoMemory(t, claude, shop, "memo.md", flat("memo-word", "Read when you write.", "feedback", "Write a memo."))
	autoMemory(t, claude, shop, "MEMORY.md", "- [Deploy](deploy_day.md)\n")
	// The app holds the name of a memory of the shop. ~/--- holds the name of a memory in the repo.
	autoMemory(t, claude, app, "deploy.md", flat("deploy-day", "Read before you deploy the app.", "project", "Deploy the app on Mondays. See [[deploy-day]]."))
	autoMemory(t, claude, dashes, "care.md", flat("care_rule", "Read when you take care.", "project", "Take care."))
	// These folders resolve to no directory, to two, or to one outside home.
	autoMemory(t, claude, filepath.Join(home, "gone"), "a.md", flat("gone-fact", "Read when you test.", "user", "Gone."))
	mkdirT(t, filepath.Join(home, "x-y"))
	mkdirT(t, filepath.Join(home, "x", "y"))
	autoMemory(t, claude, filepath.Join(home, "x-y"), "a.md", flat("twin-fact", "Read when you test.", "user", "Twin."))
	outside := t.TempDir()
	autoMemory(t, claude, outside, "a.md", flat("outside-fact", "Read when you test.", "user", "Outside."))

	complete, out := importNow(t, home, repo, claude)
	if complete {
		t.Errorf("complete = true, want false while files stay out\n%s", out)
	}

	t.Run("both forms import at the level of their project", func(t *testing.T) {
		got := readT(t, filepath.Join(repo, "dev", "shop", "memories", "deploy-day.md"))
		for _, want := range []string{"name: deploy-day", "type: project", "claude:s1", "[[review-rules]]"} {
			if !strings.Contains(got, want) {
				t.Errorf("deploy-day =\n%s\nwant %q", got, want)
			}
		}
		if !strings.Contains(got, "and ghost.") {
			t.Errorf("deploy-day =\n%s\nwant [[ghost]] without brackets", got)
		}
		rules := readT(t, filepath.Join(repo, "dev", "shop", "memories", "review-rules.md"))
		if !strings.Contains(rules, "[[deploy-day]]") || !strings.Contains(rules, "and skipped.") {
			t.Errorf("review-rules =\n%s\nwant the cycle kept and the link to a file left out without brackets", rules)
		}
		if !strings.Contains(rules, "sessions: []") {
			t.Errorf("review-rules =\n%s\nwant no session", rules)
		}
	})

	t.Run("one commit per project", func(t *testing.T) {
		log := gitT(t, repo, "log", "--format=%s")
		for _, dir := range []string{"~/dev/shop", "~/dev/the App", "~/---"} {
			if strings.Count(log, "init: import the auto-memory of "+dir+"\n") != 1 {
				t.Errorf("log =\n%s\nwant one import of %s", log, dir)
			}
		}
		body := gitT(t, repo, "log", "-1", "--format=%b", "--grep=of ~/dev/shop$")
		for _, want := range []string{"skipped.md", "secret", "broken.md", "spec_link.md", "memo.md", "ghost", "Import: "} {
			if !strings.Contains(body, want) {
				t.Errorf("body =\n%s\nwant %q", body, want)
			}
		}
	})

	t.Run("a name in two projects gives distinct names", func(t *testing.T) {
		got := readT(t, filepath.Join(repo, "dev", "the App", "memories", "deploy-day-the-app.md"))
		if !strings.Contains(got, "name: deploy-day-the-app") || !strings.Contains(got, "[[deploy-day-the-app]]") {
			t.Errorf("the copy of the app =\n%s\nwant its own name, and its link to itself", got)
		}
		if _, err := os.Stat(filepath.Join(repo, store.Folder("---"), "memories", "care-rule-2.md")); err != nil {
			t.Errorf("the copy of ~/--- is missing: %v", err)
		}
	})

	t.Run("the files left out stay on disk", func(t *testing.T) {
		for _, f := range []string{"skipped.md", "broken.md", "spec_link.md", "memo.md"} {
			if _, err := os.Stat(filepath.Join(claude, "projects", projectFolder(shop), "memory", f)); err != nil {
				t.Errorf("%s: %v", f, err)
			}
			if _, err := os.Stat(filepath.Join(repo, "dev", "shop", "memories", strings.TrimSuffix(f, ".md")+".md")); err == nil {
				t.Errorf("%s was imported", f)
			}
		}
		rec := readRecord(t, repo)
		var reasons []string
		for _, f := range rec.Files {
			reasons = append(reasons, f.Reason)
		}
		all := strings.Join(reasons, "\n")
		for _, want := range []string{"secret", "names no directory", "names 2 directories", "outside home", "dead link", "forbidden word"} {
			if !strings.Contains(all, want) {
				t.Errorf("reasons =\n%s\nwant %q", all, want)
			}
		}
	})

	t.Run("a second run makes no commit", func(t *testing.T) {
		head := gitT(t, repo, "rev-parse", "HEAD")
		importNow(t, home, repo, claude)
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != head {
			t.Error("the second run made a commit")
		}
	})

	t.Run("a file left out imports once its cause is gone", func(t *testing.T) {
		commitByHand(t, repo, map[string]string{"dev/shop/notes/spec.md": "---\nname: spec\ndescription: \"Read when you read the spec.\"\n---\n\nThe spec.\n"})
		importNow(t, home, repo, claude)
		if _, err := os.Stat(filepath.Join(repo, "dev", "shop", "memories", "spec-link.md")); err != nil {
			t.Errorf("spec-link was not imported: %v", err)
		}
	})

	t.Run("a pending project after its commit and a revision", func(t *testing.T) {
		later := mkdirT(t, filepath.Join(home, "dev", "later"))
		autoMemory(t, claude, later, "fact.md", flat("later-fact", "Read when you test later.", "user", "Later."))
		importNow(t, home, repo, claude)
		token := strings.TrimSpace(gitT(t, repo, "log", "-1", "--format=%(trailers:key=Import,valueonly)", "--grep=of ~/dev/later$"))
		if token == "" {
			t.Fatal("the import of ~/dev/later has no token")
		}
		// The process stopped after the commit, before it marked the project done.
		rec := readRecord(t, repo)
		key := filepath.Join(claude, "projects", projectFolder(later), "memory", "fact.md")
		rec.Pending = map[string]pendingProject{projectFolder(later): {Token: token, Files: map[string]importedFile{key: rec.Files[key]}}}
		delete(rec.Files, key)
		b, _ := json.Marshal(rec)
		writeT(t, store.StateDir(repo), "import.json", string(b))
		if out, err := run(t, later, memoryText("later-fact", "Read when you test later.", "Later, revised."), "revise", "later-fact", "--reason", "A test."); err != nil {
			t.Fatalf("revise: %v\n%s", err, out)
		}
		head := gitT(t, repo, "rev-parse", "HEAD")
		importNow(t, home, repo, claude)
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != head {
			t.Error("the rerun imported the pending project again")
		}
		if rec := readRecord(t, repo); rec.Files[key].Path == "" || len(rec.Pending) != 0 {
			t.Errorf("record = %+v, want the project done", rec)
		}
	})

	t.Run("a changed file is not complete", func(t *testing.T) {
		autoMemory(t, claude, shop, "review_rules.md", flat("review-rules", "Read before you review.", "feedback", "Review tiny diffs."))
		complete, out := importNow(t, home, repo, claude)
		if complete || !strings.Contains(out, "review_rules.md: it changed since its import") {
			t.Errorf("complete = %v, output =\n%s\nwant the changed file named", complete, out)
		}
	})
}

func TestImportSafety(t *testing.T) {
	home, repo, _ := newMemory(t)
	claude := filepath.Join(home, ".claude")
	remember(t, home, "shared", "Read when you share a fact across projects.")
	// The tokens are built from parts, so no scanner takes this file for a leak.
	token := "ghp_" + strings.Repeat("a1B2", 9)

	p := mkdirT(t, filepath.Join(home, "dev", "p"))
	autoMemory(t, claude, p, "typed.md", "---\nname: typed\ndescription: Read when you test.\ntype: "+token+"\n---\nBody.\n")
	autoMemory(t, claude, p, "escaped.md", "---\nname: escaped\ndescription: \"Read when you send Bearer\\u0020Abcd"+"1234efgh.\"\ntype: user\n---\nBody.\n")
	autoMemory(t, claude, p, "a.md", flat("alpha_name", "Read when you test alpha.", "user", "Alpha."))
	autoMemory(t, claude, p, "b.md", flat("beta", "Read when you test beta.", "user", "See [[alpha_name]] and [the note](../notes/x.md)."))
	autoMemory(t, claude, p, "shared.md", flat("shared", "Read when you test shared.", "user", "token "+token))
	autoMemory(t, claude, p, "c.md", flat("gamma", "Read when you test gamma.", "user", "See [[shared]]."))
	importNow(t, home, repo, claude)

	t.Run("no secret reaches a commit", func(t *testing.T) {
		all := gitT(t, repo, "log", "-p", "--format=%B")
		for _, s := range []string{token, "1234efgh"} {
			if strings.Contains(all, s) {
				t.Errorf("the history holds %q", s)
			}
		}
		for _, f := range []string{"typed", "escaped"} {
			if _, err := os.Stat(filepath.Join(repo, "dev", "p", "memories", f+".md")); err == nil {
				t.Errorf("%s was imported", f)
			}
		}
	})

	t.Run("a link to a file left out does not reach another memory", func(t *testing.T) {
		got := readT(t, filepath.Join(repo, "dev", "p", "memories", "gamma.md"))
		if strings.Contains(got, "[[shared]]") || !strings.Contains(got, "See shared.") {
			t.Errorf("gamma =\n%s\nwant the link to the file left out without brackets", got)
		}
	})

	t.Run("a retry keeps the links by the original name", func(t *testing.T) {
		commitByHand(t, repo, map[string]string{"dev/p/notes/x.md": "---\nname: x\ndescription: \"Read when you test x.\"\n---\n\nX.\n"})
		importNow(t, home, repo, claude)
		got := readT(t, filepath.Join(repo, "dev", "p", "memories", "beta.md"))
		if !strings.Contains(got, "[[alpha-name]]") {
			t.Errorf("beta =\n%s\nwant [[alpha-name]], by the original name of a file imported before", got)
		}
	})

	t.Run("a crash before the commit leaves nothing behind", func(t *testing.T) {
		s := mkdirT(t, filepath.Join(home, "dev", "s"))
		autoMemory(t, claude, s, "fact.md", flat("s-fact", "Read when you test s.", "user", "S."))
		// The process wrote the file and an index, then stopped before the commit.
		planned := "dev/s/memories/s-fact.md"
		writeT(t, repo, planned, "a half-written memory\n")
		writeT(t, repo, "README.md", readT(t, filepath.Join(repo, "README.md"))+"- a stray line\n")
		rec := readRecord(t, repo)
		key := filepath.Join(claude, "projects", projectFolder(s), "memory", "fact.md")
		rec.Pending = map[string]pendingProject{projectFolder(s): {Token: "0123456789abcdef", Files: map[string]importedFile{key: {Hash: "x", Path: planned}}}}
		b, _ := json.Marshal(rec)
		writeT(t, store.StateDir(repo), "import.json", string(b))

		importNow(t, home, repo, claude)
		if got := gitT(t, repo, "status", "--porcelain"); got != "" {
			t.Errorf("status = %q, want a clean tree", got)
		}
		if got := readT(t, filepath.Join(repo, planned)); !strings.Contains(got, "name: s-fact") {
			t.Errorf("s-fact =\n%s\nwant the imported memory", got)
		}
		if strings.Contains(readT(t, filepath.Join(repo, "README.md")), "stray") {
			t.Error("the stray index line stayed")
		}
	})
}

func TestImportBlockers(t *testing.T) {
	home, repo, _ := newMemory(t)
	claude := filepath.Join(home, ".claude")
	shop := mkdirT(t, filepath.Join(home, "dev", "shop"))
	autoMemory(t, claude, shop, "good.md", flat("good", "Read when you test.", "user", "Good."))
	autoMemory(t, claude, shop, "leaky.md", flat("leaky", "Read when you test.", "user", "token ghp_"+strings.Repeat("a1B2", 9)))

	if out, _ := run(t, home, "", "inbox"); strings.Contains(out, "Auto-memory") {
		t.Errorf("inbox before any import =\n%s\nwant no word of auto-memory", out)
	}
	if complete, _ := importNow(t, home, repo, claude); complete {
		t.Fatal("complete = true with a file left out")
	}
	out, err := run(t, home, "", "inbox")
	if err != nil || !strings.Contains(out, "Auto-memory stays on") || !strings.Contains(out, "leaky.md: it holds a likely secret") {
		t.Errorf("inbox: %v\n%s\nwant the file that keeps auto-memory on, with its reason", err, out)
	}

	var asked string
	if ok, err := acceptExclusions(Env{Dir: home, Out: &strings.Builder{}, Confirm: answer(false, &asked)}, repo); err != nil || ok {
		t.Errorf("acceptExclusions with no = %v, %v", ok, err)
	}
	if out, _ := run(t, home, "", "inbox"); !strings.Contains(out, "leaky.md: it holds a likely secret") {
		t.Errorf("inbox after a no =\n%s\nwant the file still listed", out)
	}

	var shown strings.Builder
	if ok, err := acceptExclusions(Env{Dir: home, Out: &shown, Confirm: answer(true, &asked)}, repo); err != nil || !ok {
		t.Errorf("acceptExclusions with yes = %v, %v", ok, err)
	}
	if !strings.Contains(shown.String(), "leaky.md") || asked == "" {
		t.Errorf("acceptExclusions showed\n%s\nand asked %q, want the file and one question", shown.String(), asked)
	}
	if out, _ := run(t, home, "", "inbox"); strings.Contains(out, "Auto-memory stays on") {
		t.Errorf("inbox after a yes =\n%s\nwant no blocker", out)
	}
	if complete, _ := importNow(t, home, repo, claude); !complete {
		t.Error("complete = false after the person accepted the file")
	}
}

func TestAcceptExclusionsRace(t *testing.T) {
	home, repo, _ := newMemory(t)
	claude := filepath.Join(home, ".claude")
	shop := mkdirT(t, filepath.Join(home, "dev", "shop"))
	leaky := autoMemory(t, claude, shop, "leaky.md", flat("leaky", "Read when you test.", "user", "token ghp_"+strings.Repeat("a1B2", 9)))
	importNow(t, home, repo, claude)
	ok, err := acceptExclusions(Env{Dir: home, Out: &strings.Builder{}, Confirm: func(string) (bool, error) {
		// Auto-memory writes while the person answers.
		writeT(t, filepath.Dir(leaky), "leaky.md", flat("leaky", "Read when you test.", "user", "token ghp_"+strings.Repeat("c3D4", 9)))
		autoMemory(t, claude, shop, "fresh.md", flat("fresh", "Read when you test.", "user", "Fresh."))
		return true, nil
	}}, repo)
	if err != nil || ok {
		t.Errorf("acceptExclusions = %v, %v, want false: the files changed after the person saw them", ok, err)
	}
	out, _ := run(t, home, "", "inbox")
	if !strings.Contains(out, "leaky.md") || !strings.Contains(out, "fresh.md") {
		t.Errorf("inbox =\n%s\nwant the changed file and the new file listed", out)
	}
}

func TestBlockersAfterTheSwitch(t *testing.T) {
	home, repo, _ := newMemory(t)
	claude := filepath.Join(home, ".claude")
	shop := mkdirT(t, filepath.Join(home, "dev", "shop"))
	src := autoMemory(t, claude, shop, "fact.md", flat("fact", "Read when you test.", "user", "A fact."))
	if complete, _ := importNow(t, home, repo, claude); !complete {
		t.Fatal("complete = false")
	}
	writeT(t, claude, "settings.json", "{\n  \"autoMemoryEnabled\": false\n}\n")
	writeT(t, filepath.Dir(src), "fact.md", flat("fact", "Read when you test.", "user", "A changed fact."))
	out, _ := run(t, home, "", "inbox")
	if strings.Contains(out, "stays on") || !strings.Contains(out, "fact.md: it changed since its import") {
		t.Errorf("inbox =\n%s\nwant the changed file, without a claim that auto-memory is on", out)
	}
}
