package commands

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	t.Run("MEMCORE_REPO", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "")
		home := newHome(t)
		remote := bareRemote(t)
		repo := filepath.Join(t.TempDir(), "scratch", "memory")
		t.Setenv("MEMCORE_REPO", repo)
		if out, err := run(t, home, "", "init", remote); err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		remember(t, mkdirT(t, filepath.Join(home, "dev")), "small-commits", "Read before you commit: one change per commit.")
		if _, err := os.Stat(filepath.Join(repo, "dev", "memories", "small-commits.md")); err != nil {
			t.Errorf("the memory is not in MEMCORE_REPO: %v", err)
		}
		if _, err := os.Stat(filepath.Join(home, "memory")); err == nil {
			t.Error("~/memory exists, want it absent")
		}
		out, err := run(t, home, "", "orient")
		if err != nil || !strings.Contains(out, "in "+repo+".") {
			t.Errorf("orient: %v, output %q, want it to name %s", err, out, repo)
		}
	})

	t.Run("relative MEMCORE_REPO", func(t *testing.T) {
		home := newHome(t)
		t.Setenv("MEMCORE_REPO", "memory")
		if _, err := run(t, home, "", "init", bareRemote(t)); err == nil || !strings.Contains(err.Error(), "absolute") {
			t.Errorf("error = %v, want a refusal of a relative path", err)
		}
	})

	t.Run("empty remote", func(t *testing.T) {
		_, repo, remote := newMemory(t)
		if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s")); got != "init: create the memory repo" {
			t.Errorf("history = %q", got)
		}
		if got := strings.TrimSpace(gitT(t, repo, "remote", "get-url", "origin")); got != remote {
			t.Errorf("origin = %q, want %q", got, remote)
		}
		if got := strings.TrimSpace(gitT(t, repo, "branch", "--show-current")); got != "main" {
			t.Errorf("branch = %q, want main", got)
		}
		voice := readT(t, filepath.Join(repo, "skills", "voice", "SKILL.md"))
		if !strings.Contains(voice, "name: voice") {
			t.Errorf("voice skill = %q", voice)
		}
		index := readT(t, filepath.Join(repo, "README.md"))
		if !strings.HasPrefix(index, "# ~\n\n## Skills\n\n- [voice](skills/voice/SKILL.md): ") {
			t.Errorf("root index = %q", index)
		}
		if got := strings.TrimSpace(gitT(t, repo, "status", "--porcelain")); got != "" {
			t.Errorf("status = %q, want clean", got)
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "memcore")); err != nil {
			t.Errorf("local state: %v", err)
		}
	})

	t.Run("second run", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		head := gitT(t, repo, "rev-parse", "HEAD")
		out, err := run(t, home, "", "init", remote)
		if err != nil {
			t.Fatalf("second init: %v\n%s", err, out)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != head {
			t.Errorf("HEAD moved from %s to %s", head, got)
		}
		if got := strings.TrimSpace(gitT(t, repo, "status", "--porcelain")); got != "" {
			t.Errorf("status = %q, want clean", got)
		}
		if !strings.Contains(out, "already") {
			t.Errorf("output = %q, want one that says the repo is already set up", out)
		}
	})

	t.Run("remote that holds a memory repo", func(t *testing.T) {
		_, first, remote := newMemory(t)
		gitT(t, first, "push", "-q", "origin", "main")

		home := newHome(t)
		out, err := run(t, home, "", "init", remote)
		if err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		repo := filepath.Join(home, "memory")
		if got, want := gitT(t, repo, "rev-parse", "HEAD"), gitT(t, first, "rev-parse", "HEAD"); got != want {
			t.Errorf("HEAD = %s, want the pushed %s", got, want)
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "memcore")); err != nil {
			t.Errorf("local state: %v", err)
		}
		if out, err := run(t, home, "", "init", remote); err != nil || !strings.Contains(out, "already") {
			t.Errorf("init after the clone: %v\n%s", err, out)
		}
	})

	t.Run("remote whose HEAD names master", func(t *testing.T) {
		_, first, remote := newMemory(t)
		gitT(t, first, "push", "-q", "origin", "main")
		gitT(t, remote, "symbolic-ref", "HEAD", "refs/heads/master")

		home := newHome(t)
		if out, err := run(t, home, "", "init", remote); err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		repo := filepath.Join(home, "memory")
		if got := strings.TrimSpace(gitT(t, repo, "branch", "--show-current")); got != "main" {
			t.Errorf("branch = %q, want main", got)
		}
		if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
			t.Errorf("root index: %v", err)
		}
	})

	t.Run("failed clone", func(t *testing.T) {
		remote := bareRemote(t)
		other := t.TempDir()
		gitT(t, other, "init", "-q", "-b", "master")
		gitT(t, other, "commit", "-q", "--allow-empty", "-m", "first")
		gitT(t, other, "push", "-q", remote, "master")

		home := newHome(t)
		if out, err := run(t, home, "", "init", remote); err == nil {
			t.Fatalf("init of a remote without main: no error\n%s", out)
		}
		entries, err := os.ReadDir(home)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("home holds %v after a failed init, want nothing", entries)
		}
	})

	t.Run("concurrent runs", func(t *testing.T) {
		home := newHome(t)
		remote := bareRemote(t)
		errs := make(chan error, 2)
		for range 2 {
			go func() {
				var out strings.Builder
				errs <- runInit(Env{Dir: home, Out: &out, Err: &out}, []string{remote})
			}()
		}
		for range 2 {
			if err := <-errs; err != nil {
				t.Errorf("init: %v", err)
			}
		}
		repo := filepath.Join(home, "memory")
		if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s")); got != "init: create the memory repo" {
			t.Errorf("history = %q, want one commit", got)
		}
	})

	t.Run("a commit by hand after init", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		writeT(t, repo, "plans/first.md", "---\nname: first\ndescription: \"Read when you plan.\"\n---\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "test: a plan by hand, so the root index is behind")
		head := gitT(t, repo, "rev-parse", "HEAD")
		out, err := run(t, home, "", "init", remote)
		if err != nil || !strings.Contains(out, "already") {
			t.Fatalf("init after a commit by hand: %v, output %q, want one that says the repo is already set up", err, out)
		}
		if got := gitT(t, repo, "rev-parse", "HEAD"); got != head {
			t.Errorf("HEAD moved from %s to %s", head, got)
		}
	})

	t.Run("foreign git repo", func(t *testing.T) {
		home := newHome(t)
		repo := mkdirT(t, filepath.Join(home, "memory"))
		gitT(t, repo, "init", "-q", "-b", "main")
		writeT(t, repo, "README.md", "# ~\n")
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", "Describe my notes")
		out, err := run(t, home, "", "init", bareRemote(t))
		if err == nil || !strings.Contains(err.Error(), "not a memory repo") {
			t.Fatalf("error = %v, want a refusal\n%s", err, out)
		}
		if got := strings.TrimSpace(gitT(t, repo, "log", "--format=%s")); got != "Describe my notes" {
			t.Errorf("history = %q, want it untouched", got)
		}
	})

	t.Run("foreign directory", func(t *testing.T) {
		home := newHome(t)
		writeT(t, home, "memory/README.md", "# My notes\n")
		out, err := run(t, home, "", "init", bareRemote(t))
		if err == nil || !strings.Contains(err.Error(), "not a memory repo") {
			t.Fatalf("error = %v, want a refusal\n%s", err, out)
		}
		if got := readT(t, filepath.Join(home, "memory", "README.md")); got != "# My notes\n" {
			t.Errorf("README.md = %q, want it untouched", got)
		}
		if _, err := os.Stat(filepath.Join(home, "memory", ".git")); err == nil {
			t.Error("init created .git in a foreign directory")
		}
	})
}

