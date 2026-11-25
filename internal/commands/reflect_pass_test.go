package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/thinking"
)

// fakeAgent replaces the headless agent for a test. extract answers a call of the extraction, by the number of
// the call, and judge answers a call of the judge. It returns the requests that the agent got.
func fakeAgent(t *testing.T, extract func(n int, req thinking.Request) (thinking.Reply, error), judge func(req thinking.Request) (thinking.Reply, error)) *[]thinking.Request {
	t.Helper()
	var calls []thinking.Request
	extracts := 0
	prev := thinking.Run
	t.Cleanup(func() { thinking.Run = prev })
	thinking.Run = func(req thinking.Request) (thinking.Reply, error) {
		calls = append(calls, req)
		if strings.Contains(req.Schema, `"findings"`) {
			return judge(req)
		}
		extracts++
		return extract(extracts, req)
	}
	return &calls
}

// reflectFixture makes a memory repo with one memory at ~/dev, and two old sessions there: s-first with two
// corrections, and s-second with none. It returns the directory ~/dev and the repo.
func reflectFixture(t *testing.T) (dev, repo string) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev = mkdirT(t, filepath.Join(home, "dev"))
	if out, err := run(t, dev, memoryText("ask-before-push", "Read before you push: ask the person first.", "Push only when the person asks."), "remember"); err != nil {
		t.Fatalf("remember: %v\n%s", err, out)
	}
	commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	old := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	transcriptT(t, repo, "claude/p1/s-first.jsonl", transcriptText("s-first", old, "you|Add a retry.", "you|Never push without asking.", "you|Use small commits."))
	transcriptT(t, repo, "claude/p1/s-second.jsonl", transcriptText("s-second", old.Add(time.Hour), "you|Rename the flag."))
	for _, e := range []Event{startEvent("s-first", old, commit), startEvent("s-second", old.Add(time.Hour), commit)} {
		if err := appendEvent(repo, &e); err != nil {
			t.Fatal(err)
		}
	}
	return dev, repo
}

// twoCorrections is the extraction of s-first, and nothing for any other transcript.
func twoCorrections(cost float64) func(int, thinking.Request) (thinking.Reply, error) {
	return func(_ int, req thinking.Request) (thinking.Reply, error) {
		out := `{"corrections":[]}`
		if strings.Contains(req.Input, `"session":"s-first"`) {
			out = `{"corrections":[{"time":"","quote":"Never push without asking.","summary":"Ask before a push."},{"time":"","quote":"Use small commits.","summary":"Commit small."}]}`
		}
		return thinking.Reply{Output: []byte(out), Cost: cost}, nil
	}
}

// heldAndNone judges the first correction as held by the memory of the fixture, and the second as held by none.
func heldAndNone(cost float64) func(thinking.Request) (thinking.Reply, error) {
	return func(thinking.Request) (thinking.Reply, error) {
		return thinking.Reply{Output: []byte(`{"findings":[{"index":0,"kind":"holds","path":"dev/memories/ask-before-push.md","reason":"The memory says so."},{"index":1,"kind":"none","reason":"No memory holds it."}]}`), Cost: cost}, nil
	}
}

// nearestFixture gives both sessions eight memories. Five match "push". The first session
// reads a sixth at its last correction, and a seventh after it. The eighth exceeds the test context.
func nearestFixture(t *testing.T) (dev, repo string) {
	t.Helper()
	dev, repo = reflectFixture(t)
	for _, name := range []string{"push-a", "push-b", "push-c", "push-d", "frost", "later", "long"} {
		body := "Snow."
		if strings.HasPrefix(name, "push-") {
			body = "Push."
		}
		if name == "long" {
			body = strings.Repeat("Snow. ", 1000)
		}
		writeT(t, repo, "dev/memories/"+name+".md", memoryText(name, "Weather.", body))
	}
	gitT(t, repo, "add", "dev/memories")
	gitT(t, repo, "commit", "-m", "Add memories")
	commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	times, second := lineTimes(t, repo)
	last, err := time.Parse(time.RFC3339, times[2])
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{
		{Session: "s-first", Time: times[0], Command: "orient", Commit: commit},
		{Session: "s-second", Time: second, Command: "orient", Commit: commit},
		{Session: "s-first", Time: times[2], Command: "show", Outcome: "ok", Paths: []string{"dev/memories/frost.md"}, Commit: commit},
		{Session: "s-first", Time: last.Add(time.Second).Format(time.RFC3339), Command: "show", Outcome: "ok", Paths: []string{"dev/memories/later.md"}, Commit: commit},
	} {
		if err := appendEvent(repo, &e); err != nil {
			t.Fatal(err)
		}
	}
	return dev, repo
}

var nearestPaths = []string{
	"dev/memories/ask-before-push.md", "dev/memories/frost.md", "dev/memories/push-a.md",
	"dev/memories/push-b.md", "dev/memories/push-c.md", "dev/memories/push-d.md",
}

func checkJudgePaths(t *testing.T, req thinking.Request, want []string) {
	t.Helper()
	var in struct{ Memories []thinking.Memory }
	if err := json.Unmarshal([]byte(req.Input), &in); err != nil {
		t.Fatal(err)
	}
	if in.Memories == nil {
		t.Fatal("the judge must receive a JSON array of memories")
	}
	var paths []string
	for _, m := range in.Memories {
		paths = append(paths, m.Path)
	}
	if !slices.Equal(paths, want) {
		t.Errorf("judge got %v, want %v", paths, want)
	}
}

