package commands

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/thinking"
)

// settled is how old the last line of a transcript must be before a sleep pass judges it.
// A younger transcript most likely belongs to a session that still runs.
const settled = time.Hour

// sleepCorrection is one correction of the person, with the verdict of a sleep pass.
type reflectCorrection struct {
	Time    string `json:"time"`              // the time of the line that holds the quote
	Quote   string `json:"quote"`             // the words of the person, as the transcript spells them
	Summary string `json:"summary"`           // the rule that the correction states
	Verdict string `json:"verdict,omitempty"` // capture, retrieval, application, stale or unrelated
	Path    string `json:"path,omitempty"`    // the memory of a retrieval miss, an application miss or a stale memory
	Reason  string `json:"reason,omitempty"`  // one sentence of the judge
}

// sleepRecord is one line of sleep.jsonl: what a sleep pass found in one file of the transcripts branch.
type reflectRecord struct {
	Session     string              `json:"session"`
	File        string              `json:"file"` // the path on the transcripts branch
	Blob        string              `json:"blob"` // the version of the file that the pass read
	Dir         string              `json:"dir,omitempty"`
	Level       string              `json:"level,omitempty"`
	Start       string              `json:"start"` // the time of the first logged start of the session
	Time        string              `json:"time"`  // the time of the pass
	Agent       string              `json:"agent"`
	Model       string              `json:"model,omitempty"`
	Cost        float64             `json:"cost"` // in dollars at API prices
	Corrections []reflectCorrection `json:"corrections"`
}

// where names the place of the session of a record: its level, else its directory, else "outside home".
// A session outside home has no level, and its directory tells the reader where it ran.
func (r reflectRecord) where() string {
	switch {
	case r.Level != "":
		return r.Level
	case r.Dir != "":
		return tilde(r.Dir)
	}
	return "outside home"
}

// sleepUnit is one file of the transcripts branch that a sleep pass judges.
type reflectUnit struct {
	Session string
	File    string
	Blob    string
	Dir     string
	Level   string
	Start   time.Time
}

// sleepPath is the file of the records of sleep passes, in the local state.
func reflectPath(repo string) string {
	return filepath.Join(store.StateDir(repo), "sleep.jsonl")
}

// readSleep returns the records of the sleep passes, in the order of the file. A later record of a file of the
// transcripts branch replaces an earlier one, at the place of the later one. A missing file holds no record.
func readReflect(repo string) ([]reflectRecord, error) {
	f, err := os.Open(reflectPath(repo))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the records of sleep: %w", err)
	}
	defer f.Close()
	var all []reflectRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<28)
	for n := 1; sc.Scan(); n++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r reflectRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("read the records of sleep: line %d: %w", n, err)
		}
		all = append(all, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the records of sleep: %w", err)
	}
	newest := map[string]int{}
	for i, r := range all {
		newest[r.File] = i
	}
	var records []reflectRecord
	for i, r := range all {
		if newest[r.File] == i {
			records = append(records, r)
		}
	}
	return records, nil
}

// appendSleep appends one record to the records of the sleep passes.
func appendReflect(repo string, r reflectRecord) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("write a record of sleep: %w", err)
	}
	f, err := os.OpenFile(reflectPath(repo), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("write a record of sleep: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("write a record of sleep: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write a record of sleep: %w", err)
	}
	return nil
}

// judged is one correction with the record that holds it.
type judged struct {
	reflectCorrection
	Record reflectRecord
}

// sleepCorrections returns the corrections of the records, in their order. Two corrections of one session with
// the same time and the same quote count once: a version beside a transcript repeats the start of the session.
func reflectCorrections(records []reflectRecord) []judged {
	seen := map[[3]string]bool{}
	var out []judged
	for _, r := range records {
		for _, c := range r.Corrections {
			key := [3]string{r.Session, c.Time, c.Quote}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, judged{c, r})
		}
	}
	return out
}

// id returns the id of the correction.
func (j judged) id() string {
	return correctionID(j.Record.Session, j.Time, j.Quote)
}

// correctionID returns the id of a correction: the SHA-256 of its session, its time and its quote, as 64
// hexadecimal digits. A zero byte joins the three values. The id holds nothing of a pass, so it stays the same
// when a later pass finds the same words.
func correctionID(session, at, quote string) string {
	sum := sha256.Sum256([]byte(session + "\x00" + at + "\x00" + quote))
	return hex.EncodeToString(sum[:])
}

// shortDigits is the least count of digits of an id that a print shows, and that an answer takes.
const shortDigits = 7

// shortIDs returns the short form of each id: its first shortDigits digits, and more while another id of the
// list shares them.
func shortIDs(ids []string) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		n := min(shortDigits, len(id))
		for ; n < len(id); n++ {
			shared := false
			for _, other := range ids {
				if other != id && strings.HasPrefix(other, id[:n]) {
					shared = true
					break
				}
			}
			if !shared {
				break
			}
		}
		out[id] = id[:n]
	}
	return out
}

