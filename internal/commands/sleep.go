package commands

import (
	"bufio"
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
type sleepCorrection struct {
	Time    string `json:"time"`              // the time of the line that holds the quote
	Quote   string `json:"quote"`             // the words of the person, as the transcript spells them
	Summary string `json:"summary"`           // the rule that the correction states
	Verdict string `json:"verdict,omitempty"` // capture, retrieval, application, stale or unrelated
	Path    string `json:"path,omitempty"`    // the memory of a retrieval miss, an application miss or a stale memory
	Reason  string `json:"reason,omitempty"`  // one sentence of the judge
}

// sleepRecord is one line of sleep.jsonl: what a sleep pass found in one file of the transcripts branch.
type sleepRecord struct {
	Session     string            `json:"session"`
	File        string            `json:"file"` // the path on the transcripts branch
	Blob        string            `json:"blob"` // the version of the file that the pass read
	Dir         string            `json:"dir,omitempty"`
	Level       string            `json:"level,omitempty"`
	Start       string            `json:"start"` // the time of the first logged start of the session
	Time        string            `json:"time"`  // the time of the pass
	Agent       string            `json:"agent"`
	Model       string            `json:"model,omitempty"`
	Cost        float64           `json:"cost"` // in dollars at API prices
	Corrections []sleepCorrection `json:"corrections"`
}

// sleepUnit is one file of the transcripts branch that a sleep pass judges.
type sleepUnit struct {
	Session string
	File    string
	Blob    string
	Dir     string
	Level   string
	Start   time.Time
}

// sleepPath is the file of the records of sleep passes, in the local state.
func sleepPath(repo string) string {
	return filepath.Join(store.StateDir(repo), "sleep.jsonl")
}

// readSleep returns the records of the sleep passes, in the order of the file. A later record of a file of the
// transcripts branch replaces an earlier one, at the place of the later one. A missing file holds no record.
func readSleep(repo string) ([]sleepRecord, error) {
	f, err := os.Open(sleepPath(repo))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the records of sleep: %w", err)
	}
	defer f.Close()
	var all []sleepRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<28)
	for n := 1; sc.Scan(); n++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r sleepRecord
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
	var records []sleepRecord
	for i, r := range all {
		if newest[r.File] == i {
			records = append(records, r)
		}
	}
	return records, nil
}

// appendSleep appends one record to the records of the sleep passes.
func appendSleep(repo string, r sleepRecord) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("write a record of sleep: %w", err)
	}
	f, err := os.OpenFile(sleepPath(repo), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
	sleepCorrection
	Record sleepRecord
}

// sleepCorrections returns the corrections of the records, in their order. Two corrections of one session with
// the same time and the same quote count once: a version beside a transcript repeats the start of the session.
func sleepCorrections(records []sleepRecord) []judged {
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
func sleepSessions(repo string, events []Event, records []sleepRecord, now time.Time) ([]sleepUnit, error) {
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
	var units []sleepUnit
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
		units = append(units, sleepUnit{Session: session, File: p, Blob: f[2], Dir: start.Dir, Level: start.Level, Start: eventTime(start)})
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
)

// seenCommit returns the commit of the memory repo that a session saw at a time: the commit of its newest
// event at or before that time. When no event is that old, it returns the commit of the first logged start of
// the session, or of its oldest event when the log holds no start. It returns "" for a session with no commit
// in the use log.
func seenCommit(events []Event, session string, at time.Time) string {
	var before, start, oldest *Event
	for i := range events {
		e := &events[i]
		if e.Session != session || e.Commit == "" {
			continue
		}
		t := eventTime(*e)
		if oldest == nil || t.Before(eventTime(*oldest)) {
			oldest = e
		}
		if e.Command == "hook" && e.Hook == "session-start" && (start == nil || t.Before(eventTime(*start))) {
			start = e
		}
		if !t.After(at) && (before == nil || !t.Before(eventTime(*before))) {
			before = e
		}
	}
	for _, e := range []*Event{before, start, oldest} {
		if e != nil {
			return e.Commit
		}
	}
	return ""
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
func verdicts(corrections []thinking.Correction, findings []thinking.Finding, events []Event, session string) []sleepCorrection {
	out := make([]sleepCorrection, len(corrections))
	for i, c := range corrections {
		f := findings[i]
		v := sleepCorrection{Time: c.Time, Quote: c.Quote, Summary: c.Summary, Reason: f.Reason}
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
