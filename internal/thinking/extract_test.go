package thinking

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeRun replaces Run for a test. It answers each call with the next reply, and records the requests.
func fakeRun(t *testing.T, replies ...func(Request) (Reply, error)) *[]Request {
	t.Helper()
	var calls []Request
	prev := Run
	t.Cleanup(func() { Run = prev })
	Run = func(req Request) (Reply, error) {
		calls = append(calls, req)
		if len(calls) > len(replies) {
			t.Fatalf("call %d of the agent, want at most %d", len(calls), len(replies))
		}
		return replies[len(calls)-1](req)
	}
	return &calls
}

// answer returns a reply of the agent with an output and a cost.
func answer(output string, cost float64) func(Request) (Reply, error) {
	return func(Request) (Reply, error) { return Reply{Output: []byte(output), Cost: cost}, nil }
}

func transcript(lines ...string) string {
	var b strings.Builder
	b.WriteString(`{"agent":"claude","session":"s1","dir":"/home/me"}` + "\n")
	for _, l := range lines {
		f := strings.SplitN(l, "|", 3)
		fmt.Fprintf(&b, "{\"time\":%q,\"who\":%q,\"text\":%q}\n", f[0], f[1], f[2])
	}
	return b.String()
}

func TestExtract(t *testing.T) {
	tr := transcript(
		"2026-11-18T10:00:00Z|you|Add a retry to the fetch.",
		"2026-11-18T10:01:00Z|agent|Done. I also use small commits. Never push without asking.",
		"2026-11-18T10:02:00Z|you|No.  Never push\n without asking. I said so before.",
		"2026-11-18T10:03:00Z|rejected|Bash: git push",
		"2026-11-18T10:04:00Z|you|Use small commits.",
		"2026-11-18T10:30:00Z|you|Use small commits.",
	)

	t.Run("keeps only the words of the person", func(t *testing.T) {
		calls := fakeRun(t, answer(`{"corrections":[
			{"time":"2026-11-18T10:02:00Z","quote":"Never push without asking.","summary":"Ask  before a push."},
			{"time":"2026-11-18T10:02:00Z","quote":"Never force push.","summary":"Invented."},
			{"time":"2026-11-18T10:01:00Z","quote":"I also use small commits.","summary":"From the agent."},
			{"time":"2026-11-18T09:00:00Z","quote":"No. Never push","summary":"A wrong time."},
			{"time":"2026-11-18T10:29:00Z","quote":"Use small commits.","summary":"Commit small."},
			{"time":"2026-11-18T10:29:30Z","quote":"Use small commits.","summary":"The same again."},
			{"time":"not a time","quote":"Use small commits.","summary":"The earliest line."},
			{"time":"2026-11-18T10:02:00Z","quote":"  ","summary":"Empty."}
		]}`, 0.25))
		got, cost, err := Extract(ExtractRequest{Transcript: tr, Model: "sonnet"})
		if err != nil {
			t.Fatal(err)
		}
		want := []Correction{
			{"2026-11-18T10:02:00Z", "Never push\n without asking.", "Ask before a push."},
			{"2026-11-18T10:02:00Z", "No.  Never push", "A wrong time."},
			{"2026-11-18T10:30:00Z", "Use small commits.", "Commit small."},
			{"2026-11-18T10:04:00Z", "Use small commits.", "The earliest line."},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("corrections:\n got %q\nwant %q", got, want)
		}
		if cost != 0.25 {
			t.Errorf("cost = %v, want 0.25", cost)
		}
		req := (*calls)[0]
		if req.Input != tr || req.Model != "sonnet" || req.Schema != extractSchema || req.Prompt != extractPrompt {
			t.Errorf("request = %+v", req)
		}
	})

	t.Run("no word of the person", func(t *testing.T) {
		fakeRun(t)
		got, cost, err := Extract(ExtractRequest{Transcript: transcript("2026-11-18T10:01:00Z|agent|Done.")})
		if err != nil || len(got) != 0 || cost != 0 {
			t.Errorf("got %v, %v, %v; want nothing and no call", got, cost, err)
		}
	})

	t.Run("an answer that is no list", func(t *testing.T) {
		fakeRun(t, answer(`{"corrections":"none"}`, 0.1))
		if _, cost, err := Extract(ExtractRequest{Transcript: tr}); err == nil || cost != 0.1 {
			t.Errorf("err = %v, cost %v; want an error with the cost", err, cost)
		}
	})
}

