package commands

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/thinking"
)

// transcriptT commits one file onto the transcripts branch of repo, and returns its blob.
func transcriptT(t *testing.T, repo, p, content string) string {
	t.Helper()
	blob, err := store.GitStdin(repo, content, "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	blob = strings.TrimSpace(blob)
	parent, _ := revParse(repo, transcriptsRef)
	if err := commitSnapshot(repo, parent, []string{"100644 " + blob + "\t" + p}); err != nil {
		t.Fatal(err)
	}
	return blob
}

// transcriptText returns a trimmed transcript: its header, then one line for each "who|text", a minute apart from at.
func transcriptText(session string, at time.Time, lines ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "{\"agent\":\"claude\",\"session\":%q,\"dir\":\"/home/me/dev\"}\n", session)
	for i, l := range lines {
		who, text, _ := strings.Cut(l, "|")
		fmt.Fprintf(&b, "{\"time\":%q,\"who\":%q,\"text\":%q}\n", at.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339), who, text)
	}
	return b.String()
}

// startEvent returns the logged start of a session.
func startEvent(session string, at time.Time, commit string) Event {
	return Event{Time: at.UTC().Format(time.RFC3339), Session: session, Agent: "claude", Command: "hook", Hook: "session-start",
		Level: "~/dev", Dir: "~/dev", Commit: commit, Outcome: "ok"}
}

func TestSleepSessions(t *testing.T) {
	_, repo, _ := newMemory(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)

	if units, err := sleepSessions(repo, []Event{startEvent("s-old", old, "")}, nil, now); err != nil || len(units) != 0 {
		t.Fatalf("without a transcripts branch: %v, %v", units, err)
	}

	blobs := map[string]string{}
	add := func(p, content string) { blobs[p] = transcriptT(t, repo, p, content) }
	add("claude/p1/s-old.jsonl", transcriptText("s-old", old, "you|Use small commits."))
	add("claude/p1/s-older.jsonl", transcriptText("s-older", old.Add(-time.Hour), "you|No."))
	add("claude/p1/s-nostart.jsonl", transcriptText("s-nostart", old, "you|No start in the log."))
	add("claude/p1/s-judged.jsonl", transcriptText("s-judged", old, "you|Judged before."))
	add("claude/p1/s-grown.jsonl", transcriptText("s-grown", old, "you|First.", "you|Second."))
	add("claude/p1/s-young.jsonl", transcriptText("s-young", now.Add(-10*time.Minute), "you|Still running."))
	add("claude/p1/s-old/subagents/agent-1.jsonl", transcriptText("s-old", old, "parent|Do it."))
	add("claude/p1/s-old.0123abcd.jsonl", transcriptText("s-old", old, "you|Use small commits.", "you|A version beside."))
	add("claude/p1/s-empty.jsonl", "{\"agent\":\"claude\",\"session\":\"s-empty\"}\n")
	add("other/p1/s-old.jsonl", transcriptText("s-old", old, "you|Another agent."))

	events := []Event{
		startEvent("s-old", old.Add(time.Minute), "c2"), // a resume
		startEvent("s-old", old, "c1"),
		startEvent("s-older", old.Add(-time.Hour), "c0"),
		startEvent("s-judged", old, "c1"),
		startEvent("s-grown", old, "c1"),
		startEvent("s-young", now.Add(-10*time.Minute), "c1"),
		startEvent("s-empty", old, "c1"),
		startEvent("s-gone", old, "c1"), // no transcript
		{Time: old.Format(time.RFC3339), Session: "s-nostart", Command: "show", Outcome: "ok"},
	}
	records := []sleepRecord{
		{Session: "s-judged", File: "claude/p1/s-judged.jsonl", Blob: blobs["claude/p1/s-judged.jsonl"]},
		{Session: "s-grown", File: "claude/p1/s-grown.jsonl", Blob: "0000000000000000000000000000000000000000"}, // an older, shorter version
	}
	units, err := sleepSessions(repo, events, records, now)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range units {
		got = append(got, u.File)
		if u.Blob != blobs[u.File] || u.Level != "~/dev" || u.Dir != "~/dev" {
			t.Errorf("unit %+v, want the blob %s at ~/dev", u, blobs[u.File])
		}
	}
	want := []string{
		"claude/p1/s-older.jsonl",
		"claude/p1/s-grown.jsonl",
		"claude/p1/s-old.0123abcd.jsonl",
		"claude/p1/s-old.jsonl",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("files = %q, want %q", got, want)
	}
	for _, u := range units {
		if u.Session == "s-old" && !u.Start.Equal(old) {
			t.Errorf("the start of s-old = %v, want its first start %v", u.Start, old)
		}
	}
}