// correctionIDs returns the ids of corrections, in their order.
func correctionIDs(corrections []judged) []string {
	ids := make([]string, len(corrections))
	for i, c := range corrections {
		ids[i] = c.id()
	}
	return ids
}

// sleepAnswer is one line of answers.jsonl: what happened to one correction.
type reflectAnswer struct {
	ID       string `json:"id"`               // the whole id of the correction
	Session  string `json:"session"`          // the session of the correction
	Time     string `json:"time"`             // the time of the correction
	Path     string `json:"path,omitempty"`   // the document that now holds the rule
	Reason   string `json:"reason,omitempty"` // why, also for no change and for a removed document
	Answered string `json:"answered"`         // the time of the answer
}

// answersPath is the file of the answers, in the local state.
func answersPath(repo string) string {
	return filepath.Join(store.StateDir(repo), "answers.jsonl")
}

// readAnswers returns the answers by the id of their correction. The newest answer of an id replaces the older
// ones. A missing file holds no answer.
func readAnswers(repo string) (map[string]reflectAnswer, error) {
	answers := map[string]reflectAnswer{}
	f, err := os.Open(answersPath(repo))
	if errors.Is(err, fs.ErrNotExist) {
		return answers, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the answers of sleep: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<28)
	for n := 1; sc.Scan(); n++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var a reflectAnswer
		if err := json.Unmarshal(sc.Bytes(), &a); err != nil {
			return nil, fmt.Errorf("read the answers of sleep: line %d: %w", n, err)
		}
		answers[a.ID] = a
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the answers of sleep: %w", err)
	}
	return answers, nil
}

// appendAnswers appends answers to the file of the answers, in one write.
func appendAnswers(repo string, answers []reflectAnswer) error {
	var lines []byte
	for _, a := range answers {
		line, err := json.Marshal(a)
		if err != nil {
			return fmt.Errorf("write an answer of sleep: %w", err)
		}
		lines = append(append(lines, line...), '\n')
	}
	f, err := os.OpenFile(answersPath(repo), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("write an answer of sleep: %w", err)
	}
	if _, err := f.Write(lines); err != nil {
		f.Close()
		return fmt.Errorf("write an answer of sleep: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write an answer of sleep: %w", err)
	}
	return nil
}

// sessionOf returns the session of a path on the transcripts branch, and false for any file that is no transcript
// of a main session. Such a transcript is claude/<project>/<session id>.jsonl, or a version beside it,
// claude/<project>/<session id>.<digits of its blob>.jsonl. A session id holds no dot.
func sessionOf(p string) (string, bool) {
	parts := strings.Split(p, "/")
	if len(parts) != 3 || parts[0] != "claude" || !strings.HasSuffix(parts[2], ".jsonl") {
		return "", false
	}
	id, _, _ := strings.Cut(strings.TrimSuffix(parts[2], ".jsonl"), ".")
	return id, id != ""
}

// sleepSessions returns the files of the transcripts branch that a sleep pass judges now, by the oldest start of
// their sessions first. A file counts when all of these hold:
//   - It is the transcript of a main session, or a version beside it.
//   - The use log holds a start of that session, so memcore knows what the session saw.
//   - No record holds the blob of the file.
//   - Its last line is older than settled.
func reflectSessions(repo string, events []Event, records []reflectRecord, now time.Time) ([]reflectUnit, error) {
	ref, ok := revParse(repo, transcriptsRef)
	if !ok {
		return nil, nil
	}
	starts := map[string]Event{} // the first logged start of each session
	for _, e := range events {
		if e.Command != "hook" || e.Hook != "session-start" || e.Session == "" {
			continue
		}
		if first, ok := starts[e.Session]; !ok || eventTime(e).Before(eventTime(first)) {
			starts[e.Session] = e
		}
	}
	done := map[string]bool{}
	for _, r := range records {
		done[r.Blob] = true
	}
	tree, err := store.Git(repo, "ls-tree", "-r", "-z", ref)
	if err != nil {
		return nil, fmt.Errorf("read the transcripts branch: %w", err)
	}
	var units []reflectUnit
	for _, entry := range strings.Split(strings.TrimRight(tree, "\x00"), "\x00") {
		meta, p, ok := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			continue
		}
		session, ok := sessionOf(p)
		if !ok || done[f[2]] {
			continue
		}
		start, ok := starts[session]
		if !ok {
			continue
		}
		content, err := store.Git(repo, "cat-file", "blob", f[2])
		if err != nil {
			return nil, fmt.Errorf("read %s on the transcripts branch: %w", p, err)
		}
		if last, ok := lastLineTime(content); !ok || now.Sub(last) < settled {
			continue
		}
		units = append(units, reflectUnit{Session: session, File: p, Blob: f[2], Dir: start.Dir, Level: start.Level, Start: eventTime(start)})
	}
	sort.SliceStable(units, func(i, j int) bool {
		if !units[i].Start.Equal(units[j].Start) {
			return units[i].Start.Before(units[j].Start)
		}
		// The files of one session stay together.
		if units[i].Session != units[j].Session {
			return units[i].Session < units[j].Session
		}
		return units[i].File < units[j].File
	})
	return units, nil
}