func TestReflectPass(t *testing.T) {
	t.Run("judges and records each session", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		calls := fakeAgent(t, twoCorrections(0.10), heldAndNone(0.05))
		out, err := run(t, dev, "", "reflect")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		for _, want := range []string{
			" ~/dev s-first: 2 corrections: 1 capture, 1 retrieval, 0 application, 0 stale, 0 unrelated.\n",
			" ~/dev s-secon: 0 corrections.\n",
			"Judged 2 sessions with 2 corrections: 1 capture, 1 retrieval, 0 application, 0 stale, 0 unrelated.\n",
			"The pass cost about 0.25 dollars at API prices. No session waits.\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if len(*calls) != 3 {
			t.Fatalf("%d calls of the agent, want 3: two extractions and one judge", len(*calls))
		}
		for i, req := range *calls {
			if req.Model != "sonnet" {
				t.Errorf("call %d uses the model %q, want sonnet", i, req.Model)
			}
		}
		if in := (*calls)[1].Input; !strings.Contains(in, "Push only when the person asks.") || !strings.Contains(in, "dev/memories/ask-before-push.md") {
			t.Errorf("the judge does not see the memory of the session:\n%s", in)
		}
		if _, err := os.Stat(filepath.Join(store.StateDir(repo), "reflect.jsonl")); err != nil {
			t.Errorf("the pass wrote no reflect.jsonl in the local state: %v", err)
		}
		records, err := readReflect(repo)
		if err != nil || len(records) != 2 {
			t.Fatalf("records = %+v, %v; want 2", records, err)
		}
		first := records[0]
		if first.Session != "s-first" || first.Agent != "claude" || first.Model != "sonnet" || first.Level != "~/dev" || first.Cost < 0.149 || first.Cost > 0.151 {
			t.Errorf("record = %+v", first)
		}
		if len(first.Corrections) != 2 || first.Corrections[0].Verdict != verdictRetrieval || first.Corrections[0].Path != "dev/memories/ask-before-push.md" ||
			first.Corrections[0].Quote != "Never push without asking." || first.Corrections[1].Verdict != verdictCapture {
			t.Errorf("corrections = %+v", first.Corrections)
		}
		if first.Corrections[0].Time == "" || first.Corrections[0].Time >= first.Corrections[1].Time {
			t.Errorf("the times %q and %q are not those of the lines, in order", first.Corrections[0].Time, first.Corrections[1].Time)
		}
		if second := records[1]; second.Session != "s-second" || second.Corrections == nil || len(second.Corrections) != 0 {
			t.Errorf("record of the session without a correction = %+v", second)
		}
		if e := last(t, repo); e.Command != "reflect" || e.Outcome != "ok" {
			t.Errorf("event = %+v, want an ok reflect", e)
		}

		// A second pass judges nothing new.
		fakeAgent(t, func(int, thinking.Request) (thinking.Reply, error) {
			t.Error("a second pass calls the agent")
			return thinking.Reply{}, nil
		}, nil)
		out, err = run(t, dev, "", "reflect")
		if err != nil || out != "No session waits for a reflect pass.\n" {
			t.Errorf("second pass: %v\n%s", err, out)
		}
	})

	t.Run("nearest memories and reads at the latest correction", func(t *testing.T) {
		dev, _ := nearestFixture(t)
		extract := twoCorrections(0)
		var judged int
		fakeAgent(t, func(n int, req thinking.Request) (thinking.Reply, error) {
			if strings.Contains(req.Input, `"session":"s-second"`) {
				return thinking.Reply{Output: []byte(`{"corrections":[{"quote":"Rename the flag.","summary":"Naming."}]}`)}, nil
			}
			return extract(n, req)
		}, func(req thinking.Request) (thinking.Reply, error) {
			judged++
			if strings.Contains(req.Input, "Rename the flag.") {
				checkJudgePaths(t, req, nil)
				return thinking.Reply{Output: []byte(`{"findings":[{"index":0,"kind":"none","reason":"No memory holds it."}]}`)}, nil
			}
			checkJudgePaths(t, req, nearestPaths)
			return thinking.Reply{Output: []byte(`{"findings":[{"index":0,"kind":"holds","path":"dev/memories/frost.md","reason":"Held."},{"index":1,"kind":"holds","path":"dev/memories/frost.md","reason":"Held."}]}`)}, nil
		})
		out, err := run(t, dev, "", "reflect")
		if err != nil || judged != 2 || !strings.Contains(out, "s-first: 2 corrections: 0 capture, 1 retrieval, 1 application") {
			t.Errorf("reflect with %d judge calls: %v\n%s", judged, err, out)
		}
	})

	t.Run("a correction before the first event of its session is before memory", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
		early := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Second)
		// memcore saw the session first between its two corrections.
		transcriptT(t, repo, "claude/p1/s-early.jsonl", transcriptText("s-early", early, "you|Never push without asking.", "you|Use small commits."))
		start := startEvent("s-early", early.Add(30*time.Second), commit)
		if err := appendEvent(repo, &start); err != nil {
			t.Fatal(err)
		}
		var judged []string
		fakeAgent(t, func(_ int, req thinking.Request) (thinking.Reply, error) {
			out := `{"corrections":[]}`
			if strings.Contains(req.Input, `"session":"s-early"`) {
				out = `{"corrections":[{"time":"","quote":"Never push without asking.","summary":"Ask before a push."},{"time":"","quote":"Use small commits.","summary":"Commit small."}]}`
			}
			return thinking.Reply{Output: []byte(out)}, nil
		}, func(req thinking.Request) (thinking.Reply, error) {
			judged = append(judged, req.Input)
			return thinking.Reply{Output: []byte(`{"findings":[{"index":0,"kind":"none","reason":"No memory holds it."}]}`)}, nil
		})
		out, err := run(t, dev, "", "reflect")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		for _, want := range []string{
			" ~/dev s-early: 2 corrections: 1 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated, 1 before memory.\n",
			"Judged 3 sessions with 2 corrections: 1 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated, 1 before memory.\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if len(judged) != 1 || strings.Contains(judged[0], "Never push without asking.") || !strings.Contains(judged[0], "Use small commits.") {
			t.Errorf("the judge got %q, want one call with only the later correction", judged)
		}
		records, err := readReflect(repo)
		if err != nil || len(records) != 3 || records[0].Session != "s-early" {
			t.Fatalf("records = %+v, %v; want s-early first of 3", records, err)
		}
		c := records[0].Corrections
		if len(c) != 2 || c[0].Verdict != verdictBefore || c[0].Path != "" || c[0].Reason != "" || c[1].Verdict != verdictCapture {
			t.Errorf("corrections = %+v, want before memory, then a capture miss", c)
		}
	})

	t.Run("a read before the correction is an application miss", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		at := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
		if err := appendEvent(repo, &Event{Time: at.Format(time.RFC3339), Session: "s-first", Command: "show", Outcome: "ok", Paths: []string{"dev/memories/ask-before-push.md"}}); err != nil {
			t.Fatal(err)
		}
		fakeAgent(t, twoCorrections(0), heldAndNone(0))
		out, err := run(t, dev, "", "reflect")
		if err != nil || !strings.Contains(out, "1 capture, 0 retrieval, 1 application") {
			t.Errorf("reflect: %v\n%s", err, out)
		}
		if !strings.Contains(out, "The pass cost about 0.00 dollars at API prices.") {
			t.Errorf("an agent that reports no cost:\n%s", out)
		}
	})

	t.Run("a session of Codex", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
		at := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Second)
		// The first correction comes before the orient, and the second after the show of the memory.
		header := fmt.Sprintf("{\"agent\":\"codex\",\"session\":\"t-codex\",\"dir\":%q}\n", dev)
		_, body, _ := strings.Cut(transcriptText("t-codex", at, "you|Never push without asking.", "you|Add a retry.", "you|Push only when I ask."), "\n")
		transcriptT(t, repo, "codex/p2/t-codex.jsonl", header+body)
		for _, e := range []Event{
			{Time: at.Add(30 * time.Second).Format(time.RFC3339), Session: "t-codex", Agent: "codex", Command: "orient", Level: "~/dev", Commit: commit, Outcome: "ok"},
			{Time: at.Add(90 * time.Second).Format(time.RFC3339), Session: "t-codex", Agent: "codex", Command: "show", Paths: []string{"dev/memories/ask-before-push.md"}, Commit: commit, Outcome: "ok"},
		} {
			if err := appendEvent(repo, &e); err != nil {
				t.Fatal(err)
			}
		}
		var judged []string
		fakeAgent(t, func(_ int, req thinking.Request) (thinking.Reply, error) {
			out := `{"corrections":[]}`
			if strings.Contains(req.Input, `"session":"t-codex"`) {
				out = `{"corrections":[{"time":"","quote":"Never push without asking.","summary":"Ask before a push."},{"time":"","quote":"Push only when I ask.","summary":"Ask before a push."}]}`
			}
			return thinking.Reply{Output: []byte(out)}, nil
		}, func(req thinking.Request) (thinking.Reply, error) {
			judged = append(judged, req.Input)
			return thinking.Reply{Output: []byte(`{"findings":[{"index":0,"kind":"holds","path":"dev/memories/ask-before-push.md","reason":"The memory says so."}]}`)}, nil
		})
		out, err := run(t, dev, "", "reflect")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		if want := " ~/dev t-codex: 2 corrections: 0 capture, 0 retrieval, 1 application, 0 stale, 0 unrelated, 1 before memory.\n"; !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
		if len(judged) != 1 || strings.Contains(judged[0], "Never push without asking.") || !strings.Contains(judged[0], "Push only when I ask.") {
			t.Errorf("the judge got %q, want one call with only the later correction", judged)
		}
		records, err := readReflect(repo)
		if err != nil || len(records) != 3 || records[0].Session != "t-codex" {
			t.Fatalf("records = %+v, %v; want t-codex first of 3", records, err)
		}
		r := records[0]
		if r.File != "codex/p2/t-codex.jsonl" || r.Level != "~/dev" || r.Dir != "~/dev" || r.Start != at.Add(30*time.Second).Format(time.RFC3339) {
			t.Errorf("record = %+v, want the level of the orient and the directory of the header", r)
		}
		if c := r.Corrections; len(c) != 2 || c[0].Verdict != verdictBefore || c[1].Verdict != verdictApplication || c[1].Path != "dev/memories/ask-before-push.md" {
			t.Errorf("corrections = %+v, want before memory, then an application miss", c)
		}
	})

	t.Run("the budget ends the pass", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		fakeAgent(t, twoCorrections(0.20), heldAndNone(0.07))
		out, err := run(t, dev, "", "reflect", "--budget", "0.25")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		for _, want := range []string{"Judged 1 session with 2 corrections", "about 0.27 dollars at API prices. 1 session waits.\n", "The pass stopped at its budget of 0.25 dollars.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if records, _ := readReflect(repo); len(records) != 1 {
			t.Errorf("%d records, want 1", len(records))
		}
	})

	t.Run("a pass finishes the session that it started", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		// The extraction alone passes the budget, and the judge of the same session still runs.
		calls := fakeAgent(t, twoCorrections(0.20), heldAndNone(0.07))
		out, err := run(t, dev, "", "reflect", "--budget", "0.15")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		for _, want := range []string{" ~/dev s-first: 2 corrections: 1 capture, 1 retrieval,", "Judged 1 session with 2 corrections", "about 0.27 dollars at API prices. 1 session waits.\n", "The pass stopped at its budget of 0.15 dollars.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		records, _ := readReflect(repo)
		if len(records) != 1 || records[0].Session != "s-first" || len(records[0].Corrections) != 2 {
			t.Errorf("records = %+v, want the first session with both corrections", records)
		}
		if len(*calls) != 2 {
			t.Errorf("%d calls, want 2: the extraction and the judge of the first session", len(*calls))
		}
	})

	t.Run("a cost equal to the budget ends the pass", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		fakeAgent(t, twoCorrections(0.25), heldAndNone(0.25)) // two costs that add up to 0.5 exactly
		out, err := run(t, dev, "", "reflect", "--budget", "0.50")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		for _, want := range []string{"Judged 1 session with 2 corrections", "about 0.50 dollars at API prices. 1 session waits.\n", "The pass stopped at its budget of 0.50 dollars.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if records, _ := readReflect(repo); len(records) != 1 {
			t.Errorf("%d records, want 1", len(records))
		}
	})

	t.Run("a cost below the budget starts the next session", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		fakeAgent(t, twoCorrections(0.20), heldAndNone(0.07))
		out, err := run(t, dev, "", "reflect", "--budget", "0.30")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		for _, want := range []string{"Judged 2 sessions with 2 corrections", "about 0.47 dollars at API prices. No session waits.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "The pass stopped") {
			t.Errorf("the pass names a stop, and it judged every session:\n%s", out)
		}
		if records, _ := readReflect(repo); len(records) != 2 {
			t.Errorf("%d records, want 2", len(records))
		}
	})

	t.Run("the budget never splits the files of one session", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		events, _, err := readLog(repo)
		if err != nil {
			t.Fatal(err)
		}
		var old time.Time
		for _, e := range events {
			if e.Session == "s-first" && e.Hook == "session-start" {
				old = eventTime(e)
			}
		}
		transcriptT(t, repo, "claude/p1/s-first.0123abcd.jsonl", transcriptText("s-first", old, "you|Add a retry.", "you|Never push without asking.", "you|Use small commits.", "you|A version beside."))
		calls := fakeAgent(t, twoCorrections(0.20), heldAndNone(0.07))
		// The first file of the session passes the budget, and the pass still reads the second file.
		out, err := run(t, dev, "", "reflect", "--budget", "0.15")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		for _, want := range []string{"Judged 1 session with 2 corrections", "about 0.54 dollars at API prices. 1 session waits.\n", "The pass stopped at its budget of 0.15 dollars.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if records, _ := readReflect(repo); len(records) != 2 || records[0].Session != "s-first" || records[1].Session != "s-first" {
			t.Errorf("records = %+v, want both files of the first session", records)
		}
		if len(*calls) != 4 {
			t.Errorf("%d calls, want 4: an extraction and a judge for each file of the session", len(*calls))
		}
	})

	t.Run("the setting of the budget and of the model", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		gitT(t, repo, "config", "memcore.reflectbudget", "0.75")
		gitT(t, repo, "config", "memcore.reflectmodel", "haiku")
		// The first session costs 0.80 dollars: more than the setting, and less than the default of 2 dollars.
		calls := fakeAgent(t, twoCorrections(0.50), heldAndNone(0.30))
		out, err := run(t, dev, "", "reflect")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		if !strings.Contains(out, "The pass stopped at its budget of 0.75 dollars.\n") || !strings.Contains(out, "1 session waits.\n") {
			t.Errorf("the pass does not stop at the budget of the setting:\n%s", out)
		}
		if req := (*calls)[0]; req.Model != "haiku" {
			t.Errorf("request = model %q, want haiku", req.Model)
		}
		gitT(t, repo, "config", "memcore.reflectbudget", "much")
		if _, err := run(t, dev, "", "reflect"); err == nil || !strings.Contains(err.Error(), "memcore.reflectbudget") {
			t.Errorf("a budget that is no number: %v", err)
		}
		for _, bad := range []string{"NaN", "Inf", "-Inf", "0", "-1"} {
			gitT(t, repo, "config", "memcore.reflectbudget", bad)
			if _, err := run(t, dev, "", "reflect"); err == nil || !strings.Contains(err.Error(), "memcore.reflectbudget") {
				t.Errorf("the setting %s: %v, want an error", bad, err)
			}
			if _, err := run(t, dev, "", "reflect", "--budget", bad); err == nil || !strings.Contains(err.Error(), "--budget") {
				t.Errorf("--budget %s: %v, want an error", bad, err)
			}
		}
	})

	t.Run("--sessions ends the pass", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		// A version beside the transcript of the first session belongs to that session.
		// It repeats the lines of the transcript at their times, so it starts at the logged start of the session.
		events, _, err := readLog(repo)
		if err != nil {
			t.Fatal(err)
		}
		var old time.Time
		for _, e := range events {
			if e.Session == "s-first" && e.Hook == "session-start" {
				old = eventTime(e)
			}
		}
		transcriptT(t, repo, "claude/p1/s-first.0123abcd.jsonl", transcriptText("s-first", old, "you|Add a retry.", "you|Never push without asking.", "you|Use small commits.", "you|A version beside."))
		calls := fakeAgent(t, twoCorrections(0.10), heldAndNone(0.05))
		out, err := run(t, dev, "", "reflect", "--sessions", "1")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		if n := strings.Count(out, " s-first: "); n != 1 || !strings.Contains(out, " ~/dev s-first: 2 corrections: 1 capture, 1 retrieval,") {
			t.Errorf("the session has %d lines, want one line that counts each correction once:\n%s", n, out)
		}
		for _, want := range []string{"Judged 1 session with 2 corrections: 1 capture, 1 retrieval,", "1 session waits.\n", "The pass stopped after 1 session, as --sessions says.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if records, _ := readReflect(repo); len(records) != 2 || records[0].Session != "s-first" || records[1].Session != "s-first" {
			t.Errorf("records = %+v, want both files of the first session", records)
		}
		if len(*calls) != 4 {
			t.Errorf("%d calls, want 4: an extraction and a judge for each file of the session", len(*calls))
		}
		for _, bad := range []string{"0", "-1", "two"} {
			if _, err := run(t, dev, "", "reflect", "--sessions", bad); err == nil || !strings.Contains(err.Error(), "--sessions") || !strings.Contains(err.Error(), "memcore help reflect") {
				t.Errorf("--sessions %s: %v, want a usage error", bad, err)
			}
		}
		if _, err := run(t, dev, "", "reflect", "now"); err == nil || !strings.Contains(err.Error(), "usage: memcore reflect") {
			t.Errorf("an argument: %v, want a usage error", err)
		}
	})

	t.Run("an error of the agent keeps the finished sessions", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		fakeAgent(t, func(n int, req thinking.Request) (thinking.Reply, error) {
			if n == 2 {
				return thinking.Reply{Cost: 0.01}, errors.New("run the agent: the agent reports an error: Not logged in: check that claude is logged in")
			}
			return twoCorrections(0.10)(n, req)
		}, heldAndNone(0.05))
		out, err := run(t, dev, "", "reflect")
		if err == nil || !strings.Contains(err.Error(), "Not logged in") || !strings.Contains(err.Error(), "keeps the sessions that it finished") {
			t.Fatalf("err = %v, want the cause", err)
		}
		if !strings.Contains(out, "Judged 1 session with 2 corrections") || !strings.Contains(out, "about 0.16 dollars at API prices. 1 session waits.") {
			t.Errorf("output:\n%s", out)
		}
		if records, _ := readReflect(repo); len(records) != 1 || records[0].Session != "s-first" {
			t.Errorf("records = %+v, want the first session", records)
		}
	})

	t.Run("the line of a session outside home names its directory", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
		old := time.Now().Add(-50 * time.Hour).UTC().Truncate(time.Second)
		transcriptT(t, repo, "claude/p2/s-away.jsonl", transcriptText("s-away", old, "you|Run the checks."))
		start := startEvent("s-away", old, commit)
		start.Level, start.Dir = "", "/tmp/work/checks"
		if err := appendEvent(repo, &start); err != nil {
			t.Fatal(err)
		}
		fakeAgent(t, twoCorrections(0), heldAndNone(0))
		out, err := run(t, dev, "", "reflect")
		if err != nil {
			t.Fatalf("reflect: %v\n%s", err, out)
		}
		if want := " /tmp/work/checks s-away: 0 corrections.\n"; !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	})

	t.Run("refuses an agent", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		calls := fakeAgent(t, twoCorrections(0), heldAndNone(0))
		t.Setenv("CLAUDECODE", "1")
		_, err := run(t, dev, "", "reflect")
		if err == nil || !strings.Contains(err.Error(), "only the person runs a reflect pass") {
			t.Errorf("err = %v, want a refusal", err)
		}
		if records, _ := readReflect(repo); len(*calls) != 0 || len(records) != 0 {
			t.Errorf("%d calls and %d records, want none", len(*calls), len(records))
		}
	})

	t.Run("an unknown agent", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		gitT(t, repo, "config", "memcore.reflectagent", "llama")
		calls := fakeAgent(t, twoCorrections(0), heldAndNone(0))
		_, err := run(t, dev, "", "reflect")
		if err == nil || !strings.Contains(err.Error(), `"llama"`) || !strings.Contains(err.Error(), "the known agents are claude") || len(*calls) != 0 {
			t.Errorf("err = %v with %d calls, want the known agents and no call", err, len(*calls))
		}
	})

	t.Run("a pass that already runs", func(t *testing.T) {
		dev, repo := reflectFixture(t)
		unlock, ok, err := store.TryLockFile(repo, "reflect.lock")
		if err != nil || !ok {
			t.Fatalf("lock: %v, %v", ok, err)
		}
		defer unlock()
		calls := fakeAgent(t, twoCorrections(0), heldAndNone(0))
		if _, err := run(t, dev, "", "reflect"); err == nil || !strings.Contains(err.Error(), "a reflect pass already runs") || len(*calls) != 0 {
			t.Errorf("err = %v with %d calls, want a refusal", err, len(*calls))
		}
	})
}