func TestSleepCorrections(t *testing.T) {
	_, repo, _ := newMemory(t)
	c1 := sleepCorrection{Time: "2026-11-18T10:00:00Z", Quote: "Use small commits.", Summary: "Commit small.", Verdict: "capture"}
	c2 := sleepCorrection{Time: "2026-11-18T10:05:00Z", Quote: "No trailers.", Summary: "No trailers.", Verdict: "stale", Path: "dev/memories/commits.md"}
	write := func(r sleepRecord) {
		t.Helper()
		if err := appendSleep(repo, r); err != nil {
			t.Fatal(err)
		}
	}
	if records, err := readSleep(repo); err != nil || len(records) != 0 {
		t.Fatalf("without a file: %v, %v", records, err)
	}
	write(sleepRecord{Session: "s1", File: "claude/p/s1.jsonl", Blob: "b1", Corrections: []sleepCorrection{c1}})
	write(sleepRecord{Session: "s2", File: "claude/p/s2.jsonl", Blob: "b2", Corrections: []sleepCorrection{c1}})
	write(sleepRecord{Session: "s1", File: "claude/p/s1.0123abcd.jsonl", Blob: "b3", Corrections: []sleepCorrection{c1, c2}})
	write(sleepRecord{Session: "s1", File: "claude/p/s1.jsonl", Blob: "b4", Corrections: []sleepCorrection{c1}}) // the transcript grew
	write(sleepRecord{Session: "s3", File: "claude/p/s3.jsonl", Blob: "b5"})

	records, err := readSleep(repo)
	if err != nil {
		t.Fatal(err)
	}
	var blobs []string
	for _, r := range records {
		blobs = append(blobs, r.Blob)
	}
	if want := []string{"b2", "b3", "b4", "b5"}; !reflect.DeepEqual(blobs, want) {
		t.Fatalf("blobs = %q, want %q: a later record of a file replaces an earlier one", blobs, want)
	}
	var got []string
	for _, c := range sleepCorrections(records) {
		got = append(got, c.Record.Session+" "+c.Quote)
	}
	want := []string{"s2 Use small commits.", "s1 Use small commits.", "s1 No trailers."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("corrections = %q, want %q: the same correction of one session counts once", got, want)
	}
}

