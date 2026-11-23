package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/thinking"
)

// tl returns one line of a trimmed transcript, from "who|text".
func tl(at int, l string) transcriptLine {
	who, text, _ := strings.Cut(l, "|")
	return transcriptLine{Time: fmt.Sprintf("2026-11-20T12:%02d:00Z", at), Who: who, Text: text}
}

// tls returns the lines of a trimmed transcript, one for each "who|text", a minute apart.
func tls(lines ...string) []transcriptLine {
	var out []transcriptLine
	for i, l := range lines {
		out = append(out, tl(i, l))
	}
	return out
}

func TestMeasureRules(t *testing.T) {
	long := func(n int) string { return "you|" + strings.Repeat("z", n) }
	cases := []struct {
		rule  string
		lines []transcriptLine
		i     int
		want  bool
	}{
		{"first", tls("agent|Hello.", "you|Fix it."), 1, true},
		{"first", tls("you|Fix it.", "agent|Done.", "you|Again."), 2, false},
		{"first", tls("you|", "you|Fix it."), 1, true}, // a line of the person with no text is no line of the person
		{"reply", tls("you|Fix it.", "agent|Should I push? ", "call|git status", "you|Yes."), 3, true},
		{"reply", tls("you|Fix it.", "agent|Should I push?", "agent|Done.", "you|Good."), 3, false},
		{"reply", tls("agent|Should I push?", "you|Wait.", "you|No."), 2, false},
		{"reply", tls("you|Fix it."), 0, false},
		{"long", tls(long(2001)), 0, true},
		{"long", tls(long(2000)), 0, false},
		{"approval", tls("you|  Go ahead! "), 0, true},
		{"approval", tls("you|LGTM."), 0, true},
		{"approval", tls("you|yes please"), 0, true},
		{"approval", tls("you|Yes, but add a test."), 0, false},
		{"approval", tls("you|ok!!"), 0, false}, // only one final mark goes
		{"harness", tls("you| /compact"), 0, true},
		{"harness", tls("you|<command-name>/clear</command-name>"), 0, true},
		{"harness", tls("you|Run /compact."), 0, false},
		{"after-rejected", tls("you|Fix it.", "rejected|rm -rf build", "you|Keep the build."), 2, true},
		{"after-rejected", tls("rejected|rm -rf build", "agent|Fine.", "you|Keep the build."), 2, false},
		{"mark", tls("you|That is #wrong again."), 0, true},
		{"mark", tls("you|#stale"), 0, true},
		{"mark", tls("you|That is wrong."), 0, false},
		{"negation", tls("you|Don't push."), 0, true},
		{"negation", tls("you|Don’t push."), 0, true},
		{"negation", tls("you|I'd RATHER keep it."), 0, true},
		{"negation", tls("you|'no' is the answer."), 0, true},
		{"negation", tls("you|Note the nothing button."), 0, false},
		{"negation", tls("you|Add a retry."), 0, false},
	}
	for _, c := range cases {
		t.Run(c.rule+" "+c.lines[c.i].Text[:min(20, len(c.lines[c.i].Text))], func(t *testing.T) {
			i := slices.IndexFunc(measureRules, func(r measureRule) bool { return r.name == c.rule })
			if i < 0 {
				t.Fatalf("no rule %q", c.rule)
			}
			if got := measureRules[i].hits(c.lines, c.i); got != c.want {
				t.Errorf("%s(%.40q) = %v, want %v", c.rule, c.lines[c.i].Text, got, c.want)
			}
		})
	}
	if got := slices.Collect(func(yield func(string) bool) {
		for _, r := range measureRules {
			if r.drop && !yield(r.name) {
				return
			}
		}
	}); !slices.Equal(got, []string{"first", "reply", "long", "approval", "harness"}) {
		t.Errorf("drop rules = %v", got)
	}
	if got := longer(1000)(tls(long(1001)), 0); !got {
		t.Error("longer(1000) misses a line of 1001 bytes")
	}
}

