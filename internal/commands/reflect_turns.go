package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// turnSource is the proof of the source turn of a correction. Two corrections that have the same Prefix quote the
// same turn of the same history. The zero value means that the turn is not proven.
type turnSource struct {
	Prefix string // the hash of every event of the transcript through the turn
	Text   string // the complete text of the turn, as the transcript holds it
	OK     bool   // the turn is the only match of the correction in a valid prefix
}

// turnObject decodes a line as a JSON object, and returns nil when it is anything else.
func turnObject(line string) map[string]json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(line), &obj) != nil {
		return nil
	}
	return obj
}

// turnString returns the string that a field of the object holds. A field that is absent, null or no string is missing.
func turnString(obj map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := obj[name]
	var s string
	if !ok || string(bytes.TrimSpace(raw)) == "null" || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// turnEventOf decodes a line as an event: a JSON object with the strings time, who and text, and a time and a who that
// are not empty.
func turnEventOf(line string) (transcriptLine, bool) {
	obj := turnObject(line)
	time, okTime := turnString(obj, "time")
	who, okWho := turnString(obj, "who")
	text, okText := turnString(obj, "text")
	ev := transcriptLine{Time: time, Who: who, Text: text}
	return ev, okTime && okWho && okText && time != "" && who != ""
}

// turnSources finds the source turn of each correction in one transcript, in one scan. The result holds one turnSource
// for each correction, in order.
//
// The transcript is a header, then one event on each line. The header is a JSON object whose agent is claude or codex.
// An event is a JSON object with the strings time, who and text. Time and who are not empty.
//
// A blank line, a second header, a missing field or invalid JSON breaks the prefix. That line and all later lines are
// no part of any proof. Later valid events still count as matches, so a repeated turn after a break stays ambiguous.
//
// A correction belongs to the line of the person with its exact time that holds its quote, as lineOf joins whitespace.
// It needs exactly one such line in the valid prefix. Its Prefix hashes the decoded time, speaker and text of every
// event through that line. Its Text is the decoded text of that line.
func turnSources(content string, corrections []reflectCorrection) []turnSource {
	out := make([]turnSource, len(corrections))
	content = strings.TrimSuffix(content, "\n")
	header, rest, found := strings.Cut(content, "\n")
	if agent, ok := turnString(turnObject(header), "agent"); !ok || (agent != "claude" && agent != "codex") || !found {
		return out // no valid header, or a header alone that holds no event
	}

	quotes := make([]string, len(corrections))
	byTime := map[string][]int{}
	for i, c := range corrections {
		if quotes[i] = strings.Join(strings.Fields(c.Quote), " "); quotes[i] != "" {
			byTime[c.Time] = append(byTime[c.Time], i)
		}
	}
	matched := make([]bool, len(corrections))

	h := sha256.New()
	enc := json.NewEncoder(h)
	enc.SetEscapeHTML(false)
	valid := true
	for _, line := range strings.Split(rest, "\n") {
		ev, ok := turnEventOf(line)
		if !ok {
			valid = false // the line holds no event, so it matches nothing
			continue
		}
		if valid {
			_ = enc.Encode(ev) // a struct of strings always encodes
		}
		if ev.Who != "you" {
			continue
		}
		candidates := byTime[ev.Time]
		if len(candidates) == 0 {
			continue
		}
		text := strings.Join(strings.Fields(ev.Text), " ")
		for _, i := range candidates {
			if !strings.Contains(text, quotes[i]) {
				continue
			}
			if matched[i] {
				out[i] = turnSource{} // a second match leaves the turn ambiguous
				continue
			}
			matched[i] = true
			if valid {
				out[i] = turnSource{Prefix: hex.EncodeToString(h.Sum(nil)), Text: ev.Text, OK: true}
			}
		}
	}
	return out
}