func TestSeenCommit(t *testing.T) {
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	ev := func(session string, minutes int, commit string) Event {
		return Event{Time: at.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339), Session: session, Command: "show", Commit: commit, Outcome: "ok"}
	}
	events := []Event{
		ev("s1", 0, "c1"),
		ev("s1", 10, "c2"),
		ev("s2", 11, "other"),
		ev("s1", 12, ""), // a line without a commit
		ev("s1", 20, "c3"),
		ev("s4", 5, "a show before the start"),
		{Time: at.Add(8 * time.Minute).Format(time.RFC3339), Session: "s4", Command: "hook", Hook: "session-start", Commit: "the start", Outcome: "ok"},
		{Time: at.Add(9 * time.Minute).Format(time.RFC3339), Session: "s4", Command: "hook", Hook: "session-start", Commit: "a resume", Outcome: "ok"},
	}
	cases := []struct {
		name    string
		session string
		minutes int
		want    string
	}{
		{"the newest event before the correction", "s1", 15, "c2"},
		{"an event at the time of the correction", "s1", 10, "c2"},
		{"after every event", "s1", 60, "c3"},
		{"only later events", "s1", -5, ""},
		{"another session", "s2", 15, "other"},
		{"no event of the session", "s3", 15, ""},
		{"only later events, with a start", "s4", 0, ""},
		{"a show before the start", "s4", 6, "a show before the start"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := seenCommit(events, c.session, at.Add(time.Duration(c.minutes)*time.Minute)); got != c.want {
				t.Errorf("commit = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSeenMemories(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, repo, _ := newMemory(t)
	dev := mkdirT(t, filepath.Join(home, "dev"))
	shop := mkdirT(t, filepath.Join(home, "dev", "shop"))
	other := mkdirT(t, filepath.Join(home, "other"))
	bin := mkdirT(t, filepath.Join(home, "bin"))
	must := func(dir, stdin string, args ...string) {
		t.Helper()
		if out, err := run(t, dir, stdin, args...); err != nil {
			t.Fatalf("memcore %v: %v\n%s", args, err, out)
		}
	}
	must(home, memoryText("who-you-are", "Read before you work with the person.", "The person reviews everything."), "remember")
	must(dev, memoryText("small-commits", "Read before you commit: one change per commit.", "Commit one change."), "remember")
	must(shop, memoryText("shop-stack", "Read before you start the stack of the shop.", "Start the stack with make up."), "remember")
	must(shop, "", "draft", "topic", "memories/payments", "--description", "Read when you work on payments.")
	must(shop, memoryText("refund-rules", "Read before you change a refund.", "A refund needs a ticket."), "remember", "--topic", "payments")
	must(other, memoryText("other-thing", "Read before you work on the other thing.", "It is apart."), "remember")
	must(bin, memoryText("own-scripts", "Read before you add a script of your own.", "Scripts live in ~/bin."), "remember")
	commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	must(shop, memoryText("added-later", "Read when a release goes out on Monday.", "Deploy on Monday."), "remember", "--new")

	got, err := seenMemories(repo, commit, "~/dev/shop")
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{}
	descriptions := map[string]string{}
	for _, m := range got {
		bodies[m.Path], descriptions[m.Path] = m.Body, m.Description
	}
	want := map[string]string{
		"memories/who-you-are.md":                    "The person reviews everything.",
		"dev/memories/small-commits.md":              "Commit one change.",
		"dev/shop/memories/shop-stack.md":            "Start the stack with make up.",
		"dev/shop/memories/payments/refund-rules.md": "A refund needs a ticket.",
	}
	if !reflect.DeepEqual(bodies, want) {
		t.Errorf("memories:\n got %q\nwant %q", bodies, want)
	}
	if d := descriptions["dev/memories/small-commits.md"]; d != "Read before you commit: one change per commit." {
		t.Errorf("description = %q", d)
	}

	root, err := seenMemories(repo, commit, "~")
	if err != nil || len(root) != 1 || root[0].Path != "memories/who-you-are.md" {
		t.Errorf("the root sees %+v, %v; want only its own memory", root, err)
	}
	escaped, err := seenMemories(repo, commit, "~/bin")
	if err != nil || len(escaped) != 2 || escaped[0].Path != "+bin/memories/own-scripts.md" {
		t.Errorf("~/bin sees %+v, %v; want the memory of its escaped folder, then the root", escaped, err)
	}
	if _, err := seenMemories(repo, "0000000000000000000000000000000000000000", "~/dev"); err == nil {
		t.Error("a commit that git lacks: no error")
	}
}

func TestVerdicts(t *testing.T) {
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	stamp := func(minutes int) string { return at.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339) }
	events := []Event{
		{Time: stamp(-5), Session: "s1", Command: "show", Outcome: "ok", Paths: []string{"dev/memories/read-before.md"}},
		{Time: stamp(-4), Session: "s1", Command: "hook", Hook: "tool", Outcome: "ok", Paths: []string{"dev/memories/read-around.md"}},
		{Time: stamp(-3), Session: "s1", Command: "show", Outcome: "error", Paths: []string{"dev/memories/failed-show.md"}},
		{Time: stamp(-2), Session: "s2", Command: "show", Outcome: "ok", Paths: []string{"dev/memories/other-session.md"}},
		{Time: stamp(0), Session: "s1", Command: "show", Outcome: "ok", Paths: []string{"dev/memories/read-at.md"}},
		{Time: stamp(5), Session: "s1", Command: "show", Outcome: "ok", Paths: []string{"dev/memories/read-after.md"}},
	}
	cases := []struct {
		name    string
		finding thinking.Finding
		verdict string
		path    string
	}{
		{"no memory", thinking.Finding{Kind: thinking.None, Reason: "r"}, verdictCapture, ""},
		{"contradicts", thinking.Finding{Kind: thinking.Contradicts, Path: "dev/memories/read-before.md", Reason: "r"}, verdictStale, "dev/memories/read-before.md"},
		{"unrelated", thinking.Finding{Kind: thinking.Unrelated, Reason: "r"}, verdictUnrelated, ""},
		{"read before", thinking.Finding{Kind: thinking.Holds, Path: "dev/memories/read-before.md", Reason: "r"}, verdictApplication, "dev/memories/read-before.md"},
		{"read around memcore", thinking.Finding{Kind: thinking.Holds, Path: "dev/memories/read-around.md", Reason: "r"}, verdictApplication, "dev/memories/read-around.md"},
		{"read at the time", thinking.Finding{Kind: thinking.Holds, Path: "dev/memories/read-at.md", Reason: "r"}, verdictApplication, "dev/memories/read-at.md"},
		{"read after", thinking.Finding{Kind: thinking.Holds, Path: "dev/memories/read-after.md", Reason: "r"}, verdictRetrieval, "dev/memories/read-after.md"},
		{"a failed show", thinking.Finding{Kind: thinking.Holds, Path: "dev/memories/failed-show.md", Reason: "r"}, verdictRetrieval, "dev/memories/failed-show.md"},
		{"read by another session", thinking.Finding{Kind: thinking.Holds, Path: "dev/memories/other-session.md", Reason: "r"}, verdictRetrieval, "dev/memories/other-session.md"},
		{"never read", thinking.Finding{Kind: thinking.Holds, Path: "dev/memories/unread.md", Reason: "r"}, verdictRetrieval, "dev/memories/unread.md"},
	}
	var corrections []thinking.Correction
	var findings []thinking.Finding
	for _, c := range cases {
		corrections = append(corrections, thinking.Correction{Time: stamp(0), Quote: c.name, Summary: "s"})
		findings = append(findings, c.finding)
	}
	got := verdicts(corrections, findings, events, "s1")
	for i, c := range cases {
		want := sleepCorrection{Time: stamp(0), Quote: c.name, Summary: "s", Verdict: c.verdict, Path: c.path, Reason: "r"}
		if got[i] != want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got[i], want)
		}
	}
}
