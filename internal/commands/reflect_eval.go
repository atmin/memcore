package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/thinking"
)

// ReflectEval is the input of the check that compares a local model with the records of the reflect passes.
// Its stage measure calls no model, and measures how much code could take over from the model of a pass.
// scripts/reflect-eval runs it by hand, and no command of memcore calls it.
type ReflectEval struct {
	Endpoint string // the base URL of the local model server, on this machine
	Model    string // the model that the server runs
	Context  int    // the context of the model, in tokens
	Out      string // the folder of results.jsonl
	Sessions int    // the most records that the check reads, in the order of the file; 0 for all
	Stage    string // extract, judge, measure, or "" for extract and judge
}

// evalReserve is the part of the context, in tokens, that the check keeps for the prompt and the answer.
// evalBytesPerToken is its estimate of the size of a token.
const (
	evalReserve       = 8192
	evalBytesPerToken = 3
)

// The outcomes of a record in the extract part, and of a group in the judge part.
const (
	evalOK     = "ok"
	evalFailed = "failed"
	evalOver   = "over the context"
)

// evalResult is one line of results.jsonl: what the check found for one record.
type evalResult struct {
	Session string       `json:"session"`
	File    string       `json:"file"`
	Extract *evalExtract `json:"extract,omitempty"`
	Judge   []evalGroup  `json:"judge,omitempty"`
}

// evalExtract is the extract part of one record. A line is one time of a line of the person. Unmatched holds the
// corrections of the local model whose quote matches no line of the person, which the extract drops.
type evalExtract struct {
	Outcome   string          `json:"outcome"`
	Error     string          `json:"error,omitempty"`
	Found     []evalLine      `json:"found,omitempty"`
	Missed    []evalLine      `json:"missed,omitempty"`
	Extra     []evalLine      `json:"extra,omitempty"`
	Unmatched []evalUnmatched `json:"unmatched,omitempty"`
}

// evalLine is one line of the person, with the corrections that the record and the local model give for it.
type evalLine struct {
	Time     string      `json:"time"`
	Recorded []evalQuote `json:"recorded,omitempty"`
	Local    []evalQuote `json:"local,omitempty"`
}

// evalQuote is one correction of a line.
type evalQuote struct {
	Quote   string `json:"quote"`
	Summary string `json:"summary"`
}

// evalUnmatched is one correction of the local model whose quote matches no line of the person, with the time
// and the quote as the model gave them.
type evalUnmatched struct {
	Time    string `json:"time"`
	Quote   string `json:"quote"`
	Summary string `json:"summary"`
}

// evalGroup is the judge part of the corrections of one record that share a seen commit.
type evalGroup struct {
	Commit  string     `json:"commit"`
	Outcome string     `json:"outcome"`
	Error   string     `json:"error,omitempty"`
	Pairs   []evalPair `json:"pairs,omitempty"`
}

// evalPair is the recorded verdict of a correction, and the verdict of the local model.
type evalPair struct {
	Time     string `json:"time"`
	Quote    string `json:"quote"`
	Recorded string `json:"recorded"`
	Local    string `json:"local"`
	Path     string `json:"path,omitempty"`   // the memory of the local verdict
	Reason   string `json:"reason,omitempty"` // the reason of the local model
}

// evalCounts are the totals of the report.
type evalCounts struct {
	records, extractFailed, extractOver              int
	lines, found, documented, documentedFound, extra int
	unmatched                                        int
	groups, judgeFailed, judgeOver                   int
	judged, same                                     int
	memory, memorySame                               int            // the judged corrections whose recorded verdict names a memory
	recorded                                         map[string]int // the judged corrections by recorded verdict
	pairs                                            map[string]int // the pairs that differ, as "<recorded> as <local>"
	calls                                            int
	took                                             time.Duration
	in, out, inBytes                                 int
}

