package readers

import (
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/guards"
)

func TestTrim(t *testing.T) {
	const at = `"timestamp":"2026-11-04T10:00:00Z"`
	const head = `{"agent":"claude","session":"s1","dir":"/home/a/dev/shop"}`
	user := func(content string) string {
		return `{"type":"user",` + at + `,"sessionId":"s1","cwd":"/home/a/dev/shop","message":{"role":"user","content":` + content + `}}`
	}
	agent := func(content string) string {
		return `{"type":"assistant",` + at + `,"sessionId":"s1","message":{"role":"assistant","content":` + content + `}}`
	}
	line := func(who, text string) string {
		return `{"time":"2026-11-04T10:00:00Z","who":"` + who + `","text":"` + text + `"}`
	}
	tests := []struct {
		name    string
		source  []string
		opts    TrimOptions
		want    []string
		broken  int
		partial bool
	}{
		{"your text as a string", []string{user(`"Use small commits."`)}, TrimOptions{},
			[]string{head, line("you", "Use small commits.")}, 0, false},
		{"your text as blocks, without the reminder", []string{user(`[{"type":"text","text":"Run the tests.<system-reminder>noise</system-reminder>"}]`)}, TrimOptions{},
			[]string{head, line("you", "Run the tests.")}, 0, false},
		{"agent text, thinking and a call", []string{agent(`[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"I run them."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./...","description":"Run the tests"}}]`)}, TrimOptions{},
			[]string{line("agent", "I run them."), line("call", "Bash: Run the tests")}, 0, false},
		{"a call without a description", []string{agent(`[{"type":"tool_use","id":"t1","name":"Read","input":{"limit":20,"file_path":"/home/a/dev/shop/main.go"}}]`)}, TrimOptions{},
			[]string{line("call", "Read: /home/a/dev/shop/main.go")}, 0, false},
		{"a call with several strings, in one order", []string{agent(`[{"type":"tool_use","id":"t1","name":"Grep","input":{"zeta":"z","pattern":"TODO","alpha":"a"}}]`)}, TrimOptions{},
			[]string{line("call", "Grep: TODO")}, 0, false},
		{"a call with several strings, in another order", []string{agent(`[{"type":"tool_use","id":"t1","name":"Custom","input":{"zeta":"z","alpha":"a"}}]`)}, TrimOptions{},
			[]string{line("call", "Custom: a")}, 0, false},
		{"a call on one line", []string{agent(`[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo one\necho two"}}]`)}, TrimOptions{},
			[]string{line("call", "Bash: echo one")}, 0, false},
		{"a plain tool result", []string{agent(`[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]`), user(`[{"type":"tool_result","tool_use_id":"t1","content":"a.go\nb.go"}]`)}, TrimOptions{},
			[]string{line("call", "Bash: ls"), head}, 0, false},
		{"a rejection", []string{agent(`[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"rm -rf build"}}]`), user(`[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"The user doesn't want to proceed with this tool use. The tool use was rejected."}]`), user(`"Keep the build folder."`)}, TrimOptions{},
			[]string{line("call", "Bash: rm -rf build"), head, line("rejected", "Bash: rm -rf build"), line("you", "Keep the build folder.")}, 0, false},
		{"meta, sidechain and compaction", []string{
			`{"type":"user",` + at + `,"isMeta":true,"message":{"role":"user","content":"caveat"}}`,
			`{"type":"user",` + at + `,"isSidechain":true,"message":{"role":"user","content":"aside"}}`,
			`{"type":"user",` + at + `,"isCompactSummary":true,"message":{"role":"user","content":"summary"}}`,
		}, TrimOptions{}, nil, 0, false},
		{"a subagent file", []string{
			`{"type":"user",` + at + `,"isSidechain":true,"sessionId":"s1","agentId":"a7","cwd":"/home/a/dev/shop","message":{"role":"user","content":"Find the config."}}`,
			`{"type":"assistant",` + at + `,"isSidechain":true,"sessionId":"s1","agentId":"a7","message":{"role":"assistant","content":[{"type":"text","text":"It is in etc."}]}}`,
		}, TrimOptions{Subagent: true},
			[]string{`{"agent":"claude","session":"a7","parent":"s1","dir":"/home/a/dev/shop"}`, line("parent", "Find the config."), line("agent", "It is in etc.")}, 0, false},
		{"a broken line in the middle", []string{user(`"one"`), `{"type":"user",`, user(`"two"`)}, TrimOptions{},
			[]string{head, line("you", "one"), line("you", "two")}, 1, false},
		{"an unknown type", []string{`{"type":"queue-operation","operation":"enqueue"}`, user(`"hi"`)}, TrimOptions{},
			[]string{head, line("you", "hi")}, 0, false},
		{"a redaction of text that holds a tab", []string{user(`"token Bearer\tAbcd1234 here"`)}, TrimOptions{Redact: func(s string) string {
			return strings.ReplaceAll(s, "Bearer\tAbcd1234", "[secret:bearer-token]")
		}}, []string{head, line("you", "token [secret:bearer-token] here")}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			stats, err := Trim(strings.NewReader(strings.Join(tt.source, "\n")+"\n"), &out, tt.opts)
			if err != nil {
				t.Fatalf("Trim: %v", err)
			}
			want := ""
			if len(tt.want) > 0 {
				want = sortHead(tt.want)
			}
			if got := out.String(); got != want {
				t.Errorf("Trim =\n%s\nwant\n%s", got, want)
			}
			if stats.Broken != tt.broken || stats.Partial != tt.partial {
				t.Errorf("stats = %+v, want %d broken, partial %v", stats, tt.broken, tt.partial)
			}
		})
	}
}

