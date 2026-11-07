package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/store"
)

func TestSettingsTree(t *testing.T) {
	// JSON.stringify(value, null, 2) writes this form, as Claude Code does.
	in := `{
  "zeta": 1.50,
  "alpha": {
    "list": [
      1e3,
      "a <b> & c",
      true,
      null,
      {}
    ],
    "empty": []
  },
  "unicode": "café ✓"
}
`
	tree, err := parseSettings([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(tree.encode()); got != in {
		t.Errorf("round trip =\n%s\nwant\n%s", got, in)
	}
	if _, err := parseSettings([]byte(`{"a": }`)); err == nil {
		t.Error("parseSettings of broken JSON succeeded")
	}
	if _, err := parseSettings([]byte(`[1]`)); err == nil {
		t.Error("parseSettings of an array succeeded, want an object")
	}
}

const exe = "/home/a/.local/bin/memcore"

// settingsOf returns the settings after the changes, as a value of encoding/json.
func settingsOf(t *testing.T, tree *jsonNode) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(tree.encode(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestClaudeChanges(t *testing.T) {
	yours := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo hi"}]}]},"permissions":{"deny":["Bash(rm *)"]}}`
	tests := []struct {
		name      string
		in        string // "" means no file
		autoOff   bool
		additions int // hooks, rules and the setting
	}{
		{"no file", "", false, 5 + 4},
		{"an empty file", "{}", true, 5 + 4 + 1},
		{"a hook and a rule of yours", yours, false, 5 + 4},
		{"a rule already there", `{"permissions":{"deny":["Bash(memcore drop *)"]}}`, false, 5 + 3},
		{"auto-memory already off", `{"autoMemoryEnabled":false}`, true, 5 + 4},
		{"a tool hook of yours with another matcher", `{"hooks":{"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"'` + exe + `' hook tool"}]}]}}`, false, 5 + 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, err := parseSettings([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			add := claudeChanges(tree, "/home/a/memory", "/home/a", exe, tt.autoOff)
			if got := len(add.Hooks) + len(add.Deny) + map[bool]int{true: 1}[add.AutoMemory != nil]; got != tt.additions {
				t.Errorf("additions = %+v, want %d", add, tt.additions)
			}
			v := settingsOf(t, tree)
			hooks := v["hooks"].(map[string]any)
			for _, ev := range []struct{ event, arg, matcher string }{
				{"SessionStart", "session-start", ""},
				{"UserPromptSubmit", "prompt", ""},
				{"SessionEnd", "session-end", ""},
				{"PostToolUse", "tool", "Bash|Read|Grep"},
				{"PostToolUseFailure", "tool", "Bash|Read|Grep"},
			} {
				groups := hooks[ev.event].([]any)
				last := groups[len(groups)-1].(map[string]any)
				if m, ok := last["matcher"]; ev.matcher == "" && ok || ev.matcher != "" && m != ev.matcher {
					t.Errorf("%s has the matcher %v, want %q", ev.event, m, ev.matcher)
				}
				h := last["hooks"].([]any)[0].(map[string]any)
				if h["type"] != "command" || h["command"] != "'"+exe+"' hook "+ev.arg {
					t.Errorf("%s hook = %v", ev.event, h)
				}
			}
			if strings.Contains(tt.in, `"matcher":"Bash"`) {
				groups := hooks["PostToolUse"].([]any)
				if len(groups) != 2 || groups[0].(map[string]any)["matcher"] != "Bash" {
					t.Errorf("PostToolUse = %v, want your group, then the group of init", groups)
				}
			}
			deny := strings.Join(func() []string {
				var s []string
				for _, r := range v["permissions"].(map[string]any)["deny"].([]any) {
					s = append(s, r.(string))
				}
				return s
			}(), "\n")
			for _, want := range []string{"Edit(~/memory/**)", "Bash(memcore drop *)", "Bash(memcore recover *)", "Bash(memcore ui *)"} {
				if strings.Count(deny, want) != 1 {
					t.Errorf("deny =\n%s\nwant %s once", deny, want)
				}
			}
			for _, cmd := range []string{"init", "push"} {
				if strings.Contains(deny, cmd) {
					t.Errorf("deny =\n%s\nwant no rule for %s", deny, cmd)
				}
			}
			if tt.in == yours && !strings.Contains(string(tree.encode()), `"echo hi"`) {
				t.Error("your hook is gone")
			}
			if tt.autoOff && v["autoMemoryEnabled"] != false {
				t.Errorf("autoMemoryEnabled = %v, want false", v["autoMemoryEnabled"])
			}
			if !tt.autoOff && strings.Contains(tt.in, "autoMemory") == false && v["autoMemoryEnabled"] != nil {
				t.Errorf("autoMemoryEnabled = %v, want it absent while the import is not complete", v["autoMemoryEnabled"])
			}
			wantCreated := map[string]string{
				"no file":                    "hooks hooks.SessionStart hooks.UserPromptSubmit hooks.SessionEnd hooks.PostToolUse hooks.PostToolUseFailure permissions permissions.deny",
				"a hook and a rule of yours": "hooks.UserPromptSubmit hooks.SessionEnd hooks.PostToolUse hooks.PostToolUseFailure",
			}
			if w, ok := wantCreated[tt.name]; ok && strings.Join(add.Created, " ") != w {
				t.Errorf("created = %q, want %q", add.Created, w)
			}
			if again := claudeChanges(tree, "/home/a/memory", "/home/a", exe, tt.autoOff); len(again.Hooks)+len(again.Deny)+len(again.Created) != 0 || again.AutoMemory != nil {
				t.Errorf("a second run adds %+v, want nothing", again)
			}
		})
	}

	t.Run("a repo outside home", func(t *testing.T) {
		tree, _ := parseSettings(nil)
		add := claudeChanges(tree, "/srv/memory", "/home/a", exe, false)
		if add.Deny[0] != "Edit(//srv/memory/**)" {
			t.Errorf("deny = %v, want an absolute path with //", add.Deny)
		}
	})
}

func TestApplySettings(t *testing.T) {
	home, repo, _ := newMemory(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	original := "{\n  \"theme\": \"dark\"\n}\n"
	writeT(t, filepath.Dir(settings), "settings.json", original)
	change := func(yes bool) (string, string, error) {
		tree, err := readSettings(settings)
		if err != nil {
			t.Fatal(err)
		}
		add := claudeChanges(tree, repo, home, exe, true)
		var asked string
		var out strings.Builder
		err = applySettings(Env{Dir: home, Out: &out, Confirm: answer(yes, &asked)}, repo, settings, tree, add)
		return out.String(), asked, err
	}

	t.Run("no changes nothing", func(t *testing.T) {
		out, asked, err := change(false)
		if err != nil {
			t.Fatal(err)
		}
		if asked == "" || !strings.Contains(out, "+") || !strings.Contains(out, "hook session-start") {
			t.Errorf("asked %q, output =\n%s\nwant the diff and one question", asked, out)
		}
		if got := readT(t, settings); got != original {
			t.Errorf("settings =\n%s\nwant them as they were", got)
		}
		if _, err := os.Stat(filepath.Join(store.StateDir(repo), "init.json")); err == nil {
			t.Error("a no wrote the record")
		}
	})

	t.Run("yes changes, keeps the original and records", func(t *testing.T) {
		if _, _, err := change(true); err != nil {
			t.Fatal(err)
		}
		got := readT(t, settings)
		if !strings.HasPrefix(got, "{\n  \"theme\": \"dark\",") || !strings.Contains(got, "'"+exe+"' hook prompt") {
			t.Errorf("settings =\n%s", got)
		}
		kept, _ := filepath.Glob(filepath.Join(store.StateDir(repo), "init", "settings-*.json"))
		if len(kept) != 1 || readT(t, kept[0]) != original {
			t.Errorf("kept = %v, want one copy of the original", kept)
		}
		rec := readInitRecordT(t, repo)
		if len(rec.Hooks) != 5 || len(rec.Deny) != 4 || rec.AutoMemory == nil || !rec.AutoMemory.Absent {
			t.Errorf("record = %+v", rec)
		}
		for _, ev := range []string{"PostToolUse", "PostToolUseFailure"} {
			if !slices.ContainsFunc(rec.Hooks, func(h hookAddition) bool { return h.Event == ev && strings.Contains(h.Entry, "hook tool") }) {
				t.Errorf("record = %+v, want the tool hook of %s", rec, ev)
			}
		}
	})

	t.Run("a second run asks nothing", func(t *testing.T) {
		out, asked, err := change(true)
		if err != nil || asked != "" || out != "" {
			t.Errorf("err %v, asked %q, output %q, want nothing", err, asked, out)
		}
	})

	t.Run("a record without its write", func(t *testing.T) {
		// The process stopped after the record, before it wrote the settings.
		writeT(t, filepath.Dir(settings), "settings.json", original)
		if _, _, err := change(true); err != nil {
			t.Fatal(err)
		}
		if got := readT(t, settings); !strings.Contains(got, "hook session-end") {
			t.Errorf("settings =\n%s\nwant the hooks back", got)
		}
		if rec := readInitRecordT(t, repo); len(rec.Hooks) != 5 || len(rec.Deny) != 4 {
			t.Errorf("record = %+v, want each addition once", rec)
		}
	})
}

func readInitRecordT(t *testing.T, repo string) initRecord {
	t.Helper()
	rec, err := readInitRecord(repo)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}
func TestSettingsSafety(t *testing.T) {
	t.Run("a hook with a matcher does not count", func(t *testing.T) {
		in := `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"'` + exe + `' hook session-start"}]}]}}`
		tree, err := parseSettings([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		add := claudeChanges(tree, "/home/a/memory", "/home/a", exe, false)
		if len(add.Hooks) != 5 {
			t.Errorf("hooks added = %v, want all five: a hook for startup alone misses the other sources", add.Hooks)
		}
	})

	t.Run("a tool hook for every tool does not count", func(t *testing.T) {
		in := `{"hooks":{"PostToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"'` + exe + `' hook tool"}]}]}}`
		tree, err := parseSettings([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		add := claudeChanges(tree, "/home/a/memory", "/home/a", exe, false)
		if len(add.Hooks) != 5 {
			t.Errorf("hooks added = %v, want all five: the matcher of the tool hooks is Bash|Read|Grep", add.Hooks)
		}
	})

	home, repo, _ := newMemory(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	setup := func(t *testing.T, mode os.FileMode) {
		writeT(t, filepath.Dir(settings), "settings.json", "{\n  \"theme\": \"dark\"\n}\n")
		if err := os.Chmod(settings, mode); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(confirm func(string) (bool, error)) (string, error) {
		tree, err := readSettings(settings)
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		err = applySettings(Env{Dir: home, Out: &out, Confirm: confirm}, repo, settings, tree, claudeChanges(tree, repo, home, exe, false))
		return out.String(), err
	}

	t.Run("a change while you answer stops the write", func(t *testing.T) {
		setup(t, 0o644)
		_, err := apply(func(string) (bool, error) {
			writeT(t, filepath.Dir(settings), "settings.json", "{\n  \"theme\": \"light\"\n}\n")
			return true, nil
		})
		if err == nil || !strings.Contains(err.Error(), "changed") {
			t.Errorf("error = %v, want a refusal of settings that changed", err)
		}
		if got := readT(t, settings); !strings.Contains(got, "light") || strings.Contains(got, "hook") {
			t.Errorf("settings =\n%s\nwant your change, and nothing of init", got)
		}
	})

	t.Run("the copy of private settings stays private", func(t *testing.T) {
		os.RemoveAll(filepath.Join(store.StateDir(repo), "init"))
		setup(t, 0o600)
		if _, err := apply(answer(true, new(string))); err != nil {
			t.Fatal(err)
		}
		kept, _ := filepath.Glob(filepath.Join(store.StateDir(repo), "init", "settings-*.json"))
		if len(kept) != 1 {
			t.Fatalf("kept = %v", kept)
		}
		if info, err := os.Stat(kept[0]); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the copy has mode %v, want 0600", info.Mode().Perm())
		}
	})

	t.Run("an external diff tool does not hide the diff", func(t *testing.T) {
		t.Setenv("GIT_EXTERNAL_DIFF", "true")
		diff, err := settingsDiff([]byte("{}\n"), []byte("{\n  \"a\": 1\n}\n"), "settings.json")
		if err != nil || !strings.Contains(diff, `+  "a": 1`) {
			t.Errorf("diff = %q, %v", diff, err)
		}
	})
}

func TestUndoSettings(t *testing.T) {
	original := "{\n  \"theme\": \"dark\"\n}\n"
	tests := []struct {
		name      string
		after     func(t *testing.T, settings string) // a change of the person after init
		want      []string                            // what the settings hold after the undo
		wantNot   []string
		conflicts []string
	}{
		{"a plain undo restores the original", nil, nil, []string{"hook", "memcore", "autoMemoryEnabled"}, nil},
		{"a setting added after init stays", func(t *testing.T, s string) {
			replaceT(t, s, `"theme": "dark",`, `"theme": "dark",`+"\n  \"model\": \"opus\",")
		}, []string{`"model": "opus"`}, []string{"hook session-start", "Bash(memcore drop *)"}, nil},
		{"a changed hook and auto-memory turned back on stay", func(t *testing.T, s string) {
			replaceT(t, s, "hook session-start\"", "hook session-start --quiet\"")
			replaceT(t, s, `"autoMemoryEnabled": false`, `"autoMemoryEnabled": true`)
		}, []string{"hook session-start --quiet", `"autoMemoryEnabled": true`}, []string{"hook prompt", "hook session-end"},
			[]string{"SessionStart", "autoMemoryEnabled"}},
		{"an addition that you removed is skipped", func(t *testing.T, s string) {
			replaceT(t, s, `"Bash(memcore drop *)",`, ``)
		}, nil, []string{"memcore"}, nil},
		{"a hook with a new path is a conflict", func(t *testing.T, s string) {
			replaceT(t, s, "'"+exe+"' hook session-start", "/usr/local/bin/memcore hook session-start")
		}, []string{"/usr/local/bin/memcore hook session-start"}, []string{"hook prompt"}, []string{"SessionStart"}},
		{"an edited rule is a conflict", func(t *testing.T, s string) {
			replaceT(t, s, `"Bash(memcore drop *)"`, `"Bash(memcore drop:*)"`)
		}, []string{"Bash(memcore drop:*)"}, []string{"Bash(memcore recover *)"}, []string{"Bash(memcore drop *)"}},
		{"a changed matcher is a conflict", func(t *testing.T, s string) {
			replaceT(t, s, `"matcher": "Bash|Read|Grep"`, `"matcher": "Bash"`)
		}, []string{`"matcher": "Bash"`, "hook tool"}, []string{"hook prompt", "PostToolUseFailure", "Bash|Read|Grep"}, []string{"the PostToolUse hook"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, repo, _ := newMemory(t)
			settings := filepath.Join(home, ".claude", "settings.json")
			writeT(t, filepath.Dir(settings), "settings.json", original)
			tree, err := readSettings(settings)
			if err != nil {
				t.Fatal(err)
			}
			var asked string
			if err := applySettings(Env{Dir: home, Out: &strings.Builder{}, Confirm: answer(true, &asked)}, repo, settings, tree, claudeChanges(tree, repo, home, exe, true)); err != nil {
				t.Fatal(err)
			}
			if tt.after != nil {
				tt.after(t, settings)
			}
			var out strings.Builder
			asked = ""
			if err := undoSettings(Env{Dir: home, Out: &out, Confirm: answer(true, &asked)}, repo, settings); err != nil {
				t.Fatalf("undo: %v\n%s", err, out.String())
			}
			got := readT(t, settings)
			if tt.after == nil && got != original {
				t.Errorf("settings =\n%s\nwant the original", got)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("settings =\n%s\nwant %q", got, w)
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(got, w) {
					t.Errorf("settings =\n%s\nwant no %q", got, w)
				}
			}
			for _, c := range tt.conflicts {
				if !strings.Contains(out.String(), c) {
					t.Errorf("output =\n%s\nwant the conflict %q named", out.String(), c)
				}
			}
			if asked == "" {
				t.Error("undo asked nothing")
			}
			// A second undo finds only the conflicts, and changes nothing.
			before := readT(t, settings)
			out.Reset()
			if err := undoSettings(Env{Dir: home, Out: &out, Confirm: answer(true, &asked)}, repo, settings); err != nil {
				t.Fatal(err)
			}
			if readT(t, settings) != before {
				t.Error("a second undo changed the settings")
			}
		})
	}

	t.Run("--undo removes the old rule of push with the other rules", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		settings := filepath.Join(home, ".claude", "settings.json")
		original := "{\n  \"theme\": \"dark\"\n}\n"
		writeT(t, filepath.Dir(settings), "settings.json", original)
		tree, err := readSettings(settings)
		if err != nil {
			t.Fatal(err)
		}
		if err := applySettings(Env{Dir: home, Out: &strings.Builder{}, Confirm: answer(true, new(string))}, repo, settings, tree, claudeChanges(tree, repo, home, exe, true)); err != nil {
			t.Fatal(err)
		}
		// Before this rule list, init denied push too, and recorded the rule.
		const old = "Bash(memcore push *)"
		replaceT(t, settings, `"Bash(memcore drop *)",`, `"Bash(memcore drop *)",`+"\n      \""+old+"\",")
		rec := readInitRecordT(t, repo)
		rec.Deny = append(rec.Deny, old)
		if err := writeInitRecord(repo, rec); err != nil {
			t.Fatal(err)
		}
		if err := undoSettings(Env{Dir: home, Out: &strings.Builder{}, Confirm: answer(true, new(string))}, repo, settings); err != nil {
			t.Fatal(err)
		}
		if got := readT(t, settings); got != original {
			t.Errorf("settings =\n%s\nwant the original", got)
		}
	})

	t.Run("a record from before M3 gains the tool hooks, and --undo removes all five", func(t *testing.T) {
		home, repo, _ := newMemory(t)
		settings := filepath.Join(home, ".claude", "settings.json")
		original := "{\n  \"theme\": \"dark\"\n}\n"
		writeT(t, filepath.Dir(settings), "settings.json", original)
		apply := func() {
			t.Helper()
			tree, err := readSettings(settings)
			if err != nil {
				t.Fatal(err)
			}
			if err := applySettings(Env{Dir: home, Out: &strings.Builder{}, Confirm: answer(true, new(string))}, repo, settings, tree, claudeChanges(tree, repo, home, exe, true)); err != nil {
				t.Fatal(err)
			}
		}
		// Before M3, init added the first three hooks.
		all := hookEvents
		t.Cleanup(func() { hookEvents = all })
		hookEvents = all[:3]
		apply()
		hookEvents = all
		if rec := readInitRecordT(t, repo); len(rec.Hooks) != 3 || strings.Contains(readT(t, settings), "hook tool") {
			t.Fatalf("record = %+v, want the three hooks from before M3", rec)
		}

		apply()
		rec := readInitRecordT(t, repo)
		if len(rec.Hooks) != 5 || strings.Count(readT(t, settings), "hook tool") != 2 {
			t.Errorf("record = %+v, settings =\n%s\nwant the two tool hooks added", rec, readT(t, settings))
		}
		if err := undoSettings(Env{Dir: home, Out: &strings.Builder{}, Confirm: answer(true, new(string))}, repo, settings); err != nil {
			t.Fatal(err)
		}
		if got := readT(t, settings); got != original {
			t.Errorf("settings =\n%s\nwant the original", got)
		}
	})
}

// replaceT replaces old with new once in a file, and fails the test when old is missing.
func replaceT(t *testing.T, p, old, new string) {
	t.Helper()
	s := readT(t, p)
	if !strings.Contains(s, old) {
		t.Fatalf("%s holds no %q:\n%s", p, old, s)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(s, old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}
func TestUndoSettingsSafety(t *testing.T) {
	home, repo, _ := newMemory(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	setup := func(t *testing.T, original string) {
		writeT(t, filepath.Dir(settings), "settings.json", original)
		tree, err := readSettings(settings)
		if err != nil {
			t.Fatal(err)
		}
		if err := applySettings(Env{Dir: home, Out: &strings.Builder{}, Confirm: answer(true, new(string))}, repo, settings, tree, claudeChanges(tree, repo, home, exe, true)); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("containers that you had stay", func(t *testing.T) {
		original := "{\n  \"hooks\": {},\n  \"permissions\": {\n    \"deny\": []\n  }\n}\n"
		setup(t, original)
		if err := undoSettings(Env{Dir: home, Out: &strings.Builder{}, Confirm: answer(true, new(string))}, repo, settings); err != nil {
			t.Fatal(err)
		}
		if got := readT(t, settings); got != original {
			t.Errorf("settings =\n%s\nwant the original, with its empty containers", got)
		}
	})

	t.Run("a change while you answer stops the write", func(t *testing.T) {
		setup(t, "{\n  \"theme\": \"dark\"\n}\n")
		err := undoSettings(Env{Dir: home, Out: &strings.Builder{}, Confirm: func(string) (bool, error) {
			replaceT(t, settings, `"theme": "dark"`, `"theme": "light"`)
			return true, nil
		}}, repo, settings)
		if err == nil || !strings.Contains(err.Error(), "changed") {
			t.Errorf("error = %v, want a refusal of settings that changed", err)
		}
		if got := readT(t, settings); !strings.Contains(got, "light") || !strings.Contains(got, "hook prompt") {
			t.Errorf("settings =\n%s\nwant your change, and the hooks still there", got)
		}
	})
}