// RunReflectEval runs the check: it runs extract and judge with a local model over the transcripts that reflect
// passes recorded, compares the results with the records, prints a report to out and writes results.jsonl in
// e.Out. It reads the memory repo and its local state, and writes nothing there. A call that fails, or an input
// over the context, counts in the report and stops nothing. The stage measure runs runMeasure instead.
func RunReflectEval(e ReflectEval, out io.Writer) error {
	if err := checkEval(e); err != nil {
		return err
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	results, err := createResults(repo, e.Out)
	if err != nil {
		return err
	}
	defer results.Close()
	records, err := readReflect(repo)
	if err != nil {
		return err
	}
	answers, err := readAnswers(repo)
	if err != nil {
		return err
	}
	events, _, err := readLog(repo)
	if err != nil {
		return err
	}
	if e.Sessions > 0 && len(records) > e.Sessions {
		records = records[:e.Sessions]
	}
	if e.Stage == stageMeasure {
		return runMeasure(repo, records, events, results, out)
	}

	c := evalCounts{recorded: map[string]int{}, pairs: map[string]int{}}
	prev := thinking.Run
	defer func() { thinking.Run = prev }()
	thinking.Run = func(req thinking.Request) (thinking.Reply, error) {
		start := clock()
		reply, err := prev(req)
		c.calls++
		c.took += clock().Sub(start)
		if reply.In > 0 {
			c.in += reply.In
			c.inBytes += len(req.Prompt) + len(req.Input)
		}
		c.out += reply.Out
		return reply, err
	}

	limit := (e.Context - evalReserve) * evalBytesPerToken
	for _, r := range records {
		res := evalResult{Session: r.Session, File: r.File}
		if e.Stage != "judge" {
			res.Extract = evalExtractPart(e, repo, r, answers, limit, &c)
		}
		if e.Stage != "extract" {
			res.Judge = evalJudgePart(e, repo, r, events, limit, &c)
		}
		line, err := json.Marshal(res)
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
	printEval(out, e, c)
	return nil
}

// checkEval refuses an input that the check cannot use. The transcripts stay on the machine, so the host of the
// endpoint must name the machine itself. The stage measure calls no model, so it needs only --out, and it ignores
// --endpoint, --model and --context.
func checkEval(e ReflectEval) error {
	if e.Stage == stageMeasure {
		if e.Out == "" {
			return errors.New("--stage measure needs --out")
		}
		if e.Sessions < 0 {
			return fmt.Errorf("--sessions: %d is no count of records: give 0 for all, or more", e.Sessions)
		}
		return nil
	}
	if e.Endpoint == "" || e.Model == "" || e.Out == "" {
		return errors.New("the check needs --endpoint, --model and --out")
	}
	u, err := url.Parse(e.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("--endpoint: %q is no URL with http or https and a host, such as http://127.0.0.1:8080", e.Endpoint)
	}
	switch strings.ToLower(u.Hostname()) {
	case "127.0.0.1", "::1", "localhost":
	default:
		return fmt.Errorf("--endpoint: the host %s is not on this machine, and the transcripts stay on it: give 127.0.0.1, ::1 or localhost", u.Hostname())
	}
	if e.Context <= evalReserve {
		return fmt.Errorf("--context: %d tokens leave no room for an input: give more than %d", e.Context, evalReserve)
	}
	if e.Sessions < 0 {
		return fmt.Errorf("--sessions: %d is no count of records: give 0 for all, or more", e.Sessions)
	}
	if e.Stage != "" && e.Stage != "extract" && e.Stage != "judge" {
		return fmt.Errorf("--stage: %q is no part of the check: give extract, judge or measure, or nothing for extract and judge", e.Stage)
	}
	return nil
}

// createResults makes the folder dir, and creates results.jsonl in it. It refuses a folder that is the memory repo
// or inside it, also through a symlink, and checks so before it makes the folder. It replaces no file and follows
// no link, so a second check needs a new folder.
func createResults(repo, dir string) (*os.File, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("--out: %w", err)
	}
	// The deepest folder of the path that exists decides where the rest goes.
	existing := abs
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	if err := outsideRepo(repo, existing, dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("--out: make %s: %w", dir, err)
	}
	if err := outsideRepo(repo, abs, dir); err != nil {
		return nil, err
	}
	p := filepath.Join(abs, "results.jsonl")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("--out: %s already holds results.jsonl: give a new folder for each check", dir)
	}
	if err != nil {
		return nil, fmt.Errorf("--out: create %s: %w", p, err)
	}
	return f, nil
}