// sortHead puts the header first, where Trim writes it, and ends each line with a newline.
func sortHead(lines []string) string {
	var head, rest []string
	for _, l := range lines {
		if strings.HasPrefix(l, `{"agent":`) {
			head = append(head, l)
		} else {
			rest = append(rest, l)
		}
	}
	if len(head) == 0 {
		head = []string{`{"agent":"claude","session":"s1"}`} // the lines hold a session, but no directory
	}
	return strings.Join(append(head[:1], rest...), "\n") + "\n"
}

func TestTrimPartial(t *testing.T) {
	var out strings.Builder
	src := `{"type":"user","timestamp":"t","sessionId":"s1","cwd":"/d","message":{"role":"user","content":"one"}}` + "\n" + `{"type":"user","timestamp":"t","message":{"role":"user","content":"tw`
	stats, err := Trim(strings.NewReader(src), &out, TrimOptions{})
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if !stats.Partial || stats.Broken != 0 {
		t.Errorf("stats = %+v, want a partial last line and nothing broken", stats)
	}
	if strings.Contains(out.String(), "tw") {
		t.Errorf("Trim kept the partial line:\n%s", out.String())
	}
}

func TestTrimSame(t *testing.T) {
	src := `{"type":"assistant","timestamp":"t","message":{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"T","input":{"b":"2","a":"1","c":"3"}}]}}` + "\n"
	var first string
	for i := 0; i < 20; i++ {
		var out strings.Builder
		if _, err := Trim(strings.NewReader(src), &out, TrimOptions{}); err != nil {
			t.Fatalf("Trim: %v", err)
		}
		if i == 0 {
			first = out.String()
		} else if out.String() != first {
			t.Fatalf("Trim gave\n%s\nthen\n%s", first, out.String())
		}
	}
}

func TestTrimRedactsBeforeCut(t *testing.T) {
	// The password is built from parts, so no scanner takes this file for a leak.
	command := "echo " + strings.Repeat("x", 166) + " postgres://app:" + "s3cret" + "pw@db/shop" // the cut at 200 falls after "s3cretp"
	src := `{"type":"assistant","timestamp":"t","message":{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"Bash","input":{"command":"` + command + `"}}]}}` + "\n"
	var out strings.Builder
	if _, err := Trim(strings.NewReader(src), &out, TrimOptions{Redact: guards.Redact}); err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Errorf("Trim kept a part of the password:\n%s", out.String())
	}
}

func TestTrimSubagentHeader(t *testing.T) {
	src := `{"type":"queue-operation","sessionId":"s1"}` + "\n" +
		`{"type":"user","timestamp":"t","isSidechain":true,"sessionId":"s1","agentId":"a7","cwd":"/d","message":{"role":"user","content":"Find it."}}` + "\n"
	var out strings.Builder
	if _, err := Trim(strings.NewReader(src), &out, TrimOptions{Subagent: true}); err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if first, _, _ := strings.Cut(out.String(), "\n"); first != `{"agent":"claude","session":"a7","parent":"s1","dir":"/d"}` {
		t.Errorf("header = %s", first)
	}
}

func TestTrimRedactsTheHeader(t *testing.T) {
	// The token is built from parts, so no scanner takes this file for a leak.
	token := "ghp_" + strings.Repeat("a1B2", 9)
	src := `{"type":"user","timestamp":"t","sessionId":"s1","cwd":"/home/a/` + token + `","message":{"role":"user","content":"hi"}}` + "\n"
	var out strings.Builder
	if _, err := Trim(strings.NewReader(src), &out, TrimOptions{Redact: guards.Redact}); err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if strings.Contains(out.String(), token) || !strings.Contains(out.String(), "[secret:hosting-token]") {
		t.Errorf("Trim =\n%s\nwant the directory of the header redacted", out.String())
	}
}
