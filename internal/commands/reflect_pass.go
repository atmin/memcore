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

// The settings of a reflect pass, in the git configuration of the memory repo, and their first values.
// Real passes will show whether these values fit, so each one lives only here.
const (
	reflectBudgetKey = "memcore.reflectbudget" // the budget of a pass, in dollars at API prices
	reflectAgentKey  = "memcore.reflectagent"  // the agent that thinks
	reflectModelKey  = "memcore.reflectmodel"  // the model of that agent

	reflectBudget = 2.0
	reflectAgent  = "claude"
	reflectModel  = "sonnet"

	// reflectLock is the file of the local state that a running pass locks.
	reflectLock = "reflect.lock"
)

// reflectAgents are the agents that a reflect pass can run.
var reflectAgents = []string{"claude"}

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

// reflectSettings returns the budget, the agent and the model of a pass. A budget of the command line wins over
// the setting.
func reflectSettings(repo, budgetFlag string) (budget float64, agent, model string, err error) {
	budget, agent, model = reflectBudget, reflectAgent, reflectModel
	text, from := budgetFlag, "--budget"
	if text == "" {
		if value, ok, err := setting(repo, reflectBudgetKey); err != nil {
			return 0, "", "", err
		} else if ok {
			text, from = value, "the setting "+reflectBudgetKey
		}
	}
	if text != "" {
		if budget, err = strconv.ParseFloat(text, 64); err != nil || !(budget > 0) || math.IsInf(budget, 0) {
			return 0, "", "", inputErrf("%s: the value %q is no amount of dollars above 0, such as 2 or 0.50", from, text)
		}
	}
	if value, ok, err := setting(repo, reflectAgentKey); err != nil {
		return 0, "", "", err
	} else if ok {
		agent = value
	}
	known := false
	for _, a := range reflectAgents {
		known = known || a == agent
	}
	if !known {
		return 0, "", "", fmt.Errorf("the setting %s names the agent %q, which memcore cannot run: the known agents are %s", reflectAgentKey, agent, strings.Join(reflectAgents, ", "))
	}
	if value, ok, err := setting(repo, reflectModelKey); err != nil {
		return 0, "", "", err
	} else if ok {
		model = value
	}
	return budget, agent, model, nil
}

