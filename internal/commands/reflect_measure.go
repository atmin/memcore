package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/thinking"
)

// stageMeasure is the stage of the check that calls no model. It measures, against the records of the reflect
// passes, how much code could take over from the model of a pass.
const stageMeasure = "measure"

// transcriptLine is one line of a trimmed transcript after its header.
type transcriptLine struct {
	Time string `json:"time"`
	Who  string `json:"who"`
	Text string `json:"text"`
}

// parseTranscript returns the lines of a trimmed transcript after its header. It skips a line that is no JSON.
func parseTranscript(content string) []transcriptLine {
	_, rest, _ := strings.Cut(content, "\n")
	var out []transcriptLine
	for _, raw := range strings.Split(rest, "\n") {
		var l transcriptLine
		if strings.TrimSpace(raw) != "" && json.Unmarshal([]byte(raw), &l) == nil {
			out = append(out, l)
		}
	}
	return out
}

// isYou reports whether a line is a line of the person. A line with no text is none, as thinking.Extract reads it.
func isYou(l transcriptLine) bool {
	return l.Who == "you" && l.Text != ""
}

// measureRule is a pure function of the lines of one transcript and the index of one line of the person. A drop
// rule says that the line holds no correction. A flag rule says that the line likely holds one, and drops nothing.
type measureRule struct {
	name string
	drop bool
	hits func(lines []transcriptLine, i int) bool
}

// longBytes is the size of the text over which the rule long drops a line. extraLong holds the other sizes that the
// report counts, outside the drop rules together.
const longBytes = 2000

var extraLong = []int{1000, 4000}

// measureRules are the rules of the stage, the drop rules first.
var measureRules = []measureRule{
	{"first", true, ruleFirst},
	{"reply", true, ruleReply},
	{"long", true, longer(longBytes)},
	{"approval", true, ruleApproval},
	{"harness", true, ruleHarness},
	{"after-rejected", false, ruleAfterRejected},
	{"mark", false, ruleMark},
	{"negation", false, ruleNegation},
}

// ruleFirst hits the first line of the person of a transcript.
func ruleFirst(lines []transcriptLine, i int) bool {
	for _, l := range lines[:i] {
		if isYou(l) {
			return false
		}
	}
	return true
}

// ruleReply hits a line that answers a question: the last agent line before it ends with a question mark, and no
// other line of the person comes between them.
func ruleReply(lines []transcriptLine, i int) bool {
	for j := i - 1; j >= 0; j-- {
		switch {
		case isYou(lines[j]):
			return false
		case lines[j].Who == "agent":
			return strings.HasSuffix(strings.TrimSpace(lines[j].Text), "?")
		}
	}
	return false
}

// longer returns a rule that hits a line whose text is over n bytes.
func longer(n int) func([]transcriptLine, int) bool {
	return func(lines []transcriptLine, i int) bool {
		return len(lines[i].Text) > n
	}
}

// approvals are the texts of the rule approval, in lower case and without a final full stop or exclamation mark.
var approvals = setOf("yes", "y", "ok", "okay", "go", "go ahead", "continue", "proceed", "do it", "lgtm", "thanks",
	"thank you", "sure", "great", "perfect", "yes please", "sounds good")

// ruleApproval hits a line that only approves.
func ruleApproval(lines []transcriptLine, i int) bool {
	text := strings.ToLower(strings.TrimSpace(lines[i].Text))
	if strings.HasSuffix(text, ".") || strings.HasSuffix(text, "!") {
		text = text[:len(text)-1]
	}
	return approvals[text]
}

// ruleHarness hits a line that the harness of the agent wrote, such as a command or a tag.
func ruleHarness(lines []transcriptLine, i int) bool {
	text := strings.TrimSpace(lines[i].Text)
	return strings.HasPrefix(text, "/") || strings.HasPrefix(text, "<")
}

