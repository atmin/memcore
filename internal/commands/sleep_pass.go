package commands

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
	"github.com/atmin/memcore/internal/thinking"
)

// The settings of a sleep pass, in the git configuration of the memory repo, and their first values.
// Real passes will show whether these values fit, so each one lives only here.
const (
	sleepBudgetKey = "memcore.sleepbudget" // the budget of a pass, in dollars at API prices
	sleepAgentKey  = "memcore.sleepagent"  // the agent that thinks
	sleepModelKey  = "memcore.sleepmodel"  // the model of that agent

	sleepBudget = 2.0
	sleepAgent  = "claude"
	sleepModel  = "sonnet"

	// sleepLock is the file of the local state that a running pass locks.
	sleepLock = "sleep.lock"
)

// sleepAgents are the agents that a sleep pass can run.
var sleepAgents = []string{"claude"}

// setting reads a key of the git configuration of the memory repo, and returns false when it is unset.
func setting(repo, key string) (string, bool, error) {
	out, err := store.Git(repo, "config", "--get", key)
	if err != nil {
		// Only the exit status 1 of git means that the key is unset.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read the setting %s: %w", key, err)
	}
	return strings.TrimSpace(out), true, nil
}

// sleepSettings returns the budget, the agent and the model of a pass. A budget of the command line wins over
// the setting.
func sleepSettings(repo, budgetFlag string) (budget float64, agent, model string, err error) {
	budget, agent, model = sleepBudget, sleepAgent, sleepModel
	text, from := budgetFlag, "--budget"
	if text == "" {
		if value, ok, err := setting(repo, sleepBudgetKey); err != nil {
			return 0, "", "", err
		} else if ok {
			text, from = value, "the setting "+sleepBudgetKey
		}
	}
	if text != "" {
		if budget, err = strconv.ParseFloat(text, 64); err != nil || !(budget > 0) || math.IsInf(budget, 0) {
			return 0, "", "", inputErrf("%s: the value %q is no amount of dollars above 0, such as 2 or 0.50", from, text)
		}
	}
	if value, ok, err := setting(repo, sleepAgentKey); err != nil {
		return 0, "", "", err
	} else if ok {
		agent = value
	}
	known := false
	for _, a := range sleepAgents {
		known = known || a == agent
	}
	if !known {
		return 0, "", "", fmt.Errorf("the setting %s names the agent %q, which memcore cannot run: the known agents are %s", sleepAgentKey, agent, strings.Join(sleepAgents, ", "))
	}
	if value, ok, err := setting(repo, sleepModelKey); err != nil {
		return 0, "", "", err
	} else if ok {
		model = value
	}
	return budget, agent, model, nil
}

