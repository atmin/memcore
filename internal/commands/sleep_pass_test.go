package commands

import (
	"errors"
	"path/filepath"
	"reflect"
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

// sleepFixture makes a memory repo with one memory at ~/dev, and two old sessions there: s-first with two
// corrections, and s-second with none. It returns the directory ~/dev and the repo.
func sleepFixture(t *testing.T) (dev, repo string) {
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

func TestSleepPass(t *testing.T) {
	t.Run("judges and records each session", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		calls := fakeAgent(t, twoCorrections(0.10), heldAndNone(0.05))
		out, err := run(t, dev, "", "sleep")
		if err != nil {
			t.Fatalf("sleep: %v\n%s", err, out)
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
		records, err := readSleep(repo)
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
		if e := last(t, repo); e.Command != "sleep" || e.Outcome != "ok" {
			t.Errorf("event = %+v, want an ok sleep", e)
		}

		// A second pass judges nothing new.
		fakeAgent(t, func(int, thinking.Request) (thinking.Reply, error) {
			t.Error("a second pass calls the agent")
			return thinking.Reply{}, nil
		}, nil)
		out, err = run(t, dev, "", "sleep")
		if err != nil || out != "No session waits for a sleep pass.\n" {
			t.Errorf("second pass: %v\n%s", err, out)
		}
	})

	t.Run("a correction before the first event of its session is before memory", func(t *testing.T) {
		dev, repo := sleepFixture(t)
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
		out, err := run(t, dev, "", "sleep")
		if err != nil {
			t.Fatalf("sleep: %v\n%s", err, out)
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
		records, err := readSleep(repo)
		if err != nil || len(records) != 3 || records[0].Session != "s-early" {
			t.Fatalf("records = %+v, %v; want s-early first of 3", records, err)
		}
		c := records[0].Corrections
		if len(c) != 2 || c[0].Verdict != verdictBefore || c[0].Path != "" || c[0].Reason != "" || c[1].Verdict != verdictCapture {
			t.Errorf("corrections = %+v, want before memory, then a capture miss", c)
		}
	})

	t.Run("a read before the correction is an application miss", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		at := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
		if err := appendEvent(repo, &Event{Time: at.Format(time.RFC3339), Session: "s-first", Command: "show", Outcome: "ok", Paths: []string{"dev/memories/ask-before-push.md"}}); err != nil {
			t.Fatal(err)
		}
		fakeAgent(t, twoCorrections(0), heldAndNone(0))
		out, err := run(t, dev, "", "sleep")
		if err != nil || !strings.Contains(out, "1 capture, 0 retrieval, 1 application") {
			t.Errorf("sleep: %v\n%s", err, out)
		}
		if !strings.Contains(out, "The pass cost about 0.00 dollars at API prices.") {
			t.Errorf("an agent that reports no cost:\n%s", out)
		}
	})

	t.Run("the budget ends the pass", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		fakeAgent(t, twoCorrections(0.20), heldAndNone(0.07))
		out, err := run(t, dev, "", "sleep", "--budget", "0.25")
		if err != nil {
			t.Fatalf("sleep: %v\n%s", err, out)
		}
		for _, want := range []string{"Judged 1 session with 2 corrections", "about 0.27 dollars at API prices. 1 session waits.\n", "The pass stopped at its budget of 0.25 dollars.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if records, _ := readSleep(repo); len(records) != 1 {
			t.Errorf("%d records, want 1", len(records))
		}
	})

	t.Run("a pass finishes the session that it started", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		// The extraction alone passes the budget, and the judge of the same session still runs.
		calls := fakeAgent(t, twoCorrections(0.20), heldAndNone(0.07))
		out, err := run(t, dev, "", "sleep", "--budget", "0.15")
		if err != nil {
			t.Fatalf("sleep: %v\n%s", err, out)
		}
		for _, want := range []string{" ~/dev s-first: 2 corrections: 1 capture, 1 retrieval,", "Judged 1 session with 2 corrections", "about 0.27 dollars at API prices. 1 session waits.\n", "The pass stopped at its budget of 0.15 dollars.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		records, _ := readSleep(repo)
		if len(records) != 1 || records[0].Session != "s-first" || len(records[0].Corrections) != 2 {
			t.Errorf("records = %+v, want the first session with both corrections", records)
		}
		if len(*calls) != 2 {
			t.Errorf("%d calls, want 2: the extraction and the judge of the first session", len(*calls))
		}
	})

	t.Run("a cost equal to the budget ends the pass", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		fakeAgent(t, twoCorrections(0.25), heldAndNone(0.25)) // two costs that add up to 0.5 exactly
		out, err := run(t, dev, "", "sleep", "--budget", "0.50")
		if err != nil {
			t.Fatalf("sleep: %v\n%s", err, out)
		}
		for _, want := range []string{"Judged 1 session with 2 corrections", "about 0.50 dollars at API prices. 1 session waits.\n", "The pass stopped at its budget of 0.50 dollars.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if records, _ := readSleep(repo); len(records) != 1 {
			t.Errorf("%d records, want 1", len(records))
		}
	})

	t.Run("a cost below the budget starts the next session", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		fakeAgent(t, twoCorrections(0.20), heldAndNone(0.07))
		out, err := run(t, dev, "", "sleep", "--budget", "0.30")
		if err != nil {
			t.Fatalf("sleep: %v\n%s", err, out)
		}
		for _, want := range []string{"Judged 2 sessions with 2 corrections", "about 0.47 dollars at API prices. No session waits.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "The pass stopped") {
			t.Errorf("the pass names a stop, and it judged every session:\n%s", out)
		}
		if records, _ := readSleep(repo); len(records) != 2 {
			t.Errorf("%d records, want 2", len(records))
		}
	})

	t.Run("the budget never splits the files of one session", func(t *testing.T) {
		dev, repo := sleepFixture(t)
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
		out, err := run(t, dev, "", "sleep", "--budget", "0.15")
		if err != nil {
			t.Fatalf("sleep: %v\n%s", err, out)
		}
		for _, want := range []string{"Judged 1 session with 2 corrections", "about 0.54 dollars at API prices. 1 session waits.\n", "The pass stopped at its budget of 0.15 dollars.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if records, _ := readSleep(repo); len(records) != 2 || records[0].Session != "s-first" || records[1].Session != "s-first" {
			t.Errorf("records = %+v, want both files of the first session", records)
		}
		if len(*calls) != 4 {
			t.Errorf("%d calls, want 4: an extraction and a judge for each file of the session", len(*calls))
		}
	})

	t.Run("the setting of the budget and of the model", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		gitT(t, repo, "config", "memcore.sleepbudget", "0.75")
		gitT(t, repo, "config", "memcore.sleepmodel", "haiku")
		// The first session costs 0.80 dollars: more than the setting, and less than the default of 2 dollars.
		calls := fakeAgent(t, twoCorrections(0.50), heldAndNone(0.30))
		out, err := run(t, dev, "", "sleep")
		if err != nil {
			t.Fatalf("sleep: %v\n%s", err, out)
		}
		if !strings.Contains(out, "The pass stopped at its budget of 0.75 dollars.\n") || !strings.Contains(out, "1 session waits.\n") {
			t.Errorf("the pass does not stop at the budget of the setting:\n%s", out)
		}
		if req := (*calls)[0]; req.Model != "haiku" {
			t.Errorf("request = model %q, want haiku", req.Model)
		}
		gitT(t, repo, "config", "memcore.sleepbudget", "much")
		if _, err := run(t, dev, "", "sleep"); err == nil || !strings.Contains(err.Error(), "memcore.sleepbudget") {
			t.Errorf("a budget that is no number: %v", err)
		}
		for _, bad := range []string{"NaN", "Inf", "-Inf", "0", "-1"} {
			gitT(t, repo, "config", "memcore.sleepbudget", bad)
			if _, err := run(t, dev, "", "sleep"); err == nil || !strings.Contains(err.Error(), "memcore.sleepbudget") {
				t.Errorf("the setting %s: %v, want an error", bad, err)
			}
			if _, err := run(t, dev, "", "sleep", "--budget", bad); err == nil || !strings.Contains(err.Error(), "--budget") {
				t.Errorf("--budget %s: %v, want an error", bad, err)
			}
		}
	})

	t.Run("--sessions ends the pass", func(t *testing.T) {
		dev, repo := sleepFixture(t)
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
		out, err := run(t, dev, "", "sleep", "--sessions", "1")
		if err != nil {
			t.Fatalf("sleep: %v\n%s", err, out)
		}
		if n := strings.Count(out, " s-first: "); n != 1 || !strings.Contains(out, " ~/dev s-first: 2 corrections: 1 capture, 1 retrieval,") {
			t.Errorf("the session has %d lines, want one line that counts each correction once:\n%s", n, out)
		}
		for _, want := range []string{"Judged 1 session with 2 corrections: 1 capture, 1 retrieval,", "1 session waits.\n", "The pass stopped after 1 session, as --sessions says.\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if records, _ := readSleep(repo); len(records) != 2 || records[0].Session != "s-first" || records[1].Session != "s-first" {
			t.Errorf("records = %+v, want both files of the first session", records)
		}
		if len(*calls) != 4 {
			t.Errorf("%d calls, want 4: an extraction and a judge for each file of the session", len(*calls))
		}
		for _, bad := range []string{"0", "-1", "two"} {
			if _, err := run(t, dev, "", "sleep", "--sessions", bad); err == nil || !strings.Contains(err.Error(), "--sessions") || !strings.Contains(err.Error(), "memcore help sleep") {
				t.Errorf("--sessions %s: %v, want a usage error", bad, err)
			}
		}
		if _, err := run(t, dev, "", "sleep", "now"); err == nil || !strings.Contains(err.Error(), "usage: memcore sleep") {
			t.Errorf("an argument: %v, want a usage error", err)
		}
	})

	t.Run("an error of the agent keeps the finished sessions", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		fakeAgent(t, func(n int, req thinking.Request) (thinking.Reply, error) {
			if n == 2 {
				return thinking.Reply{Cost: 0.01}, errors.New("run the agent: the agent reports an error: Not logged in: check that claude is logged in")
			}
			return twoCorrections(0.10)(n, req)
		}, heldAndNone(0.05))
		out, err := run(t, dev, "", "sleep")
		if err == nil || !strings.Contains(err.Error(), "Not logged in") || !strings.Contains(err.Error(), "keeps the sessions that it finished") {
			t.Fatalf("err = %v, want the cause", err)
		}
		if !strings.Contains(out, "Judged 1 session with 2 corrections") || !strings.Contains(out, "about 0.16 dollars at API prices. 1 session waits.") {
			t.Errorf("output:\n%s", out)
		}
		if records, _ := readSleep(repo); len(records) != 1 || records[0].Session != "s-first" {
			t.Errorf("records = %+v, want the first session", records)
		}
	})

	t.Run("refuses an agent", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		calls := fakeAgent(t, twoCorrections(0), heldAndNone(0))
		t.Setenv("CLAUDECODE", "1")
		_, err := run(t, dev, "", "sleep")
		if err == nil || !strings.Contains(err.Error(), "only the person runs a sleep pass") {
			t.Errorf("err = %v, want a refusal", err)
		}
		if records, _ := readSleep(repo); len(*calls) != 0 || len(records) != 0 {
			t.Errorf("%d calls and %d records, want none", len(*calls), len(records))
		}
	})

	t.Run("an unknown agent", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		gitT(t, repo, "config", "memcore.sleepagent", "llama")
		calls := fakeAgent(t, twoCorrections(0), heldAndNone(0))
		_, err := run(t, dev, "", "sleep")
		if err == nil || !strings.Contains(err.Error(), `"llama"`) || !strings.Contains(err.Error(), "the known agents are claude") || len(*calls) != 0 {
			t.Errorf("err = %v with %d calls, want the known agents and no call", err, len(*calls))
		}
	})

	t.Run("a pass that already runs", func(t *testing.T) {
		dev, repo := sleepFixture(t)
		unlock, ok, err := store.TryLockFile(repo, sleepLock)
		if err != nil || !ok {
			t.Fatalf("lock: %v, %v", ok, err)
		}
		defer unlock()
		calls := fakeAgent(t, twoCorrections(0), heldAndNone(0))
		if _, err := run(t, dev, "", "sleep"); err == nil || !strings.Contains(err.Error(), "a sleep pass already runs") || len(*calls) != 0 {
			t.Errorf("err = %v with %d calls, want a refusal", err, len(*calls))
		}
	})
}