// ruleAfterRejected hits a line that comes right after a rejected call.
func ruleAfterRejected(lines []transcriptLine, i int) bool {
	return i > 0 && lines[i-1].Who == "rejected"
}

// ruleMark hits a line that holds a mark.
func ruleMark(lines []transcriptLine, i int) bool {
	for _, m := range []string{"#wrong", "#again", "#stale"} {
		if strings.Contains(lines[i].Text, m) {
			return true
		}
	}
	return false
}

// negations are the words of the rule negation.
var negations = setOf("no", "not", "don't", "dont", "never", "instead", "wrong", "shouldn't", "why", "again", "stop",
	"actually", "but", "rather", "prefer", "always")

// ruleNegation hits a line that holds one of negations as a whole word, in any case.
func ruleNegation(lines []transcriptLine, i int) bool {
	text := strings.ReplaceAll(strings.ToLower(lines[i].Text), "’", "'")
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' })
	for _, w := range words {
		if negations[strings.Trim(w, "'")] {
			return true
		}
	}
	return false
}

// setOf returns a set of words.
func setOf(words ...string) map[string]bool {
	set := map[string]bool{}
	for _, w := range words {
		set[w] = true
	}
	return set
}

// excerptCuts are the sizes, in bytes, to which an excerpt cuts its agent line.
var excerptCuts = []int{1000, 2000, 4000}

// excerptCalls is the most calls that an excerpt keeps.
const excerptCalls = 5

// excerpt returns what a model would read to classify the line of the person at i, in the order of the transcript.
// The span of the line runs from the line of the person before it. The excerpt holds each rejected line of the
// span, its last calls, and its last agent line cut to its last n bytes. The line itself comes last.
func excerpt(lines []transcriptLine, i, n int) []transcriptLine {
	start := 0
	for j := i - 1; j >= 0; j-- {
		if isYou(lines[j]) {
			start = j + 1
			break
		}
	}
	lastAgent, calls := -1, []int{}
	for j := start; j < i; j++ {
		switch lines[j].Who {
		case "agent":
			lastAgent = j
		case "call":
			calls = append(calls, j)
		}
	}
	firstCall := i
	if len(calls) > 0 {
		firstCall = calls[max(0, len(calls)-excerptCalls)]
	}
	var out []transcriptLine
	for j := start; j < i; j++ {
		l := lines[j]
		switch {
		case l.Who == "rejected", l.Who == "call" && j >= firstCall:
			out = append(out, l)
		case j == lastAgent:
			l.Text = lastBytes(l.Text, n)
			out = append(out, l)
		}
	}
	return append(out, lines[i])
}

// lastBytes returns the end of a text of at most n bytes. It starts at a rune boundary.
func lastBytes(text string, n int) string {
	if len(text) <= n {
		return text
	}
	s := len(text) - n
	for s < len(text) && !utf8.RuneStart(text[s]) {
		s++
	}
	return text[s:]
}

// lineBytes returns the size of lines as JSON lines, each with its line end, as the readers write them.
func lineBytes(lines []transcriptLine) int {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, l := range lines {
		_ = enc.Encode(l) // a struct of strings always encodes
	}
	return b.Len()
}

// The parameters of BM25.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// stopWords are the words that searchTerms leaves out: English words of grammar, the pieces of contractions, and
// words of talk such as "please". A word of under three runes goes anyway.
var stopWords = setOf(strings.Fields(`about above after again all also and any are aren because been before being
	below both but can could couldn did didn does doesn doing done don each few for from further had hadn has hasn
	have haven having her here hers him his how into isn its just let may might more most must nor not now off once
	only onto other our ours out over own please same shall she should shouldn some such than that the their theirs
	them then there these they this those too under until very via was wasn were weren what when where which while
	who whom why will with won would wouldn yes you your yours`)...)