// runReflect runs one reflect pass: it extracts the corrections of each new transcript, and judges each one.
// A pass proposes no change, commits nothing and writes only the records of the local state.
func runReflect(env Env, args []string) error {
	flags := flag.NewFlagSet("reflect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	budgetFlag := flags.String("budget", "", "the budget of the pass, in dollars at API prices")
	sessionsFlag := flags.String("sessions", "", "the most sessions that the pass judges")
	list := flags.Bool("list", false, "list the corrections of the records, and run no pass")
	all := flags.Bool("all", false, "with --list: also the corrections that are not open")
	answerFlag := flags.String("answer", "", "record what happened to these corrections, and run no pass")
	withFlag := flags.String("with", "", "the document that now holds the rule of an answered correction")
	reasonFlag := flags.String("reason", "", "the reason of an answer")
	pos, err := parseInterleaved(flags, args)
	if err != nil {
		return usageErr(err.Error())
	}
	if len(pos) != 0 {
		return usageErr("usage: memcore reflect [--budget <dollars>] [--sessions <n>] | --list [--all] | --answer <id>[,<id>] [--with <path>] [--reason <text>]")
	}
	given := map[string]bool{} // also a flag with an empty value, as in --budget=
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	// An answer and a list run no agent and no pass, so an agent may run them too.
	if given["answer"] {
		if given["budget"] || given["sessions"] || given["list"] || given["all"] {
			return usageErr("usage: memcore reflect --answer takes only --with and --reason: no --budget, --sessions, --list or --all")
		}
		return answerReflect(env, *answerFlag, *withFlag, *reasonFlag)
	}
	if given["with"] || given["reason"] {
		return usageErr("usage: memcore reflect --with and --reason belong to --answer <id>[,<id>]")
	}
	if given["all"] && !*list {
		return usageErr("usage: memcore reflect --all belongs to --list")
	}
	if *list {
		if given["budget"] || given["sessions"] {
			return usageErr("usage: memcore reflect --list takes no --budget and no --sessions, because it runs no pass")
		}
		return listReflect(env, *all)
	}
	limit := 0 // no limit
	if *sessionsFlag != "" {
		if limit, err = strconv.Atoi(*sessionsFlag); err != nil || limit < 1 {
			return inputErrf("--sessions: the value %q is no count of sessions, 1 or more", *sessionsFlag)
		}
	}
	// A pass spends the limits of the plan of the person, and sends redacted transcripts to the provider.
	if err := guards.Agent(); err != nil {
		return fmt.Errorf("only the person runs a reflect pass, at a terminal: %w", err)
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	budget, agent, model, err := reflectSettings(repo, *budgetFlag)
	if err != nil {
		return err
	}
	unlock, ok, err := store.TryLockFile(repo, reflectLock)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("a reflect pass already runs: wait until it ends")
	}
	defer unlock()

	events, _, err := readLog(repo)
	if err != nil {
		return err
	}
	records, err := readReflect(repo)
	if err != nil {
		return err
	}
	units, err := reflectSessions(repo, events, records, time.Now())
	if err != nil {
		return err
	}
	if len(units) == 0 {
		fmt.Fprintln(env.Out, "No session waits for a reflect pass.")
		return nil
	}

	p := pass{env: env, repo: repo, events: events, agent: agent, model: model, budget: budget, judged: map[string]bool{}, seen: map[[3]string]bool{}}
	// A correction that an earlier pass recorded counts in no total of this pass.
	for _, c := range reflectCorrections(records) {
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
		return fmt.Errorf("the reflect pass stopped, and it keeps the sessions that it finished: %w", failure)
	}
	return nil
}

// pass is the state of one running reflect pass.
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
func (p *pass) judge(u reflectUnit) error {
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
	groups := groupBySeen(p.events, u.Session, found)
	result := make([]reflectCorrection, len(found))
	for _, group := range groups {
		if group.commit == "" {
			// memcore saw the session only after these corrections, so it does not know what memory held then.
			for _, i := range group.indices {
				result[i] = reflectCorrection{Time: found[i].Time, Quote: found[i].Quote, Summary: found[i].Summary, Verdict: verdictBefore}
			}
			continue
		}
		memories, err := seenMemories(p.repo, group.commit, u.Level)
		if err != nil {
			return err
		}
		memories = group.nearest(memories, p.events, u.Session)
		findings, cost, err := thinking.Judge(thinking.JudgeRequest{Corrections: group.corrections, Memories: memories, Model: p.model})
		p.cost += cost
		unitCost += cost
		if err != nil {
			return err
		}
		for k, v := range verdicts(group.corrections, findings, p.events, u.Session) {
			result[group.indices[k]] = v
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Time < result[j].Time })
	record := reflectRecord{Session: u.Session, File: u.File, Blob: u.Blob, Dir: u.Dir, Level: u.Level,
		Start: u.Start.UTC().Format(time.RFC3339), Time: time.Now().UTC().Format(time.RFC3339),
		Agent: p.agent, Model: p.model, Cost: unitCost, Corrections: result}
	if record.Corrections == nil {
		record.Corrections = []reflectCorrection{}
	}
	if err := appendReflect(p.repo, record); err != nil {
		return err
	}
	p.judged[u.Session] = true
	p.line, p.lineWritten = fmt.Sprintf("%s %s %s", u.Start.UTC().Format("2006-01-02"), record.where(), short(u.Session)), true
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

// A seenGroup holds corrections that saw the same commit, in their original order.
type seenGroup struct {
	commit      string
	corrections []thinking.Correction
	indices     []int
	latest      time.Time
}

func (g seenGroup) nearest(memories []thinking.Memory, events []Event, session string) []thinking.Memory {
	return nearestMemories(g.corrections, memories, readPaths(events, session, g.latest))
}

// groupBySeen groups corrections by seen commit in order of first occurrence.
// An empty commit holds corrections from before memcore saw the session.
func groupBySeen(events []Event, session string, corrections []thinking.Correction) []seenGroup {
	positions := map[string]int{}
	var groups []seenGroup
	for i, c := range corrections {
		at, _ := time.Parse(time.RFC3339, c.Time)
		commit := seenCommit(events, session, at)
		pos, ok := positions[commit]
		if !ok {
			pos = len(groups)
			positions[commit] = pos
			groups = append(groups, seenGroup{commit: commit, latest: at})
		}
		group := &groups[pos]
		group.indices = append(group.indices, i)
		group.corrections = append(group.corrections, c)
		if at.After(group.latest) {
			group.latest = at
		}
	}
	return groups
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

// openCorrection reports whether a correction is open: it is a miss or a stale memory, and it has no answer.
func openCorrection(c judged, answers map[string]reflectAnswer) bool {
	switch c.Verdict {
	case verdictCapture, verdictRetrieval, verdictApplication, verdictStale:
		_, answered := answers[c.id()]
		return !answered
	}
	return false
}

// listReflect prints the open corrections of the records, or every correction with all. The newest session comes
// first, and each session in the order of its corrections. Each one starts with its short id, which reflect
// --answer takes. The first line counts every correction. With all, a turn that several sessions copied prints once,
// with its corrections under it, and the list reads the transcript blobs for that proof, each blob at most once.
// Plain --list reads no blob. It runs no agent and writes nothing, so an agent may run it too.
func listReflect(env Env, all bool) error {
	repo, err := openRepo()
	if err != nil {
		return err
	}
	records, err := readReflect(repo)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(env.Out, "No reflect pass has judged a session yet. The person runs one with memcore reflect.")
		return nil
	}
	corrections := reflectCorrections(records)
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
	answers, err := readAnswers(repo)
	if err != nil {
		return err
	}
	var counts verdictCounts
	open := 0
	for _, c := range corrections {
		counts.add(c.Verdict)
		if openCorrection(c, answers) {
			open++
		}
	}
	fmt.Fprintf(env.Out, "%s in %s: %s. %d open.\n", plural(len(corrections), "correction"), plural(len(sessions), "judged session"), counts.text(), open)
	if open == 0 && !all {
		fmt.Fprintln(env.Out, "No correction is open.")
		return nil
	}
	follow, err := answerFollower(repo, answers)
	if err != nil {
		return err
	}
	ids := shortIDs(correctionIDs(corrections))
	// Only the history of all corrections reads transcripts, to show a copied turn once.
	var turns map[int]*copiedTurn
	if all {
		turns = copiedTurns(repo, corrections)
	}
	for i, c := range corrections {
		if !all && !openCorrection(c, answers) {
			continue
		}
		if turn := turns[i]; turn != nil {
			if i == turn.members[0] { // a copied turn prints at its first member
				printCopiedTurn(env, turn, corrections, ids, answers, follow)
			}
			continue
		}
		fmt.Fprintf(env.Out, "\n%s %s %s %s: %s\n", ids[c.id()], correctionWhen(c), c.Record.where(), short(c.Record.Session), verdictText(c))
		fmt.Fprintf(env.Out, "  You: %s\n", foldSpace(c.Quote))
		printVerdictDetail(env, "  ", c, answers, follow)
	}
	return nil
}

// foldSpace joins the whitespace of a text into single spaces, as the list shows a quote.
func foldSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// correctionWhen is the time of a correction as the list shows it: the UTC minute.
func correctionWhen(c judged) string {
	if t, err := time.Parse(time.RFC3339, c.Time); err == nil {
		return t.UTC().Format("2006-01-02 15:04")
	}
	return c.Time
}

// verdictText names the verdict of a correction, and its path when it has one.
func verdictText(c judged) string {
	name := verdictNames[c.Verdict]
	if name == "" {
		name = c.Verdict
	}
	if c.Path != "" {
		name += ", " + c.Path
	}
	return name
}

// printVerdictDetail prints the rule, the reason and the answer of a correction, each line after the indent.
func printVerdictDetail(env Env, indent string, c judged, answers map[string]reflectAnswer, follow func(reflectAnswer) string) {
	fmt.Fprintf(env.Out, "%sRule: %s\n", indent, c.Summary)
	if c.Reason != "" {
		fmt.Fprintf(env.Out, "%sWhy: %s\n", indent, c.Reason)
	}
	if a, ok := answers[c.id()]; ok {
		text := follow(a)
		if a.Path != "" && a.Reason != "" {
			text += ": "
		}
		fmt.Fprintf(env.Out, "%sAnswer: %s\n", indent, text+a.Reason)
	}
}

// A copiedTurn is one turn of a person that several sessions copied, with the corrections that quote it.
type copiedTurn struct {
	text    string // the complete turn
	members []int  // the indices of its corrections in the sorted list, in list order
}

// copiedTurns finds the turns that corrections of two or more sessions quote on the same verified history. It
// returns each member index with its turn. A correction with no proof is no member: a missing blob, an unclear turn
// or another history leaves it alone. Only a time that distinct sessions share needs a transcript, and each such
// blob is read once, for all its candidates together.
func copiedTurns(repo string, corrections []judged) map[int]*copiedTurn {
	byTime := map[string][]int{}
	for i, c := range corrections {
		byTime[c.Time] = append(byTime[c.Time], i)
	}
	byBlob := map[string][]int{}
	for _, indices := range byTime {
		sessions := map[string]bool{}
		for _, i := range indices {
			sessions[corrections[i].Record.Session] = true
		}
		if len(sessions) < 2 {
			continue
		}
		for _, i := range indices {
			blob := corrections[i].Record.Blob
			byBlob[blob] = append(byBlob[blob], i)
		}
	}
	proofs := map[int]turnSource{}
	for blob, indices := range byBlob {
		content, err := store.Git(repo, "cat-file", "blob", blob)
		if err != nil {
			continue // the blob is gone, so its corrections stay single
		}
		candidates := make([]reflectCorrection, len(indices))
		for k, i := range indices {
			candidates[k] = corrections[i].reflectCorrection
		}
		for k, proof := range turnSources(content, candidates) {
			if proof.OK {
				proofs[indices[k]] = proof
			}
		}
	}
	byPrefix := map[string]*copiedTurn{}
	for i := range corrections { // in list order, so each turn lists its members in list order
		proof, ok := proofs[i]
		if !ok {
			continue
		}
		turn := byPrefix[proof.Prefix]
		if turn == nil {
			turn = &copiedTurn{text: proof.Text}
			byPrefix[proof.Prefix] = turn
		}
		turn.members = append(turn.members, i)
	}
	out := map[int]*copiedTurn{}
	for _, turn := range byPrefix {
		sessions := map[string]bool{}
		for _, i := range turn.members {
			sessions[corrections[i].Record.Session] = true
		}
		if len(sessions) < 2 {
			continue
		}
		for _, i := range turn.members {
			out[i] = turn
		}
	}
	return out
}

// printCopiedTurn shows a shared partial quote once under Quote. Otherwise it shows the whole turn under You.
// When the quotes differ, each child shows its own partial quote. A child that quotes the whole turn needs only
// the group's You line.
func printCopiedTurn(env Env, turn *copiedTurn, corrections []judged, ids map[string]string, answers map[string]reflectAnswer, follow func(reflectAnswer) string) {
	first := corrections[turn.members[0]]
	full := foldSpace(turn.text)
	shared := foldSpace(first.Quote)
	var sessions []string
	seen := map[string]bool{}
	for _, i := range turn.members {
		c := corrections[i]
		if foldSpace(c.Quote) != shared {
			shared = ""
		}
		if session := c.Record.Session; !seen[session] {
			seen[session] = true
			sessions = append(sessions, session)
		}
	}
	names := shortIDs(sessions)
	if shared == full {
		shared = ""
	}
	fmt.Fprintf(env.Out, "\n%s  Copied turn\n", correctionWhen(first))
	if shared != "" {
		fmt.Fprintf(env.Out, "  Quote: %s\n", shared)
	} else {
		fmt.Fprintf(env.Out, "  You: %s\n", full)
	}
	for _, i := range turn.members {
		c := corrections[i]
		fmt.Fprintf(env.Out, "  %s  %s  session %s: %s\n", ids[c.id()], c.Record.where(), names[c.Record.Session], verdictText(c))
		if excerpt := foldSpace(c.Quote); shared == "" && excerpt != full {
			fmt.Fprintf(env.Out, "    Quote: %s\n", excerpt)
		}
		printVerdictDetail(env, "    ", c, answers, follow)
	}
}

// answerFollower returns the path that the document of an answer has now. It follows each move of the use log
// after the answer, and it undoes a dropped move, as the reads of stats do. Each answer gets a line of its own
// among the lines of the log, after a line in the same second. These lines stay here, and never reach the use
// log. An answer with no path, or with a time that does not parse, keeps its path as recorded.
func answerFollower(repo string, answers map[string]reflectAnswer) (func(reflectAnswer) string, error) {
	events, _, err := readLog(repo)
	if err != nil {
		return nil, err
	}
	logged := len(events)
	var ids []string
	for id, a := range answers {
		if _, err := time.Parse(time.RFC3339, a.Answered); a.Path != "" && err == nil {
			ids = append(ids, id)
			events = append(events, Event{Time: a.Answered})
		}
	}
	lines := ordered(events)
	places := map[string]int{}
	for _, l := range lines {
		if l.index >= logged {
			places[ids[l.index-logged]] = l.place
		}
	}
	follow := follower(lines, clock().UTC())
	return func(a reflectAnswer) string {
		if place, ok := places[a.ID]; ok {
			return follow(a.Path, place)
		}
		return a.Path
	}, nil
}

// answerReflect records what happened to corrections: the document that now holds their rule, the reason, or
// both. It runs no agent and changes no document, so an agent may run it too. It writes every answer of a call,
// or none.
func answerReflect(env Env, idList, with, reason string) error {
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
	records, err := readReflect(repo)
	if err != nil {
		return err
	}
	corrections := reflectCorrections(records)
	all := correctionIDs(corrections)
	now := time.Now().UTC().Format(time.RFC3339)
	var answers []reflectAnswer
	chosen := map[string]bool{}
	for _, prefix := range strings.Split(idList, ",") {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix == "" {
			return usageErr("usage: memcore reflect --answer <id>[,<id>] takes one id or more, with a comma between two")
		}
		if len(prefix) < shortDigits {
			return inputErrf("--answer: the id %s is too short: give 7 digits or more, as memcore reflect --list prints them", prefix)
		}
		var fits []int
		for i, id := range all {
			if strings.HasPrefix(id, prefix) {
				fits = append(fits, i)
			}
		}
		switch len(fits) {
		case 0:
			return inputErrf("--answer: no recorded correction has the id %s: memcore reflect --list prints the ids", prefix)
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
			answers = append(answers, reflectAnswer{ID: id, Session: c.Record.Session, Time: c.Time, Path: p, Reason: reason, Answered: now})
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