func TestReflectList(t *testing.T) {
	dev, repo := reflectFixture(t)
	t.Setenv("CLAUDECODE", "1") // an agent may list
	out, err := run(t, dev, "", "reflect", "--list")
	if err != nil || out != "No reflect pass has judged a session yet. The person runs one with memcore reflect.\n" {
		t.Fatalf("without records: %v\n%s", err, out)
	}
	write := func(r reflectRecord) {
		t.Helper()
		if err := appendReflect(repo, r); err != nil {
			t.Fatal(err)
		}
	}
	held := reflectCorrection{Time: "2026-11-18T10:02:03Z", Quote: "Never push\n without asking.", Summary: "Ask before a push.", Verdict: verdictRetrieval, Path: "dev/memories/ask-before-push.md", Reason: "The memory says so."}
	small := reflectCorrection{Time: "2026-11-18T10:05:00Z", Quote: "Use small commits.", Summary: "Commit small.", Verdict: verdictCapture, Reason: "No memory holds it."}
	later := reflectCorrection{Time: "2026-11-19T09:00:00Z", Quote: "Not that file.", Summary: "A detail.", Verdict: verdictUnrelated}
	early := reflectCorrection{Time: "2026-11-19T07:30:00Z", Quote: "No tabs here.", Summary: "Use spaces.", Verdict: verdictBefore}
	applied := reflectCorrection{Time: "2026-11-19T09:10:00Z", Quote: "I said: ask first.", Summary: "Ask before a push.", Verdict: verdictApplication, Path: "dev/memories/ask-before-push.md", Reason: "The session read it."}
	stale := reflectCorrection{Time: "2026-11-19T09:20:00Z", Quote: "We use tabs now.", Summary: "Use tabs.", Verdict: verdictStale, Path: "dev/memories/spaces.md", Reason: "The memory says spaces."}
	write(reflectRecord{Session: "s-first", File: "claude/p/s-first.jsonl", Blob: "b1", Level: "~/dev", Start: "2026-11-18T10:00:00Z", Corrections: []reflectCorrection{small, held}})
	write(reflectRecord{Session: "s-first", File: "claude/p/s-first.0123abcd.jsonl", Blob: "b2", Level: "~/dev", Start: "2026-11-18T10:00:00Z", Corrections: []reflectCorrection{held}})
	write(reflectRecord{Session: "s-empty", File: "claude/p/s-empty.jsonl", Blob: "b3", Level: "~", Start: "2026-11-18T12:00:00Z", Corrections: []reflectCorrection{}})
	write(reflectRecord{Session: "s-later", File: "claude/p/s-later.jsonl", Blob: "b4", Level: "~", Start: "2026-11-19T08:00:00Z", Corrections: []reflectCorrection{later, early, applied, stale}})

	// Each head line starts with the short id of its correction.
	id := func(session string, c reflectCorrection) string { return short(correctionID(session, c.Time, c.Quote)) }
	fill := strings.NewReplacer("{early}", id("s-later", early), "{later}", id("s-later", later), "{applied}", id("s-later", applied), "{stale}", id("s-later", stale), "{held}", id("s-first", held), "{small}", id("s-first", small))
	const (
		totals      = "6 corrections in 3 judged sessions: 1 capture, 1 retrieval, 1 application, 1 stale, 1 unrelated, 1 before memory."
		earlyText   = "\n{early} 2026-11-19 07:30 ~ s-later: before memory\n  You: No tabs here.\n  Rule: Use spaces.\n"
		laterText   = "\n{later} 2026-11-19 09:00 ~ s-later: unrelated to memory\n  You: Not that file.\n  Rule: A detail.\n"
		appliedText = "\n{applied} 2026-11-19 09:10 ~ s-later: application miss, dev/memories/ask-before-push.md\n  You: I said: ask first.\n  Rule: Ask before a push.\n  Why: The session read it.\n"
		staleText   = "\n{stale} 2026-11-19 09:20 ~ s-later: stale memory, dev/memories/spaces.md\n  You: We use tabs now.\n  Rule: Use tabs.\n  Why: The memory says spaces.\n"
		heldText    = "\n{held} 2026-11-18 10:02 ~/dev s-first: retrieval miss, dev/memories/ask-before-push.md\n  You: Never push without asking.\n  Rule: Ask before a push.\n  Why: The memory says so.\n"
		smallText   = "\n{small} 2026-11-18 10:05 ~/dev s-first: capture miss\n  You: Use small commits.\n  Rule: Commit small.\n  Why: No memory holds it.\n"
	)
	calls := fakeAgent(t, twoCorrections(0), heldAndNone(0))
	list := func(want string, args ...string) {
		t.Helper()
		out, err := run(t, dev, "", append([]string{"reflect"}, args...)...)
		if want = fill.Replace(want); err != nil || out != want {
			t.Errorf("reflect %v: %v\n got %s\nwant %s", args, err, out, want)
		}
	}
	// A miss with no answer is open. The list prints no other correction, and --all prints each one.
	list(totals+" 4 open.\n"+appliedText+staleText+heldText+smallText, "--list")
	list(totals+" 4 open.\n"+earlyText+laterText+appliedText+staleText+heldText+smallText, "--list", "--all")
	list(totals+" 4 open.\n"+earlyText+laterText+appliedText+staleText+heldText+smallText, "--all", "--list")

	const memory = "dev/memories/ask-before-push.md"
	answer := func(c reflectCorrection, session string, args ...string) {
		t.Helper()
		if out, err := run(t, dev, "", append([]string{"reflect", "--answer", correctionID(session, c.Time, c.Quote)}, args...)...); err != nil {
			t.Fatalf("reflect --answer: %v\n%s", err, out)
		}
	}
	answer(small, "s-first", "--reason", "It concerns only its task.")
	list(totals+" 3 open.\n"+appliedText+staleText+heldText, "--list")
	list(totals+" 3 open.\n"+earlyText+laterText+appliedText+staleText+heldText+smallText+"  Answer: It concerns only its task.\n", "--list", "--all")

	// An answer shows its path, its reason, or both. With every miss answered, no correction is open.
	answer(held, "s-first", "--with", memory)
	answer(applied, "s-later", "--with", memory, "--reason", "The body states the rule first.")
	answer(stale, "s-later", "--reason", "The memory went")
	list(totals+" 0 open.\nNo correction is open.\n", "--list")
	list(totals+" 0 open.\n"+earlyText+laterText+appliedText+"  Answer: "+memory+": The body states the rule first.\n"+staleText+"  Answer: The memory went\n"+heldText+"  Answer: "+memory+"\n"+smallText+"  Answer: It concerns only its task.\n", "--list", "--all")

	if len(*calls) != 0 {
		t.Errorf("--list called the agent %d times", len(*calls))
	}
	if records, _ := readReflect(repo); len(records) != 4 {
		t.Errorf("--list changed the records: %d, want 4", len(records))
	}
	for _, args := range [][]string{{"--list", "--budget", "1"}, {"--list", "--sessions", "2"}, {"--list", "--budget="}, {"--sessions=", "--list"}, {"--list", "--all", "--budget", "1"}} {
		if _, err := run(t, dev, "", append([]string{"reflect"}, args...)...); err == nil || !strings.Contains(err.Error(), "usage: memcore reflect --list") {
			t.Errorf("reflect %v: %v, want a usage error", args, err)
		}
	}
	// --all without --list starts no pass, also for the person at a terminal.
	t.Setenv("CLAUDECODE", "")
	for _, args := range [][]string{{"--all"}, {"--all", "--sessions", "1"}, {"--answer", id("s-first", small), "--reason", "r", "--all"}} {
		if _, err := run(t, dev, "", append([]string{"reflect"}, args...)...); err == nil || !strings.Contains(err.Error(), "usage: memcore reflect") || !strings.Contains(err.Error(), "--all") {
			t.Errorf("reflect %v: %v, want a usage error that names --all", args, err)
		}
	}
	if records, _ := readReflect(repo); len(*calls) != 0 || len(records) != 4 {
		t.Errorf("%d calls and %d records, want no call and 4 records: --all may start no pass", len(*calls), len(records))
	}

	// A session outside home has no level. Its head line names its directory, or says that it ran outside home.
	away := reflectCorrection{Time: "2026-11-20T10:00:00Z", Quote: "Run the checks first.", Summary: "Run the checks first.", Verdict: verdictCapture}
	nowhere := reflectCorrection{Time: "2026-11-20T11:00:00Z", Quote: "Keep the log.", Summary: "Keep the log.", Verdict: verdictCapture}
	write(reflectRecord{Session: "s-away", File: "claude/p/s-away.jsonl", Blob: "b5", Dir: "/tmp/work/checks", Start: "2026-11-20T09:00:00Z", Corrections: []reflectCorrection{away}})
	write(reflectRecord{Session: "s-nowhere", File: "claude/p/s-nowhere.jsonl", Blob: "b6", Start: "2026-11-20T10:30:00Z", Corrections: []reflectCorrection{nowhere}})
	t.Setenv("CLAUDECODE", "1")
	out, err = run(t, dev, "", "reflect", "--list")
	if err != nil {
		t.Fatalf("reflect --list: %v\n%s", err, out)
	}
	for _, want := range []string{
		"\n" + id("s-away", away) + " 2026-11-20 10:00 /tmp/work/checks s-away: capture miss\n",
		"\n" + id("s-nowhere", nowhere) + " 2026-11-20 11:00 outside home s-nowhe: capture miss\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("reflect --list lacks %q:\n%s", want, out)
		}
	}
}