// searchTerms returns the terms of a text for BM25: its runs of letters and digits, in lower case, of three runes
// or more, without stopWords.
func searchTerms(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(w) >= 3 && !stopWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// bm25 returns the BM25 score of each document for a query. Each distinct term of the query counts once. The idf
// is ln(1 + (N - n + 0.5) / (n + 0.5)), which stays positive.
func bm25(query string, docs []string) []float64 {
	counts := make([]map[string]int, len(docs))
	lengths := make([]int, len(docs))
	df := map[string]int{}
	total := 0
	for i, d := range docs {
		counts[i] = map[string]int{}
		for _, t := range searchTerms(d) {
			counts[i][t]++
			lengths[i]++
		}
		for t := range counts[i] {
			df[t]++
		}
		total += lengths[i]
	}
	scores := make([]float64, len(docs))
	seen := map[string]bool{}
	for _, q := range searchTerms(query) {
		if seen[q] || df[q] == 0 {
			continue
		}
		seen[q] = true
		n, all := float64(df[q]), float64(len(docs))
		idf := math.Log(1 + (all-n+0.5)/(n+0.5))
		avg := float64(total) / all
		for i := range docs {
			f := float64(counts[i][q])
			if f == 0 {
				continue
			}
			scores[i] += idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(lengths[i])/avg))
		}
	}
	return scores
}

// rankMemories returns the indices of memories by their BM25 score for a query, over the description and the body
// of each. Equal scores go by path.
func rankMemories(query string, memories []thinking.Memory) []int {
	docs := make([]string, len(memories))
	order := make([]int, len(memories))
	for i, m := range memories {
		docs[i] = m.Description + " " + m.Body
		order[i] = i
	}
	scores := bm25(query, docs)
	sort.SliceStable(order, func(a, b int) bool {
		if scores[order[a]] != scores[order[b]] {
			return scores[order[a]] > scores[order[b]]
		}
		return memories[order[a]].Path < memories[order[b]].Path
	})
	return order
}

// memoryBytes returns the size of memories as JSON, as thinking.Judge writes each.
func memoryBytes(memories []thinking.Memory) int {
	n := 0
	for _, m := range memories {
		data, _ := json.Marshal(m) // a struct of strings always encodes
		n += len(data)
	}
	return n
}

// measureTops are the ranks that the report counts a recorded memory within.
var measureTops = []int{1, 3, 5, 10}

// measureQueries names the two queries of a correction.
var measureQueries = []string{"the quote", "the quote and the summary"}

// measureResult is one line of results.jsonl in the measure stage: what the stage found for one record. It holds no
// text of the transcript besides the recorded quotes.
type measureResult struct {
	Session   string         `json:"session"`
	File      string         `json:"file"`
	Error     string         `json:"error,omitempty"`
	Bytes     int            `json:"bytes"` // the size of the transcript
	Lines     []measureLine  `json:"lines,omitempty"`
	Unmatched []measureQuote `json:"unmatched,omitempty"`
	Ranked    []measureRank  `json:"ranked,omitempty"`
}

// measureLine is one line of the person: its time, the size of its text, the rules that hit it, and the count of
// its recorded corrections. A line that the rule harness hits also has its start, from harnessStart.
type measureLine struct {
	Time        string   `json:"time"`
	Bytes       int      `json:"bytes"`
	Rules       []string `json:"rules,omitempty"`
	Start       string   `json:"start,omitempty"`
	Corrections int      `json:"corrections,omitempty"`
}

// startBytes is the most bytes of a name in a start.
const startBytes = 40