func TestExtractParts(t *testing.T) {
	long := strings.Repeat("x", partBytes) // one line over the size of a part
	var lines []string
	for i := range 6 {
		lines = append(lines, fmt.Sprintf("2026-11-18T10:%02d:00Z|you|%s rule %d.", i, strings.Repeat("y", partBytes/3), i))
	}
	lines = append(lines, "2026-11-18T11:00:00Z|you|"+long+" last rule.")
	tr := transcript(lines...)

	got := parts(tr, partBytes)
	header := `{"agent":"claude","session":"s1","dir":"/home/me"}` + "\n"
	var body strings.Builder
	for i, p := range got {
		if !strings.HasPrefix(p, header) {
			t.Fatalf("part %d lacks the header", i)
		}
		rest := strings.TrimPrefix(p, header)
		if len(p) > partBytes && strings.Count(rest, "\n") != 1 {
			t.Errorf("part %d has %d bytes and %d lines: only a single line may pass the size", i, len(p), strings.Count(rest, "\n"))
		}
		body.WriteString(rest)
	}
	if header+body.String() != tr {
		t.Error("the parts do not add up to the transcript")
	}
	if len(got) != 4 {
		t.Errorf("%d parts, want 4: three of two lines, and the long line alone", len(got))
	}
	if small := parts(transcript("2026-11-18T10:00:00Z|you|Short."), partBytes); len(small) != 1 {
		t.Errorf("a small transcript gives %d parts, want 1", len(small))
	}

	// Each part is one call, and no cost stops the calls between two parts.
	replies := []func(Request) (Reply, error){
		answer(`{"corrections":[{"time":"2026-11-18T10:00:00Z","quote":"rule 0.","summary":"Rule 0."}]}`, 0.4),
		answer(`{"corrections":[]}`, 0.4),
		answer(`{"corrections":[{"time":"2026-11-18T10:00:00Z","quote":"rule 0.","summary":"From another part."}]}`, 0.4),
		answer(`{"corrections":[{"time":"2026-11-18T11:00:00Z","quote":"last rule.","summary":"Last."}]}`, 0.4),
	}
	calls := fakeRun(t, replies...)
	found, cost, err := Extract(ExtractRequest{Transcript: tr})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Quote != "rule 0." || found[1].Quote != "last rule." {
		t.Errorf("corrections = %+v, want rule 0 once and the last rule", found)
	}
	if cost < 1.59 || cost > 1.61 {
		t.Errorf("cost = %v, want 1.6", cost)
	}
	for i, req := range *calls {
		if req.Input != got[i] {
			t.Errorf("call %d does not read part %d", i, i)
		}
	}
	if len(*calls) != len(got) {
		t.Errorf("%d calls, want one for each of the %d parts", len(*calls), len(got))
	}
}

func TestExtractPartBytes(t *testing.T) {
	tr := transcript(
		"2026-11-18T10:00:00Z|you|"+strings.Repeat("a", 100)+" Use small commits.",
		"2026-11-18T10:01:00Z|you|"+strings.Repeat("b", 100)+" Never push.",
	)
	calls := fakeRun(t, answer(`{"corrections":[]}`, 0), answer(`{"corrections":[]}`, 0))
	if _, _, err := Extract(ExtractRequest{Transcript: tr, Model: "small", Endpoint: "http://127.0.0.1:0", PartBytes: len(tr) - 10}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("%d calls, want 2", len(*calls))
	}
	for i, req := range *calls {
		if req.Endpoint != "http://127.0.0.1:0" || req.Model != "small" {
			t.Errorf("call %d: endpoint %q, model %q", i, req.Endpoint, req.Model)
		}
	}
}

func TestExtractUnmatched(t *testing.T) {
	tr := transcript(
		"2026-11-18T10:00:00Z|you|Add a retry to the fetch.",
		"2026-11-18T10:02:00Z|you|Never push without asking.",
	)
	fakeRun(t, answer(`{"corrections":[
		{"time":"2026-11-18T10:02:00Z","quote":"Never push without asking.","summary":"Ask before a push."},
		{"time":"2026-11-18T10:02:00Z","quote":"Never push without asking.","summary":"The same again."},
		{"time":"2026-11-18T10:01:00Z","quote":"Never force  push.","summary":"Ask  before\n a force push."}
	]}`, 0))
	var unmatched []Correction
	found, _, err := Extract(ExtractRequest{Transcript: tr, Unmatched: func(c Correction) { unmatched = append(unmatched, c) }})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Correction{{"2026-11-18T10:02:00Z", "Never push without asking.", "Ask before a push."}}; !reflect.DeepEqual(found, want) {
		t.Errorf("corrections:\n got %q\nwant %q", found, want)
	}
	want := []Correction{{"2026-11-18T10:01:00Z", "Never force  push.", "Ask before a force push."}}
	if !reflect.DeepEqual(unmatched, want) {
		t.Errorf("unmatched:\n got %q\nwant %q", unmatched, want)
	}
}
