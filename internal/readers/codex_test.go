package readers

import (
	"strconv"
	"strings"
	"testing"

	"github.com/atmin/memcore/internal/guards"
)

// The lines below follow the format of Codex in the plan codex-transcripts. They are written by hand.

const codexAt = "2026-11-23T14:38:00.000Z"

// cxMeta is the session_meta line of a session. source is raw JSON, and start is subagent_history_start_ordinal, or 0 for none.
func cxMeta(ordinal int, id, cwd, source string, start int) string {
	extra := ""
	if start > 0 {
		extra = `,"subagent_history_start_ordinal":` + strconv.Itoa(start)
	}
	return `{"timestamp":"` + codexAt + `","ordinal":` + strconv.Itoa(ordinal) + `,"type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `","source":` + source + extra + `}}`
}

// cxItem is an item_completed line that holds item, raw JSON.
func cxItem(ordinal int, item string) string {
	return `{"timestamp":"` + codexAt + `","ordinal":` + strconv.Itoa(ordinal) + `,"type":"event_msg","payload":{"type":"item_completed","item":` + item + `}}`
}

// cxAgentMessage is a message between agents.
func cxAgentMessage(ordinal int, author, recipient, text string) string {
	return `{"timestamp":"` + codexAt + `","ordinal":` + strconv.Itoa(ordinal) + `,"type":"response_item","payload":{"type":"agent_message","author":"` + author + `","recipient":"` + recipient + `","content":[{"type":"input_text","text":"` + text + `"},{"type":"encrypted_content","encrypted_content":"abc"}]}}`
}

func cxUser(ordinal int, text string) string {
	return cxItem(ordinal, `{"type":"UserMessage","content":[{"type":"text","text":"`+text+`"}]}`)
}

func cxAgent(ordinal int, text string) string {
	return cxItem(ordinal, `{"type":"AgentMessage","content":[{"type":"Text","text":"`+text+`"}]}`)
}

const (
	codexMain     = `"vscode"`
	codexExec     = `"exec"`
	codexSpawn    = `{"subagent":{"thread_spawn":{"parent_thread_id":"p1","depth":1,"agent_path":"/root/finder"}}}`
	codexGuardian = `{"subagent":{"other":"guardian"}}`
)

func cxLine(who, text string) string {
	return `{"time":"` + codexAt + `","who":"` + who + `","text":"` + text + `"}`
}