func TestHarnessStart(t *testing.T) {
	cases := []struct {
		text, want string
	}{
		{"<!-- reply to the agent -->", "<!-- reply"},
		{"<!--reply-to-->", "<!-- reply-to"},
		{"<!-- -->", "<!--"},
		{"<task-notification>done</task-notification>", "<task-notification"},
		{"  <bash-input>ls</bash-input>", "<bash-input"},
		{"<ns:tag_1 x='y'>", "<ns:tag_1"},
		{"<" + strings.Repeat("a", 50) + ">", "<" + strings.Repeat("a", 40)},
		{"< spaced", "<other>"},
		{"<>", "<other>"},
		{"/loop 5m /check", "/loop"},
		{"/Users/me/app/main.go fails", "/<path>"},
		{"//comment", "/<path>"},
		{"/ next", "/other"},
		{"/", "/other"},
		{"/" + strings.Repeat("b", 50), "/" + strings.Repeat("b", 40)},
		{"/" + strings.Repeat("é", 30), "/" + strings.Repeat("é", 20)}, // 60 bytes, cut at a rune boundary
		{"Run /loop.", ""},
	}
	for _, c := range cases {
		t.Run(c.text[:min(20, len(c.text))], func(t *testing.T) {
			if got := harnessStart(c.text); got != c.want {
				t.Errorf("harnessStart(%.60q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

func TestStartRows(t *testing.T) {
	starts := map[string]startCount{
		"/b": {hits: 1, with: 1, bytes: 5},
		"<c": {hits: 3, with: 1, bytes: 7},
		"/d": {hits: 1, with: 0, bytes: 2},
		"/a": {hits: 3, with: 0, bytes: 10},
	}
	// The most hits first, and equal hits by name.
	rows, rest, others := startRows(starts, 2)
	if want := []startRow{{"/a", startCount{3, 0, 10}}, {"<c", startCount{3, 1, 7}}}; !slices.Equal(rows, want) {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
	if want := (startCount{2, 1, 7}); rest != want || others != 2 {
		t.Errorf("rest = %+v of %d starts, want %+v of 2", rest, others, want)
	}
	if rows, rest, others := startRows(starts, 30); len(rows) != 4 || rest != (startCount{}) || others != 0 {
		t.Errorf("with room for all: %d rows, rest %+v of %d starts", len(rows), rest, others)
	}

	// The report prints 30 starts, and one line for the rest.
	c := measureCounts{starts: map[string]startCount{}}
	for i := range 32 {
		c.starts[fmt.Sprintf("/s%02d", i)] = startCount{hits: 1, bytes: 3}
	}
	var out bytes.Buffer
	printMeasure(&out, c)
	block := out.String()
	if n := strings.Count(block, "\n  /s"); n != 30 {
		t.Errorf("the report prints %d starts, want 30:\n%s", n, block)
	}
	if !strings.Contains(block, "  /s29: 1, 0, 3 bytes.\n  The rest, 2 starts: 2, 0, 6 bytes.\n") {
		t.Errorf("the report lacks the line of the rest after /s29:\n%s", block)
	}
}

func TestExcerpt(t *testing.T) {
	lines := tls(
		"you|Start.",  // 0
		"agent|early", // 1
		"call|c1",     // 2
		"rejected|r1", // 3
		"call|c2",     // 4
		"call|c3",     // 5
		"call|c4",     // 6
		"call|c5",     // 7
		"call|c6",     // 8
		"call|c7",     // 9
		"agent|abcdé", // 10: é takes two bytes
		"rejected|r2", // 11
		"you|Fix it.", // 12
		"agent|after", // 13
		"you|Next.",   // 14
	)
	cut := func(text string) transcriptLine {
		l := lines[10]
		l.Text = text
		return l
	}
	keep := func(idx ...int) []transcriptLine {
		var out []transcriptLine
		for _, i := range idx {
			out = append(out, lines[i])
		}
		return out
	}
	tail := keep(11, 12)
	cases := []struct {
		i, n int
		want []transcriptLine
	}{
		{0, 1000, keep(0)},
		{12, 1000, slices.Concat(keep(3, 5, 6, 7, 8, 9), []transcriptLine{cut("abcdé")}, tail)},
		{12, 3, slices.Concat(keep(3, 5, 6, 7, 8, 9), []transcriptLine{cut("dé")}, tail)},
		{12, 2, slices.Concat(keep(3, 5, 6, 7, 8, 9), []transcriptLine{cut("é")}, tail)},
		{12, 1, slices.Concat(keep(3, 5, 6, 7, 8, 9), []transcriptLine{cut("")}, tail)},
		{14, 1000, keep(13, 14)},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("line %d cut to %d", c.i, c.n), func(t *testing.T) {
			if got := excerpt(lines, c.i, c.n); !slices.Equal(got, c.want) {
				t.Errorf("excerpt =\n%v\nwant\n%v", got, c.want)
			}
		})
	}
	one := transcriptLine{Time: "T", Who: "you", Text: `<a> "é"`}
	if got, want := lineBytes([]transcriptLine{one, one}), 2*len(`{"time":"T","who":"you","text":"<a> \"é\""}`+"\n"); got != want {
		t.Errorf("lineBytes = %d, want %d", got, want)
	}
}

func TestLineOf(t *testing.T) {
	// One message of the person with two texts gives two lines with the same time.
	lines := []transcriptLine{
		{Time: "T1", Who: "you", Text: "Fix it."},
		{Time: "T2", Who: "agent", Text: "Done."},
		{Time: "T3", Who: "you", Text: "<ide_opened_file>a.go</ide_opened_file>"},
		{Time: "T3", Who: "you", Text: "Never   push\nwithout asking."},
		{Time: "T4", Who: "you", Text: ""},
	}
	cases := []struct {
		name  string
		rc    reflectCorrection
		want  int
		found bool
	}{
		{"one line with the time", reflectCorrection{Time: "T1", Quote: "Fix it."}, 0, true},
		{"the second line with the time holds the quote", reflectCorrection{Time: "T3", Quote: "Never push without asking."}, 3, true},
		{"no line with the time holds the quote", reflectCorrection{Time: "T3", Quote: "Something else."}, 2, true},
		{"the time of an agent line", reflectCorrection{Time: "T2", Quote: "Done."}, -1, false},
		{"the time of a line with no text", reflectCorrection{Time: "T4", Quote: "x"}, -1, false},
		{"no line with the time", reflectCorrection{Time: "T9", Quote: "Fix it."}, -1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, found := lineOf(lines, c.rc); got != c.want || found != c.found {
				t.Errorf("lineOf = %d, %v, want %d, %v", got, found, c.want, c.found)
			}
		})
	}
}

func TestBM25(t *testing.T) {
	if got, want := searchTerms("Don't push the Branch, café-42 ok, Ünïcode"), []string{"push", "branch", "café", "ünïcode"}; !slices.Equal(got, want) {
		t.Errorf("searchTerms = %q, want %q", got, want)
	}
	// One term of a corpus of three: the idf is ln(1.6), and the average length is 2.
	scores := bm25("apple", []string{"apple banana", "apple apple cherry", "durian"})
	idf := math.Log(1.6)
	want := []float64{idf, idf * 4.4 / 3.65, 0}
	for i := range want {
		if math.Abs(scores[i]-want[i]) > 1e-9 {
			t.Errorf("score %d = %v, want %v", i, scores[i], want[i])
		}
	}

	memories := []thinking.Memory{
		{Path: "dev/memories/a.md", Description: "Ask before a push.", Body: "Push only when the person asks."},
		{Path: "dev/memories/b.md", Description: "Commit small.", Body: "Commit one change at a time. Keep each commit small."},
		{Path: "dev/memories/c.md", Description: "Retry fetches.", Body: "Retry a failed fetch three times."},
	}
	for _, c := range []struct {
		query string
		want  []int
	}{
		{"Never push the branch.", []int{0, 1, 2}},
		{"Use a small change.", []int{1, 0, 2}},
		{"Retry the fetch.", []int{2, 0, 1}},
		{"Nothing matches here.", []int{0, 1, 2}}, // equal scores go by path
	} {
		if got := rankMemories(c.query, memories); !slices.Equal(got, c.want) {
			t.Errorf("rankMemories(%q) = %v, want %v", c.query, got, c.want)
		}
	}
}

// measureFixture adds three memories at ~/dev to reflectFixture, and the session s-measure that saw them. It records
// a pass over s-measure and s-second. It returns the repo, the times of the lines of s-measure, and the raw lines
// of the two transcripts, without their headers.
func measureFixture(t *testing.T) (repo string, times []string, raw []string) {
	t.Helper()
	dev, repo := reflectFixture(t)
	for _, m := range []string{
		memoryText("small-commits", "Read before you commit: one change per commit.", "Commit one change at a time."),
		memoryText("retry-fetches", "Read before you fetch: retry on failure.", "Retry each fetch three times."),
	} {
		if out, err := run(t, dev, m, "remember"); err != nil {
			t.Fatalf("remember: %v\n%s", err, out)
		}
	}
	commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	old := time.Now().Add(-40 * time.Hour).UTC().Truncate(time.Second)
	text := transcriptText("s-measure", old,
		"you|Fix the login bug.", // 0: before the logged start
		"agent|Should I also update the tests?",
		"you|yes",
		"call|edit a.go",
		"rejected|edit b.go",
		"you|No, never edit b.go. Ask before you push.", // 5
		"agent|Done.",
		"you|/compact",
		"you|<command-name>/clear</command-name>",
		"you|"+strings.Repeat("z", 2100)+" #wrong",
		"you|Commit one change at a time, please.", // 10
	)
	transcriptT(t, repo, "claude/p1/s-measure.jsonl", text)
	if e := startEvent("s-measure", old.Add(30*time.Second), commit); appendEvent(repo, &e) != nil {
		t.Fatal("append the start of s-measure")
	}
	// A fourth memory, which the session sees from a resume before its last line.
	if out, err := run(t, dev, memoryText("test-deploys", "Read before you deploy: run the tests.", "Run the tests before a deploy."), "remember"); err != nil {
		t.Fatalf("remember: %v\n%s", err, out)
	}
	commit = strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	if e := startEvent("s-measure", old.Add(9*time.Minute+30*time.Second), commit); appendEvent(repo, &e) != nil {
		t.Fatal("append the resume of s-measure")
	}
	for i := range 11 {
		times = append(times, old.Add(time.Duration(i)*time.Minute).Format(time.RFC3339))
	}
	_, second := lineTimes(t, repo)
	for _, r := range []reflectRecord{
		{Session: "s-measure", File: "claude/p1/s-measure.jsonl", Level: "~/dev", Corrections: []reflectCorrection{
			{Time: times[0], Quote: "Fix the login bug.", Summary: "Fix bugs.", Verdict: verdictStale, Path: "dev/memories/ask-before-push.md"},
			{Time: times[5], Quote: "Ask before you push.", Summary: "Ask the person before a push.", Verdict: verdictRetrieval, Path: "dev/memories/ask-before-push.md"},
			{Time: times[8], Quote: "/clear", Summary: "Clear less often.", Verdict: verdictCapture},
			{Time: times[9], Quote: "#wrong", Summary: "Memory failed.", Verdict: verdictRetrieval, Path: "dev/memories/gone.md"},
			{Time: times[10], Quote: "Commit one change at a time", Summary: "Commit small.", Verdict: verdictApplication, Path: "dev/memories/small-commits.md"},
			{Time: "2000-01-01T00:00:00Z", Quote: "A ghost.", Summary: "No line.", Verdict: verdictCapture},
		}},
		{Session: "s-second", File: "claude/p1/s-second.jsonl", Level: "~/dev", Corrections: []reflectCorrection{
			{Time: second, Quote: "Rename the flag.", Summary: "Name flags well.", Verdict: verdictCapture},
		}},
	} {
		r.Blob = strings.TrimSpace(gitT(t, repo, "rev-parse", transcriptsRef+":"+r.File))
		r.Agent = "claude"
		if err := appendReflect(repo, r); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"claude/p1/s-measure.jsonl", "claude/p1/s-second.jsonl"} {
		lines := strings.Split(strings.TrimSuffix(gitT(t, repo, "cat-file", "-p", transcriptsRef+":"+f), "\n"), "\n")
		raw = append(raw, lines[1:]...)
	}
	return repo, times, raw
}

// noModel fails the test when the stage calls the agent.
func noModel(t *testing.T) {
	t.Helper()
	fakeAgent(t, func(int, thinking.Request) (thinking.Reply, error) {
		t.Error("the stage called the agent")
		return thinking.Reply{}, errors.New("no call")
	}, func(thinking.Request) (thinking.Reply, error) {
		t.Error("the stage called the agent")
		return thinking.Reply{}, errors.New("no call")
	})
}

func TestReflectMeasure(t *testing.T) {
	repo, times, raw := measureFixture(t)
	noModel(t)
	before := readT(t, reflectPath(repo))
	dir := filepath.Join(t.TempDir(), "measure")
	var out bytes.Buffer
	if err := RunReflectEval(ReflectEval{Stage: "measure", Out: dir, Endpoint: evalTrap(t)}, &out); err != nil {
		t.Fatal(err)
	}

	size := 0
	for _, f := range []string{"claude/p1/s-measure.jsonl", "claude/p1/s-second.jsonl"} {
		n, err := strconv.Atoi(strings.TrimSpace(gitT(t, repo, "cat-file", "-s", transcriptsRef+":"+f)))
		if err != nil {
			t.Fatal(err)
		}
		size += n
	}
	// Each line of the two transcripts is in one excerpt, and no agent line is long enough to cut.
	all := 0
	for _, l := range raw {
		all += len(l) + 1
	}
	// The drop rules keep the lines 5 and 10 of s-measure, and the excerpt of line 5 holds lines 3 and 4.
	kept := 0
	for _, i := range []int{3, 4, 5, 10} {
		kept += len(raw[i]) + 1
	}
	excerpts := ""
	for _, n := range []int{1000, 2000, 4000} {
		excerpts += fmt.Sprintf("  With the last agent line cut to %d bytes: %d for all lines%s, %d for the kept lines%s.\n", n, all, percent(all, size), kept, percent(kept, size))
	}
	want := "Measure: 2 records, 0 failed. No model ran.\n" +
		"\n" +
		"Lines of the person: 8, 6 with a recorded correction. Recorded corrections: 6.\n" +
		"  Recorded corrections whose time matches no line of the person: 1.\n" +
		"  Transcripts with fewer than two lines of the person: 1, with 1 recorded correction.\n" +
		"Each rule: the lines that it hits, and the share of all lines; then the hits with a recorded correction, and the share of all lines with one.\n" +
		"  Drop first: 2 (25%), 2 (33%).\n" +
		"  Drop reply: 1 (13%), 0 (0%).\n" +
		"  Drop long, over 2000 bytes: 1 (13%), 1 (17%).\n" +
		"  Drop approval: 1 (13%), 0 (0%).\n" +
		"  Drop harness: 2 (25%), 1 (17%).\n" +
		"  Flag after-rejected: 1 (13%), 1 (17%).\n" +
		"  Flag mark: 1 (13%), 1 (17%).\n" +
		"  Flag negation: 2 (25%), 2 (33%).\n" +
		"  Long, over 1000 bytes: 1 (13%), 1 (17%).\n" +
		"  Long, over 4000 bytes: 0 (0%), 0 (0%).\n" +
		"  Hits of reply that negation also hits: 0 of 1, and 0 of 0 with a recorded correction.\n" +
		"  The drop rules together keep 2 of 8 lines (25%), with 2 of 6 recorded corrections (33%).\n" +
		"Starts of the lines that the rule harness hits: the hits, the hits with a recorded correction, and the bytes of their text.\n" +
		"  /compact: 1, 0, 8 bytes.\n" +
		"  <command-name: 1, 1, 35 bytes.\n" +
		fmt.Sprintf("Excerpts: the transcripts hold %d bytes.\n", size) +
		excerpts +
		"Rank: recorded corrections with a memory: 4. No seen commit: 1. A path that is not among the memories: 1. Memories that fail to load: 0. Ranked: 2.\n" +
		"  By the quote: top 1: 2 (100%), top 3: 2 (100%), top 5: 2 (100%), top 10: 2 (100%).\n" +
		"  By the quote and the summary: top 1: 2 (100%), top 3: 2 (100%), top 5: 2 (100%), top 10: 2 (100%).\n"
	got := out.String()
	if !strings.HasPrefix(got, want) {
		t.Errorf("report:\n%s\nwant it to start with:\n%s", got, want)
	}
	// The two ranked corrections saw two commits, so they make two groups: one of three memories, and one of four.
	// The top 5 of each query hold all the memories of the group.
	last := strings.TrimPrefix(got, want)
	var groups int
	var memories float64
	var allBytes, byQuote, bySummary int
	if _, err := fmt.Sscanf(last, "  Groups of ranked corrections: %d, with a mean of %f memories and %d bytes.\n", &groups, &memories, &allBytes); err != nil {
		t.Fatalf("the line of the groups in %q: %v", last, err)
	}
	_, last, _ = strings.Cut(last, "\n")
	if _, err := fmt.Sscanf(last, "  The top 5 of a ranked correction hold a mean of %d bytes by the quote, and %d by the quote and the summary.\n", &byQuote, &bySummary); err != nil {
		t.Fatalf("the line of the top 5 in %q: %v", last, err)
	}
	if groups != 2 || memories != 3.5 || allBytes == 0 || byQuote != allBytes || bySummary != allBytes {
		t.Errorf("the means:\n%s", strings.TrimPrefix(got, want))
	}

	results := readT(t, filepath.Join(dir, "results.jsonl"))
	// The start of a harness line, such as "/compact", is a label and no free text, and "/clear" is a recorded quote.
	for _, text := range []string{"Should I also", "edit b.go", "never edit", "zzz"} {
		if strings.Contains(results, text) {
			t.Errorf("results.jsonl holds the text %q of a transcript", text)
		}
	}
	lines := strings.Split(strings.TrimSpace(results), "\n")
	if len(lines) != 2 {
		t.Fatalf("results.jsonl has %d lines, want 2", len(lines))
	}
	var r measureResult
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatal(err)
	}
	if r.Session != "s-measure" || len(r.Lines) != 7 {
		t.Fatalf("result = %+v", r)
	}
	for _, c := range []struct {
		line        int
		time        string
		rules       []string
		start       string
		corrections int
	}{
		{0, times[0], []string{"first"}, "", 1},
		{1, times[2], []string{"reply", "approval"}, "", 0},
		{2, times[5], []string{"after-rejected", "negation"}, "", 1},
		{3, times[7], []string{"harness"}, "/compact", 0},
		{4, times[8], []string{"harness"}, "<command-name", 1},
		{5, times[9], []string{"long", "mark", "negation"}, "", 1},
		{6, times[10], nil, "", 1},
	} {
		l := r.Lines[c.line]
		if l.Time != c.time || !slices.Equal(l.Rules, c.rules) || l.Start != c.start || l.Corrections != c.corrections {
			t.Errorf("line %d = %+v, want the time %s, the rules %v, the start %q and %d corrections", c.line, l, c.time, c.rules, c.start, c.corrections)
		}
	}
	if r.Lines[5].Bytes != 2107 {
		t.Errorf("the long line has %d bytes, want 2107", r.Lines[5].Bytes)
	}
	if want := []measureQuote{{Time: "2000-01-01T00:00:00Z", Quote: "A ghost."}}; !slices.Equal(r.Unmatched, want) {
		t.Errorf("unmatched = %+v, want %+v", r.Unmatched, want)
	}
	wantRanked := []measureRank{
		{Time: times[0], Quote: "Fix the login bug.", Verdict: verdictStale, Path: "dev/memories/ask-before-push.md", Skipped: "no seen commit"},
		{Time: times[5], Quote: "Ask before you push.", Verdict: verdictRetrieval, Path: "dev/memories/ask-before-push.md", Memories: 3, Rank: 1, RankWithSummary: 1},
		{Time: times[9], Quote: "#wrong", Verdict: verdictRetrieval, Path: "dev/memories/gone.md", Memories: 3, Skipped: "not among the memories"},
		{Time: times[10], Quote: "Commit one change at a time", Verdict: verdictApplication, Path: "dev/memories/small-commits.md", Memories: 4, Rank: 1, RankWithSummary: 1},
	}
	if !slices.Equal(r.Ranked, wantRanked) {
		t.Errorf("ranked =\n%+v\nwant\n%+v", r.Ranked, wantRanked)
	}

	if got := readT(t, reflectPath(repo)); got != before {
		t.Error("the stage changed reflect.jsonl")
	}
	clean(t, repo)
}

func TestReflectMeasureFlags(t *testing.T) {
	_, repo := reflectFixture(t)
	evalRecords(t, repo, nil, nil)
	noModel(t)
	dir := func() string { return filepath.Join(t.TempDir(), "out") }
	cases := []struct {
		name    string
		e       ReflectEval
		wantErr string
	}{
		{"measure with only an out", ReflectEval{Stage: "measure", Out: dir()}, ""},
		{"measure with an endpoint elsewhere, a model and a small context", ReflectEval{Stage: "measure", Endpoint: "http://example.com", Model: "big", Context: 1, Out: dir()}, ""},
		{"measure without an out", ReflectEval{Stage: "measure"}, "--out"},
		{"measure with a negative count of sessions", ReflectEval{Stage: "measure", Out: dir(), Sessions: -1}, "--sessions"},
		{"measure with an out in the memory repo", ReflectEval{Stage: "measure", Out: filepath.Join(repo, "measure")}, "memory repo"},
		{"extract without an endpoint", ReflectEval{Stage: "extract", Model: "small", Context: 32768, Out: dir()}, "--endpoint"},
		{"extract without a model", ReflectEval{Stage: "extract", Endpoint: "http://127.0.0.1:1", Context: 32768, Out: dir()}, "--model"},
		{"judge without an endpoint", ReflectEval{Stage: "judge", Model: "small", Context: 32768, Out: dir()}, "--endpoint"},
		{"judge without a model", ReflectEval{Stage: "judge", Endpoint: "http://127.0.0.1:1", Context: 32768, Out: dir()}, "--model"},
		{"judge with a small context", ReflectEval{Stage: "judge", Endpoint: "http://127.0.0.1:1", Model: "small", Context: 8192, Out: dir()}, "--context"},
		{"an unknown stage", ReflectEval{Stage: "measures", Endpoint: "http://127.0.0.1:1", Model: "small", Context: 32768, Out: dir()}, "measure"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			err := RunReflectEval(c.e, &out)
			if c.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(out.String(), "Measure: 2 records, 0 failed. No model ran.\n") {
					t.Errorf("report:\n%s", out.String())
				}
				if _, err := os.Stat(filepath.Join(c.e.Out, "results.jsonl")); err != nil {
					t.Error(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want one with %q", err, c.wantErr)
			}
			if out.Len() != 0 {
				t.Errorf("the check printed %q", out.String())
			}
		})
	}
	clean(t, repo)
}