// runSleep runs one sleep pass: it extracts the corrections of each new transcript, and judges each one.
// A pass proposes no change, commits nothing and writes only the records of the local state.
func runSleep(env Env, args []string) error {
	flags := flag.NewFlagSet("sleep", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	budgetFlag := flags.String("budget", "", "the budget of the pass, in dollars at API prices")
	sessionsFlag := flags.String("sessions", "", "the most sessions that the pass judges")
	list := flags.Bool("list", false, "list the corrections of the records, and run no pass")
	answerFlag := flags.String("answer", "", "record what happened to these corrections, and run no pass")
	withFlag := flags.String("with", "", "the document that now holds the rule of an answered correction")
	reasonFlag := flags.String("reason", "", "the reason of an answer")
	pos, err := parseInterleaved(flags, args)
	if err != nil {
		return usageErr(err.Error())
	}
	if len(pos) != 0 {
		return usageErr("usage: memcore sleep [--budget <dollars>] [--sessions <n>] | --list | --answer <id>[,<id>] [--with <path>] [--reason <text>]")
	}
	given := map[string]bool{} // also a flag with an empty value, as in --budget=
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	// An answer and a list run no agent and no pass, so an agent may run them too.
	if given["answer"] {
		if given["budget"] || given["sessions"] || given["list"] {
			return usageErr("usage: memcore sleep --answer takes only --with and --reason, because it runs no pass and prints no list")
		}
		return answerSleep(env, *answerFlag, *withFlag, *reasonFlag)
	}
	if given["with"] || given["reason"] {
		return usageErr("usage: memcore sleep --with and --reason belong to --answer <id>[,<id>]")
	}
	if *list {
		if given["budget"] || given["sessions"] {
			return usageErr("usage: memcore sleep --list takes no --budget and no --sessions, because it runs no pass")
		}
		return listSleep(env)
	}
	limit := 0 // no limit
	if *sessionsFlag != "" {
		if limit, err = strconv.Atoi(*sessionsFlag); err != nil || limit < 1 {
			return inputErrf("--sessions: the value %q is no count of sessions, 1 or more", *sessionsFlag)
		}
	}
	// A pass spends the limits of the plan of the person, and sends redacted transcripts to the provider.
	if err := guards.Agent(); err != nil {
		return fmt.Errorf("only the person runs a sleep pass, at a terminal: %w", err)
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	budget, agent, model, err := sleepSettings(repo, *budgetFlag)
	if err != nil {
		return err
	}
	unlock, ok, err := store.TryLockFile(repo, sleepLock)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("a sleep pass already runs: wait until it ends")
	}
	defer unlock()

	events, _, err := readLog(repo)
	if err != nil {
		return err
	}
	records, err := readSleep(repo)
	if err != nil {
		return err
	}
	units, err := sleepSessions(repo, events, records, time.Now())
	if err != nil {
		return err
	}
	if len(units) == 0 {
		fmt.Fprintln(env.Out, "No session waits for a sleep pass.")
		return nil
	}

	p := pass{env: env, repo: repo, events: events, agent: agent, model: model, budget: budget, judged: map[string]bool{}, seen: map[[3]string]bool{}}
	// A correction that an earlier pass recorded counts in no total of this pass.
	for _, c := range sleepCorrections(records) {
		p.seen[[3]string{c.Record.Session, c.Time, c.Quote}] = true
	}
	sessions := map[string]bool{} // the sessions that the pass started
	next := 0
	var failure error
	stopped := ""
	for ; next < len(units); next++ {
		u := units[next]
		if !sessions[u.Session] && limit > 0 && len(sessions) == limit {
			stopped = fmt.Sprintf("The pass stopped after %s, as --sessions says.", plural(limit, "session"))
			break
		}
		// The budget decides only whether the pass starts one more session. A session that the pass started
		// always ends, so the pass loses no paid work.
		if !sessions[u.Session] && p.cost >= p.budget {
			stopped = fmt.Sprintf("The pass stopped at its budget of %s dollars.", dollars(p.budget))
			break
		}
		if !sessions[u.Session] {
			p.flush()
		}
		sessions[u.Session] = true
		if err := p.judge(u); err != nil {
			failure = err
			break
		}
	}
	p.flush()
	waiting := map[string]bool{}
	for _, u := range units[next:] {
		waiting[u.Session] = true
	}
	fmt.Fprintf(env.Out, "Judged %s with %s: %s.\n", plural(len(p.judged), "session"), plural(p.corrections, "correction"), p.counts.text())
	fmt.Fprintf(env.Out, "The pass cost about %s dollars at API prices. %s.\n", dollars(p.cost), waits(len(waiting)))
	if stopped != "" {
		fmt.Fprintln(env.Out, stopped)
	}
	if failure != nil {
		return fmt.Errorf("the sleep pass stopped, and it keeps the sessions that it finished: %w", failure)
	}
	return nil
}

// pass is the state of one running sleep pass.
type pass struct {
	env    Env
	repo   string
	events []Event
	agent  string
	model  string
	budget float64

	cost        float64            // of every call, also of a session that failed; the pass compares it with the budget only before a new session
	judged      map[string]bool    // the sessions with a record of this pass
	seen        map[[3]string]bool // the corrections that count already, by session, time and quote
	corrections int
	counts      verdictCounts

	line        string // the start of the line of the session that the pass works on
	lineCount   int    // its new corrections
	lineCounts  verdictCounts
	lineWritten bool // a record of the session exists, so the line prints
}

// flush prints the line of the session that the pass worked on: one line for all its files.
func (p *pass) flush() {
	if p.lineWritten {
		text := fmt.Sprintf("%s: %s", p.line, plural(p.lineCount, "correction"))
		if p.lineCount > 0 {
			text += ": " + p.lineCounts.text()
		}
		fmt.Fprintln(p.env.Out, text+".")
	}
	p.line, p.lineCount, p.lineCounts, p.lineWritten = "", 0, verdictCounts{}, false
}

// judge extracts and judges one file of the transcripts branch, appends its record, and prints its line.
// No call gets a limit of its cost: the size of its input bounds it. On an error it writes no record, so the
// next pass reads the file again.
func (p *pass) judge(u sleepUnit) error {
	transcript, err := store.Git(p.repo, "cat-file", "blob", u.Blob)
	if err != nil {
		return fmt.Errorf("read %s on the transcripts branch: %w", u.File, err)
	}
	found, cost, err := thinking.Extract(thinking.ExtractRequest{Transcript: transcript, Model: p.model})
	p.cost += cost
	unitCost := cost
	if err != nil {
		return err
	}
	// The corrections of one seen commit go to the judge together, with the memories at that commit.
	groups := map[string][]int{}
	var commits []string
	for i, c := range found {
		at, _ := time.Parse(time.RFC3339, c.Time)
		commit := seenCommit(p.events, u.Session, at)
		if _, ok := groups[commit]; !ok {
			commits = append(commits, commit)
		}
		groups[commit] = append(groups[commit], i)
	}
	result := make([]sleepCorrection, len(found))
	for _, commit := range commits {
		if commit == "" {
			// memcore saw the session only after these corrections, so it does not know what memory held then.
			for _, i := range groups[commit] {
				result[i] = sleepCorrection{Time: found[i].Time, Quote: found[i].Quote, Summary: found[i].Summary, Verdict: verdictBefore}
			}
			continue
		}
		memories, err := seenMemories(p.repo, commit, u.Level)
		if err != nil {
			return err
		}
		var group []thinking.Correction
		for _, i := range groups[commit] {
			group = append(group, found[i])
		}
		findings, cost, err := thinking.Judge(thinking.JudgeRequest{Corrections: group, Memories: memories, Model: p.model})
		p.cost += cost
		unitCost += cost
		if err != nil {
			return err
		}
		for k, v := range verdicts(group, findings, p.events, u.Session) {
			result[groups[commit][k]] = v
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Time < result[j].Time })
	record := sleepRecord{Session: u.Session, File: u.File, Blob: u.Blob, Dir: u.Dir, Level: u.Level,
		Start: u.Start.UTC().Format(time.RFC3339), Time: time.Now().UTC().Format(time.RFC3339),
		Agent: p.agent, Model: p.model, Cost: unitCost, Corrections: result}
	if record.Corrections == nil {
		record.Corrections = []sleepCorrection{}
	}
	if err := appendSleep(p.repo, record); err != nil {
		return err
	}
	p.judged[u.Session] = true
	p.line, p.lineWritten = fmt.Sprintf("%s %s %s", u.Start.UTC().Format("2006-01-02"), u.Level, short(u.Session)), true
	// A version beside a transcript repeats the start of its session, so a correction counts once.
	for _, c := range result {
		if key := [3]string{u.Session, c.Time, c.Quote}; !p.seen[key] {
			p.seen[key] = true
			p.corrections++
			p.counts.add(c.Verdict)
			p.lineCount++
			p.lineCounts.add(c.Verdict)
		}
	}
	return nil
}

// verdictCounts counts the verdicts of corrections, by kind.
type verdictCounts struct{ capture, retrieval, application, stale, unrelated, before int }

func (c *verdictCounts) add(verdict string) {
	switch verdict {
	case verdictCapture:
		c.capture++
	case verdictRetrieval:
		c.retrieval++
	case verdictApplication:
		c.application++
	case verdictStale:
		c.stale++
	case verdictBefore:
		c.before++
	default:
		c.unrelated++
	}
}

// text names the misses by kind, then the corrections that concern no memory. It names the corrections from
// before memcore saw their session only when there are some.
func (c verdictCounts) text() string {
	text := fmt.Sprintf("%d capture, %d retrieval, %d application, %d stale, %d unrelated", c.capture, c.retrieval, c.application, c.stale, c.unrelated)
	if c.before > 0 {
		text += fmt.Sprintf(", %d before memory", c.before)
	}
	return text
}

// dollars prints an amount with two decimals.
func dollars(amount float64) string {
	return strconv.FormatFloat(amount, 'f', 2, 64)
}

// waits says how many sessions wait for the next pass.
func waits(n int) string {
	switch n {
	case 0:
		return "No session waits"
	case 1:
		return "1 session waits"
	}
	return fmt.Sprintf("%d sessions wait", n)
}

// verdictNames names each verdict for a reader.
var verdictNames = map[string]string{
	verdictCapture:     "capture miss",
	verdictRetrieval:   "retrieval miss",
	verdictApplication: "application miss",
	verdictStale:       "stale memory",
	verdictUnrelated:   "unrelated to memory",
	verdictBefore:      "before memory",
}

// listSleep prints the corrections of the records, the newest session first, and each session in the order of
// its corrections. Each one starts with its short id, which sleep --answer takes. It runs no agent and writes nothing, so an agent may run it too.
func listSleep(env Env) error {
	repo, err := openRepo()
	if err != nil {
		return err
	}
	records, err := readSleep(repo)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(env.Out, "No sleep pass has judged a session yet. The person runs one with memcore sleep.")
		return nil
	}
	corrections := sleepCorrections(records)
	sort.SliceStable(corrections, func(i, j int) bool {
		a, b := corrections[i], corrections[j]
		if a.Record.Start != b.Record.Start {
			return a.Record.Start > b.Record.Start
		}
		if a.Record.Session != b.Record.Session {
			return a.Record.Session < b.Record.Session
		}
		return a.Time < b.Time
	})
	sessions := map[string]bool{}
	for _, r := range records {
		sessions[r.Session] = true
	}
	var counts verdictCounts
	for _, c := range corrections {
		counts.add(c.Verdict)
	}
	fmt.Fprintf(env.Out, "%s in %s: %s.\n", plural(len(corrections), "correction"), plural(len(sessions), "judged session"), counts.text())
	ids := shortIDs(correctionIDs(corrections))
	for _, c := range corrections {
		when := c.Time
		if t, err := time.Parse(time.RFC3339, c.Time); err == nil {
			when = t.UTC().Format("2006-01-02 15:04")
		}
		name := verdictNames[c.Verdict]
		if name == "" {
			name = c.Verdict
		}
		head := fmt.Sprintf("\n%s %s %s %s: %s", ids[c.id()], when, c.Record.Level, short(c.Record.Session), name)
		if c.Path != "" {
			head += ", " + c.Path
		}
		fmt.Fprintln(env.Out, head)
		fmt.Fprintf(env.Out, "  You: %s\n", strings.Join(strings.Fields(c.Quote), " "))
		fmt.Fprintf(env.Out, "  Rule: %s\n", c.Summary)
		if c.Reason != "" {
			fmt.Fprintf(env.Out, "  Why: %s\n", c.Reason)
		}
	}
	return nil
}

