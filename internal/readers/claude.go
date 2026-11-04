// Package readers reads the files of agents, such as the transcripts and the auto-memory of Claude Code.
package readers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// TrimOptions sets how Trim treats a transcript.
type TrimOptions struct {
	Redact   func(string) string // applied to each text before Trim encodes it; nil keeps the text
	Subagent bool                // the transcript is the file of a subagent
}

// TrimStats says what Trim could not read.
type TrimStats struct {
	Broken  int  // complete lines that failed to parse
	Partial bool // the last line had no newline, so it is still being written, and Trim left it out
}

// header is the first line of a trimmed transcript.
type header struct {
	Agent   string `json:"agent"`
	Session string `json:"session,omitempty"`
	Parent  string `json:"parent,omitempty"`
	Dir     string `json:"dir,omitempty"`
}

// record is one line of the conversation in a trimmed transcript.
type record struct {
	Time string `json:"time"`
	Who  string `json:"who"`
	Text string `json:"text"`
}

// source is the part of a line of a Claude Code transcript that Trim reads.
type source struct {
	Type             string `json:"type"`
	Timestamp        string `json:"timestamp"`
	SessionID        string `json:"sessionId"`
	AgentID          string `json:"agentId"`
	Cwd              string `json:"cwd"`
	IsMeta           bool   `json:"isMeta"`
	IsSidechain      bool   `json:"isSidechain"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Message          struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// block is one block of the content of a message.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// callKeys are the inputs that describe a call best, in order. Without any of them, the key that sorts first wins.
var callKeys = []string{"description", "command", "file_path", "path", "pattern", "url", "query", "prompt"}

// maxCall is the most characters that a call keeps.
const maxCall = 200

// rejection starts the result of a call that the person rejected.
const rejection = "The user doesn't want to proceed with this tool use"

var reminderRE = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// Trim reads a Claude Code transcript and writes the conversation in the trimmed format of docs/transcripts.md:
// a header line, then one line for each text of the person and the agent, each call, and each rejected call.
// It writes nothing when no line of the conversation survives. The same input always gives the same output.
func Trim(r io.Reader, w io.Writer, opts TrimOptions) (TrimStats, error) {
	var stats TrimStats
	var h header
	var records []record
	calls := map[string]string{} // the id of each call, to its text
	redact := opts.Redact
	if redact == nil {
		redact = func(s string) string { return s }
	}
	add := func(time, who, text string) {
		if text = strings.TrimSpace(text); text == "" {
			return
		}
		// A cut first could split a secret, so the redaction sees the whole text.
		text = redact(text)
		if (who == "call" || who == "rejected") && utf8.RuneCountInString(text) > maxCall {
			text = string([]rune(text)[:maxCall])
		}
		records = append(records, record{Time: time, Who: who, Text: text})
	}
	you := "you"
	if opts.Subagent {
		you = "parent"
	}

	br := bufio.NewReader(r)
	for {
		raw, err := br.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return stats, fmt.Errorf("read the transcript: %w", err)
		}
		if errors.Is(err, io.EOF) {
			if len(bytes.TrimSpace(raw)) > 0 {
				stats.Partial = true
			}
			break
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var s source
		if json.Unmarshal(raw, &s) != nil {
			stats.Broken++
			continue
		}
		// A subagent is its own session, so its header waits for a line that names the agent.
		switch {
		case opts.Subagent && h.Session == "" && s.AgentID != "":
			h.Session, h.Parent = s.AgentID, s.SessionID
		case !opts.Subagent && h.Session == "":
			h.Session = s.SessionID
		}
		if h.Dir == "" {
			h.Dir = s.Cwd
		}
		if s.IsMeta || s.IsCompactSummary || (s.IsSidechain && !opts.Subagent) {
			continue
		}
		switch s.Type {
		case "user":
			var text string
			if json.Unmarshal(s.Message.Content, &text) == nil {
				add(s.Timestamp, you, reminderRE.ReplaceAllString(text, ""))
				continue
			}
			var blocks []block
			if json.Unmarshal(s.Message.Content, &blocks) != nil {
				stats.Broken++
				continue
			}
			for _, b := range blocks {
				switch b.Type {
				case "text":
					add(s.Timestamp, you, reminderRE.ReplaceAllString(b.Text, ""))
				case "tool_result":
					if b.IsError && strings.HasPrefix(resultText(b.Content), rejection) {
						add(s.Timestamp, "rejected", calls[b.ToolUseID])
					}
				}
			}
		case "assistant":
			var blocks []block
			if json.Unmarshal(s.Message.Content, &blocks) != nil {
				stats.Broken++
				continue
			}
			for _, b := range blocks {
				switch b.Type {
				case "text":
					add(s.Timestamp, "agent", b.Text)
				case "tool_use":
					c := call(b)
					calls[b.ID] = c
					add(s.Timestamp, "call", c)
				}
			}
		}
	}
	if len(records) == 0 {
		return stats, nil
	}
	h.Agent = "claude"
	h.Session, h.Parent, h.Dir = redact(h.Session), redact(h.Parent), redact(h.Dir) // a directory may name a secret too
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(h); err != nil {
		return stats, fmt.Errorf("write the trimmed transcript: %w", err)
	}
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			return stats, fmt.Errorf("write the trimmed transcript: %w", err)
		}
	}
	return stats, nil
}

// call describes a call on one line: the tool name, then the input that describes it best.
// Trim cuts it to maxCall characters after the redaction.
func call(b block) string {
	var input map[string]json.RawMessage
	json.Unmarshal(b.Input, &input)
	strs := map[string]string{}
	var keys []string
	for k, v := range input {
		var s string
		if json.Unmarshal(v, &s) == nil && strings.TrimSpace(s) != "" {
			strs[k] = s
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	value := ""
	for _, k := range callKeys {
		if s, ok := strs[k]; ok {
			value = s
			break
		}
	}
	if value == "" && len(keys) > 0 {
		value = strs[keys[0]]
	}
	text := b.Name
	if value != "" {
		first, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
		text += ": " + first
	}
	return text
}

// resultText returns the text of the content of a tool result, which is a string or a list of text blocks.
func resultText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []block
	json.Unmarshal(content, &blocks)
	var parts []string
	for _, b := range blocks {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n")
}
