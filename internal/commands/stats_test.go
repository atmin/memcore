package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// statsNow is the time of the call in the tests of stats.
var statsNow = time.Date(2026, 12, 10, 10, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// ago returns the time of a log line d before statsNow.
func ago(d time.Duration) string {
	return statsNow.Add(-d).Format(time.RFC3339)
}

// setClock makes clock return now for one test.
func setClock(t *testing.T, now time.Time) {
	t.Helper()
	old := clock
	clock = func() time.Time { return now }
	t.Cleanup(func() { clock = old })
}

// writeLog replaces the use log of a memory repo with lines.
func writeLog(t *testing.T, repo string, lines ...string) {
	t.Helper()
	text := strings.Join(lines, "\n")
	if text != "" {
		text += "\n"
	}
	if err := os.WriteFile(filepath.Join(store.StateDir(repo), "log.jsonl"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMeasure(t *testing.T) {
	end1, end2, end3 := statsNow.Add(-7*day), statsNow.Add(-14*day), statsNow.Add(-21*day)
	for _, c := range []struct {
		name   string
		events []Event
		want   []week
	}{
		{
			name: "a line at the edge of a week",
			events: []Event{
				{Time: ago(0), Command: "help", Outcome: "ok"},
				{Time: ago(7*day - time.Second), Command: "help", Outcome: "ok"},
				{Time: ago(7 * day), Command: "help", Outcome: "ok"},
				{Time: ago(22 * day), Command: "help", Outcome: "ok"},
				{Time: ago(-time.Minute), Command: "help", Outcome: "ok"},
			},
			want: []week{{end: statsNow, lines: 2}, {end: end1, lines: 1}, {end: end2}, {end: end3, lines: 1}},
		},
		{
			name: "a young log leaves out the weeks before it",
			events: []Event{
				{Time: ago(day), Command: "help", Outcome: "ok"},
				{Time: ago(10 * day), Command: "help", Outcome: "ok"},
			},
			want: []week{{end: statsNow, lines: 1}, {end: end1, lines: 1}},
		},
		{
			name:   "an empty log has no week",
			events: nil,
			want:   nil,
		},
		{
			name: "capture",
			events: []Event{
				{Time: ago(day), Session: "s1", Command: "remember", Paths: []string{"dev/memories/a.md"}, Outcome: "ok", Made: []string{"c1"}},
				{Time: ago(day), Command: "revise", Paths: []string{"dev/memories/a.md"}, Outcome: "ok", Made: []string{"c2"}},
				{Time: ago(day), Command: "revise", Paths: []string{"dev/plans/p.md"}, Outcome: "ok", Made: []string{"c3"}},
				{Time: ago(day), Command: "revise", Paths: []string{"dev/memories/a.md"}, Outcome: "ok"},
				{Time: ago(day), Command: "forget", Paths: []string{"dev/memories/b.md"}, Outcome: "ok", Made: []string{"c4"}},
				{Time: ago(day), Command: "draft", Paths: []string{"dev/plans/q.md"}, Outcome: "ok", Made: []string{"c5"}},
				{Time: ago(day), Command: "forget", Paths: []string{"dev/skills/deploy"}, Outcome: "ok", Made: []string{"c6"}},
				{Time: ago(day), Command: "remember", Paths: []string{"dev/memories/c.md"}, Outcome: "error", Error: "index the level", Made: []string{"c7"}},
			},
			want: []week{{
				end: statsNow, lines: 8, sessions: 1,
				memories: tally{remembered: 2, revised: 1, forgotten: 1},
				others:   tally{drafted: 1, revised: 1, forgotten: 1},
				failed:   1, errors: []ranked{{1, "remember: index the level"}},
			}},
		},
		{
			name: "use",
			events: []Event{
				{Time: ago(day), Session: "s1", Command: "show", Paths: []string{"dev/memories/a.md"}, Outcome: "ok"},
				{Time: ago(day), Session: "s1", Command: "show", Paths: []string{"dev/memories/a.md"}, Outcome: "ok"},
				{Time: ago(day), Session: "s1", Command: "show", Paths: []string{"dev/memories/a.md"}, Outcome: "ok"},
				{Time: ago(day), Session: "s2", Command: "show", Paths: []string{"dev/memories/b.md"}, Outcome: "ok"},
				{Time: ago(day), Session: "s1", Command: "show", Paths: []string{"skills/voice/SKILL.md"}, Outcome: "ok"},
				{Time: ago(day), Session: "s1", Command: "show", Paths: []string{"dev/memories/z.md"}, Outcome: "error", Error: "no document dev/memories/z.md"},
				{Time: ago(day), Command: "show", Paths: []string{"dev/memories/a.md"}, Outcome: "ok"},
				{Time: ago(day), Session: "s2", Command: "hook", Hook: "tool", Paths: []string{"dev/memories/b.md", "dev/memories/c.md"}, Outcome: "ok"},
				{Time: ago(day), Command: "hook", Hook: "tool", Paths: []string{"dev/memories/c.md"}, Outcome: "ok"},
				{Time: ago(day), Session: "s3", Command: "hook", Hook: "session-end", Outcome: "ok"},
			},
			want: []week{{
				end: statsNow, lines: 10, sessions: 3, reads: 7, around: 2,
				top:    []ranked{{3, "dev/memories/a.md"}, {2, "dev/memories/b.md"}, {1, "dev/memories/c.md"}},
				failed: 1, errors: []ranked{{1, "show: no document <path>"}},
			}},
		},
		{
			name: "the five most-read memories, ties by path",
			events: []Event{
				{Time: ago(day), Session: "s1", Command: "hook", Hook: "tool", Paths: []string{"m/memories/f.md", "m/memories/e.md", "m/memories/d.md"}, Outcome: "ok"},
				{Time: ago(day), Session: "s1", Command: "hook", Hook: "tool", Paths: []string{"m/memories/c.md", "m/memories/b.md", "m/memories/a.md"}, Outcome: "ok"},
				{Time: ago(day), Session: "s1", Command: "show", Paths: []string{"m/memories/e.md"}, Outcome: "ok"},
			},
			want: []week{{
				end: statsNow, lines: 3, sessions: 1, reads: 7, around: 6,
				top: []ranked{{2, "m/memories/e.md"}, {1, "m/memories/a.md"}, {1, "m/memories/b.md"}, {1, "m/memories/c.md"}, {1, "m/memories/d.md"}},
			}},
		},
		{
			name: "context cost",
			events: []Event{
				{Time: ago(3 * day), Session: "s1", Command: "orient", Outcome: "ok", Words: map[string]int{"header": 500, "~": 100, "~/dev": 50}, Lines: map[string]int{"~": 10, "~/dev": 3}},
				{Time: ago(2 * day), Session: "s2", Command: "hook", Outcome: "ok", Words: map[string]int{"header": 575, "~": 120}},
				{Time: ago(day), Command: "orient", Outcome: "ok", Words: map[string]int{"header": 580, "~/dev/app": 954}, Lines: map[string]int{"~/dev/app": 48}},
				{Time: ago(5 * day), Session: "s1", Command: "orient", Outcome: "ok", Words: map[string]int{"header": 1, "~": 1}, Lines: map[string]int{"~": 1}},
			},
			want: []week{{
				end: statsNow, lines: 4, sessions: 2,
				words: 674, // (652 + 695) / 2 = 673.5
				costs: []cost{{"header", 580, -1}, {"~", 120, -1}, {"~/dev", 50, 3}, {"~/dev/app", 954, 48}},
			}},
		},
		{
			name: "friction groups errors that differ in a path, a commit or a number",
			events: []Event{
				{Time: ago(day), Command: "revise", Paths: []string{"dev/memories/a.md"}, Outcome: "error", Error: "dev/memories/a.md: 2 faults, so revise wrote nothing"},
				{Time: ago(day), Command: "revise", Paths: []string{"dev/plans/b.md"}, Outcome: "error", Error: "dev/plans/b.md: 13 faults, so revise wrote nothing"},
				{Time: ago(day), Command: "drop", Outcome: "error", Error: "no commit 1a2b3c4 in the queue"},
				{Time: ago(day), Command: "drop", Outcome: "error", Error: "no commit 9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f0e in the queue"},
				{Time: ago(day), Command: "drop", Outcome: "error", Error: "no commit deadbeef in the queue"},
				{Time: ago(day), Command: "push", Outcome: "error", Error: "push e"},
				{Time: ago(day), Command: "push", Outcome: "error", Error: "push f"},
				{Time: ago(day), Command: "push", Outcome: "error", Error: "push g"},
				{Time: ago(day), Session: "s1", Command: "push", Outcome: "ok"},
			},
			want: []week{{
				end: statsNow, lines: 9, sessions: 1, failed: 8,
				errors: []ranked{
					{2, "drop: no commit <commit> in the queue"},
					{2, "revise: <path>: <n> faults, so revise wrote nothing"},
					{1, "drop: no commit deadbeef in the queue"},
					{1, "push: push e"},
					{1, "push: push f"},
				},
				moreErrors: 1,
			}},
		},
		{
			name: "marks",
			events: []Event{
				{Time: ago(day), Session: "s1", Command: "hook", Hook: "prompt", Outcome: "ok", Marks: []string{"wrong"}},
				{Time: ago(day), Command: "hook", Hook: "prompt", Outcome: "ok", Marks: []string{"again", "wrong"}},
				{Time: ago(day), Session: "s2", Command: "hook", Hook: "prompt", Outcome: "ok"},
			},
			want: []week{{end: statsNow, lines: 3, sessions: 2, marks: [3]int{1, 2, 0}}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := measure(c.events, statsNow)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("measure:\n got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestStats(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	setClock(t, statsNow)

	t.Run("a fixture log", func(t *testing.T) {
		writeLog(t, repo,
			`{"time":"`+ago(15*day+time.Hour)+`","session":"s0","command":"orient","outcome":"ok","words":{"header":575,"~":200},"lines":{"~":12}}`,
			`{"time":"`+ago(26*time.Hour)+`","session":"s1","level":"~/dev/app","command":"hook","hook":"session-start","outcome":"ok","words":{"header":575,"~/dev/app":954,"~":300},"lines":{"~/dev/app":48,"~":12}}`,
			`{"time":"`+ago(25*time.Hour)+`","session":"s1","command":"show","paths":["dev/app/memories/small-commits.md"],"outcome":"ok"}`,
			`{"time":"`+ago(25*time.Hour)+`","session":"s1","command":"show","paths":["dev/app/memories/small-commits.md"],"outcome":"ok"}`,
			`{"time":"`+ago(24*time.Hour)+`","session":"s2","command":"orient","outcome":"ok","words":{"header":575,"~":300},"lines":{"~":12}}`,
			`{"time": "2026-12-09T`,
			`{"time":"`+ago(23*time.Hour)+`","session":"s2","command":"hook","hook":"tool","paths":["dev/app/memories/small-commits.md","dev/memories/older.md"],"outcome":"ok"}`,
			`{"time":"`+ago(22*time.Hour)+`","session":"s1","command":"remember","paths":["dev/app/memories/new.md"],"outcome":"ok","made":["1111111111111111111111111111111111111111"]}`,
			`{"time":"`+ago(21*time.Hour)+`","session":"s1","command":"revise","paths":["dev/app/plans/launch.md"],"outcome":"ok","made":["2222222222222222222222222222222222222222"]}`,
			`{"time":"`+ago(20*time.Hour)+`","session":"s2","command":"revise","paths":["skills/plan/SKILL.md"],"outcome":"error","error":"2 faults, so revise wrote nothing; memcore help revise shows the input"}`,
			`{"time":"`+ago(19*time.Hour)+`","session":"s2","command":"hook","hook":"prompt","outcome":"ok","marks":["wrong"]}`,
		)
		out, err := run(t, home, "", "stats")
		if err != nil {
			t.Fatalf("stats: %v\n%s", err, out)
		}
		want := `memcore stats to 2026-12-10 10:00 UTC, in weeks of 7 days, newest first.
1 line of the use log does not parse, so stats leaves it out.

The 7 days to 2026-12-10 10:00 UTC:
  Capture of memories: 1 remembered, 0 revised, 0 forgotten. Other documents: 0 drafted, 1 revised, 0 forgotten.
  Use: 2 sessions, 4 reads, 2.0 per session, 2 of them around memcore.
      3  dev/app/memories/small-commits.md
      1  dev/memories/older.md
  Context: orient printed 1352 words per session, about 1803 tokens.
    header: 575 words, about 767 tokens
    ~: 300 words, about 400 tokens, 12 lines of 40
    ~/dev/app: 954 words, about 1272 tokens, 48 lines of 40, over the budget
  Friction: 1 failed call.
      1  revise: <n> faults, so revise wrote nothing; memcore help revise shows the input
  Marks: 0 #again, 1 #wrong, 0 #stale.

The 7 days to 2026-12-03 10:00 UTC:
  No call of memcore.

The 7 days to 2026-11-26 10:00 UTC:
  Capture of memories: 0 remembered, 0 revised, 0 forgotten. Other documents: 0 drafted, 0 revised, 0 forgotten.
  Use: 1 session, 0 reads, 0.0 per session, 0 of them around memcore.
  Context: orient printed 775 words per session, about 1033 tokens.
    header: 575 words, about 767 tokens
    ~: 200 words, about 267 tokens, 12 lines of 40
  Friction: 0 failed calls.
  Marks: 0 #again, 0 #wrong, 0 #stale.
`
		if out != want {
			t.Errorf("output:\n%s\nwant:\n%s", out, want)
		}
	})

	t.Run("five groups of friction and the rest", func(t *testing.T) {
		var lines []string
		for _, e := range []string{"a", "b", "c", "d", "e", "f", "f"} {
			lines = append(lines, `{"time":"`+ago(time.Hour)+`","command":"push","outcome":"error","error":"`+e+`"}`)
		}
		writeLog(t, repo, lines...)
		out, err := run(t, home, "", "stats")
		if err != nil {
			t.Fatalf("stats: %v\n%s", err, out)
		}
		want := "  Friction: 7 failed calls.\n      2  push: f\n      1  push: a\n      1  push: b\n      1  push: c\n      1  push: d\n      and 1 more\n"
		if !strings.Contains(out, want) {
			t.Errorf("output:\n%s\nwant it to hold:\n%s", out, want)
		}
	})

	t.Run("an empty log", func(t *testing.T) {
		want := "memcore stats to 2026-12-10 10:00 UTC, in weeks of 7 days, newest first.\n\nThe use log is empty.\n"
		writeLog(t, repo)
		if out, err := run(t, home, "", "stats"); err != nil || out != want {
			t.Errorf("stats of an empty log: %v, output %q, want %q", err, out, want)
		}
		if err := os.Remove(filepath.Join(store.StateDir(repo), "log.jsonl")); err != nil {
			t.Fatal(err)
		}
		if out, err := run(t, home, "", "stats"); err != nil || out != want {
			t.Errorf("stats without a log: %v, output %q, want %q", err, out, want)
		}
	})

	t.Run("blank lines do not parse", func(t *testing.T) {
		writeLog(t, repo, "", "  ")
		want := "memcore stats to 2026-12-10 10:00 UTC, in weeks of 7 days, newest first.\n2 lines of the use log do not parse, so stats leaves them out.\n"
		if out, err := run(t, home, "", "stats"); err != nil || out != want {
			t.Errorf("stats of blank lines: %v, output %q, want %q", err, out, want)
		}
	})

	t.Run("stats writes nothing", func(t *testing.T) {
		before := gitT(t, repo, "rev-parse", "HEAD")
		if _, err := run(t, home, "", "stats", "extra"); err == nil || !strings.HasSuffix(err.Error(), "memcore help stats shows the input") {
			t.Errorf("stats extra: error = %v, want the usage", err)
		}
		if out, err := run(t, home, "", "stats"); err != nil {
			t.Fatalf("stats: %v\n%s", err, out)
		}
		if after := gitT(t, repo, "rev-parse", "HEAD"); after != before {
			t.Errorf("HEAD moved from %s to %s", before, after)
		}
		clean(t, repo)
	})
}