// outsideRepo refuses a folder that is the memory repo or inside it, or inside its local state, which can be a
// symlink to a folder elsewhere. It resolves each with its symlinks, and compares each folder above the path with
// them as files, so a name in another case finds them too.
func outsideRepo(repo, folder, given string) error {
	resolved, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return fmt.Errorf("--out: resolve %s: %w", given, err)
	}
	places := []struct{ dir, name string }{{repo, "the memory repo"}, {store.StateDir(repo), "the local state of the memory repo"}}
	var infos []fs.FileInfo
	for _, place := range places {
		resolvedPlace, err := filepath.EvalSymlinks(place.dir)
		if errors.Is(err, fs.ErrNotExist) {
			infos = append(infos, nil) // no local state yet
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve %s: %w", place.name, err)
		}
		info, err := os.Stat(resolvedPlace)
		if err != nil {
			return fmt.Errorf("read %s: %w", place.name, err)
		}
		infos = append(infos, info)
	}
	for p := resolved; ; p = filepath.Dir(p) {
		if info, err := os.Stat(p); err == nil {
			for i, place := range places {
				if infos[i] != nil && os.SameFile(info, infos[i]) {
					return fmt.Errorf("--out: %s is in %s, and the check writes nothing there: give a folder outside it", given, place.name)
				}
			}
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

// evalExtractPart runs the extract of the local model over the transcript of one record, and compares the lines
// of the person that it finds with those of the record. It also counts and keeps each correction whose quote matches
// no line, also those of the parts before a call that fails.
func evalExtractPart(e ReflectEval, repo string, r reflectRecord, answers map[string]reflectAnswer, limit int, c *evalCounts) *evalExtract {
	x := &evalExtract{Outcome: evalOK}
	transcript, err := store.Git(repo, "cat-file", "blob", r.Blob)
	if err != nil {
		c.extractFailed++
		x.Outcome, x.Error = evalFailed, fmt.Sprintf("read %s on the transcripts branch: %v", r.File, err)
		return x
	}
	// The extract gives a line over the size of a part a part of its own, so such a line is over the context.
	header, rest, _ := strings.Cut(transcript, "\n")
	for _, l := range strings.SplitAfter(rest, "\n") {
		if len(header)+1+len(l) > limit {
			c.extractOver++
			x.Outcome = evalOver
			return x
		}
	}
	found, _, err := thinking.Extract(thinking.ExtractRequest{Transcript: transcript, Model: e.Model, Endpoint: e.Endpoint, PartBytes: limit,
		Unmatched: func(u thinking.Correction) {
			c.unmatched++
			x.Unmatched = append(x.Unmatched, evalUnmatched{u.Time, u.Quote, u.Summary})
		}})
	if err != nil {
		c.extractFailed++
		x.Outcome, x.Error = evalFailed, err.Error()
		return x
	}
	recorded := map[string][]evalQuote{}
	documented := map[string]bool{}
	for _, rc := range r.Corrections {
		recorded[rc.Time] = append(recorded[rc.Time], evalQuote{rc.Quote, rc.Summary})
		if answers[correctionID(r.Session, rc.Time, rc.Quote)].Path != "" {
			documented[rc.Time] = true
		}
	}
	local := map[string][]evalQuote{}
	for _, f := range found {
		local[f.Time] = append(local[f.Time], evalQuote{f.Quote, f.Summary})
	}
	for _, t := range sortedKeys(recorded) {
		c.lines++
		line := evalLine{Time: t, Recorded: recorded[t], Local: local[t]}
		if documented[t] {
			c.documented++
		}
		if _, ok := local[t]; ok {
			c.found++
			if documented[t] {
				c.documentedFound++
			}
			x.Found = append(x.Found, line)
		} else {
			x.Missed = append(x.Missed, line)
		}
	}
	for _, t := range sortedKeys(local) {
		if _, ok := recorded[t]; !ok {
			c.extra++
			x.Extra = append(x.Extra, evalLine{Time: t, Local: local[t]})
		}
	}
	return x
}

// evalJudgePart runs the judge of the local model over the recorded corrections of one record, grouped as a pass
// groups them, and compares each verdict with the recorded one. A correction from before memcore saw its session
// has no judge. A group with no seen commit counts as failed.
func evalJudgePart(e ReflectEval, repo string, r reflectRecord, events []Event, limit int, c *evalCounts) []evalGroup {
	var kept []reflectCorrection
	var corrections []thinking.Correction
	for _, rc := range r.Corrections {
		if rc.Verdict != verdictBefore {
			kept = append(kept, rc)
			corrections = append(corrections, thinking.Correction{Time: rc.Time, Quote: rc.Quote, Summary: rc.Summary})
		}
	}
	commits, groups := groupBySeen(events, r.Session, corrections)
	var out []evalGroup
	for _, commit := range commits {
		c.groups++
		g := evalGroup{Commit: commit, Outcome: evalOK}
		if commit == "" {
			// A pass judged these corrections, so the use log held an event of the session before them then.
			c.judgeFailed++
			g.Outcome, g.Error = evalFailed, "the use log holds no event of the session with a commit at or before these corrections, so no commit says what memory held then"
			out = append(out, g)
			continue
		}
		var group []thinking.Correction
		for _, i := range groups[commit] {
			group = append(group, corrections[i])
		}
		memories, err := seenMemories(repo, commit, r.Level)
		if err != nil {
			c.judgeFailed++
			g.Outcome, g.Error = evalFailed, err.Error()
			out = append(out, g)
			continue
		}
		if size, err := judgeInputSize(group, memories); err != nil || size > limit {
			c.judgeOver++
			g.Outcome = evalOver
			out = append(out, g)
			continue
		}
		findings, _, err := thinking.Judge(thinking.JudgeRequest{Corrections: group, Memories: memories, Model: e.Model, Endpoint: e.Endpoint})
		if err != nil {
			c.judgeFailed++
			g.Outcome, g.Error = evalFailed, err.Error()
			out = append(out, g)
			continue
		}
		for k, v := range verdicts(group, findings, events, r.Session) {
			was := kept[groups[commit][k]]
			c.judged++
			c.recorded[was.Verdict]++
			sameVerdict := was.Verdict == v.Verdict
			if sameVerdict {
				c.same++
			} else {
				c.pairs[was.Verdict+" as "+v.Verdict]++
			}
			switch was.Verdict {
			case verdictRetrieval, verdictApplication, verdictStale:
				c.memory++
				if sameVerdict {
					c.memorySame++
				}
			}
			g.Pairs = append(g.Pairs, evalPair{Time: v.Time, Quote: v.Quote, Recorded: was.Verdict, Local: v.Verdict, Path: v.Path, Reason: v.Reason})
		}
		out = append(out, g)
	}
	return out
}

// judgeInputSize returns the size of the input that the judge gets for corrections and memories, in bytes.
// It writes the JSON as thinking.Judge writes it.
func judgeInputSize(corrections []thinking.Correction, memories []thinking.Memory) (int, error) {
	type item struct {
		Index   int    `json:"index"`
		Quote   string `json:"quote"`
		Summary string `json:"summary"`
	}
	in := struct {
		Corrections []item            `json:"corrections"`
		Memories    []thinking.Memory `json:"memories"`
	}{Memories: memories}
	if in.Memories == nil {
		in.Memories = []thinking.Memory{}
	}
	for i, c := range corrections {
		in.Corrections = append(in.Corrections, item{i, c.Quote, c.Summary})
	}
	data, err := json.Marshal(in)
	if err != nil {
		return 0, fmt.Errorf("write the input of the judge: %w", err)
	}
	return len(data), nil
}

// sortedKeys returns the keys of a map in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// printEval prints the report of the check. With a stage, it leaves out the lines of the other part. The judge part
// prints the share of same verdicts among the corrections whose recorded verdict names a memory, and the score of a
// judge that always gives the most frequent recorded verdict, with equal counts to the first name in alphabetical
// order. A part of no whole prints no percentage, and with no judged correction the report names no verdict.
func printEval(w io.Writer, e ReflectEval, c evalCounts) {
	fmt.Fprintf(w, "Model %s at %s, with a context of %d tokens.\n\n", e.Model, e.Endpoint, e.Context)
	if e.Stage != "judge" {
		fmt.Fprintf(w, "Extract: %s, %d failed, %d over the context.\n", plural(c.records, "record"), c.extractFailed, c.extractOver)
		fmt.Fprintf(w, "  Recorded lines: %d. Found: %d%s. Missed: %d.\n", c.lines, c.found, percent(c.found, c.lines), c.lines-c.found)
		fmt.Fprintf(w, "  With a document: %d of %d found%s.\n", c.documentedFound, c.documented, percent(c.documentedFound, c.documented))
		fmt.Fprintf(w, "  Extra lines: %d.\n", c.extra)
		fmt.Fprintf(w, "  Quotes that match no line: %d.\n", c.unmatched)
	}
	if e.Stage != "extract" {
		fmt.Fprintf(w, "Judge: %s, %d failed, %d over the context.\n", plural(c.groups, "group"), c.judgeFailed, c.judgeOver)
		fmt.Fprintf(w, "  Corrections judged: %d. Same verdict: %d%s.\n", c.judged, c.same, percent(c.same, c.judged))
		fmt.Fprintf(w, "  With a memory: %d of %d same%s.\n", c.memorySame, c.memory, percent(c.memorySame, c.memory))
		if c.judged > 0 {
			top := ""
			for _, v := range sortedKeys(c.recorded) {
				if c.recorded[v] > c.recorded[top] {
					top = v
				}
			}
			fmt.Fprintf(w, "  The most frequent recorded verdict, %s: %d of %d%s.\n", top, c.recorded[top], c.judged, percent(c.recorded[top], c.judged))
		}
		pairs := sortedKeys(c.pairs)
		sort.SliceStable(pairs, func(i, j int) bool { return c.pairs[pairs[i]] > c.pairs[pairs[j]] })
		for _, p := range pairs {
			fmt.Fprintf(w, "  %s: %d\n", p, c.pairs[p])
		}
	}
	tokens := fmt.Sprintf("Tokens: %d in, %d out", c.in, c.out)
	if c.in > 0 {
		tokens += fmt.Sprintf(", at %.1f bytes for a token", float64(c.inBytes)/float64(c.in))
	}
	fmt.Fprintf(w, "Calls: %d in %s. %s.\n", c.calls, took(c.took), tokens)
}

// percent returns " (n%)" for a part of a whole, with no decimals, and "" for a whole of 0.
func percent(part, whole int) string {
	if whole == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d%%)", int(math.Round(100*float64(part)/float64(whole))))
}

// took prints a duration as hours and minutes, or under an hour as minutes and seconds.
func took(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
}