func TestSleepList(t *testing.T) {
	dev, repo := sleepFixture(t)
	t.Setenv("CLAUDECODE", "1") // an agent may list
	out, err := run(t, dev, "", "sleep", "--list")
	if err != nil || out != "No sleep pass has judged a session yet. The person runs one with memcore sleep.\n" {
		t.Fatalf("without records: %v\n%s", err, out)
	}
	write := func(r sleepRecord) {
		t.Helper()
		if err := appendSleep(repo, r); err != nil {
			t.Fatal(err)
		}
	}
	held := sleepCorrection{Time: "2026-11-18T10:02:03Z", Quote: "Never push\n without asking.", Summary: "Ask before a push.", Verdict: verdictRetrieval, Path: "dev/memories/ask-before-push.md", Reason: "The memory says so."}
	small := sleepCorrection{Time: "2026-11-18T10:05:00Z", Quote: "Use small commits.", Summary: "Commit small.", Verdict: verdictCapture, Reason: "No memory holds it."}
	later := sleepCorrection{Time: "2026-11-19T09:00:00Z", Quote: "Not that file.", Summary: "A detail.", Verdict: verdictUnrelated}
	early := sleepCorrection{Time: "2026-11-19T07:30:00Z", Quote: "No tabs here.", Summary: "Use spaces.", Verdict: verdictBefore}
	write(sleepRecord{Session: "s-first", File: "claude/p/s-first.jsonl", Blob: "b1", Level: "~/dev", Start: "2026-11-18T10:00:00Z", Corrections: []sleepCorrection{small, held}})
	write(sleepRecord{Session: "s-first", File: "claude/p/s-first.0123abcd.jsonl", Blob: "b2", Level: "~/dev", Start: "2026-11-18T10:00:00Z", Corrections: []sleepCorrection{held}})
	write(sleepRecord{Session: "s-empty", File: "claude/p/s-empty.jsonl", Blob: "b3", Level: "~", Start: "2026-11-18T12:00:00Z", Corrections: []sleepCorrection{}})
	write(sleepRecord{Session: "s-later", File: "claude/p/s-later.jsonl", Blob: "b4", Level: "~", Start: "2026-11-19T08:00:00Z", Corrections: []sleepCorrection{later, early}})

	calls := fakeAgent(t, twoCorrections(0), heldAndNone(0))
	out, err = run(t, dev, "", "sleep", "--list")
	if err != nil {
		t.Fatalf("sleep --list: %v\n%s", err, out)
	}
	want := `4 corrections in 3 judged sessions: 1 capture, 1 retrieval, 0 application, 0 stale, 1 unrelated, 1 before memory.

{early} 2026-11-19 07:30 ~ s-later: before memory
  You: No tabs here.
  Rule: Use spaces.

{later} 2026-11-19 09:00 ~ s-later: unrelated to memory
  You: Not that file.
  Rule: A detail.

{held} 2026-11-18 10:02 ~/dev s-first: retrieval miss, dev/memories/ask-before-push.md
  You: Never push without asking.
  Rule: Ask before a push.
  Why: The memory says so.

{small} 2026-11-18 10:05 ~/dev s-first: capture miss
  You: Use small commits.
  Rule: Commit small.
  Why: No memory holds it.
`
	// Each head line starts with the short id of its correction.
	id := func(session string, c sleepCorrection) string { return short(correctionID(session, c.Time, c.Quote)) }
	want = strings.NewReplacer("{early}", id("s-later", early), "{later}", id("s-later", later), "{held}", id("s-first", held), "{small}", id("s-first", small)).Replace(want)
	if out != want {
		t.Errorf("sleep --list:\n got %s\nwant %s", out, want)
	}
	if len(*calls) != 0 {
		t.Errorf("--list called the agent %d times", len(*calls))
	}
	if records, _ := readSleep(repo); len(records) != 4 {
		t.Errorf("--list changed the records: %d, want 4", len(records))
	}
	for _, args := range [][]string{{"--list", "--budget", "1"}, {"--list", "--sessions", "2"}, {"--list", "--budget="}, {"--sessions=", "--list"}} {
		if _, err := run(t, dev, "", append([]string{"sleep"}, args...)...); err == nil || !strings.Contains(err.Error(), "usage: memcore sleep --list") {
			t.Errorf("sleep %v: %v, want a usage error", args, err)
		}
	}
}