func TestTrimCodex(t *testing.T) {
	const head = `{"agent":"codex","session":"s1","dir":"/home/a/shop"}`
	const subHead = `{"agent":"codex","session":"a1","parent":"p1","dir":"/home/a/shop"}`
	main := cxMeta(0, "s1", "/home/a/shop", codexMain, 0)
	sub := cxMeta(0, "a1", "/home/a/shop", codexSpawn, 0)
	tests := []struct {
		name    string
		source  []string
		opts    TrimOptions
		want    []string // with the header first, or nil for no output
		session CodexSession
	}{
		{"the first session_meta, and not a later one", []string{
			main, cxMeta(1, "s2", "/home/b", codexExec, 0), cxUser(2, "Use small commits."),
		}, TrimOptions{}, []string{head, cxLine("you", "Use small commits.")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"the lines below the start of the history", []string{
			cxMeta(0, "a1", "/home/a/shop", codexSpawn, 3),
			cxMeta(1, "p1", "/home/a/shop", codexMain, 0),
			cxUser(2, "The task of the parent."),
			cxUser(3, "Find the config."),
		}, TrimOptions{}, []string{subHead, cxLine("parent", "Find the config.")}, CodexSession{ID: "a1", Dir: "/home/a/shop", Parent: "p1"}},
		{"a guardian", []string{
			cxMeta(0, "g1", "/home/a/shop", codexGuardian, 0), cxUser(1, "Review the approval."), cxAgent(2, "Approved."),
		}, TrimOptions{}, nil, CodexSession{ID: "g1", Dir: "/home/a/shop"}},
		{"the text of the person in a main session", []string{
			main, cxItem(1, `{"type":"UserMessage","content":[{"type":"text","text":"Look at this."},{"type":"local_image","path":"/tmp/a.png"},{"type":"text","text":"And fix it."}]}`),
		}, TrimOptions{}, []string{head, cxLine("you", "Look at this."), cxLine("you", "And fix it.")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"the text of the parent in a subagent", []string{sub, cxUser(1, "Find the config.")}, TrimOptions{},
			[]string{subHead, cxLine("parent", "Find the config.")}, CodexSession{ID: "a1", Dir: "/home/a/shop", Parent: "p1"}},
		{"the text of the agent", []string{
			main, cxItem(1, `{"type":"AgentMessage","content":[{"type":"Text","text":"One."},{"type":"Text","text":"Two."}]}`),
		}, TrimOptions{}, []string{head, cxLine("agent", "One."), cxLine("agent", "Two.")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"a message from another agent to a subagent", []string{
			sub, cxAgentMessage(1, "/root", "/root/finder", "Find the config."), cxAgentMessage(2, "/root/finder", "/root", "It is in etc."),
		}, TrimOptions{}, []string{subHead, cxLine("parent", "Find the config.")}, CodexSession{ID: "a1", Dir: "/home/a/shop", Parent: "p1"}},
		{"a message between agents in a main session", []string{
			main, cxAgentMessage(1, "/root/finder", "/root", "It is in etc."), cxUser(2, "Thanks."),
		}, TrimOptions{}, []string{head, cxLine("you", "Thanks.")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"a command", []string{
			main, cxItem(1, `{"type":"CommandExecution","command":["/bin/zsh","-lc","go test ./...\ngo vet ./..."],"status":"failed"}`),
		}, TrimOptions{}, []string{head, cxLine("call", "exec: go test ./...")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"a change of files", []string{
			main, cxItem(1, `{"type":"FileChange","changes":{"b/main.go":{"type":"update"},"a/go.mod":{"type":"add"}}}`),
		}, TrimOptions{}, []string{head, cxLine("call", "patch: a/go.mod, b/main.go")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"a call of a tool server", []string{
			main, cxItem(1, `{"type":"McpToolCall","server":"docs","tool":"search","arguments":{"q":"x"}}`),
		}, TrimOptions{}, []string{head, cxLine("call", "mcp: docs.search")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"a web search", []string{
			main, cxItem(1, `{"type":"Extension","kind":"web.search","query":"go zstd\nmore","revisedPrompt":null,"action":{"type":"search","url":"https://example.com"}}`),
		}, TrimOptions{}, []string{head, cxLine("call", "web.search: go zstd")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"an opened page", []string{
			main, cxItem(1, `{"type":"Extension","kind":"web.search","query":"","revisedPrompt":null,"action":{"type":"open_page","url":"https://example.com/a"}}`),
		}, TrimOptions{}, []string{head, cxLine("call", "web.search: https://example.com/a")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"a generated image", []string{
			main, cxItem(1, `{"type":"Extension","kind":"image_gen.generation","query":null,"revisedPrompt":"A red bicycle","action":null}`),
		}, TrimOptions{}, []string{head, cxLine("call", "image_gen.generation: A red bicycle")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"an extension with no value", []string{
			main, cxItem(1, `{"type":"Extension","kind":"web.search","query":null,"revisedPrompt":null,"action":{"type":"other"}}`),
		}, TrimOptions{}, []string{head, cxLine("call", "web.search")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"the items that are no part of the conversation", []string{
			main,
			cxItem(1, `{"type":"Reasoning","content":[{"type":"text","text":"hmm"}],"summary":["x"]}`),
			cxItem(2, `{"type":"SubAgentActivity","content":"x"}`),
			cxItem(3, `{"type":"ImageView","path":"/tmp/a.png"}`),
			cxItem(4, `{"type":"ContextCompaction"}`),
			cxItem(5, `{"type":"CollabAgentToolCall","tool":"spawn"}`),
			`{"timestamp":"` + codexAt + `","ordinal":6,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"AGENTS.md"}]}}`,
			`{"timestamp":"` + codexAt + `","ordinal":7,"type":"event_msg","payload":{"type":"task_started"}}`,
		}, TrimOptions{}, nil, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"the time of each line", []string{
			main, strings.Replace(cxUser(1, "Late."), codexAt, "2026-11-23T15:00:00Z", 1),
		}, TrimOptions{}, []string{head, `{"time":"2026-11-23T15:00:00Z","who":"you","text":"Late."}`}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"a redaction of the text and the header", []string{
			cxMeta(0, "s1", "/home/a/KEY", codexMain, 0), cxUser(1, "Use KEY here."), cxItem(2, `{"type":"CommandExecution","command":["echo KEY"],"status":"completed"}`),
		}, TrimOptions{Redact: func(s string) string { return strings.ReplaceAll(s, "KEY", "[secret:api-key]") }},
			[]string{`{"agent":"codex","session":"s1","dir":"/home/a/[secret:api-key]"}`, cxLine("you", "Use [secret:api-key] here."), cxLine("call", "exec: echo [secret:api-key]")},
			CodexSession{ID: "s1", Dir: "/home/a/KEY"}},
		{"a broken line", []string{main, cxUser(1, "one"), `{"type":"event_msg",`, cxUser(3, "two")}, TrimOptions{},
			[]string{head, cxLine("you", "one"), cxLine("you", "two")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"an exec run", []string{cxMeta(0, "s1", "/home/a/shop", codexExec, 0), cxUser(1, "Review the commit.")}, TrimOptions{},
			[]string{`{"agent":"codex","session":"s1","dir":"/home/a/shop","source":"exec"}`, cxLine("you", "Review the commit.")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"no line of the conversation", []string{main, cxUser(1, "  ")}, TrimOptions{}, nil, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
		{"no session_meta", []string{cxUser(1, "Hello.")}, TrimOptions{}, nil, CodexSession{}},
		{"the option of a subagent", []string{main, cxUser(1, "Hello.")}, TrimOptions{Subagent: true},
			[]string{head, cxLine("you", "Hello.")}, CodexSession{ID: "s1", Dir: "/home/a/shop"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			sess, stats, err := TrimCodex(strings.NewReader(strings.Join(tt.source, "\n")+"\n"), &out, tt.opts)
			if err != nil {
				t.Fatalf("TrimCodex: %v", err)
			}
			want := ""
			if len(tt.want) > 0 {
				want = strings.Join(tt.want, "\n") + "\n"
			}
			if got := out.String(); got != want {
				t.Errorf("TrimCodex =\n%s\nwant\n%s", got, want)
			}
			if sess != tt.session {
				t.Errorf("session = %+v, want %+v", sess, tt.session)
			}
			wantBroken := 0
			if tt.name == "a broken line" {
				wantBroken = 1
			}
			if stats.Broken != wantBroken || stats.Partial {
				t.Errorf("stats = %+v, want %d broken and no partial line", stats, wantBroken)
			}
		})
	}
}

func TestTrimCodexFork(t *testing.T) {
	// A forked subagent starts with a copy of the history of its parent, with a second session_meta.
	src := []string{
		cxMeta(0, "a1", "/home/a/shop", codexSpawn, 4),
		cxMeta(1, "p1", "/home/a/shop", codexMain, 0),
		cxUser(2, "The parent asked this."),
		cxAgent(3, "The parent answered this."),
		cxAgentMessage(4, "/root", "/root/finder", "Find the config."),
		cxItem(5, `{"type":"CommandExecution","command":["/bin/zsh","-lc","ls etc"],"status":"completed"}`),
		cxAgent(6, "It is in etc."),
	}
	var out strings.Builder
	sess, _, err := TrimCodex(strings.NewReader(strings.Join(src, "\n")+"\n"), &out, TrimOptions{})
	if err != nil {
		t.Fatalf("TrimCodex: %v", err)
	}
	want := strings.Join([]string{
		`{"agent":"codex","session":"a1","parent":"p1","dir":"/home/a/shop"}`,
		cxLine("parent", "Find the config."),
		cxLine("call", "exec: ls etc"),
		cxLine("agent", "It is in etc."),
	}, "\n") + "\n"
	if out.String() != want {
		t.Errorf("TrimCodex =\n%s\nwant\n%s", out.String(), want)
	}
	if sess != (CodexSession{ID: "a1", Dir: "/home/a/shop", Parent: "p1"}) {
		t.Errorf("session = %+v", sess)
	}
}

func TestTrimCodexGuardian(t *testing.T) {
	src := cxMeta(0, "g1", "/home/a/shop", codexGuardian, 2) + "\n" +
		cxMeta(1, "s1", "/home/a/shop", codexMain, 0) + "\n" +
		cxUser(2, "Approve rm -rf build?") + "\n" + cxAgent(3, "Denied.") + "\n"
	var out strings.Builder
	sess, _, err := TrimCodex(strings.NewReader(src), &out, TrimOptions{})
	if err != nil {
		t.Fatalf("TrimCodex: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("TrimCodex wrote a guardian:\n%s", out.String())
	}
	if sess.ID != "g1" {
		t.Errorf("session = %+v, want the ID g1", sess)
	}
}

func TestTrimCodexPartial(t *testing.T) {
	src := cxMeta(0, "s1", "/d", codexMain, 0) + "\n" + cxUser(1, "one") + "\n" + cxUser(2, "tw")[:60]
	var out strings.Builder
	_, stats, err := TrimCodex(strings.NewReader(src), &out, TrimOptions{})
	if err != nil {
		t.Fatalf("TrimCodex: %v", err)
	}
	if !stats.Partial || stats.Broken != 0 {
		t.Errorf("stats = %+v, want a partial last line and nothing broken", stats)
	}
	if want := `{"agent":"codex","session":"s1","dir":"/d"}` + "\n" + cxLine("you", "one") + "\n"; out.String() != want {
		t.Errorf("TrimCodex =\n%s\nwant\n%s", out.String(), want)
	}
}

func TestTrimCodexSame(t *testing.T) {
	src := cxMeta(0, "s1", "/d", codexMain, 0) + "\n" +
		cxItem(1, `{"type":"FileChange","changes":{"d":{},"b":{},"a":{},"c":{},"e":{}}}`) + "\n"
	var first string
	for i := 0; i < 20; i++ {
		var out strings.Builder
		if _, _, err := TrimCodex(strings.NewReader(src), &out, TrimOptions{}); err != nil {
			t.Fatalf("TrimCodex: %v", err)
		}
		if i == 0 {
			first = out.String()
		} else if out.String() != first {
			t.Fatalf("TrimCodex gave\n%s\nthen\n%s", first, out.String())
		}
	}
}

func TestTrimCodexRedactsBeforeCut(t *testing.T) {
	// The password is built from parts, so no scanner takes this file for a leak.
	command := "echo " + strings.Repeat("x", 166) + " postgres://app:" + "s3cret" + "pw@db/shop" // the cut at 200 falls after "s3cretp"
	src := cxMeta(0, "s1", "/d", codexMain, 0) + "\n" +
		cxItem(1, `{"type":"CommandExecution","command":["/bin/zsh","-lc","`+command+`"],"status":"completed"}`) + "\n"
	var out strings.Builder
	if _, _, err := TrimCodex(strings.NewReader(src), &out, TrimOptions{Redact: guards.Redact}); err != nil {
		t.Fatalf("TrimCodex: %v", err)
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Errorf("TrimCodex kept a part of the password:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `"who":"call"`) {
		t.Errorf("TrimCodex wrote no call:\n%s", out.String())
	}
}

func TestTrimCodexKeepsLongText(t *testing.T) {
	you, agent, parent := strings.Repeat("y", 250), strings.Repeat("a", 250), strings.Repeat("p", 250)
	call := strings.Repeat("c", 250)
	tests := []struct {
		name   string
		source []string
		want   []string
	}{
		{"a main session", []string{
			cxMeta(0, "s1", "/d", codexMain, 0), cxUser(1, you), cxAgent(2, agent),
			cxItem(3, `{"type":"CommandExecution","command":["`+call+`"],"status":"completed"}`),
		}, []string{
			`{"agent":"codex","session":"s1","dir":"/d"}`, cxLine("you", you), cxLine("agent", agent), cxLine("call", "exec: "+call[:194]),
		}},
		{"a subagent", []string{
			cxMeta(0, "a1", "/d", codexSpawn, 0), cxAgentMessage(1, "/root", "/root/finder", parent),
		}, []string{
			`{"agent":"codex","session":"a1","parent":"p1","dir":"/d"}`, cxLine("parent", parent),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			if _, _, err := TrimCodex(strings.NewReader(strings.Join(tt.source, "\n")+"\n"), &out, TrimOptions{}); err != nil {
				t.Fatalf("TrimCodex: %v", err)
			}
			if want := strings.Join(tt.want, "\n") + "\n"; out.String() != want {
				t.Errorf("TrimCodex =\n%s\nwant\n%s", out.String(), want)
			}
		})
	}
}