// lastLineTime returns the time of the last line of a trimmed transcript that has one, and false when no line has.
func lastLineTime(content string) (time.Time, bool) {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var rec struct {
			Time string `json:"time"`
		}
		if json.Unmarshal([]byte(lines[i]), &rec) != nil || rec.Time == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, rec.Time); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// The verdicts of a sleep pass about one correction.
const (
	verdictCapture     = "capture"     // no memory held the fact
	verdictRetrieval   = "retrieval"   // a memory held the fact, and the session did not read it
	verdictApplication = "application" // the session read the memory, and acted against it
	verdictStale       = "stale"       // a memory says otherwise
	verdictUnrelated   = "unrelated"   // the correction concerns no memory
	verdictBefore      = "before"      // memcore saw the session only after the correction, so no pass judges it
)

// seenCommit returns the commit of the memory repo that a session saw at a time: the commit of its newest
// event at or before that time. It returns "" when the use log holds no such event with a commit. Then memcore
// saw the session only after that time, and no later commit says what memory held at the time.
func seenCommit(events []Event, session string, at time.Time) string {
	var before *Event
	for i := range events {
		e := &events[i]
		if e.Session != session || e.Commit == "" {
			continue
		}
		if t := eventTime(*e); !t.After(at) && (before == nil || !t.Before(eventTime(*before))) {
			before = e
		}
	}
	if before == nil {
		return ""
	}
	return before.Commit
}

// seenMemories returns the memories on the path of a level at a commit: those of the level and of each level
// above it, up to the root. The level is as the use log holds it, ~ or ~/<level>.
func seenMemories(repo, commit, level string) ([]thinking.Memory, error) {
	rel := strings.TrimPrefix(strings.TrimPrefix(level, "~"), "/")
	var memories []thinking.Memory
	for _, l := range store.Path(rel) {
		folder := path.Join(store.Folder(l), "memories")
		tree, err := store.Git(repo, "ls-tree", "-r", "-z", commit, "--", folder)
		if err != nil {
			return nil, fmt.Errorf("list the memories of %s at %s: %w", title(l), short(commit), err)
		}
		for _, entry := range strings.Split(strings.TrimRight(tree, "\x00"), "\x00") {
			meta, p, ok := strings.Cut(entry, "\t")
			f := strings.Fields(meta)
			if !ok || len(f) != 3 || f[1] != "blob" || !store.IsMemory(p) {
				continue
			}
			text, err := store.Git(repo, "cat-file", "blob", f[2])
			if err != nil {
				return nil, fmt.Errorf("read %s at %s: %w", p, short(commit), err)
			}
			m := thinking.Memory{Path: p, Body: text}
			if fm, body, err := store.ParseFrontmatter(strings.NewReader(text)); err == nil {
				m.Description, m.Body = fm.Values["description"], strings.TrimSpace(body)
			}
			memories = append(memories, m)
		}
	}
	return memories, nil
}

// verdicts turns the findings of the judge into verdicts, one for each correction. The use log decides between
// a retrieval miss and an application miss: the session read the memory at or before the time of the correction,
// or it did not.
func verdicts(corrections []thinking.Correction, findings []thinking.Finding, events []Event, session string) []reflectCorrection {
	out := make([]reflectCorrection, len(corrections))
	for i, c := range corrections {
		f := findings[i]
		v := reflectCorrection{Time: c.Time, Quote: c.Quote, Summary: c.Summary, Reason: f.Reason}
		switch f.Kind {
		case thinking.None:
			v.Verdict = verdictCapture
		case thinking.Contradicts:
			v.Verdict, v.Path = verdictStale, f.Path
		case thinking.Holds:
			v.Verdict, v.Path = verdictRetrieval, f.Path
			if at, err := time.Parse(time.RFC3339, c.Time); err == nil && readBefore(events, session, f.Path, at) {
				v.Verdict = verdictApplication
			}
		default:
			v.Verdict = verdictUnrelated
		}
		out[i] = v
	}
	return out
}

// readBefore reports whether a session read a document at or before a time.
func readBefore(events []Event, session, p string, at time.Time) bool {
	for _, e := range events {
		if e.Session == session && !eventTime(e).After(at) && slices.Contains(readsOf(e), p) {
			return true
		}
	}
	return false
}