func TestReflectListFollowsMoves(t *testing.T) {
	const (
		memory = "dev/memories/ask-before-push.md"
		topic  = "dev/memories/git/ask-before-push.md"
		newest = "dev/memories/review/ask-before-push.md"
	)
	base := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	at := func(d time.Duration) string { return base.Add(d).Format(time.RFC3339) }
	writeEvents := func(t *testing.T, repo string, events []Event) {
		t.Helper()
		var lines []string
		for _, e := range events {
			line, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(line))
		}
		writeLog(t, repo, lines...)
	}

	// moved makes the fixture and moves its memory with a real memcore move, once for each target. Then it gives
	// the moves the times base, base+1h and so on in the use log, so each answer has a known place among them.
	moved := func(t *testing.T, targets ...string) (dev, repo string, moves []Event) {
		t.Helper()
		dev, repo = reflectFixture(t)
		for _, topic := range []string{"memories/git", "memories/review"} {
			if out, err := run(t, dev, "", "draft", "topic", topic, "--description", "Read when you test a move."); err != nil {
				t.Fatalf("draft topic: %v\n%s", err, out)
			}
		}
		from := memory
		for _, to := range targets {
			if out, err := run(t, dev, "", "move", from, to, "--reason", "It belongs there."); err != nil {
				t.Fatalf("move: %v\n%s", err, out)
			}
			from = to
		}
		events, _, err := readLog(repo)
		if err != nil {
			t.Fatal(err)
		}
		for i := range events {
			if events[i].Command == "move" && events[i].Outcome == "ok" {
				events[i].Time = at(time.Duration(len(moves)) * time.Hour)
				moves = append(moves, events[i])
			}
		}
		if len(moves) != len(targets) {
			t.Fatalf("%d moves in the use log, want %d", len(moves), len(targets))
		}
		writeEvents(t, repo, events)
		return dev, repo, moves
	}

	// answered records one correction for each answer, and the answer with its path and time. It returns the
	// print of reflect --list --all.
	type answer struct{ quote, path, answered string }
	answered := func(t *testing.T, dev, repo string, answers ...answer) string {
		t.Helper()
		var corrections []reflectCorrection
		var records []reflectAnswer
		for i, a := range answers {
			c := reflectCorrection{Time: at(time.Duration(i-48) * time.Minute), Quote: a.quote, Summary: a.quote, Verdict: verdictCapture}
			corrections = append(corrections, c)
			records = append(records, reflectAnswer{ID: correctionID("s-first", c.Time, c.Quote), Session: "s-first", Time: c.Time, Path: a.path, Answered: a.answered})
		}
		if err := appendReflect(repo, reflectRecord{Session: "s-first", File: "claude/p/s-first.jsonl", Blob: "b1", Level: "~/dev", Start: at(-time.Hour), Corrections: corrections}); err != nil {
			t.Fatal(err)
		}
		if err := appendAnswers(repo, records); err != nil {
			t.Fatal(err)
		}
		out, err := run(t, dev, "", "reflect", "--list", "--all")
		if err != nil {
			t.Fatalf("reflect --list --all: %v\n%s", err, out)
		}
		return out
	}
	shows := func(t *testing.T, out, quote, want string) {
		t.Helper()
		if text := "  You: " + quote + "\n  Rule: " + quote + "\n  Answer: " + want + "\n"; !strings.Contains(out, text) {
			t.Errorf("the answer of %q is not %s:\n%s", quote, want, out)
		}
	}

	t.Run("a move carries an answer from before it", func(t *testing.T) {
		dev, repo, _ := moved(t, topic)
		out := answered(t, dev, repo,
			answer{"Answered before the move.", memory, at(-time.Hour)},
			answer{"Answered after the move.", topic, at(30 * time.Minute)},
			answer{"Answered in the second of the move.", topic, at(0)},
			answer{"Answered at no readable time.", memory, "yesterday"})
		shows(t, out, "Answered before the move.", topic)
		shows(t, out, "Answered after the move.", topic)
		shows(t, out, "Answered in the second of the move.", topic)
		shows(t, out, "Answered at no readable time.", memory)
	})

	t.Run("a second move carries the answer on", func(t *testing.T) {
		dev, repo, _ := moved(t, topic, newest)
		out := answered(t, dev, repo,
			answer{"Answered before both moves.", memory, at(-time.Hour)},
			answer{"Answered between the moves.", topic, at(30 * time.Minute)},
			answer{"Answered after both moves.", newest, at(2 * time.Hour)})
		shows(t, out, "Answered before both moves.", newest)
		shows(t, out, "Answered between the moves.", newest)
		shows(t, out, "Answered after both moves.", newest)
	})

	t.Run("an answer older than the first line of the use log", func(t *testing.T) {
		dev, repo, moves := moved(t, topic)
		writeEvents(t, repo, moves) // the move is the only line of the use log
		out := answered(t, dev, repo, answer{"Answered before the log began.", memory, at(-time.Hour)})
		shows(t, out, "Answered before the log began.", topic)
	})

	t.Run("a dropped move gives the answer its old path back", func(t *testing.T) {
		dev, repo, moves := moved(t, topic)
		drop := Event{Time: at(time.Hour), Command: "drop", Outcome: "ok", Dropped: []string{moves[0].Made[0]}}
		if err := appendEvent(repo, &drop); err != nil {
			t.Fatal(err)
		}
		out := answered(t, dev, repo,
			answer{"Answered before the move.", memory, at(-time.Hour)},
			answer{"Answered between the move and the drop.", topic, at(30 * time.Minute)})
		shows(t, out, "Answered before the move.", memory)
		shows(t, out, "Answered between the move and the drop.", memory)
	})
}