// fakeMemcore puts an executable named memcore first on PATH, and returns its path.
func fakeMemcore(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	writeT(t, bin, "memcore", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(bin, "memcore"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filepath.Join(bin, "memcore")
}

// claudeHome makes a home with a Claude Code folder: settings, and one project with one memory.
func claudeHome(t *testing.T) (home, settings string) {
	t.Helper()
	home = newHome(t)
	settings = filepath.Join(home, ".claude", "settings.json")
	writeT(t, filepath.Dir(settings), "settings.json", "{\n  \"theme\": \"dark\"\n}\n")
	shop := mkdirT(t, filepath.Join(home, "dev", "shop"))
	autoMemory(t, filepath.Join(home, ".claude"), shop, "fact.md", flat("shop-fact", "Read when you test the shop.", "user", "A fact."))
	return home, settings
}

func TestInitClaude(t *testing.T) {
	t.Run("a home without Claude Code", func(t *testing.T) {
		home := newHome(t)
		out, err := run(t, home, "", "init", bareRemote(t))
		if err != nil || !strings.Contains(out, "No Claude Code folder") {
			t.Errorf("init: %v\n%s", err, out)
		}
	})

	t.Run("--dry-run without MEMCORE_REPO", func(t *testing.T) {
		home, _ := claudeHome(t)
		if _, err := run(t, home, "", "init", "--dry-run", bareRemote(t)); err == nil || !strings.Contains(err.Error(), "MEMCORE_REPO") {
			t.Errorf("error = %v, want a refusal that names MEMCORE_REPO", err)
		}
	})

	t.Run("--dry-run changes no setting and prints the diff", func(t *testing.T) {
		home, settings := claudeHome(t)
		exe := fakeMemcore(t)
		repo := filepath.Join(t.TempDir(), "memory")
		t.Setenv("MEMCORE_REPO", repo)
		before := readT(t, settings)
		out, err := runWith(t, Env{Dir: home, Confirm: func(string) (bool, error) { t.Error("--dry-run asked"); return false, nil }}, "init", "--dry-run", bareRemote(t))
		if err != nil {
			t.Fatalf("init --dry-run: %v\n%s", err, out)
		}
		if got := readT(t, settings); got != before {
			t.Errorf("settings =\n%s\nwant them byte for byte", got)
		}
		if !strings.Contains(out, "+") || !strings.Contains(out, "'"+exe+"' hook session-start") {
			t.Errorf("output =\n%s\nwant the diff of the settings", out)
		}
		if _, err := os.Stat(filepath.Join(repo, "dev", "shop", "memories", "shop-fact.md")); err != nil {
			t.Errorf("the dry run did not import into MEMCORE_REPO: %v", err)
		}
	})

	t.Run("memcore missing from PATH stops before the settings", func(t *testing.T) {
		home, settings := claudeHome(t)
		git, err := exec.LookPath("git")
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", filepath.Dir(git))
		before := readT(t, settings)
		if _, err := runWith(t, Env{Dir: home, Confirm: answer(true, new(string))}, "init", bareRemote(t)); err == nil || !strings.Contains(err.Error(), "PATH") {
			t.Errorf("error = %v, want one that names PATH", err)
		}
		if got := readT(t, settings); got != before {
			t.Error("init changed the settings without memcore on PATH")
		}
	})

	t.Run("an agent may only rehearse", func(t *testing.T) {
		home, _ := claudeHome(t)
		fakeMemcore(t)
		t.Setenv("CLAUDECODE", "1")
		if _, err := run(t, home, "", "init", bareRemote(t)); err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
			t.Errorf("init by an agent: error = %v, want a refusal", err)
		}
		if _, err := os.Stat(filepath.Join(home, "memory")); err == nil {
			t.Error("the refused init made the memory repo")
		}
		if _, err := run(t, home, "", "init", "--undo"); err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
			t.Errorf("init --undo by an agent: error = %v, want a refusal", err)
		}
		t.Setenv("MEMCORE_REPO", filepath.Join(t.TempDir(), "memory"))
		if out, err := run(t, home, "", "init", "--dry-run", bareRemote(t)); err != nil {
			t.Errorf("init --dry-run by an agent: %v\n%s", err, out)
		}
	})

	t.Run("a fresh init denies drop and recover, and not push", func(t *testing.T) {
		home, settings := claudeHome(t)
		fakeMemcore(t)
		if out, err := runWith(t, Env{Dir: home, Confirm: answer(true, new(string))}, "init", bareRemote(t)); err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		got := readT(t, settings)
		for _, want := range []string{"Bash(memcore drop *)", "Bash(memcore recover *)"} {
			if !strings.Contains(got, want) {
				t.Errorf("settings =\n%s\nwant %q", got, want)
			}
		}
		if strings.Contains(got, "memcore push") {
			t.Errorf("settings =\n%s\nwant no rule for push", got)
		}
	})

	t.Run("init ends with the report of inbox", func(t *testing.T) {
		home, settings := claudeHome(t)
		fakeMemcore(t)
		out, err := runWith(t, Env{Dir: home, Confirm: answer(true, new(string))}, "init", bareRemote(t))
		if err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		if !strings.Contains(out, "The review queue holds") || !strings.Contains(out, "init: import the auto-memory of ~/dev/shop") {
			t.Errorf("output =\n%s\nwant the report of inbox", out)
		}
		if got := readT(t, settings); !strings.Contains(got, `"autoMemoryEnabled": false`) {
			t.Errorf("settings =\n%s\nwant auto-memory off after a complete import", got)
		}
	})

	t.Run("--dry-run links no script of the remote", func(t *testing.T) {
		home, repo, remote := newMemory(t)
		dev := mkdirT(t, filepath.Join(home, "dev"))
		if out, err := run(t, dev, "", "draft", "script", "hello", "--description", "Run when you greet."); err != nil {
			t.Fatalf("draft: %v\n%s", err, out)
		}
		gitT(t, repo, "push", "-q", "origin", "main")
		writeT(t, filepath.Join(home, ".claude"), "settings.json", "{}\n")
		fakeMemcore(t)
		t.Setenv("MEMCORE_REPO", filepath.Join(t.TempDir(), "memory"))
		if out, err := run(t, home, "", "init", "--dry-run", remote); err != nil {
			t.Fatalf("init --dry-run: %v\n%s", err, out)
		}
		if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "hello")); err == nil {
			t.Error("the dry run linked a script")
		}
	})
}

