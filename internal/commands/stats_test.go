package commands

import (
	"fmt"
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
				review:   review{created: 7, missing: 7},
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
			got := measure(c.events, nil, statsNow)
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
	writeT(t, repo, "dev/memories/older.md", memoryText("older", "Read when you test stats.", "A fact."))
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "remember: dev/memories/older.md")
	older := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))

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
			`{"time":"`+ago(18*time.Hour)+`","command":"push","outcome":"ok","pushed":["`+older+`"]}`,
		)
		out, err := run(t, home, "", "stats")
		if err != nil {
			t.Fatalf("stats: %v\n%s", err, out)
		}
		want := `memcore stats to 2026-12-10 10:00 UTC, in weeks of 7 days, newest first.
1 line of the use log does not parse, so stats leaves it out.

The 7 days to 2026-12-10 10:00 UTC:
  Capture of memories: 1 remembered, 0 revised, 0 forgotten. Other documents: 0 drafted, 1 revised, 0 forgotten.
  Review: 2 commits created, 0 of them fixups. 1 pushed, 0 dropped, 0 of them fixups.
  Accepted memories: 1, with 0 mistakes so far. Precision: 100%.
  2 logged commits are missing from git.
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
  Review: 0 commits created, 0 of them fixups. 0 pushed, 0 dropped, 0 of them fixups.
  Accepted memories: 0.
  Use: 1 session, 0 reads, 0.0 per session, 0 of them around memcore.
  Context: orient printed 775 words per session, about 1033 tokens.
    header: 575 words, about 767 tokens
    ~: 200 words, about 267 tokens, 12 lines of 40
  Friction: 0 failed calls.
  Marks: 0 #again, 0 #wrong, 0 #stale.

Memories with no logged read in 30 days:
  The use log starts on 2026-11-25, so this needs 30 days of it.
The log holds reads around memcore since 2026-12-09 11:00 UTC.
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

// facts builds the facts of commits for a test: a subject, then changes as "<status> <path>".
func factOf(subject string, changes ...string) fact {
	f := fact{subject: subject}
	for _, c := range changes {
		status, p, _ := strings.Cut(c, " ")
		f.changes = append(f.changes, fileStatus{status, p})
	}
	return f
}

func TestReview(t *testing.T) {
	a, b := "m/memories/a.md", "m/memories/b.md"
	remembered := map[string]fact{
		"r1": factOf("remember: "+a, "A "+a, "M m/README.md"),
		"r2": factOf("remember: "+b, "A "+b, "M m/README.md"),
	}
	with := func(more map[string]fact) map[string]fact {
		all := map[string]fact{}
		for k, v := range remembered {
			all[k] = v
		}
		for k, v := range more {
			all[k] = v
		}
		return all
	}
	for _, c := range []struct {
		name   string
		events []Event
		facts  map[string]fact
		want   []review
	}{
		{
			name: "a revise 13 days after a push is a mistake, and one 15 days after is not",
			events: []Event{
				{Time: ago(20 * day), Command: "remember", Paths: []string{a}, Outcome: "ok", Made: []string{"r1"}},
				{Time: ago(20 * day), Command: "remember", Paths: []string{b}, Outcome: "ok", Made: []string{"r2"}},
				{Time: ago(16 * day), Command: "push", Outcome: "ok", Pushed: []string{"r1", "r2"}},
				{Time: ago(3 * day), Command: "revise", Paths: []string{a}, Outcome: "ok", Made: []string{"r3"}},
				{Time: ago(1 * day), Command: "revise", Paths: []string{b}, Outcome: "ok", Made: []string{"r4"}},
			},
			facts: with(map[string]fact{"r3": factOf("revise: "+a, "M "+a), "r4": factOf("revise: "+b, "M "+b)}),
			want:  []review{{created: 2}, {}, {created: 2, pushed: 2, accepted: 2, mistakes: 1}},
		},
		{
			name: "two revisions of one memory count once, and a revise without a commit counts as none",
			events: []Event{
				{Time: ago(10 * day), Command: "push", Outcome: "ok", Pushed: []string{"r1", "r2"}},
				{Time: ago(9 * day), Command: "revise", Paths: []string{a}, Outcome: "ok", Made: []string{"r3"}},
				{Time: ago(8 * day), Command: "revise", Paths: []string{a}, Outcome: "ok", Made: []string{"r4"}},
				{Time: ago(9 * day), Command: "revise", Paths: []string{b}, Outcome: "ok"},
			},
			facts: with(map[string]fact{"r3": factOf("revise: "+a, "M "+a), "r4": factOf("fixup! revise: "+a, "M "+a)}),
			want:  []review{{}, {created: 2, createdFixups: 1, pushed: 2, accepted: 2, mistakes: 1}},
		},
		{
			name: "a revised plan is no mistake, and a forgotten memory is one",
			events: []Event{
				{Time: ago(5 * day), Command: "push", Outcome: "ok", Pushed: []string{"d1", "r1"}},
				{Time: ago(4 * day), Command: "revise", Paths: []string{"m/plans/p.md"}, Outcome: "ok", Made: []string{"r3"}},
				{Time: ago(4 * day), Command: "forget", Paths: []string{a}, Outcome: "ok", Made: []string{"r4"}},
			},
			facts: with(map[string]fact{
				"d1": factOf("draft: m/plans/p.md", "A m/plans/p.md"),
				"r3": factOf("revise: m/plans/p.md", "M m/plans/p.md"),
				"r4": factOf("forget: "+a, "D "+a),
			}),
			want: []review{{created: 2, pushed: 2, accepted: 1, mistakes: 1}},
		},
		{
			name: "an import counts no queue commit, but adds its memories",
			events: []Event{
				{Time: ago(6 * day), Command: "init", Outcome: "ok", Made: []string{"i1"}},
				{Time: ago(5 * day), Command: "push", Outcome: "ok", Pushed: []string{"i0", "i1"}},
				{Time: ago(2 * day), Command: "revise", Paths: []string{a}, Outcome: "ok", Made: []string{"r3"}},
			},
			facts: map[string]fact{
				"i0": factOf("init: create the memory repo", "A README.md", "A skills/voice/SKILL.md"),
				"i1": factOf("init: import the auto-memory of ~/m", "A "+a, "A m/README.md"),
				"r3": factOf("revise: "+a, "M "+a),
			},
			want: []review{{created: 1, accepted: 1, mistakes: 1}},
		},
		{
			name: "in the second of a push, a revise after it in the log is a mistake, and one before it is not",
			events: []Event{
				{Time: ago(day), Command: "revise", Paths: []string{b}, Outcome: "ok", Made: []string{"r3"}},
				{Time: ago(day), Command: "push", Outcome: "ok", Pushed: []string{"r1", "r2"}},
				{Time: ago(day), Command: "revise", Paths: []string{a}, Outcome: "ok", Made: []string{"r4"}},
			},
			facts: with(map[string]fact{"r3": factOf("revise: "+b, "M "+b), "r4": factOf("revise: "+a, "M "+a)}),
			want:  []review{{created: 2, pushed: 2, accepted: 2, mistakes: 1}},
		},
		{
			name: "a revise after the call counts nowhere",
			events: []Event{
				{Time: ago(day), Command: "push", Outcome: "ok", Pushed: []string{"r1"}},
				{Time: ago(-time.Minute), Command: "revise", Paths: []string{a}, Outcome: "ok", Made: []string{"r3"}},
			},
			facts: with(map[string]fact{"r3": factOf("revise: "+a, "M "+a)}),
			want:  []review{{pushed: 1, accepted: 1}},
		},
		{
			name: "a push that failed after git push still counts its pushed",
			events: []Event{
				{Time: ago(day), Command: "push", Outcome: "error", Error: "move the accepted history to the pushed tip", Pushed: []string{"r1"}},
			},
			facts: remembered,
			want:  []review{{pushed: 1, accepted: 1}},
		},
		{
			name: "fixups count as created, and a dropped fixup counts as dropped, alone and with its target",
			events: []Event{
				{Time: ago(day), Command: "remember", Paths: []string{a}, Outcome: "ok", Made: []string{"r1"}},
				{Time: ago(day), Command: "revise", Paths: []string{a}, Outcome: "ok", Made: []string{"f1"}},
				{Time: ago(day), Command: "revise", Paths: []string{a}, Outcome: "ok", Made: []string{"f2"}},
				{Time: ago(day), Command: "remember", Paths: []string{b}, Outcome: "ok", Made: []string{"r2"}},
				{Time: ago(day), Command: "revise", Paths: []string{b}, Outcome: "ok", Made: []string{"f3"}},
				{Time: ago(day), Command: "drop", Outcome: "ok", Dropped: []string{"f1"}},
				{Time: ago(day), Command: "drop", Outcome: "ok", Dropped: []string{"r2", "f3"}},
			},
			facts: with(map[string]fact{
				"f1": factOf("fixup! remember: "+a, "M "+a),
				"f2": factOf("fixup! remember: "+a, "M "+a),
				"f3": factOf("fixup! remember: "+b, "M "+b),
			}),
			want: []review{{created: 5, createdFixups: 3, dropped: 3, droppedFixups: 2}},
		},
		{
			name: "a remember that failed after its commit counts as created",
			events: []Event{
				{Time: ago(day), Command: "remember", Paths: []string{a}, Outcome: "error", Error: "index the level", Made: []string{"r1"}},
			},
			facts: remembered,
			want:  []review{{created: 1}},
		},
		{
			name: "an index commit in pushed counts nowhere, and neither does the dropped of recover",
			events: []Event{
				{Time: ago(day), Command: "push", Outcome: "ok", Made: []string{"x1"}, Pushed: []string{"x1"}},
				{Time: ago(day), Command: "recover", Outcome: "ok", Dropped: []string{"r1"}},
			},
			facts: with(map[string]fact{"x1": factOf("index: m/README.md", "M m/README.md")}),
			want:  []review{{}},
		},
		{
			name: "commits that git lacks",
			events: []Event{
				{Time: ago(day), Command: "remember", Paths: []string{a}, Outcome: "ok", Made: []string{"gone1"}},
				{Time: ago(day), Command: "push", Outcome: "ok", Pushed: []string{"gone2"}},
				{Time: ago(day), Command: "drop", Outcome: "ok", Dropped: []string{"gone1"}},
			},
			want: []review{{created: 1, missing: 2}},
		},
		{
			name: "logged commits that git lacks count as missing, though no number needs them",
			events: []Event{
				{Time: ago(day), Command: "init", Outcome: "ok", Made: []string{"gone1"}},
				{Time: ago(day), Command: "push", Outcome: "ok", Made: []string{"gone2"}},
				{Time: ago(day), Command: "recover", Outcome: "ok", Dropped: []string{"gone3"}},
				{Time: ago(day), Command: "drop", Outcome: "error", Error: "the person said no", Dropped: []string{"gone4"}},
			},
			want: []review{{missing: 4}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got []review
			for _, w := range measure(c.events, c.facts, statsNow) {
				got = append(got, w.review)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("reviews:\n got %+v\nwant %+v", got, c.want)
			}
		})
	}

	t.Run("weeks 0 and 1 say so far, and week 2 does not", func(t *testing.T) {
		events := []Event{
			{Time: ago(day), Command: "push", Outcome: "ok", Pushed: []string{"r1"}},
			{Time: ago(8 * day), Command: "push", Outcome: "ok", Pushed: []string{"r1"}},
			{Time: ago(15 * day), Command: "push", Outcome: "ok", Pushed: []string{"r1"}},
		}
		for k, w := range measure(events, remembered, statsNow) {
			var out strings.Builder
			writeWeek(&out, w, k)
			want := "  Accepted memories: 1, with 0 mistakes so far. Precision: 100%.\n"
			if k == 2 {
				want = "  Accepted memories: 1, with 0 mistakes. Precision: 100%.\n"
			}
			if !strings.Contains(out.String(), want) {
				t.Errorf("week %d:\n%s\nwant it to hold %q", k, out.String(), want)
			}
		}
	})

	t.Run("the precision", func(t *testing.T) {
		w := week{end: statsNow, lines: 1, review: review{accepted: 9, mistakes: 1}}
		var out strings.Builder
		writeWeek(&out, w, 2)
		if want := "  Accepted memories: 9, with 1 mistake. Precision: 89%.\n"; !strings.Contains(out.String(), want) {
			t.Errorf("output:\n%s\nwant it to hold %q", out.String(), want)
		}
	})
}

func TestCommitFacts(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "-b", "main")
	commit := func(subject string, allowEmpty bool) string {
		gitT(t, repo, "add", "-A")
		args := []string{"commit", "-q", "-m", subject}
		if allowEmpty {
			args = append(args, "--allow-empty")
		}
		gitT(t, repo, args...)
		return strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	}
	doc := "my café/memories/a b.md"
	writeT(t, repo, "README.md", "x\n")
	first := commit("init: create the memory repo", false)
	writeT(t, repo, doc, "a\n")
	second := commit("remember: "+doc, false)
	writeT(t, repo, doc, "b\n")
	if err := os.Remove(filepath.Join(repo, "README.md")); err != nil {
		t.Fatal(err)
	}
	third := commit("fixup! remember: "+doc, false)
	fourth := commit("revise: nothing", true)
	gone := strings.Repeat("0123456789", 4)

	facts, missing, err := commitFacts(repo, []string{first, second, third, fourth, gone, second})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]fact{
		first:  factOf("init: create the memory repo", "A README.md"),
		second: factOf("remember: "+doc, "A "+doc),
		third:  factOf("fixup! remember: "+doc, "D README.md", "M "+doc),
		fourth: factOf("revise: nothing"),
	}
	if !reflect.DeepEqual(facts, want) || missing != 1 {
		t.Errorf("commitFacts:\n got %+v, %d missing\nwant %+v, 1 missing", facts, missing, want)
	}

	if facts, missing, err := commitFacts(repo, nil); err != nil || len(facts) != 0 || missing != 0 {
		t.Errorf("commitFacts of no commit = %v, %d, %v; want none", facts, missing, err)
	}
}

func TestUnread(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "-b", "main")
	commitAt := func(when time.Time, subject string) {
		t.Helper()
		t.Setenv("GIT_AUTHOR_DATE", when.Format(time.RFC3339))
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", subject)
	}
	for _, p := range []string{
		"m/memories/old.md", "m/memories/read.md", "m/memories/around.md", "m/memories/long-ago.md",
		"m/memories/back.md", "my café/memories/menu.md", "m/plans/p.md", "m/README.md",
	} {
		writeT(t, repo, p, "A fact.\n")
	}
	commitAt(statsNow.Add(-40*day), "init: the memories of 40 days ago")
	if err := os.Remove(filepath.Join(repo, "m", "memories", "back.md")); err != nil {
		t.Fatal(err)
	}
	commitAt(statsNow.Add(-35*day), "forget: m/memories/back.md")
	writeT(t, repo, "m/memories/back.md", "A fact again.\n")
	writeT(t, repo, "m/memories/new.md", "A new fact.\n")
	commitAt(statsNow.Add(-10*day), "remember: m/memories/new.md")

	events := []Event{
		{Time: ago(31 * day), Command: "show", Paths: []string{"m/memories/long-ago.md"}, Outcome: "ok"},
		{Time: ago(10 * day), Session: "s1", Command: "show", Paths: []string{"m/memories/read.md"}, Outcome: "ok"},
		{Time: ago(5 * day), Session: "s1", Command: "hook", Hook: "tool", Paths: []string{"m/memories/around.md"}, Outcome: "ok"},
		{Time: ago(3 * day), Command: "hook", Hook: "tool", Outcome: "error", Error: "read the hook input: unexpected end of JSON input"},
		{Time: ago(4 * day), Command: "show", Paths: []string{"m/memories/old.md"}, Outcome: "error", Error: "no document"},
	}

	t.Run("the memories with no read", func(t *testing.T) {
		got, err := unread(repo, events, statsNow)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"m/memories/long-ago.md", "m/memories/old.md", "my café/memories/menu.md"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("unread = %q, want %q", got, want)
		}
	})

	t.Run("the first read around memcore", func(t *testing.T) {
		var out strings.Builder
		if err := writeUnread(&out, repo, events, statsNow); err != nil {
			t.Fatal(err)
		}
		want := "\nMemories with no logged read in 30 days: 3.\n" +
			"  m/memories/long-ago.md\n  m/memories/old.md\n  my café/memories/menu.md\n" +
			"The log holds reads around memcore since 2026-12-05 10:00 UTC.\n"
		if out.String() != want {
			t.Errorf("output:\n%q\nwant:\n%q", out.String(), want)
		}
	})

	t.Run("a young log lists nothing, and a log without hook tool says so", func(t *testing.T) {
		young := []Event{{Time: ago(20 * day), Command: "show", Paths: []string{"m/memories/read.md"}, Outcome: "ok"}}
		var out strings.Builder
		if err := writeUnread(&out, repo, young, statsNow); err != nil {
			t.Fatal(err)
		}
		want := "\nMemories with no logged read in 30 days:\n" +
			"  The use log starts on 2026-11-20, so this needs 30 days of it.\n" +
			"The log holds no read around memcore yet.\n"
		if out.String() != want {
			t.Errorf("output:\n%q\nwant:\n%q", out.String(), want)
		}
	})

	t.Run("20 paths at most, then the count of the rest", func(t *testing.T) {
		for i := range 20 {
			writeT(t, repo, fmt.Sprintf("n/memories/fact-%02d.md", i), "A fact.\n")
		}
		commitAt(statsNow.Add(-40*day), "init: 20 more memories")
		var out strings.Builder
		if err := writeUnread(&out, repo, events, statsNow); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(lines) != 24 || lines[1] != "Memories with no logged read in 30 days: 23." ||
			lines[2] != "  m/memories/long-ago.md" || lines[21] != "  n/memories/fact-16.md" || lines[22] != "  and 3 more" {
			t.Errorf("output:\n%s", out.String())
		}
	})
}
