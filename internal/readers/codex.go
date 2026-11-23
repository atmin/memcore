package readers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

// CodexSession names a session of Codex. TrimCodex returns it as the file holds it, without redaction.
type CodexSession struct {
	ID     string
	Dir    string // the working directory of the session
	Parent string // the session that spawned a subagent, or empty for a main session
}

// codexLine is one line of a session file of Codex.
type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Ordinal   int64           `json:"ordinal"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// codexMeta is the payload of a session_meta line.
type codexMeta struct {
	ID           string          `json:"id"`
	Cwd          string          `json:"cwd"`
	Source       json.RawMessage `json:"source"`
	HistoryStart int64           `json:"subagent_history_start_ordinal"`
}

// codexSubagent is the source of a subagent.
type codexSubagent struct {
	Subagent struct {
		ThreadSpawn *struct {
			ParentThreadID string `json:"parent_thread_id"`
			AgentPath      string `json:"agent_path"`
		} `json:"thread_spawn"`
	} `json:"subagent"`
}

// codexText is one element of the content of a message.
type codexText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// codexItem is a completed item of the conversation.
type codexItem struct {
	Type          string                     `json:"type"`
	Content       []codexText                `json:"content"`
	Command       []string                   `json:"command"`
	Changes       map[string]json.RawMessage `json:"changes"`
	Server        string                     `json:"server"`
	Tool          string                     `json:"tool"`
	Kind          string                     `json:"kind"`
	Query         *string                    `json:"query"`
	RevisedPrompt *string                    `json:"revisedPrompt"`
	Action        json.RawMessage            `json:"action"`
}

// codexAgentMessage is the payload of a message between agents.
type codexAgentMessage struct {
	Author  string      `json:"author"`
	Content []codexText `json:"content"`
}

// TrimCodex reads a session file of Codex and writes the conversation in the trimmed format of docs/transcripts.md.
// It writes a header line, then one line for each text of the person, the parent or the agent, and for each call.
// It returns the session that the first session_meta names.
//
// It writes nothing for a guardian, for a file with no session_meta, and when no line of the conversation
// survives. The same input always gives the same output. It ignores opts.Subagent, because the file names its own kind.
func TrimCodex(r io.Reader, w io.Writer, opts TrimOptions) (CodexSession, TrimStats, error) {
	var sess CodexSession
	var stats TrimStats
	var records []record
	var meta bool        // the first session_meta is read
	var start int64      // the first ordinal of the session itself, in a fork
	var agentPath string // the agent path of a subagent
	var source string    // the source of a main session
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
		if who == "call" && utf8.RuneCountInString(text) > maxCall {
			text = string([]rune(text)[:maxCall])
		}
		records = append(records, record{Time: time, Who: who, Text: text})
	}
	you := "you"

	br := bufio.NewReader(r)
	for {
		raw, err := br.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return sess, stats, fmt.Errorf("read the session: %w", err)
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
		var l codexLine
		if json.Unmarshal(raw, &l) != nil {
			stats.Broken++
			continue
		}
		if !meta {
			if l.Type != "session_meta" {
				continue
			}
			var m codexMeta
			if json.Unmarshal(l.Payload, &m) != nil {
				stats.Broken++
				continue
			}
			meta = true
			sess.ID, sess.Dir, start = m.ID, m.Cwd, m.HistoryStart
			if len(m.Source) > 0 && json.Unmarshal(m.Source, &source) != nil { // an object names a subagent
				var s codexSubagent
				if json.Unmarshal(m.Source, &s) != nil {
					stats.Broken++
					continue
				}
				if s.Subagent.ThreadSpawn == nil {
					return sess, stats, nil // a guardian, which reviews the approvals of another session
				}
				sess.Parent, agentPath = s.Subagent.ThreadSpawn.ParentThreadID, s.Subagent.ThreadSpawn.AgentPath
				you = "parent"
			}
			continue
		}
		if l.Ordinal < start {
			continue // a copy of the history of the parent
		}
		switch l.Type {
		case "event_msg":
			var p struct {
				Type string          `json:"type"`
				Item json.RawMessage `json:"item"`
			}
			if json.Unmarshal(l.Payload, &p) != nil {
				stats.Broken++
				continue
			}
			if p.Type != "item_completed" {
				continue
			}
			var kind struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(p.Item, &kind) != nil {
				stats.Broken++
				continue
			}
			switch kind.Type {
			case "UserMessage", "AgentMessage", "CommandExecution", "FileChange", "McpToolCall", "Extension":
			default:
				continue // no part of the conversation
			}
			var it codexItem
			if json.Unmarshal(p.Item, &it) != nil {
				stats.Broken++
				continue
			}
			switch it.Type {
			case "UserMessage":
				for _, c := range it.Content {
					if strings.EqualFold(c.Type, "text") {
						add(l.Timestamp, you, c.Text)
					}
				}
			case "AgentMessage":
				for _, c := range it.Content {
					if strings.EqualFold(c.Type, "text") {
						add(l.Timestamp, "agent", c.Text)
					}
				}
			default:
				add(l.Timestamp, "call", codexCall(it))
			}
		case "response_item":
			var p struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(l.Payload, &p) != nil {
				stats.Broken++
				continue
			}
			// A message of type "message" repeats the conversation with injected context, so only agent_message counts.
			if p.Type != "agent_message" || sess.Parent == "" {
				continue
			}
			var m codexAgentMessage
			if json.Unmarshal(l.Payload, &m) != nil {
				stats.Broken++
				continue
			}
			if m.Author == agentPath {
				continue
			}
			for _, c := range m.Content {
				add(l.Timestamp, "parent", c.Text)
			}
		}
	}
	if len(records) == 0 {
		return sess, stats, nil
	}
	h := header{Agent: "codex", Session: redact(sess.ID), Parent: redact(sess.Parent), Dir: redact(sess.Dir)} // a directory may name a secret too
	if source == "exec" {
		h.Source = "exec"
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(h); err != nil {
		return sess, stats, fmt.Errorf("write the trimmed transcript: %w", err)
	}
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			return sess, stats, fmt.Errorf("write the trimmed transcript: %w", err)
		}
	}
	return sess, stats, nil
}

// codexCall describes a call on one line: its kind, then the value that describes it best.
// TrimCodex cuts it to maxCall characters after the redaction.
func codexCall(it codexItem) string {
	var name, value string
	switch it.Type {
	case "CommandExecution":
		name = "exec"
		if len(it.Command) > 0 {
			value = it.Command[len(it.Command)-1]
		}
	case "FileChange":
		name = "patch"
		paths := make([]string, 0, len(it.Changes))
		for p := range it.Changes {
			paths = append(paths, p)
		}
		slices.Sort(paths)
		value = strings.Join(paths, ", ")
	case "McpToolCall":
		name, value = "mcp", it.Server+"."+it.Tool
	case "Extension":
		name = it.Kind
		var action struct {
			URL string `json:"url"`
		}
		json.Unmarshal(it.Action, &action)
		for _, v := range []*string{it.Query, &action.URL, it.RevisedPrompt} {
			if v != nil && strings.TrimSpace(*v) != "" {
				value = *v
				break
			}
		}
	}
	if strings.TrimSpace(value) == "" {
		return name
	}
	first, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
	return name + ": " + first
}
