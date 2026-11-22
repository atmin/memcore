package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/thinking"
)

// evalTrap returns the URL of a server that fails the test when a request reaches it: the check of a test runs a
// fake agent, so no request may leave it.
func evalTrap(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request reached the server: %s %s", r.Method, r.URL.Path)
		http.Error(w, "no", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// evalClock makes each call of the agent take 30 seconds, as the check measures it.
func evalClock(t *testing.T) {
	t.Helper()
	prev := clock
	t.Cleanup(func() { clock = prev })
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ticks := 0
	clock = func() time.Time {
		ticks++
		return now.Add(time.Duration(ticks/2) * 30 * time.Second)
	}
}

// evalRecords writes the records of a pass over the two sessions of reflectFixture, with the corrections of each.
func evalRecords(t *testing.T, repo string, first, second []reflectCorrection) {
	t.Helper()
	for _, r := range []reflectRecord{
		{Session: "s-first", File: "claude/p1/s-first.jsonl", Level: "~/dev", Corrections: first},
		{Session: "s-second", File: "claude/p1/s-second.jsonl", Level: "~/dev", Corrections: second},
	} {
		r.Blob = strings.TrimSpace(gitT(t, repo, "rev-parse", transcriptsRef+":"+r.File))
		r.Agent, r.Model = "claude", "sonnet"
		if err := appendReflect(repo, r); err != nil {
			t.Fatal(err)
		}
	}
}

// lineTimes returns the times of the lines of the sessions of reflectFixture: three of s-first, and one of s-second.
func lineTimes(t *testing.T, repo string) (first []string, second string) {
	t.Helper()
	for _, l := range strings.Split(gitT(t, repo, "cat-file", "-p", transcriptsRef+":claude/p1/s-first.jsonl"), "\n")[1:] {
		var line struct{ Time string }
		if json.Unmarshal([]byte(l), &line) == nil {
			first = append(first, line.Time)
		}
	}
	var line struct{ Time string }
	if err := json.Unmarshal([]byte(strings.Split(gitT(t, repo, "cat-file", "-p", transcriptsRef+":claude/p1/s-second.jsonl"), "\n")[1]), &line); err != nil {
		t.Fatal(err)
	}
	return first, line.Time
}

// evalAgent replaces the headless agent with extract and judge, which answer by the request. It returns the
// requests. Each reply reports a token for each two bytes of the prompt and the input, and 10 tokens of answer.
func evalAgent(t *testing.T, extract, judge func(req thinking.Request) (string, error)) *[]thinking.Request {
	t.Helper()
	var calls []thinking.Request
	prev := thinking.Run
	t.Cleanup(func() { thinking.Run = prev })
	thinking.Run = func(req thinking.Request) (thinking.Reply, error) {
		calls = append(calls, req)
		answer := extract
		if strings.Contains(req.Schema, `"findings"`) {
			answer = judge
		}
		out, err := answer(req)
		if err != nil {
			return thinking.Reply{}, err
		}
		return thinking.Reply{Output: []byte(out), In: (len(req.Prompt) + len(req.Input)) / 2, Out: 10}, nil
	}
	return &calls
}

func TestReflectEval(t *testing.T) {
	_, repo := reflectFixture(t)
	times, second := lineTimes(t, repo)
	retry, push, small := times[0], times[1], times[2]
	pushC := reflectCorrection{Time: push, Quote: "Never push without asking.", Summary: "Ask before a push.", Verdict: verdictRetrieval, Path: "dev/memories/ask-before-push.md"}
	evalRecords(t, repo,
		[]reflectCorrection{
			pushC,
			{Time: small, Quote: "Use small commits.", Summary: "Commit small.", Verdict: verdictCapture},
			{Time: small, Quote: "small commits.", Summary: "Keep commits small.", Verdict: verdictCapture},
		},
		[]reflectCorrection{{Time: second, Quote: "Rename the flag.", Summary: "Name flags well.", Verdict: verdictBefore}},
	)
	if err := appendAnswers(repo, []reflectAnswer{
		{ID: correctionID("s-first", push, pushC.Quote), Session: "s-first", Time: push, Path: "dev/memories/ask-before-push.md", Answered: "2026-11-21T10:00:00Z"},
		{ID: correctionID("s-first", small, "Use small commits."), Session: "s-first", Time: small, Reason: "No change.", Answered: "2026-11-21T10:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}
	extract := func(req thinking.Request) (string, error) {
		if strings.Contains(req.Input, `"session":"s-first"`) {
			// The last quote matches no line of the person.
			return fmt.Sprintf(`{"corrections":[{"time":%q,"quote":"Never push without asking.","summary":"Ask first."},{"time":%q,"quote":"Add a retry.","summary":"Retry fetches."},{"time":%q,"quote":"Never force push.","summary":"Ask  before a force push."}]}`, push, retry, push), nil
		}
		return fmt.Sprintf(`{"corrections":[{"time":%q,"quote":"Rename the flag.","summary":"Name flags well."}]}`, second), nil
	}
	judge := func(thinking.Request) (string, error) {
		return `{"findings":[{"index":0,"kind":"holds","path":"dev/memories/ask-before-push.md","reason":"The memory says so."},{"index":1,"kind":"none","reason":"No memory holds it."},{"index":2,"kind":"unrelated","reason":"Only this task."}]}`, nil
	}
	endpoint := evalTrap(t)

	t.Run("both parts", func(t *testing.T) {
		evalClock(t)
		calls := evalAgent(t, extract, judge)
		dir := filepath.Join(t.TempDir(), "eval")
		var out bytes.Buffer
		if err := RunReflectEval(ReflectEval{Endpoint: endpoint, Model: "small", Context: 32768, Out: dir}, &out); err != nil {
			t.Fatal(err)
		}
		in := 0
		for _, req := range *calls {
			in += (len(req.Prompt) + len(req.Input)) / 2
			if req.Endpoint != endpoint || req.Model != "small" {
				t.Errorf("request with the endpoint %q and the model %q", req.Endpoint, req.Model)
			}
		}
		want := "Model small at " + endpoint + ", with a context of 32768 tokens.\n" +
			"\n" +
			"Extract: 2 records, 0 failed, 0 over the context.\n" +
			"  Recorded lines: 3. Found: 2 (67%). Missed: 1.\n" +
			"  With a document: 1 of 1 found (100%).\n" +
			"  Extra lines: 1.\n" +
			"  Quotes that match no line: 1.\n" +
			"Judge: 1 group, 0 failed, 0 over the context.\n" +
			"  Corrections judged: 3. Same verdict: 2 (67%).\n" +
			"  With a memory: 1 of 1 same (100%).\n" +
			"  The most frequent recorded verdict, capture: 2 of 3 (67%).\n" +
			"  capture as unrelated: 1\n" +
			fmt.Sprintf("Calls: 3 in 1m30s. Tokens: %d in, 30 out, at 2.0 bytes for a token.\n", in)
		if out.String() != want {
			t.Errorf("report:\n%s\nwant:\n%s", out.String(), want)
		}

		lines := strings.Split(strings.TrimSpace(readT(t, filepath.Join(dir, "results.jsonl"))), "\n")
		if len(lines) != 2 {
			t.Fatalf("results.jsonl has %d lines, want 2", len(lines))
		}
		var results []evalResult
		for _, l := range lines {
			var r evalResult
			if err := json.Unmarshal([]byte(l), &r); err != nil {
				t.Fatal(err)
			}
			results = append(results, r)
		}
		first := results[0]
		if first.Session != "s-first" || first.File != "claude/p1/s-first.jsonl" || first.Extract == nil || first.Judge == nil {
			t.Fatalf("first result = %+v", first)
		}
		x := first.Extract
		if x.Outcome != "ok" || len(x.Found) != 1 || len(x.Missed) != 1 || len(x.Extra) != 1 {
			t.Fatalf("extract = %+v", x)
		}
		if f := x.Found[0]; f.Time != push || len(f.Recorded) != 1 || f.Recorded[0].Quote != "Never push without asking." || len(f.Local) != 1 || f.Local[0].Summary != "Ask first." {
			t.Errorf("found = %+v", f)
		}
		if m := x.Missed[0]; m.Time != small || len(m.Recorded) != 2 || len(m.Local) != 0 {
			t.Errorf("missed = %+v, want the line of two recorded corrections", m)
		}
		if e := x.Extra[0]; e.Time != retry || len(e.Recorded) != 0 || len(e.Local) != 1 || e.Local[0].Quote != "Add a retry." {
			t.Errorf("extra = %+v", e)
		}
		if want := []evalUnmatched{{Time: push, Quote: "Never force push.", Summary: "Ask before a force push."}}; !slices.Equal(x.Unmatched, want) {
			t.Errorf("unmatched = %+v, want %+v", x.Unmatched, want)
		}
		if len(first.Judge) != 1 || first.Judge[0].Outcome != "ok" || len(first.Judge[0].Pairs) != 3 {
			t.Fatalf("judge = %+v", first.Judge)
		}
		if p := first.Judge[0].Pairs[2]; p.Time != small || p.Quote != "small commits." || p.Recorded != verdictCapture || p.Local != verdictUnrelated || p.Reason != "Only this task." {
			t.Errorf("pair = %+v", p)
		}
		if p := first.Judge[0].Pairs[0]; p.Recorded != verdictRetrieval || p.Local != verdictRetrieval || p.Path != "dev/memories/ask-before-push.md" {
			t.Errorf("pair = %+v", p)
		}
		if s := results[1]; s.Session != "s-second" || s.Extract == nil || len(s.Extract.Found) != 1 || len(s.Extract.Unmatched) != 0 || len(s.Judge) != 0 {
			t.Errorf("second result = %+v", s)
		}
	})

	t.Run("unmatched quotes of a record whose last part fails", func(t *testing.T) {
		// The context fits the longest line of s-first, but not the whole transcript, so the extract makes a call
		// for each part. The first part gives a quote that matches no line, and the last part fails.
		tr := gitT(t, repo, "cat-file", "-p", transcriptsRef+":claude/p1/s-first.jsonl")
		header, rest, _ := strings.Cut(tr, "\n")
		longest := 0
		for _, l := range strings.SplitAfter(rest, "\n") {
			longest = max(longest, len(l))
		}
		calls := evalAgent(t, func(req thinking.Request) (string, error) {
			switch {
			case strings.Contains(req.Input, "Use small commits."):
				return "", errors.New("call the local model: it answered 500 Internal Server Error")
			case strings.Contains(req.Input, "Add a retry."):
				return fmt.Sprintf(`{"corrections":[{"time":%q,"quote":"Never force push.","summary":"Ask first."}]}`, push), nil
			}
			return `{"corrections":[]}`, nil
		}, judge)
		dir := filepath.Join(t.TempDir(), "eval")
		var out bytes.Buffer
		e := ReflectEval{Endpoint: endpoint, Model: "small", Context: evalReserve + (len(header)+1+longest)/evalBytesPerToken + 1, Out: dir, Sessions: 1, Stage: "extract"}
		if err := RunReflectEval(e, &out); err != nil {
			t.Fatal(err)
		}
		if len(*calls) < 2 {
			t.Fatalf("%d calls, want one for each part", len(*calls))
		}
		for _, w := range []string{"Extract: 1 record, 1 failed, 0 over the context.\n", "  Quotes that match no line: 1.\n"} {
			if !strings.Contains(out.String(), w) {
				t.Errorf("the report lacks %q:\n%s", w, out.String())
			}
		}
		var r evalResult
		if err := json.Unmarshal([]byte(readT(t, filepath.Join(dir, "results.jsonl"))), &r); err != nil {
			t.Fatal(err)
		}
		if want := []evalUnmatched{{Time: push, Quote: "Never force push.", Summary: "Ask first."}}; r.Extract.Outcome != evalFailed || !slices.Equal(r.Extract.Unmatched, want) {
			t.Errorf("extract = %+v, want a failure with the unmatched quote", r.Extract)
		}
	})

	t.Run("one part and one session", func(t *testing.T) {
		for _, c := range []struct {
			stage   string
			calls   int
			has     []string
			hasNone []string
		}{
			{"extract", 1, []string{"Extract: 1 record, 0 failed, 0 over the context.\n", "Recorded lines: 2. Found: 1 (50%). Missed: 1.\n", "  Quotes that match no line: 1.\n", "Calls: 1 in"}, []string{"Judge:", "With a memory", "The most frequent recorded verdict"}},
			{"judge", 1, []string{"Judge: 1 group, 0 failed, 0 over the context.\n", "  With a memory: 1 of 1 same (100%).\n", "  The most frequent recorded verdict, capture: 2 of 3 (67%).\n", "Calls: 1 in"}, []string{"Extract:", "Recorded lines", "With a document", "Extra lines", "Quotes that match no line"}},
		} {
			t.Run(c.stage, func(t *testing.T) {
				calls := evalAgent(t, extract, judge)
				dir := filepath.Join(t.TempDir(), "eval")
				var out bytes.Buffer
				if err := RunReflectEval(ReflectEval{Endpoint: endpoint, Model: "small", Context: 32768, Out: dir, Sessions: 1, Stage: c.stage}, &out); err != nil {
					t.Fatal(err)
				}
				if len(*calls) != c.calls {
					t.Errorf("%d calls, want %d", len(*calls), c.calls)
				}
				for _, w := range c.has {
					if !strings.Contains(out.String(), w) {
						t.Errorf("the report lacks %q:\n%s", w, out.String())
					}
				}
				for _, w := range c.hasNone {
					if strings.Contains(out.String(), w) {
						t.Errorf("the report holds %q:\n%s", w, out.String())
					}
				}
				if n := strings.Count(readT(t, filepath.Join(dir, "results.jsonl")), "\n"); n != 1 {
					t.Errorf("results.jsonl has %d lines, want 1", n)
				}
			})
		}
	})

	t.Run("a judge that fails", func(t *testing.T) {
		// No correction is judged, so "With a memory" has a whole of 0, and the report names no most frequent verdict.
		evalAgent(t, extract, func(thinking.Request) (string, error) {
			return "", errors.New("call the local model: it answered 500 Internal Server Error")
		})
		var out bytes.Buffer
		if err := RunReflectEval(ReflectEval{Endpoint: endpoint, Model: "small", Context: 32768, Out: filepath.Join(t.TempDir(), "eval"), Sessions: 1, Stage: "judge"}, &out); err != nil {
			t.Fatal(err)
		}
		want := "Judge: 1 group, 1 failed, 0 over the context.\n" +
			"  Corrections judged: 0. Same verdict: 0.\n" +
			"  With a memory: 0 of 0 same.\n" +
			"Calls: 1 in "
		if !strings.Contains(out.String(), want) {
			t.Errorf("report:\n%s\nwant it to hold:\n%s", out.String(), want)
		}
	})
}

func TestReflectEvalFailures(t *testing.T) {
	_, repo := reflectFixture(t)
	times, second := lineTimes(t, repo)
	// s-long has one line over the limit of an input.
	old := time.Now().Add(-47 * time.Hour).UTC().Truncate(time.Second)
	transcriptT(t, repo, "claude/p1/s-long.jsonl", transcriptText("s-long", old, "you|"+strings.Repeat("z", 1000)+" Stop.", "you|Short."))
	commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	if e := startEvent("s-long", old, commit); appendEvent(repo, &e) != nil {
		t.Fatal("append the start of s-long")
	}
	evalRecords(t, repo,
		[]reflectCorrection{
			{Time: times[1], Quote: "Never push without asking.", Summary: "Ask before a push.", Verdict: verdictRetrieval, Path: "dev/memories/ask-before-push.md"},
			{Time: times[2], Quote: "Use small commits.", Summary: "Commit small.", Verdict: verdictCapture},
		},
		// The summary makes the input of the judge too long for the context.
		[]reflectCorrection{{Time: second, Quote: "Rename the flag.", Summary: strings.Repeat("Name flags well. ", 50), Verdict: verdictCapture}},
	)
	long := reflectRecord{Session: "s-long", File: "claude/p1/s-long.jsonl", Level: "~/dev", Agent: "claude",
		Corrections: []reflectCorrection{
			{Time: old.Add(time.Minute).Format(time.RFC3339), Quote: "Short.", Summary: "Be short.", Verdict: verdictCapture},
			// No logged event of s-long comes before this time, so no commit says what the session saw.
			{Time: old.Add(-time.Hour).Format(time.RFC3339), Quote: "Earlier.", Summary: "Before the log.", Verdict: verdictCapture},
		}}
	long.Blob = strings.TrimSpace(gitT(t, repo, "rev-parse", transcriptsRef+":"+long.File))
	if err := appendReflect(repo, long); err != nil {
		t.Fatal(err)
	}

	calls := evalAgent(t, func(req thinking.Request) (string, error) {
		if strings.Contains(req.Input, `"session":"s-first"`) {
			return "", errors.New("call the local model: it answered 500 Internal Server Error")
		}
		return fmt.Sprintf(`{"corrections":[{"time":%q,"quote":"Rename the flag.","summary":"Name flags."}]}`, second), nil
	}, func(req thinking.Request) (string, error) {
		if strings.Contains(req.Input, "Be short.") {
			return "", errors.New("read the reply of the local model: it holds no choice")
		}
		return `{"findings":[{"index":0,"kind":"holds","path":"dev/memories/ask-before-push.md","reason":"Held."},{"index":1,"kind":"none","reason":"None."}]}`, nil
	})
	dir := filepath.Join(t.TempDir(), "eval")
	var out bytes.Buffer
	// A context of 8192 + 200 tokens leaves 600 bytes for an input.
	if err := RunReflectEval(ReflectEval{Endpoint: evalTrap(t), Model: "small", Context: 8392, Out: dir}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Extract: 3 records, 1 failed, 1 over the context.\n",
		"  Recorded lines: 1. Found: 1 (100%). Missed: 0.\n",
		"  With a document: 0 of 0 found.\n",
		"  Extra lines: 0.\n",
		"  Quotes that match no line: 0.\n",
		"Judge: 4 groups, 2 failed, 1 over the context.\n",
		"  Corrections judged: 2. Same verdict: 2 (100%).\n",
		"  With a memory: 1 of 1 same (100%).\n",
		// Capture and retrieval are as frequent, and capture comes first in alphabetical order.
		"  The most frequent recorded verdict, capture: 1 of 2 (50%).\n",
		"Calls: 4 in ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report lacks %q:\n%s", want, out.String())
		}
	}
	if len(*calls) != 4 {
		t.Errorf("%d calls, want 4: two extracts and two judges", len(*calls))
	}
	var outcomes []string
	for _, l := range strings.Split(strings.TrimSpace(readT(t, filepath.Join(dir, "results.jsonl"))), "\n") {
		var r evalResult
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatal(err)
		}
		outcomes = append(outcomes, r.Extract.Outcome)
		for _, g := range r.Judge {
			outcomes = append(outcomes, g.Outcome)
		}
		if r.Extract.Outcome == evalFailed && !strings.Contains(r.Extract.Error, "500 Internal Server Error") {
			t.Errorf("the failed extract of %s keeps the error %q", r.Session, r.Extract.Error)
		}
		for _, g := range r.Judge {
			if g.Commit == "" && (g.Outcome != evalFailed || !strings.Contains(g.Error, "use log")) {
				t.Errorf("the group without a commit = %+v, want a failure that names the use log", g)
			}
		}
	}
	if got := strings.Join(outcomes, ","); got != "failed,ok,ok,over the context,over the context,failed,failed" {
		t.Errorf("outcomes = %s", got)
	}
}

func TestReflectEvalRefuses(t *testing.T) {
	_, repo := reflectFixture(t)
	fakeAgent(t, func(int, thinking.Request) (thinking.Reply, error) {
		t.Error("the check called the agent")
		return thinking.Reply{}, errors.New("no call")
	}, func(thinking.Request) (thinking.Reply, error) {
		t.Error("the check called the agent")
		return thinking.Reply{}, errors.New("no call")
	})
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	full := mkdirT(t, filepath.Join(t.TempDir(), "full"))
	writeT(t, full, "results.jsonl", "earlier results\n")
	linked := mkdirT(t, filepath.Join(t.TempDir(), "linked"))
	target := filepath.Join(outside, "target.jsonl")
	writeT(t, outside, "target.jsonl", "a file elsewhere\n")
	if err := os.Symlink(target, filepath.Join(linked, "results.jsonl")); err != nil {
		t.Fatal(err)
	}
	ok := ReflectEval{Endpoint: "http://127.0.0.1:0", Model: "small", Context: 32768, Out: filepath.Join(t.TempDir(), "eval")}
	cases := []struct {
		name    string
		change  func(*ReflectEval)
		wantErr string
	}{
		{"a host that is not on the machine", func(e *ReflectEval) { e.Endpoint = "http://example.com:8080" }, "not on this machine"},
		{"a host that names the machine in a longer name", func(e *ReflectEval) { e.Endpoint = "http://localhost.example.com" }, "not on this machine"},
		{"an endpoint that is no URL", func(e *ReflectEval) { e.Endpoint = "127.0.0.1:8080" }, "no URL"},
		{"no endpoint", func(e *ReflectEval) { e.Endpoint = "" }, "--endpoint"},
		{"no model", func(e *ReflectEval) { e.Model = "" }, "--model"},
		{"no out", func(e *ReflectEval) { e.Out = "" }, "--out"},
		{"a context of 8192", func(e *ReflectEval) { e.Context = 8192 }, "--context"},
		{"a negative count of sessions", func(e *ReflectEval) { e.Sessions = -1 }, "--sessions"},
		{"an unknown stage", func(e *ReflectEval) { e.Stage = "both" }, "--stage"},
		{"an out inside the memory repo", func(e *ReflectEval) { e.Out = filepath.Join(repo, "eval") }, "memory repo"},
		{"an out that is the memory repo", func(e *ReflectEval) { e.Out = repo }, "memory repo"},
		{"an out that is a symlink into the memory repo", func(e *ReflectEval) { e.Out = filepath.Join(link, "eval") }, "memory repo"},
		{"an out that holds results.jsonl", func(e *ReflectEval) { e.Out = full }, "results.jsonl"},
		{"an out that holds results.jsonl as a symlink", func(e *ReflectEval) { e.Out = linked }, "results.jsonl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := ok
			c.change(&e)
			var out bytes.Buffer
			err := RunReflectEval(e, &out)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want one with %q", err, c.wantErr)
			}
			if out.Len() != 0 {
				t.Errorf("the check printed %q", out.String())
			}
		})
	}
	if _, err := os.Lstat(filepath.Join(repo, "eval")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused check made a folder in the memory repo: %v", err)
	}
	// A local state that is a symlink to a folder outside the repo is part of the repo too.
	state := store.StateDir(repo)
	moved := filepath.Join(t.TempDir(), "state")
	if err := os.Rename(state, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, state); err != nil {
		t.Fatal(err)
	}
	e := ok
	e.Out = filepath.Join(moved, "eval")
	if err := RunReflectEval(e, io.Discard); err == nil || !strings.Contains(err.Error(), "local state") {
		t.Errorf("an out in the local state: err = %v, want one with %q", err, "local state")
	}
	if _, err := os.Lstat(e.Out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused check made a folder in the local state: %v", err)
	}
	clean(t, repo)
	if got := readT(t, filepath.Join(full, "results.jsonl")); got != "earlier results\n" {
		t.Errorf("the check replaced results.jsonl: %q", got)
	}
	if got := readT(t, target); got != "a file elsewhere\n" {
		t.Errorf("the check wrote through the symlink: %q", got)
	}
}

