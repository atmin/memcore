package commands

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/store"
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
