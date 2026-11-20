package thinking

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// partBytes is the most that one call of the extraction reads of a transcript.
const partBytes = 300_000

// Correction is one correction of the person in a transcript.
type Correction struct {
	Time    string // the time of the line that holds the quote
	Quote   string // the words of the person, as the transcript spells them
	Summary string // one sentence that states the rule
}

// ExtractRequest asks for the corrections of one trimmed transcript.
type ExtractRequest struct {
	Transcript string
	Model      string
	Budget     float64 // in dollars at API prices; 0 sets no limit
}

const extractPrompt = `Standard input holds the transcript of one session between a person and a coding agent, as JSON lines.
Each line has a time, a "who" and a text. "you" is the person. "agent" is the agent. "call" is a tool call of the agent.
"rejected" is a call that the person rejected, and the next "you" line often gives the reason.

List every correction of the person. A correction is a "you" line in which the person tells the agent that it did
something wrong, or how to do it differently, or states a rule or a preference that the agent broke or did not know.
The reason after a rejected call is a correction.

These are no corrections: a new task, a question, an answer to a question of the agent, an approval, praise, and
a change of mind about what the person wants built.

For each correction give:
- "time": the time of its "you" line, copied exactly;
- "quote": the words of the person that state the correction, copied exactly from that line, at most three sentences;
- "summary": one sentence that states the rule for a future session, without the details of this task.

Return an empty list when the person corrected nothing.`

const extractSchema = `{"type":"object","properties":{"corrections":{"type":"array","items":{"type":"object","properties":{"time":{"type":"string"},"quote":{"type":"string"},"summary":{"type":"string"}},"required":["time","quote","summary"]}}},"required":["corrections"]}`

// line is one line of a trimmed transcript.
type line struct {
	Time string `json:"time"`
	Who  string `json:"who"`
	Text string `json:"text"`
}

// Extract returns the corrections of a transcript, and the cost of its calls. It keeps a correction only when
// its quote matches the text of one line of the person. The correction then takes the time of that line and the
// words as the line spells them, so nothing of the agent's own wording reaches the record. On ErrBudget it
// returns no correction, with the cost so far.
func Extract(req ExtractRequest) ([]Correction, float64, error) {
	var yours []line
	for _, raw := range strings.Split(req.Transcript, "\n") {
		var l line
		if json.Unmarshal([]byte(raw), &l) == nil && l.Who == "you" && l.Text != "" {
			yours = append(yours, l)
		}
	}
	if len(yours) == 0 {
		return nil, 0, nil // a session without a word of the person holds no correction
	}
	var cost float64
	var found []Correction
	seen := map[[2]string]bool{}
	for _, part := range parts(req.Transcript, partBytes) {
		budget := req.Budget
		if req.Budget > 0 {
			if budget = req.Budget - cost; budget <= 0 {
				return nil, cost, ErrBudget
			}
		}
		reply, err := Run(Request{Prompt: extractPrompt, Input: part, Schema: extractSchema, Model: req.Model, Budget: budget})
		cost += reply.Cost
		if err != nil {
			return nil, cost, err
		}
		var out struct {
			Corrections []struct {
				Time    string `json:"time"`
				Quote   string `json:"quote"`
				Summary string `json:"summary"`
			} `json:"corrections"`
		}
		if err := json.Unmarshal(reply.Output, &out); err != nil {
			return nil, cost, fmt.Errorf("read the corrections of the agent: %w", err)
		}
		for _, c := range out.Corrections {
			l, quote, ok := match(yours, c.Quote, c.Time)
			if key := [2]string{l.Time, quote}; ok && !seen[key] {
				seen[key] = true
				found = append(found, Correction{Time: l.Time, Quote: quote, Summary: strings.Join(strings.Fields(c.Summary), " ")})
			}
		}
	}
	return found, cost, nil
}

// parts splits a transcript at line ends into parts of at most size bytes. Each part starts with the header line.
// A line over the size is a part of its own, and goes whole.
func parts(transcript string, size int) []string {
	header, rest, _ := strings.Cut(transcript, "\n")
	header += "\n"
	if len(transcript) <= size {
		return []string{transcript}
	}
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, header+b.String())
			b.Reset()
		}
	}
	for _, l := range strings.SplitAfter(rest, "\n") {
		if l == "" {
			continue
		}
		if b.Len() > 0 && len(header)+b.Len()+len(l) > size {
			flush()
		}
		b.WriteString(l)
	}
	flush()
	return out
}

// match finds the line of the person that holds a quote, and returns it with the quote as the line spells it.
// It compares after it joins each run of whitespace. When several lines hold the quote, the line nearest to
// the time that the agent gave wins, and the earliest one when that time does not parse.
func match(yours []line, quote, at string) (line, string, bool) {
	want := strings.Join(strings.Fields(quote), " ")
	if want == "" {
		return line{}, "", false
	}
	target, terr := time.Parse(time.RFC3339, at)
	best, bestQuote, found := line{}, "", false
	var bestGap time.Duration
	for _, l := range yours {
		text, starts, ends := joined(l.Text)
		i := strings.Index(text, want)
		if i < 0 {
			continue
		}
		spelled := l.Text[starts[i]:ends[i+len(want)-1]]
		gap := time.Duration(-1)
		if t, err := time.Parse(time.RFC3339, l.Time); err == nil && terr == nil {
			if gap = t.Sub(target); gap < 0 {
				gap = -gap
			}
		}
		if !found || (gap >= 0 && (bestGap < 0 || gap < bestGap)) {
			best, bestQuote, bestGap, found = l, spelled, gap, true
		}
	}
	return best, bestQuote, found
}

// joined returns a text with each run of whitespace as one space, and none at its ends. For each byte of the
// result, it also returns where the rune behind it starts and ends in the text.
func joined(text string) (string, []int, []int) {
	var b strings.Builder
	var starts, ends []int
	space := -1 // the start of a run of whitespace that waits for a word after it
	for i, r := range text {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 1 // an invalid byte
		}
		if unicode.IsSpace(r) {
			if b.Len() > 0 && space < 0 {
				space = i
			}
			continue
		}
		if space >= 0 {
			b.WriteByte(' ')
			starts, ends = append(starts, space), append(ends, i)
			space = -1
		}
		for range size {
			starts, ends = append(starts, i), append(ends, i+size)
		}
		b.WriteString(text[i : i+size])
	}
	return b.String(), starts, ends
}