func TestReflectEvalWritesNothing(t *testing.T) {
	_, repo := reflectFixture(t)
	times, second := lineTimes(t, repo)
	evalRecords(t, repo,
		[]reflectCorrection{{Time: times[1], Quote: "Never push without asking.", Summary: "Ask before a push.", Verdict: verdictCapture}},
		[]reflectCorrection{{Time: second, Quote: "Rename the flag.", Summary: "Name flags well.", Verdict: verdictCapture}},
	)
	if err := appendAnswers(repo, []reflectAnswer{{ID: correctionID("s-first", times[1], "Never push without asking."), Session: "s-first", Time: times[1], Reason: "No change.", Answered: "2026-11-21T10:00:00Z"}}); err != nil {
		t.Fatal(err)
	}
	state := store.StateDir(repo)
	snapshot := func() map[string]string {
		files := map[string]string{}
		entries, err := os.ReadDir(state)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			files[e.Name()] = ""
		}
		for _, name := range []string{"reflect.jsonl", "answers.jsonl", "log.jsonl"} {
			files[name] = readT(t, filepath.Join(state, name))
		}
		return files
	}
	before := snapshot()
	evalAgent(t, func(thinking.Request) (string, error) {
		return `{"corrections":[]}`, nil
	}, func(thinking.Request) (string, error) {
		return `{"findings":[{"index":0,"kind":"none","reason":"None."}]}`, nil
	})
	var out bytes.Buffer
	if err := RunReflectEval(ReflectEval{Endpoint: evalTrap(t), Model: "small", Context: 32768, Out: filepath.Join(t.TempDir(), "eval")}, &out); err != nil {
		t.Fatal(err)
	}
	after := snapshot()
	if len(after) != len(before) {
		t.Errorf("the local state holds %d files, before %d", len(after), len(before))
	}
	for name, content := range before {
		if got, ok := after[name]; !ok || got != content {
			t.Errorf("%s changed", name)
		}
	}
	clean(t, repo)
}