// answerSleep records what happened to corrections: the document that now holds their rule, the reason, or
// both. It runs no agent and changes no document, so an agent may run it too. It writes every answer of a call,
// or none.
func answerSleep(env Env, idList, with, reason string) error {
	reason = strings.Join(strings.Fields(reason), " ")
	if with == "" && reason == "" {
		return inputErrf("an answer needs --with <path>, --reason <text> or both: --with names the document that now holds the rule")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	p := ""
	if with != "" {
		p = path.Clean(filepath.ToSlash(with))
		// A file system that ignores case also opens .GIT, so the test of the git directory ignores case too.
		first, _, _ := strings.Cut(p, "/")
		if path.IsAbs(p) || first == ".." || strings.EqualFold(first, ".git") {
			return inputErrf("--with: %s is not a path in the memory repo", with)
		}
		if err := noSymlink(repo, p); err != nil {
			return fmt.Errorf("--with: %w", err)
		}
		if info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(p))); errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
			return inputErrf("--with: no document at %s: an answer after forget takes only --reason", p)
		} else if err != nil {
			return fmt.Errorf("--with: read %s: %w", p, err)
		}
	}
	records, err := readSleep(repo)
	if err != nil {
		return err
	}
	corrections := sleepCorrections(records)
	all := correctionIDs(corrections)
	now := time.Now().UTC().Format(time.RFC3339)
	var answers []sleepAnswer
	chosen := map[string]bool{}
	for _, prefix := range strings.Split(idList, ",") {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix == "" {
			return usageErr("usage: memcore sleep --answer <id>[,<id>] takes one id or more, with a comma between two")
		}
		if len(prefix) < shortDigits {
			return inputErrf("--answer: the id %s is too short: give 7 digits or more, as memcore sleep --list prints them", prefix)
		}
		var fits []int
		for i, id := range all {
			if strings.HasPrefix(id, prefix) {
				fits = append(fits, i)
			}
		}
		switch len(fits) {
		case 0:
			return inputErrf("--answer: no recorded correction has the id %s: memcore sleep --list prints the ids", prefix)
		case 1:
		default:
			var ids []string
			for _, i := range fits {
				ids = append(ids, all[i])
			}
			return inputErrf("--answer: the id %s fits %d corrections: give more digits of one of %s", prefix, len(fits), strings.Join(ids, ", "))
		}
		c := corrections[fits[0]]
		if id := all[fits[0]]; !chosen[id] {
			chosen[id] = true
			answers = append(answers, sleepAnswer{ID: id, Session: c.Record.Session, Time: c.Time, Path: p, Reason: reason, Answered: now})
		}
	}
	if err := appendAnswers(repo, answers); err != nil {
		return err
	}
	shorts := shortIDs(all)
	for _, a := range answers {
		text := "Answered " + shorts[a.ID]
		if a.Path != "" {
			text += " with " + a.Path
		}
		if a.Reason != "" {
			text += ": " + strings.TrimRight(a.Reason, ".")
		}
		fmt.Fprintln(env.Out, text+".")
	}
	return nil
}