func TestOnboard(t *testing.T) {
	home := newHome(t)
	remote := bareRemote(t)
	repo := filepath.Join(home, "memory")
	claude := filepath.Join(home, ".claude")
	settings := filepath.Join(claude, "settings.json")
	exe := fakeMemcore(t)
	yours := `{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "echo hello"
          }
        ]
      }
    ]
  }
}
`
	writeT(t, claude, "settings.json", yours)
	shop := mkdirT(t, filepath.Join(home, "dev", "shop"))
	web := mkdirT(t, filepath.Join(home, "dev", "web"))
	autoMemory(t, claude, shop, "deploy_day.md", nested("deploy_day", "Read before you deploy the shop.", "project", "s1", "Fridays. See [[ci_rules]]."))
	shopCI := autoMemory(t, claude, shop, "ci_rules.md", flat("ci-rules", "Read before you change CI.", "feedback", "Keep CI green."))
	autoMemory(t, claude, web, "deploy.md", flat("deploy-day", "Read before you deploy the web.", "project", "Mondays."))
	autoMemory(t, claude, web, "leaky.md", flat("leaky", "Read when you test.", "user", "token ghp_"+strings.Repeat("a1B2", 9)))
	autoMemory(t, claude, filepath.Join(home, "gone"), "a.md", flat("gone-fact", "Read when you test.", "user", "Gone."))
	writeT(t, filepath.Join(claude, "projects", projectFolder(shop)), "s1.jsonl", said("s1", shop, "Use small commits."))
	writeT(t, filepath.Join(claude, "projects", projectFolder(shop)), "s1/subagents/agent-a1.jsonl",
		`{"type":"user","timestamp":"t","isSidechain":true,"sessionId":"s1","agentId":"a1","cwd":"`+shop+`","message":{"role":"user","content":"Find it."}}`+"\n")

	// initRun runs init with an answer for each kind of question, and returns the output and the questions.
	initRun := func(t *testing.T, exclusions, change bool) (string, []string) {
		t.Helper()
		var asked []string
		confirm := func(q string) (bool, error) {
			asked = append(asked, q)
			if strings.Contains(q, "Leave them out") {
				return exclusions, nil
			}
			return change, nil
		}
		out, err := runWith(t, Env{Dir: home, Confirm: confirm}, "init", remote)
		if err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		return out, asked
	}
	settingsOf := func(t *testing.T) map[string]any {
		t.Helper()
		var v map[string]any
		if err := json.Unmarshal([]byte(readT(t, settings)), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	t.Run("1: a no to the exclusions keeps auto-memory on", func(t *testing.T) {
		out, asked := initRun(t, false, true)
		if len(asked) != 2 {
			t.Errorf("asked %q, want the exclusions, then the settings", asked)
		}
		got := readT(t, settings)
		for _, want := range []string{"echo hello", "'" + exe + "' hook session-start", "'" + exe + "' hook prompt", "'" + exe + "' hook session-end", "'" + exe + "' hook tool", "Edit(~/memory/**)", "Bash(memcore drop *)"} {
			if !strings.Contains(got, want) {
				t.Errorf("settings =\n%s\nwant %q", got, want)
			}
		}
		if n := strings.Count(got, "'"+exe+"' hook "); n != 5 {
			t.Errorf("settings =\n%s\nwant five hooks of memcore, not %d", got, n)
		}
		if _, ok := settingsOf(t)["autoMemoryEnabled"]; ok {
			t.Error("autoMemoryEnabled is set while files stay out")
		}
		for _, want := range []string{"+++ ~/.claude/settings.json, after the change", `+            "command": "'` + exe + `' hook session-start"`, `+      "Bash(memcore drop *)"`, `+      "Edit(~/memory/**)"`} {
			if !strings.Contains(out, want) {
				t.Errorf("output =\n%s\nwant the diff line %q", out, want)
			}
		}
		if !strings.Contains(out, "Auto-memory stays on") || !strings.Contains(out, "leaky.md") || !strings.Contains(out, "names no directory") {
			t.Errorf("output =\n%s\nwant the report of the files left out", out)
		}
		for _, f := range []string{"dev/shop/memories/deploy-day.md", "dev/shop/memories/ci-rules.md", "dev/web/memories/deploy-day-web.md"} {
			if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
		if got := readT(t, filepath.Join(repo, "dev/shop/memories/deploy-day.md")); !strings.Contains(got, "[[ci-rules]]") || !strings.Contains(got, "claude:s1") {
			t.Errorf("deploy-day =\n%s", got)
		}
		log := gitT(t, repo, "log", "--format=%s")
		if !strings.Contains(log, "init: import the auto-memory of ~/dev/shop\n") || !strings.Contains(log, "init: import the auto-memory of ~/dev/web\n") {
			t.Errorf("log =\n%s\nwant one import per project", log)
		}
		files := gitT(t, repo, "ls-tree", "-r", "--name-only", "transcripts")
		if !strings.Contains(files, "s1.jsonl") || !strings.Contains(files, "subagents/agent-a1.jsonl") {
			t.Errorf("transcripts =\n%s", files)
		}
	})

	t.Run("2: a changed source keeps auto-memory on", func(t *testing.T) {
		writeT(t, filepath.Dir(shopCI), "ci_rules.md", flat("ci-rules", "Read before you change CI.", "feedback", "Keep CI fast and green."))
		out, _ := initRun(t, false, true)
		if _, ok := settingsOf(t)["autoMemoryEnabled"]; ok {
			t.Error("autoMemoryEnabled is set while a source changed")
		}
		if !strings.Contains(out, "ci_rules.md: it changed since its import") {
			t.Errorf("output =\n%s\nwant the changed file", out)
		}
	})

	t.Run("3: a yes to both turns auto-memory off", func(t *testing.T) {
		out, asked := initRun(t, true, true)
		if len(asked) != 2 {
			t.Errorf("asked %q, want two questions", asked)
		}
		if settingsOf(t)["autoMemoryEnabled"] != false {
			t.Errorf("settings =\n%s\nwant auto-memory off", readT(t, settings))
		}
		if strings.Contains(out, "Auto-memory stays on") {
			t.Errorf("output =\n%s\nwant no blocker", out)
		}
		if !strings.Contains(out, `+  "autoMemoryEnabled": false`) {
			t.Errorf("output =\n%s\nwant the diff of the setting", out)
		}
	})

	t.Run("4: a fourth run finds nothing new", func(t *testing.T) {
		head := gitT(t, repo, "rev-parse", "HEAD")
		transcripts := gitT(t, repo, "rev-parse", "transcripts")
		before := readT(t, settings)
		_, asked := initRun(t, true, true)
		if len(asked) != 0 {
			t.Errorf("asked %q, want nothing", asked)
		}
		if gitT(t, repo, "rev-parse", "HEAD") != head || gitT(t, repo, "rev-parse", "transcripts") != transcripts || readT(t, settings) != before {
			t.Error("the fourth run changed something")
		}
	})

	t.Run("5: --undo keeps your settings", func(t *testing.T) {
		replaceT(t, settings, `"theme": "dark",`, `"theme": "dark",`+"\n  \"model\": \"opus\",")
		out, err := runWith(t, Env{Dir: home, Confirm: answer(true, new(string))}, "init", "--undo")
		if err != nil {
			t.Fatalf("init --undo: %v\n%s", err, out)
		}
		got := readT(t, settings)
		if !strings.Contains(got, `"model": "opus"`) || !strings.Contains(got, "echo hello") {
			t.Errorf("settings =\n%s\nwant your setting and your hook", got)
		}
		for _, want := range []string{`-            "command": "'` + exe + `' hook session-end"`, `-            "command": "'` + exe + `' hook tool"`, `-        "matcher": "Bash|Read|Grep",`, `-      "Bash(memcore recover *)"`, `-  "autoMemoryEnabled": false`} {
			if !strings.Contains(out, want) {
				t.Errorf("output =\n%s\nwant the diff line %q", out, want)
			}
		}
		if strings.Contains(got, "memcore") || strings.Contains(got, "autoMemoryEnabled") {
			t.Errorf("settings =\n%s\nwant nothing of init", got)
		}
	})
}