func TestReflectListCopiedTurns(t *testing.T) {
	const (
		turn   = "Use the project template. Include the impact."
		memory = "dev/memories/ask-before-push.md"
		topic  = "dev/memories/git/ask-before-push.md"
	)
	base := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Hour)
	stamp := base.Add(time.Minute).Format(time.RFC3339) // the time of the turn in every blob that blob makes
	day := base.Add(time.Minute).Format("2006-01-02 15:04")
	hours := func(n int) string { return base.Add(time.Duration(n) * time.Hour).Format(time.RFC3339) }
	later := base.Add(2 * time.Minute).Format(time.RFC3339) // the time of the third line of a blob, a second turn
	nextMinute := base.Add(2 * time.Minute).Format("2006-01-02 15:04")

	// setup makes the fixture. An agent may list, and no call of the agent is allowed.
	setup := func(t *testing.T) (dev, repo string, calls *[]thinking.Request) {
		t.Helper()
		dev, repo = reflectFixture(t)
		t.Setenv("CLAUDECODE", "1")
		return dev, repo, fakeAgent(t, twoCorrections(0), heldAndNone(0))
	}
	// blob stores a transcript with a first line of its own, the turn, and then the lines of its tail.
	blob := func(t *testing.T, repo, session, first string, tail ...string) string {
		t.Helper()
		return transcriptT(t, repo, "claude/p/"+session+".jsonl", transcriptText(session, base, append([]string{"you|" + first, "you|" + turn}, tail...)...))
	}
	correction := func(quote, summary, verdict string) reflectCorrection {
		return reflectCorrection{Time: stamp, Quote: quote, Summary: summary, Verdict: verdict}
	}
	save := func(t *testing.T, repo, session, level, blob string, start int, cs ...reflectCorrection) {
		t.Helper()
		r := reflectRecord{Session: session, File: "claude/p/" + session + ".jsonl", Blob: blob, Level: level, Start: hours(start), Corrections: cs}
		if err := appendReflect(repo, r); err != nil {
			t.Fatal(err)
		}
	}
	id := func(session string, c reflectCorrection) string { return short(correctionID(session, c.Time, c.Quote)) }
	listed := func(t *testing.T, dev string, args ...string) string {
		t.Helper()
		out, err := run(t, dev, "", append([]string{"reflect", "--list"}, args...)...)
		if err != nil {
			t.Fatalf("reflect --list %v: %v\n%s", args, err, out)
		}
		return out
	}
	// group, child and single print the expected blocks.
	titled := func(title, when, shared string) string {
		text := "\n" + when + "  " + title + "\n"
		if shared != "" {
			return text + "  Quote: " + shared + "\n"
		}
		return text + "  You: " + turn + "\n"
	}
	group := func(shared string) string {
		return titled("Copied turn", day, shared)
	}
	child := func(session, level string, c reflectCorrection, label string, lines ...string) string {
		text := fmt.Sprintf("  %s  %s  session %s: %s\n", id(session, c), level, session, label)
		for _, l := range lines {
			text += "    " + l + "\n"
		}
		return text
	}
	single := func(session, level string, c reflectCorrection, label string, lines ...string) string {
		text := fmt.Sprintf("\n%s %s %s %s: %s\n", id(session, c), day, level, short(session), label)
		for _, l := range lines {
			text += "  " + l + "\n"
		}
		return text
	}

	t.Run("different excerpts, verdicts, answers and a singleton between", func(t *testing.T) {
		dev, repo, calls := setup(t)
		if out, err := run(t, dev, "", "draft", "topic", "memories/git", "--description", "Read when you test a move."); err != nil {
			t.Fatalf("draft topic: %v\n%s", err, out)
		}
		if out, err := run(t, dev, "", "move", memory, topic, "--reason", "It belongs there."); err != nil {
			t.Fatalf("move: %v\n%s", err, out)
		}
		a := correction("Use the project template.", "Use the project template.", verdictBefore)
		b := correction("Include\n the impact.", "State the impact in the description.", verdictRetrieval)
		b.Path, b.Reason = memory, "The session did not read the memory."
		lone := reflectCorrection{Time: hours(1), Quote: "Keep the log.", Summary: "Keep the log.", Verdict: verdictCapture, Reason: "No memory holds it."}
		save(t, repo, "s-shop", "~/dev/shop", blob(t, repo, "s-shop", "Open the file."), 30, a)
		save(t, repo, "s-lone", "~", "unused", 20, lone)
		save(t, repo, "s-api", "~/dev/api", blob(t, repo, "s-api", "Open the file.", "you|Later I go another way."), 10, b)
		if err := appendAnswers(repo, []reflectAnswer{{ID: correctionID("s-api", b.Time, b.Quote), Session: "s-api", Time: b.Time, Path: memory, Answered: base.Format(time.RFC3339)}}); err != nil {
			t.Fatal(err)
		}

		totals := "3 corrections in 3 judged sessions: 1 capture, 1 retrieval, 0 application, 0 stale, 0 unrelated, 1 before memory. 1 open.\n"
		loneText := "\n" + id("s-lone", lone) + " " + base.Add(time.Hour).Format("2006-01-02 15:04") + " ~ s-lone: capture miss\n  You: Keep the log.\n  Rule: Keep the log.\n  Why: No memory holds it.\n"
		want := totals + group("") +
			child("s-shop", "~/dev/shop", a, "before memory", "Quote: Use the project template.", "Rule: Use the project template.") +
			child("s-api", "~/dev/api", b, "retrieval miss, "+memory, "Quote: Include the impact.", "Rule: State the impact in the description.", "Why: The session did not read the memory.", "Answer: "+topic) +
			loneText
		if got := listed(t, dev, "--all"); got != want {
			t.Errorf("--list --all:\n got %s\nwant %s", got, want)
		}
		// Only the singleton is open in this fixture.
		if got := listed(t, dev); got != totals+loneText {
			t.Errorf("--list:\n got %s\nwant %s", got, totals+loneText)
		}

		// Each child answers on its own, with the id that the list printed.
		if out, err := run(t, dev, "", "reflect", "--answer", id("s-shop", a), "--reason", "Handled."); err != nil {
			t.Fatalf("--answer: %v\n%s", err, out)
		}
		got := listed(t, dev, "--all")
		for _, line := range []string{"    Rule: Use the project template.\n    Answer: Handled.\n", "    Answer: " + topic + "\n"} {
			if strings.Count(got, line) != 1 {
				t.Errorf("--list --all after one answer lacks %q:\n%s", line, got)
			}
		}
		if strings.Count(got, "Answer:") != 2 {
			t.Errorf("--list --all after one answer: want two answers:\n%s", got)
		}
		if len(*calls) != 0 {
			t.Errorf("the list called the agent %d times", len(*calls))
		}
	})

	t.Run("one shared excerpt", func(t *testing.T) {
		dev, repo, _ := setup(t)
		x := correction("Include the impact.", "State the impact.", verdictCapture)
		y := correction("Include  the\nimpact.", "Say what changes.", verdictUnrelated)
		z := correction("Include the impact.", "Name the impact.", verdictCapture)
		z.Reason = "No memory holds it."
		save(t, repo, "s-x", "~/dev", blob(t, repo, "s-x", "Open the file."), 30, x)
		save(t, repo, "s-y", "~", blob(t, repo, "s-y", "Open the file.", "you|One more thing."), 20, y)
		save(t, repo, "s-z", "~/dev", blob(t, repo, "s-z", "Open the file.", "you|Something else.", "you|And more."), 10, z)
		want := "3 corrections in 3 judged sessions: 2 capture, 0 retrieval, 0 application, 0 stale, 1 unrelated. 2 open.\n" + group("Include the impact.") +
			child("s-x", "~/dev", x, "capture miss", "Rule: State the impact.") +
			child("s-y", "~", y, "unrelated to memory", "Rule: Say what changes.") +
			child("s-z", "~/dev", z, "capture miss", "Rule: Name the impact.", "Why: No memory holds it.")
		// The open list drops the unrelated child, and the two capture misses still share the turn.
		plain := "3 corrections in 3 judged sessions: 2 capture, 0 retrieval, 0 application, 0 stale, 1 unrelated. 2 open.\n" + group("Include the impact.") +
			child("s-x", "~/dev", x, "capture miss", "Rule: State the impact.") +
			child("s-z", "~/dev", z, "capture miss", "Rule: Name the impact.", "Why: No memory holds it.")
		if got := listed(t, dev); got != plain {
			t.Errorf("--list:\n got %s\nwant %s", got, plain)
		}
		if got := listed(t, dev, "--all"); got != want {
			t.Errorf("--list --all:\n got %s\nwant %s", got, want)
		}
	})

	// Two sessions copied the turn, and each has one open correction.
	for _, c := range []struct {
		name     string
		sessions [2]string
		names    [2]string // the session of each child as the list prints it
		quotes   [2]string
		rules    [2]string
		shared   string // the excerpt under the turn
	}{
		{"a quote that covers the whole turn is not repeated", [2]string{"s-x", "s-y"}, [2]string{"s-x", "s-y"},
			[2]string{turn, "Use the project template.\n Include the impact."}, [2]string{"Use the template.", "State the impact."}, ""},
		{"colliding session abbreviations grow inside a group", [2]string{"4e14814a-one", "4e14814b-two"}, [2]string{"4e14814a", "4e14814b"},
			[2]string{"Include the impact.", "Include the impact."}, [2]string{"State the impact.", "Say what changes."}, "Include the impact."},
	} {
		t.Run(c.name, func(t *testing.T) {
			dev, repo, _ := setup(t)
			want := "2 corrections in 2 judged sessions: 2 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated. 2 open.\n" + group(c.shared)
			for k, session := range c.sessions {
				x := correction(c.quotes[k], c.rules[k], verdictCapture)
				save(t, repo, session, "~/dev", blob(t, repo, session, "Open the file."), 20-10*k, x)
				want += fmt.Sprintf("  %s  ~/dev  session %s: capture miss\n    Rule: %s\n", id(session, x), c.names[k], c.rules[k])
			}
			if got := listed(t, dev, "--all"); got != want {
				t.Errorf("--list --all:\n got %s\nwant %s", got, want)
			}
		})
	}

	t.Run("two corrections of one session join a copy", func(t *testing.T) {
		dev, repo, _ := setup(t)
		// The first correction quotes the whole turn, so it prints no Quote of its own, and the turn prints once.
		first := correction(turn, "Use the template.", verdictBefore)
		second := correction("Include the impact.", "State the impact.", verdictCapture)
		other := correction("Include the impact.", "Say what changes.", verdictUnrelated)
		save(t, repo, "s-x", "~/dev", blob(t, repo, "s-x", "Open the file."), 20, first, second)
		save(t, repo, "s-y", "~/dev", blob(t, repo, "s-y", "Open the file."), 10, other)
		want := "3 corrections in 2 judged sessions: 1 capture, 0 retrieval, 0 application, 0 stale, 1 unrelated, 1 before memory. 1 open.\n" + group("") +
			child("s-x", "~/dev", first, "before memory", "Rule: Use the template.") +
			child("s-x", "~/dev", second, "capture miss", "Quote: Include the impact.", "Rule: State the impact.") +
			child("s-y", "~/dev", other, "unrelated to memory", "Quote: Include the impact.", "Rule: Say what changes.")
		if got := listed(t, dev, "--all"); got != want {
			t.Errorf("--list --all:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("one session alone stays flat", func(t *testing.T) {
		dev, repo, _ := setup(t)
		first := correction("Use the project template.", "Use the template.", verdictCapture)
		second := correction("Include the impact.", "State the impact.", verdictCapture)
		save(t, repo, "s-x", "~/dev", blob(t, repo, "s-x", "Open the file."), 20, first, second)
		want := "2 corrections in 1 judged session: 2 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated. 2 open.\n" +
			single("s-x", "~/dev", first, "capture miss", "You: Use the project template.", "Rule: Use the template.") +
			single("s-x", "~/dev", second, "capture miss", "You: Include the impact.", "Rule: State the impact.")
		// The history stays flat, but the open list shows both excerpts as one turn of one session.
		plain := "2 corrections in 1 judged session: 2 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated. 2 open.\n" + titled("Same turn", day, "") +
			child("s-x", "~/dev", first, "capture miss", "Quote: Use the project template.", "Rule: Use the template.") +
			child("s-x", "~/dev", second, "capture miss", "Quote: Include the impact.", "Rule: State the impact.")
		if got := listed(t, dev); got != plain {
			t.Errorf("--list:\n got %s\nwant %s", got, plain)
		}
		if got := listed(t, dev, "--all"); got != want {
			t.Errorf("--list --all:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("a missing blob or another history stays an ordinary entry", func(t *testing.T) {
		dev, repo, _ := setup(t)
		w := correction("Include the impact.", "Rule of w.", verdictCapture)
		x := correction("Include the impact.", "Rule of x.", verdictCapture)
		y := correction("Include the impact.", "Rule of y.", verdictCapture)
		v := correction("Include the impact.", "Rule of v.", verdictCapture)
		save(t, repo, "s-w", "~/dev", "0000000000000000000000000000000000000000", 40, w)
		save(t, repo, "s-x", "~/dev", blob(t, repo, "s-x", "Open the file."), 30, x)
		save(t, repo, "s-y", "~/dev", blob(t, repo, "s-y", "Open the file."), 20, y)
		save(t, repo, "s-v", "~/dev", blob(t, repo, "s-v", "Open another file."), 10, v)
		want := "4 corrections in 4 judged sessions: 4 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated. 4 open.\n" +
			single("s-w", "~/dev", w, "capture miss", "You: Include the impact.", "Rule: Rule of w.") +
			group("Include the impact.") +
			child("s-x", "~/dev", x, "capture miss", "Rule: Rule of x.") +
			child("s-y", "~/dev", y, "capture miss", "Rule: Rule of y.") +
			single("s-v", "~/dev", v, "capture miss", "You: Include the impact.", "Rule: Rule of v.")
		// Every entry is open, so the open list has the same groups, and unproven ones stay separate.
		if got := listed(t, dev); got != want {
			t.Errorf("--list:\n got %s\nwant %s", got, want)
		}
		if got := listed(t, dev, "--all"); got != want {
			t.Errorf("--list --all:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("the open list counts only open children and keeps a lone one flat", func(t *testing.T) {
		dev, repo, _ := setup(t)
		x := correction("Include the impact.", "State the impact.", verdictCapture)
		y := correction("Include the impact.", "Say what changes.", verdictCapture)
		z := correction("Include the impact.", "Name the impact.", verdictUnrelated)
		save(t, repo, "s-x", "~/dev", blob(t, repo, "s-x", "Open the file."), 30, x)
		save(t, repo, "s-y", "~/dev", blob(t, repo, "s-y", "Open the file."), 20, y)
		save(t, repo, "s-z", "~/dev", blob(t, repo, "s-z", "Open the file."), 10, z)
		if out, err := run(t, dev, "", "reflect", "--answer", id("s-y", y), "--reason", "Handled."); err != nil {
			t.Fatalf("--answer: %v\n%s", err, out)
		}
		// One copy is answered and one is unrelated, so the one open correction has no visible sibling.
		want := "3 corrections in 3 judged sessions: 2 capture, 0 retrieval, 0 application, 0 stale, 1 unrelated. 1 open.\n" +
			single("s-x", "~/dev", x, "capture miss", "You: Include the impact.", "Rule: State the impact.")
		if got := listed(t, dev); got != want {
			t.Errorf("--list:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("separate turns of one session count in separate groups", func(t *testing.T) {
		dev, repo, _ := setup(t)
		const next = "Keep the log. Name the owner."
		x1 := correction("Include the impact.", "State the impact.", verdictCapture)
		y1 := correction("Include the impact.", "Say what changes.", verdictCapture)
		x2 := correction("Name the owner.", "Assign an owner.", verdictCapture)
		y2 := correction("Name the owner.", "Say who owns it.", verdictCapture)
		x2.Time, y2.Time = later, later
		save(t, repo, "s-x", "~/dev", blob(t, repo, "s-x", "Open the file.", "you|"+next), 20, x1, x2)
		save(t, repo, "s-y", "~/dev", blob(t, repo, "s-y", "Open the file.", "you|"+next), 10, y1, y2)
		// Each turn is one occurrence for every rule it supports, and groups follow the first appearance of a member.
		want := "4 corrections in 2 judged sessions: 4 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated. 4 open.\n" +
			group("Include the impact.") +
			child("s-x", "~/dev", x1, "capture miss", "Rule: State the impact.") +
			child("s-y", "~/dev", y1, "capture miss", "Rule: Say what changes.") +
			titled("Copied turn", nextMinute, "Name the owner.") +
			child("s-x", "~/dev", x2, "capture miss", "Rule: Assign an owner.") +
			child("s-y", "~/dev", y2, "capture miss", "Rule: Say who owns it.")
		if got := listed(t, dev); got != want {
			t.Errorf("--list:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("a copied turn in the open list keeps both excerpts of one session", func(t *testing.T) {
		dev, repo, _ := setup(t)
		first := correction(turn, "Use the template.", verdictCapture)
		second := correction("Include the impact.", "State the impact.", verdictCapture)
		other := correction("Include the impact.", "Say what changes.", verdictCapture)
		save(t, repo, "s-x", "~/dev", blob(t, repo, "s-x", "Open the file."), 20, first, second)
		save(t, repo, "s-y", "~/dev", blob(t, repo, "s-y", "Open the file."), 10, other)
		want := "3 corrections in 2 judged sessions: 3 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated. 3 open.\n" + group("") +
			child("s-x", "~/dev", first, "capture miss", "Rule: Use the template.") +
			child("s-x", "~/dev", second, "capture miss", "Quote: Include the impact.", "Rule: State the impact.") +
			child("s-y", "~/dev", other, "capture miss", "Quote: Include the impact.", "Rule: Say what changes.")
		if got := listed(t, dev); got != want {
			t.Errorf("--list:\n got %s\nwant %s", got, want)
		}
	})

	// The hidden twin is not open, but its id still decides how long the printed ids are.
	t.Run("a hidden twin still expands the id of an open correction", func(t *testing.T) {
		dev, repo, _ := setup(t)
		fixed := time.Date(2026, 11, 18, 10, 4, 0, 0, time.UTC) // fixed, so that the ids below are stable
		const twin = "Use rule 12272."
		const at = "2026-11-18T10:05:00Z"
		twinA := reflectCorrection{Time: at, Quote: twin, Summary: "Rule A.", Verdict: verdictCapture}
		hidden := reflectCorrection{Time: at, Quote: "Use rule 15967.", Summary: "Rule B.", Verdict: verdictBefore}
		copyA := reflectCorrection{Time: at, Quote: twin, Summary: "Rule C.", Verdict: verdictCapture}
		shared := transcriptT(t, repo, "claude/p/s-first.jsonl", transcriptText("s-first", fixed, "you|Open the file.", "you|"+twin))
		save(t, repo, "s-first", "~/dev", shared, 0, twinA, hidden)
		save(t, repo, "s-copy", "~/dev", shared, -24, copyA)
		idA, idB, idC := correctionID("s-first", at, twinA.Quote), correctionID("s-first", at, hidden.Quote), correctionID("s-copy", at, copyA.Quote)
		if idA[:7] != "1d2633e" || idB[:7] != "1d2633e" || idC[:7] == "1d2633e" {
			t.Fatalf("ids %s, %s, %s: want the first two to share 1d2633e", idA, idB, idC)
		}
		want := "3 corrections in 2 judged sessions: 2 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated, 1 before memory. 2 open.\n" +
			"\n2026-11-18 10:05  Copied turn\n  You: " + twin + "\n" +
			"  " + idA[:8] + "  ~/dev  session s-first: capture miss\n    Rule: Rule A.\n" +
			"  " + idC[:7] + "  ~/dev  session s-copy: capture miss\n    Rule: Rule C.\n"
		if got := listed(t, dev); got != want {
			t.Errorf("--list:\n got %s\nwant %s", got, want)
		}
		// The expanded id answers, and the copy left alone has no visible sibling, so it prints flat.
		if out, err := run(t, dev, "", "reflect", "--answer", idA[:8], "--reason", "Handled."); err != nil {
			t.Fatalf("--answer %s: %v\n%s", idA[:8], err, out)
		}
		want = "3 corrections in 2 judged sessions: 2 capture, 0 retrieval, 0 application, 0 stale, 0 unrelated, 1 before memory. 1 open.\n" +
			"\n" + idC[:7] + " 2026-11-18 10:05 ~/dev s-copy: capture miss\n  You: " + twin + "\n  Rule: Rule C.\n"
		if got := listed(t, dev); got != want {
			t.Errorf("--list after the answer:\n got %s\nwant %s", got, want)
		}
	})

	// The twins of TestReflectAnswer share seven digits of their ids. A copy joins the first twin only, so the
	// first prints inside a group and the second stays single. Both print their expanded ids.
	t.Run("expanded ids of a grouped child and a single entry", func(t *testing.T) {
		dev, repo, _ := setup(t)
		fixed := time.Date(2026, 11, 18, 10, 4, 0, 0, time.UTC) // the turn is the second line, at 10:05
		const twin = "Use rule 12272."
		const at = "2026-11-18T10:05:00Z"
		twinA := reflectCorrection{Time: at, Quote: twin, Summary: "Rule A.", Verdict: verdictCapture}
		twinB := reflectCorrection{Time: at, Quote: "Use rule 15967.", Summary: "Rule B.", Verdict: verdictCapture}
		copyA := reflectCorrection{Time: at, Quote: twin, Summary: "Rule C.", Verdict: verdictCapture}
		// One blob serves both sessions, although its header names the first.
		shared := transcriptT(t, repo, "claude/p/s-first.jsonl", transcriptText("s-first", fixed, "you|Open the file.", "you|"+twin))
		save(t, repo, "s-first", "~/dev", shared, 0, twinA, twinB)
		save(t, repo, "s-copy", "~/dev", shared, -24, copyA)
		idA, idB, idC := correctionID("s-first", at, twinA.Quote), correctionID("s-first", at, twinB.Quote), correctionID("s-copy", at, copyA.Quote)
		if idA[:7] != "1d2633e" || idB[:7] != "1d2633e" || idC[:7] == "1d2633e" {
			t.Fatalf("ids %s, %s, %s: want the first two to share 1d2633e", idA, idB, idC)
		}
		got := listed(t, dev, "--all")
		for _, want := range []string{
			"\n2026-11-18 10:05  Copied turn\n  You: " + twin + "\n",
			"\n  " + idA[:8] + "  ~/dev  session s-first: capture miss\n    Rule: Rule A.\n",
			"\n  " + idC[:7] + "  ~/dev  session s-copy: capture miss\n    Rule: Rule C.\n",
			"\n" + idB[:8] + " 2026-11-18 10:05 ~/dev s-first: capture miss\n  You: Use rule 15967.\n",
		} {
			if strings.Count(got, want) != 1 {
				t.Errorf("--list --all lacks %q:\n%s", want, got)
			}
		}
		// Answering the grouped child by its printed id leaves its open sibling answerable, and then it answers too.
		for n, c := range []struct{ id, rule string }{{idA[:8], "Rule A."}, {idC[:7], "Rule C."}} {
			if out, err := run(t, dev, "", "reflect", "--answer", c.id, "--reason", "Handled."); err != nil {
				t.Fatalf("--answer %s: %v\n%s", c.id, err, out)
			}
			got := listed(t, dev, "--all")
			if strings.Count(got, "Answer: Handled.") != n+1 || !strings.Contains(got, "Rule: "+c.rule+"\n    Answer: Handled.\n") {
				t.Errorf("--list --all after answering %s:\n%s", c.id, got)
			}
			if want := fmt.Sprintf(" %d open.\n", 2-n); !strings.Contains(got, want) {
				t.Errorf("--list --all after answering %s lacks %q:\n%s", c.id, want, got)
			}
		}
	})
}

func TestReflectAnswer(t *testing.T) {
	dev, repo := reflectFixture(t)
	t.Setenv("CLAUDECODE", "1") // an agent may answer
	held := reflectCorrection{Time: "2026-11-18T10:02:03Z", Quote: "Never push\n without asking.", Summary: "Ask before a push.", Verdict: verdictRetrieval, Path: "dev/memories/ask-before-push.md"}
	small := reflectCorrection{Time: "2026-11-18T10:05:00Z", Quote: "Use small commits.", Summary: "Commit small.", Verdict: verdictCapture}
	// The ids of these two quotes share their first 7 digits, 1d2633e.
	twinA := reflectCorrection{Time: "2026-11-18T10:05:00Z", Quote: "Use rule 12272.", Summary: "Rule A.", Verdict: verdictCapture}
	twinB := reflectCorrection{Time: "2026-11-18T10:05:00Z", Quote: "Use rule 15967.", Summary: "Rule B.", Verdict: verdictCapture}
	if err := appendReflect(repo, reflectRecord{Session: "s-first", File: "claude/p/s-first.jsonl", Blob: "b1", Level: "~/dev", Start: "2026-11-18T10:00:00Z", Corrections: []reflectCorrection{held, small, twinA, twinB}}); err != nil {
		t.Fatal(err)
	}
	full := func(c reflectCorrection) string { return correctionID("s-first", c.Time, c.Quote) }
	heldID, smallID := full(held), full(small)
	if a, b := full(twinA), full(twinB); a[:7] != "1d2633e" || b[:7] != "1d2633e" || a == b {
		t.Fatalf("the twins have the ids %s and %s, want two ids that share 1d2633e", a, b)
	}
	const memory = "dev/memories/ask-before-push.md"
	calls := fakeAgent(t, twoCorrections(0), heldAndNone(0))

	good := []struct {
		name string
		args []string
		out  string
		want map[string]reflectAnswer // by id; the test compares the path and the reason
	}{
		{"a path", []string{"--answer", heldID[:7], "--with", memory},
			"Answered " + heldID[:7] + " with " + memory + ".\n",
			map[string]reflectAnswer{heldID: {Path: memory}}},
		{"a reason only", []string{"--answer", smallID, "--reason", "It concerns only its task."},
			"Answered " + smallID[:7] + ": It concerns only its task.\n",
			map[string]reflectAnswer{heldID: {Path: memory}, smallID: {Reason: "It concerns only its task."}}},
		{"two ids, and a second answer replaces the first", []string{"--answer", heldID[:9] + ", " + smallID[:7], "--with", memory, "--reason", "One memory holds both rules"},
			"Answered " + heldID[:7] + " with " + memory + ": One memory holds both rules.\nAnswered " + smallID[:7] + " with " + memory + ": One memory holds both rules.\n",
			map[string]reflectAnswer{heldID: {Path: memory, Reason: "One memory holds both rules"}, smallID: {Path: memory, Reason: "One memory holds both rules"}}},
		// A new answer replaces the whole older one, so it keeps no path and no reason of it.
		{"a reason replaces a path and a reason", []string{"--answer", heldID, "--reason", "The memory went."},
			"Answered " + heldID[:7] + ": The memory went.\n",
			map[string]reflectAnswer{heldID: {Reason: "The memory went."}, smallID: {Path: memory, Reason: "One memory holds both rules"}}},
		{"a path replaces a path and a reason", []string{"--answer", smallID, "--with", memory},
			"Answered " + smallID[:7] + " with " + memory + ".\n",
			map[string]reflectAnswer{heldID: {Reason: "The memory went."}, smallID: {Path: memory}}},
		// Two ids share 7 digits, so the list prints 8 digits of each, and an answer takes them.
		{"the longer short id of a twin", []string{"--answer", full(twinA)[:8] + "," + full(twinB)[:8], "--reason", "Both concern only their task."},
			"Answered " + full(twinA)[:8] + ": Both concern only their task.\nAnswered " + full(twinB)[:8] + ": Both concern only their task.\n",
			map[string]reflectAnswer{heldID: {Reason: "The memory went."}, smallID: {Path: memory}, full(twinA): {Reason: "Both concern only their task."}, full(twinB): {Reason: "Both concern only their task."}}},
	}
	if list, err := run(t, dev, "", "reflect", "--list"); err != nil || !strings.Contains(list, "\n"+full(twinA)[:8]+" 2026-11-18 10:05 ") || !strings.Contains(list, "\n"+full(twinB)[:8]+" 2026-11-18 10:05 ") {
		t.Errorf("reflect --list does not print 8 digits of each twin: %v\n%s", err, list)
	}
	for _, c := range good {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, dev, "", append([]string{"reflect"}, c.args...)...)
			if err != nil || out != c.out {
				t.Fatalf("reflect %v: %v\n got %q\nwant %q", c.args, err, out, c.out)
			}
			answers, err := readAnswers(repo)
			if err != nil || len(answers) != len(c.want) {
				t.Fatalf("answers = %+v, %v; want %d", answers, err, len(c.want))
			}
			for id, want := range c.want {
				got := answers[id]
				if got.ID != id || got.Path != want.Path || got.Reason != want.Reason || got.Session != "s-first" || got.Time == "" || got.Answered == "" {
					t.Errorf("the answer of %s = %+v, want the path %q and the reason %q", id[:7], got, want.Path, want.Reason)
				}
			}
		})
	}

	before, err := readAnswers(repo)
	if err != nil {
		t.Fatal(err)
	}
	bad := []struct {
		name string
		args []string
		want []string // each must be in the error
	}{
		{"an unknown id", []string{"--answer", "0123456789", "--reason", "r"}, []string{"0123456789", "memcore reflect --list", "memcore help reflect"}},
		{"an unknown id beside a known one", []string{"--answer", smallID + ",0123456789", "--reason", "r"}, []string{"0123456789", "memcore reflect --list"}},
		{"a short prefix", []string{"--answer", heldID[:6], "--reason", "r"}, []string{heldID[:6], "7 digits"}},
		{"an empty id", []string{"--answer", heldID + ",", "--reason", "r"}, []string{"usage: memcore reflect --answer"}},
		{"a prefix that fits two corrections", []string{"--answer", "1d2633e", "--reason", "r"}, []string{full(twinA), full(twinB)}},
		{"a path that is no document", []string{"--answer", heldID, "--with", "dev/memories/none.md"}, []string{"--with", "dev/memories/none.md"}},
		{"a folder", []string{"--answer", heldID, "--with", "dev/memories"}, []string{"--with", "dev/memories"}},
		{"the git directory", []string{"--answer", heldID, "--with", ".git/config"}, []string{"--with", "not a path in the memory repo"}},
		{"the git directory in capitals", []string{"--answer", heldID, "--with", ".GIT/config"}, []string{"--with", "not a path in the memory repo"}},
		{"a path outside the memory repo", []string{"--answer", heldID, "--with", "../memory/dev/memories/ask-before-push.md"}, []string{"--with", "not a path in the memory repo"}},
		{"no --with and no --reason", []string{"--answer", heldID}, []string{"--with <path>", "--reason <text>"}},
		{"an empty reason alone", []string{"--answer", heldID, "--reason", " "}, []string{"--with <path>", "--reason <text>"}},
		{"--answer beside --budget", []string{"--answer", heldID, "--reason", "r", "--budget", "1"}, []string{"usage: memcore reflect --answer"}},
		{"--answer beside --sessions", []string{"--answer", heldID, "--reason", "r", "--sessions", "1"}, []string{"usage: memcore reflect --answer"}},
		{"--answer beside --list", []string{"--list", "--answer", heldID, "--reason", "r"}, []string{"usage: memcore reflect --answer"}},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, dev, "", append([]string{"reflect"}, c.args...)...)
			if err == nil {
				t.Fatalf("reflect %v: no error\n%s", c.args, out)
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("reflect %v: the error lacks %q: %v", c.args, want, err)
				}
			}
			if strings.Contains(out, "Answered") {
				t.Errorf("reflect %v printed an answer: %s", c.args, out)
			}
		})
	}
	if after, err := readAnswers(repo); err != nil || !reflect.DeepEqual(after, before) {
		t.Errorf("a refused call changed the answers: %+v, %v", after, err)
	}

	// --with or --reason without --answer starts no pass, also for the person at a terminal.
	t.Setenv("CLAUDECODE", "")
	for _, args := range [][]string{{"--with", memory}, {"--reason", "r"}, {"--reason", "r", "--sessions", "1"}, {"--list", "--with", memory}} {
		if _, err := run(t, dev, "", append([]string{"reflect"}, args...)...); err == nil || !strings.Contains(err.Error(), "usage: memcore reflect") || !strings.Contains(err.Error(), "--answer") {
			t.Errorf("reflect %v: %v, want a usage error that names --answer", args, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("the agent got %d calls, want none", len(*calls))
	}
	if records, _ := readReflect(repo); len(records) != 1 {
		t.Errorf("%d records, want the one of the fixture: no call may start a pass", len(records))
	}
}