// harnessStart returns the start of a line that the rule harness hits: a short label that holds no free text of the
// person. It returns "" for any other line.
//   - A comment gives "<!-- " and its first word, or "<!--" with no word.
//   - A tag gives "<" and its name, or "<other>" with no name.
//   - A command gives "/" and its name, or "/other" with no name. A path, whose name a "/" follows, gives "/<path>".
//
// A name or a word is a run of letters, digits, "-", "_" and ":", cut to startBytes.
func harnessStart(text string) string {
	t := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(t, "<!--"):
		body, _, _ := strings.Cut(t[len("<!--"):], "-->")
		word := nameRun(strings.TrimLeftFunc(body, unicode.IsSpace))
		if word == "" {
			return "<!--"
		}
		return "<!-- " + word
	case strings.HasPrefix(t, "<"):
		if name := nameRun(t[1:]); name != "" {
			return "<" + name
		}
		return "<other>"
	case strings.HasPrefix(t, "/"):
		name := nameRun(t[1:])
		switch {
		case strings.HasPrefix(t[1+len(name):], "/"):
			return "/<path>"
		case name != "":
			return "/" + name
		}
		return "/other"
	}
	return ""
}

// nameRun returns the run of letters, digits, "-", "_" and ":" at the start of a text, cut to startBytes at a rune
// boundary.
func nameRun(text string) string {
	end := len(text)
	for i, r := range text {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != ':' {
			end = i
			break
		}
	}
	run := text[:end]
	if len(run) > startBytes {
		cut := startBytes
		for cut > 0 && !utf8.RuneStart(run[cut]) {
			cut--
		}
		run = run[:cut]
	}
	return run
}

// startCount counts the lines of one start: the hits, the hits with a recorded correction, and the size of their
// text.
type startCount struct {
	hits, with, bytes int
}

// startRow is one start with its counts.
type startRow struct {
	name string
	startCount
}

// startRows returns at most limit starts, the most hits first and equal hits by name. It also returns the sum of
// the rest, and the count of the starts in it.
func startRows(starts map[string]startCount, limit int) ([]startRow, startCount, int) {
	var rows []startRow
	for _, name := range sortedKeys(starts) {
		rows = append(rows, startRow{name, starts[name]})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].hits > rows[j].hits })
	if len(rows) <= limit {
		return rows, startCount{}, 0
	}
	var rest startCount
	for _, r := range rows[limit:] {
		rest.hits += r.hits
		rest.with += r.with
		rest.bytes += r.bytes
	}
	return rows[:limit], rest, len(rows) - limit
}

// measureStartRows is the most starts that the report prints.
const measureStartRows = 30

// measureQuote is a recorded correction whose time matches no line of the person.
type measureQuote struct {
	Time  string `json:"time"`
	Quote string `json:"quote"`
}

// measureRank is one recorded correction that names a memory, with the rank of that memory for each query. A rank
// of 0 means that the stage skipped the correction, for the reason in Skipped.
type measureRank struct {
	Time            string `json:"time"`
	Quote           string `json:"quote"`
	Verdict         string `json:"verdict"`
	Path            string `json:"path"`
	Memories        int    `json:"memories,omitempty"` // the count of memories on the path of the level
	Rank            int    `json:"rank,omitempty"`     // by the quote
	RankWithSummary int    `json:"rank_with_summary,omitempty"`
	Skipped         string `json:"skipped,omitempty"`
}

// measureCounts are the totals of the report of the measure stage.
type measureCounts struct {
	records, failed                               int
	lines, withCorrection, corrections, unmatched int
	short, shortCorrections                       int
	hits, hitsWith                                map[string]int // by rule
	replyNegation, replyNegationWith              int            // the hits of reply that negation also hits, and those with a recorded correction
	starts                                        map[string]startCount
	kept, keptCorrections                         int
	transcriptBytes                               int
	excerptAll, excerptKept                       map[int]int // by cut
	named, noCommit, notAmong, loadFailed, ranked int
	tops                                          [2][4]int         // by query, then by top
	groups                                        map[[2]string]int // the size of the memories of each group, by record and seen commit
	memories                                      int               // the count of the memories of all groups
	topBytes                                      [2]int            // the size of the top 5 of all ranked corrections, by query
}

// longKey is the key in measureCounts.hits of the count of lines over size bytes, outside the drop rules.
func longKey(size int) string {
	return fmt.Sprintf("long over %d", size)
}