func TestSleepAnswer(t *testing.T) {
	dev, repo := sleepFixture(t)
	t.Setenv("CLAUDECODE", "1") // an agent may answer
	held := sleepCorrection{Time: "2026-11-18T10:02:03Z", Quote: "Never push\n without asking.", Summary: "Ask before a push.", Verdict: verdictRetrieval, Path: "dev/memories/ask-before-push.md"}
	small := sleepCorrection{Time: "2026-11-18T10:05:00Z", Quote: "Use small commits.", Summary: "Commit small.", Verdict: verdictCapture}
	// The ids of these two quotes share their first 7 digits, 1d2633e.
	twinA := sleepCorrection{Time: "2026-11-18T10:05:00Z", Quote: "Use rule 12272.", Summary: "Rule A.", Verdict: verdictCapture}
	twinB := sleepCorrection{Time: "2026-11-18T10:05:00Z", Quote: "Use rule 15967.", Summary: "Rule B.", Verdict: verdictCapture}
	if err := appendSleep(repo, sleepRecord{Session: "s-first", File: "claude/p/s-first.jsonl", Blob: "b1", Level: "~/dev", Start: "2026-11-18T10:00:00Z", Corrections: []sleepCorrection{held, small, twinA, twinB}}); err != nil {
		t.Fatal(err)
	}
	full := func(c sleepCorrection) string { return correctionID("s-first", c.Time, c.Quote) }
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
		want map[string]sleepAnswer // by id; the test compares the path and the reason
	}{
		{"a path", []string{"--answer", heldID[:7], "--with", memory},
			"Answered " + heldID[:7] + " with " + memory + ".\n",
			map[string]sleepAnswer{heldID: {Path: memory}}},
		{"a reason only", []string{"--answer", smallID, "--reason", "It concerns only its task."},
			"Answered " + smallID[:7] + ": It concerns only its task.\n",
			map[string]sleepAnswer{heldID: {Path: memory}, smallID: {Reason: "It concerns only its task."}}},
		{"two ids, and a second answer replaces the first", []string{"--answer", heldID[:9] + ", " + smallID[:7], "--with", memory, "--reason", "One memory holds both rules"},
			"Answered " + heldID[:7] + " with " + memory + ": One memory holds both rules.\nAnswered " + smallID[:7] + " with " + memory + ": One memory holds both rules.\n",
			map[string]sleepAnswer{heldID: {Path: memory, Reason: "One memory holds both rules"}, smallID: {Path: memory, Reason: "One memory holds both rules"}}},
		// A new answer replaces the whole older one, so it keeps no path and no reason of it.
		{"a reason replaces a path and a reason", []string{"--answer", heldID, "--reason", "The memory went."},
			"Answered " + heldID[:7] + ": The memory went.\n",
			map[string]sleepAnswer{heldID: {Reason: "The memory went."}, smallID: {Path: memory, Reason: "One memory holds both rules"}}},
		{"a path replaces a path and a reason", []string{"--answer", smallID, "--with", memory},
			"Answered " + smallID[:7] + " with " + memory + ".\n",
			map[string]sleepAnswer{heldID: {Reason: "The memory went."}, smallID: {Path: memory}}},
		// Two ids share 7 digits, so the list prints 8 digits of each, and an answer takes them.
		{"the longer short id of a twin", []string{"--answer", full(twinA)[:8] + "," + full(twinB)[:8], "--reason", "Both concern only their task."},
			"Answered " + full(twinA)[:8] + ": Both concern only their task.\nAnswered " + full(twinB)[:8] + ": Both concern only their task.\n",
			map[string]sleepAnswer{heldID: {Reason: "The memory went."}, smallID: {Path: memory}, full(twinA): {Reason: "Both concern only their task."}, full(twinB): {Reason: "Both concern only their task."}}},
	}
	if list, err := run(t, dev, "", "sleep", "--list"); err != nil || !strings.Contains(list, "\n"+full(twinA)[:8]+" 2026-11-18 10:05 ") || !strings.Contains(list, "\n"+full(twinB)[:8]+" 2026-11-18 10:05 ") {
		t.Errorf("sleep --list does not print 8 digits of each twin: %v\n%s", err, list)
	}
	for _, c := range good {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, dev, "", append([]string{"sleep"}, c.args...)...)
			if err != nil || out != c.out {
				t.Fatalf("sleep %v: %v\n got %q\nwant %q", c.args, err, out, c.out)
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
		{"an unknown id", []string{"--answer", "0123456789", "--reason", "r"}, []string{"0123456789", "memcore sleep --list", "memcore help sleep"}},
		{"an unknown id beside a known one", []string{"--answer", smallID + ",0123456789", "--reason", "r"}, []string{"0123456789", "memcore sleep --list"}},
		{"a short prefix", []string{"--answer", heldID[:6], "--reason", "r"}, []string{heldID[:6], "7 digits"}},
		{"an empty id", []string{"--answer", heldID + ",", "--reason", "r"}, []string{"usage: memcore sleep --answer"}},
		{"a prefix that fits two corrections", []string{"--answer", "1d2633e", "--reason", "r"}, []string{full(twinA), full(twinB)}},
		{"a path that is no document", []string{"--answer", heldID, "--with", "dev/memories/none.md"}, []string{"--with", "dev/memories/none.md"}},
		{"a folder", []string{"--answer", heldID, "--with", "dev/memories"}, []string{"--with", "dev/memories"}},
		{"the git directory", []string{"--answer", heldID, "--with", ".git/config"}, []string{"--with", "not a path in the memory repo"}},
		{"the git directory in capitals", []string{"--answer", heldID, "--with", ".GIT/config"}, []string{"--with", "not a path in the memory repo"}},
		{"a path outside the memory repo", []string{"--answer", heldID, "--with", "../memory/dev/memories/ask-before-push.md"}, []string{"--with", "not a path in the memory repo"}},
		{"no --with and no --reason", []string{"--answer", heldID}, []string{"--with <path>", "--reason <text>"}},
		{"an empty reason alone", []string{"--answer", heldID, "--reason", " "}, []string{"--with <path>", "--reason <text>"}},
		{"--answer beside --budget", []string{"--answer", heldID, "--reason", "r", "--budget", "1"}, []string{"usage: memcore sleep --answer"}},
		{"--answer beside --sessions", []string{"--answer", heldID, "--reason", "r", "--sessions", "1"}, []string{"usage: memcore sleep --answer"}},
		{"--answer beside --list", []string{"--list", "--answer", heldID, "--reason", "r"}, []string{"usage: memcore sleep --answer"}},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, dev, "", append([]string{"sleep"}, c.args...)...)
			if err == nil {
				t.Fatalf("sleep %v: no error\n%s", c.args, out)
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("sleep %v: the error lacks %q: %v", c.args, want, err)
				}
			}
			if strings.Contains(out, "Answered") {
				t.Errorf("sleep %v printed an answer: %s", c.args, out)
			}
		})
	}
	if after, err := readAnswers(repo); err != nil || !reflect.DeepEqual(after, before) {
		t.Errorf("a refused call changed the answers: %+v, %v", after, err)
	}

	// --with or --reason without --answer starts no pass, also for the person at a terminal.
	t.Setenv("CLAUDECODE", "")
	for _, args := range [][]string{{"--with", memory}, {"--reason", "r"}, {"--reason", "r", "--sessions", "1"}, {"--list", "--with", memory}} {
		if _, err := run(t, dev, "", append([]string{"sleep"}, args...)...); err == nil || !strings.Contains(err.Error(), "usage: memcore sleep") || !strings.Contains(err.Error(), "--answer") {
			t.Errorf("sleep %v: %v, want a usage error that names --answer", args, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("the agent got %d calls, want none", len(*calls))
	}
	if records, _ := readSleep(repo); len(records) != 1 {
		t.Errorf("%d records, want the one of the fixture: no call may start a pass", len(records))
	}
}