// memoryLoad is the memories of one seen commit and one level, or the error of their read.
type memoryLoad struct {
	memories []thinking.Memory
	err      error
}

// runMeasure runs the measure stage over records, writes a line of results for each, and prints the report.
// It calls no model.
func runMeasure(repo string, records []reflectRecord, events []Event, results *os.File, out io.Writer) error {
	c := measureCounts{hits: map[string]int{}, hitsWith: map[string]int{}, excerptAll: map[int]int{}, excerptKept: map[int]int{}, groups: map[[2]string]int{},
		starts: map[string]startCount{}}
	loads := map[[2]string]memoryLoad{}
	for _, r := range records {
		line, err := json.Marshal(measureRecord(repo, r, events, loads, &c))
		if err != nil {
			return fmt.Errorf("write the result of %s: %w", r.File, err)
		}
		if _, err := results.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("write the results of the check: %w", err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("write the results of the check: %w", err)
	}
	c.records = len(records)
	printMeasure(out, c)
	return nil
}

// lineOf returns the index of the line of the person that a recorded correction belongs to. That is the line with
// its time that holds its quote, else the first line with its time. Several lines share a time when one message of
// the person holds several texts. It compares after it joins each run of whitespace, as thinking.Extract does.
func lineOf(lines []transcriptLine, rc reflectCorrection) (int, bool) {
	quote := strings.Join(strings.Fields(rc.Quote), " ")
	first := -1
	for i, l := range lines {
		if !isYou(l) || l.Time != rc.Time {
			continue
		}
		if first < 0 {
			first = i
		}
		if quote != "" && strings.Contains(strings.Join(strings.Fields(l.Text), " "), quote) {
			return i, true
		}
	}
	return first, first >= 0
}

// measureRecord measures one record. A recorded correction belongs to the line that lineOf finds. A correction
// whose time matches no line of the person counts apart, and nowhere else.
func measureRecord(repo string, r reflectRecord, events []Event, loads map[[2]string]memoryLoad, c *measureCounts) measureResult {
	res := measureResult{Session: r.Session, File: r.File}
	content, err := store.Git(repo, "cat-file", "blob", r.Blob)
	if err != nil {
		c.failed++
		res.Error = fmt.Sprintf("read %s on the transcripts branch: %v", r.File, err)
		return res
	}
	res.Bytes = len(content)
	c.transcriptBytes += len(content)
	lines := parseTranscript(content)
	var yours []int
	for i, l := range lines {
		if isYou(l) {
			yours = append(yours, i)
		}
	}
	belongs := map[int]int{}
	var matched []reflectCorrection
	for _, rc := range r.Corrections {
		i, ok := lineOf(lines, rc)
		if !ok {
			c.unmatched++
			res.Unmatched = append(res.Unmatched, measureQuote{rc.Time, rc.Quote})
			continue
		}
		belongs[i]++
		matched = append(matched, rc)
	}
	c.lines += len(yours)
	c.corrections += len(matched)
	if len(yours) < 2 {
		c.short++
		c.shortCorrections += len(matched)
	}
	hit := func(key string, n int) {
		c.hits[key]++
		if n > 0 {
			c.hitsWith[key]++
		}
	}
	for _, i := range yours {
		n := belongs[i]
		if n > 0 {
			c.withCorrection++
		}
		ml := measureLine{Time: lines[i].Time, Bytes: len(lines[i].Text), Corrections: n}
		dropped := false
		for _, rule := range measureRules {
			if rule.hits(lines, i) {
				ml.Rules = append(ml.Rules, rule.name)
				hit(rule.name, n)
				dropped = dropped || rule.drop
			}
		}
		for _, size := range extraLong {
			if longer(size)(lines, i) {
				hit(longKey(size), n)
			}
		}
		if slices.Contains(ml.Rules, "reply") && slices.Contains(ml.Rules, "negation") {
			c.replyNegation++
			if n > 0 {
				c.replyNegationWith++
			}
		}
		if slices.Contains(ml.Rules, "harness") {
			ml.Start = harnessStart(lines[i].Text)
			s := c.starts[ml.Start]
			s.hits++
			if n > 0 {
				s.with++
			}
			s.bytes += len(lines[i].Text)
			c.starts[ml.Start] = s
		}
		if !dropped {
			c.kept++
			c.keptCorrections += n
		}
		for _, cut := range excerptCuts {
			b := lineBytes(excerpt(lines, i, cut))
			c.excerptAll[cut] += b
			if !dropped {
				c.excerptKept[cut] += b
			}
		}
		res.Lines = append(res.Lines, ml)
	}
	res.Ranked = measureRanks(repo, r, matched, events, loads, c)
	return res
}

// measureRanks ranks the memories for each correction of matched whose verdict names a memory. It finds the
// memories as evalJudgePart does: those on the path of the level, at the commit that the session saw then.
func measureRanks(repo string, r reflectRecord, matched []reflectCorrection, events []Event, loads map[[2]string]memoryLoad, c *measureCounts) []measureRank {
	var out []measureRank
	for _, rc := range matched {
		switch rc.Verdict {
		case verdictRetrieval, verdictApplication, verdictStale:
		default:
			continue
		}
		if rc.Path == "" {
			continue
		}
		c.named++
		rk := measureRank{Time: rc.Time, Quote: rc.Quote, Verdict: rc.Verdict, Path: rc.Path}
		at, _ := time.Parse(time.RFC3339, rc.Time)
		commit := seenCommit(events, r.Session, at)
		if commit == "" {
			c.noCommit++
			rk.Skipped = "no seen commit"
			out = append(out, rk)
			continue
		}
		key := [2]string{commit, r.Level}
		load, ok := loads[key]
		if !ok {
			load.memories, load.err = seenMemories(repo, commit, r.Level)
			loads[key] = load
		}
		if load.err != nil {
			c.loadFailed++
			rk.Skipped = load.err.Error()
			out = append(out, rk)
			continue
		}
		rk.Memories = len(load.memories)
		target := slices.IndexFunc(load.memories, func(m thinking.Memory) bool { return m.Path == rc.Path })
		if target < 0 {
			c.notAmong++
			rk.Skipped = "not among the memories"
			out = append(out, rk)
			continue
		}
		c.ranked++
		// A pass judges the corrections of a record that saw one commit together, so a group reads its memories once.
		group := [2]string{r.File, commit}
		if _, ok := c.groups[group]; !ok {
			c.groups[group] = memoryBytes(load.memories)
			c.memories += len(load.memories)
		}
		for q, query := range []string{rc.Quote, rc.Quote + " " + rc.Summary} {
			order := rankMemories(query, load.memories)
			rank := slices.Index(order, target) + 1
			for k, top := range measureTops {
				if rank <= top {
					c.tops[q][k]++
				}
			}
			var top5 []thinking.Memory
			for _, i := range order[:min(5, len(order))] {
				top5 = append(top5, load.memories[i])
			}
			c.topBytes[q] += memoryBytes(top5)
			if q == 0 {
				rk.Rank = rank
			} else {
				rk.RankWithSummary = rank
			}
		}
		out = append(out, rk)
	}
	return out
}

// printMeasure prints the report of the measure stage. It prints one block for each of these: the rules, the starts
// of the lines that the rule harness hits, the excerpts, and the rank. A rule prints the lines that it hits, and those
// of them with a recorded correction.
func printMeasure(w io.Writer, c measureCounts) {
	hits := func(key string) string {
		return fmt.Sprintf("%d%s, %d%s", c.hits[key], percent(c.hits[key], c.lines), c.hitsWith[key], percent(c.hitsWith[key], c.withCorrection))
	}
	fmt.Fprintf(w, "Measure: %s, %d failed. No model ran.\n\n", plural(c.records, "record"), c.failed)
	fmt.Fprintf(w, "Lines of the person: %d, %d with a recorded correction. Recorded corrections: %d.\n", c.lines, c.withCorrection, c.corrections)
	fmt.Fprintf(w, "  Recorded corrections whose time matches no line of the person: %d.\n", c.unmatched)
	fmt.Fprintf(w, "  Transcripts with fewer than two lines of the person: %d, with %s.\n", c.short, plural(c.shortCorrections, "recorded correction"))
	fmt.Fprintln(w, "Each rule: the lines that it hits, and the share of all lines; then the hits with a recorded correction, and the share of all lines with one.")
	for _, r := range measureRules {
		kind, name := "Flag", r.name
		if r.drop {
			kind = "Drop"
		}
		if r.name == "long" {
			name = fmt.Sprintf("long, over %d bytes", longBytes)
		}
		fmt.Fprintf(w, "  %s %s: %s.\n", kind, name, hits(r.name))
	}
	for _, size := range extraLong {
		fmt.Fprintf(w, "  Long, over %d bytes: %s.\n", size, hits(longKey(size)))
	}
	fmt.Fprintf(w, "  Hits of reply that negation also hits: %d of %d, and %d of %d with a recorded correction.\n",
		c.replyNegation, c.hits["reply"], c.replyNegationWith, c.hitsWith["reply"])
	fmt.Fprintf(w, "  The drop rules together keep %d of %d lines%s, with %d of %d recorded corrections%s.\n",
		c.kept, c.lines, percent(c.kept, c.lines), c.keptCorrections, c.corrections, percent(c.keptCorrections, c.corrections))
	fmt.Fprintln(w, "Starts of the lines that the rule harness hits: the hits, the hits with a recorded correction, and the bytes of their text.")
	rows, rest, others := startRows(c.starts, measureStartRows)
	for _, r := range rows {
		fmt.Fprintf(w, "  %s: %d, %d, %d bytes.\n", r.name, r.hits, r.with, r.bytes)
	}
	if others > 0 {
		fmt.Fprintf(w, "  The rest, %s: %d, %d, %d bytes.\n", plural(others, "start"), rest.hits, rest.with, rest.bytes)
	}
	fmt.Fprintf(w, "Excerpts: the transcripts hold %d bytes.\n", c.transcriptBytes)
	for _, cut := range excerptCuts {
		fmt.Fprintf(w, "  With the last agent line cut to %d bytes: %d for all lines%s, %d for the kept lines%s.\n",
			cut, c.excerptAll[cut], percent(c.excerptAll[cut], c.transcriptBytes), c.excerptKept[cut], percent(c.excerptKept[cut], c.transcriptBytes))
	}
	fmt.Fprintf(w, "Rank: recorded corrections with a memory: %d. No seen commit: %d. A path that is not among the memories: %d. Memories that fail to load: %d. Ranked: %d.\n",
		c.named, c.noCommit, c.notAmong, c.loadFailed, c.ranked)
	for q, name := range measureQueries {
		var tops []string
		for k, top := range measureTops {
			tops = append(tops, fmt.Sprintf("top %d: %d%s", top, c.tops[q][k], percent(c.tops[q][k], c.ranked)))
		}
		fmt.Fprintf(w, "  By %s: %s.\n", name, strings.Join(tops, ", "))
	}
	if c.ranked > 0 {
		all := 0
		for _, size := range c.groups {
			all += size
		}
		groups := float64(len(c.groups))
		fmt.Fprintf(w, "  Groups of ranked corrections: %d, with a mean of %.1f memories and %d bytes.\n",
			len(c.groups), float64(c.memories)/groups, int(math.Round(float64(all)/groups)))
		mean := func(n int) int { return int(math.Round(float64(n) / float64(c.ranked))) }
		fmt.Fprintf(w, "  The top 5 of a ranked correction hold a mean of %d bytes by the quote, and %d by the quote and the summary.\n",
			mean(c.topBytes[0]), mean(c.topBytes[1]))
	}
}
