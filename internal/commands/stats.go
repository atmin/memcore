package commands

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// clock returns the time of the call. Tests replace it.
var clock = time.Now

const (
	weekLength = 7 * 24 * time.Hour // stats counts in weeks of 7 days that end at the time of the call
	weekCount  = 4                  // stats prints weeks 0 to 3
	topReads   = 5                  // stats lists the five most-read memories
	topErrors  = 5                  // stats lists the five largest groups of failed calls
	// mistakeWindow is the time after a push that counts for mistakes. A revise or a forget of a memory in
	// this time makes the memory a mistake.
	mistakeWindow = 14 * 24 * time.Hour
	unreadWindow  = 30 * 24 * time.Hour // stats lists the memories with no logged read in 30 days
	unreadList    = 20                  // and names 20 of them at most
)

// queueSubjects start the subjects of the commits that agents add to the review queue.
var queueSubjects = []string{"remember:", "revise:", "forget:", "draft:", "move:"}

// fixupSubject starts the subject of a fixup, which push folds into the commit that it names.
const fixupSubject = "fixup! "

// fact is what git holds about a logged commit: its subject, and the paths that it changes.
type fact struct {
	subject string
	changes []fileStatus
}

// fileStatus is a path that a commit changes, with its status letter from git diff-tree. Examples of the
// letter are A, M and D.
type fileStatus struct{ status, path string }

// review counts the commits of the review queue in a week, and the memories that its pushes accepted.
type review struct {
	created, createdFixups int // the commits of remember, revise, forget and draft, with the fixups among them
	pushed                 int
	byAgent                int // the pushed commits of the push lines that an agent ran
	dropped, droppedFixups int
	accepted               int // the pairs of a push and a memory that one of its commits adds or modifies
	mistakes               int // the accepted memories that a revise or a forget changed within 14 days
	missing                int // the logged commits of the week that git lacks
}

// tally counts the committed lines of remember, draft, revise and forget for one kind of document.
type tally struct{ remembered, drafted, revised, forgotten int }

// ranked is a number with what it counts: a path, or a group of failed calls.
type ranked struct {
	n    int
	text string
}

// cost is what orient printed for the header or a level, at its last print in a week.
type cost struct {
	name  string
	words int
	lines int // the lines that the budget counts, or -1 when the print holds none
}

// week holds the numbers of 7 days of the use log.
type week struct {
	end              time.Time // the week holds the times after end minus 7 days, up to end
	lines            int       // the lines of the use log in the week
	memories, others tally     // capture
	review           review
	sessions         int
	reads            int      // the reads with a session
	around           int      // the reads with a session that hook tool logged
	top              []ranked // the most-read memories, most first, ties by path
	printed          int      // the words of the prints of orient with a session
	prints           int      // the prints of orient with a session
	printers         int      // the sessions with a print of orient
	costs            []cost   // the header, then each level by its title
	failed           int
	errors           []ranked // the largest groups of failed calls, largest first, ties by text
	moreErrors       int      // the groups after them
	marks            [3]int   // the count of each mark, in the order of marks
}

// runStats prints what the use log and git history say about the use of memory, per week, newest first.
// It writes nothing itself.
func runStats(env Env, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: memcore stats")
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	events, broken, err := readLog(repo)
	if err != nil {
		return err
	}
	facts, _, err := commitFacts(repo, loggedCommits(events))
	if err != nil {
		return err
	}
	now := clock().UTC()
	var b strings.Builder
	fmt.Fprintf(&b, "memcore stats to %s, in weeks of 7 days, newest first.\n", stamp(now))
	switch broken {
	case 0:
	case 1:
		b.WriteString("1 line of the use log does not parse, so stats leaves it out.\n")
	default:
		fmt.Fprintf(&b, "%d lines of the use log do not parse, so stats leaves them out.\n", broken)
	}
	if len(events) == 0 && broken == 0 {
		b.WriteString("\nThe use log is empty.\n")
	}
	for k, w := range measure(events, facts, now) {
		writeWeek(&b, w, k)
	}
	if len(events) > 0 {
		if err := writeUnread(&b, repo, events, now); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(env.Out, b.String()); err != nil {
		return fmt.Errorf("write the stats: %w", err)
	}
	return nil
}

// summary returns the three lines of week 0 that start inbox: use and context cost, review, friction and marks.
// A log without a line in week 0 gives zeros.
func summary(repo string, now time.Time) (string, error) {
	events, _, err := readLog(repo)
	if err != nil {
		return "", err
	}
	facts, _, err := commitFacts(repo, loggedCommits(events))
	if err != nil {
		return "", err
	}
	w := week{end: now}
	if weeks := measure(events, facts, now); len(weeks) > 0 {
		w = weeks[0]
	}
	r := w.review
	var b strings.Builder
	fmt.Fprintf(&b, "This week: %s, %.1f reads per session, %s around memcore, %.1f prints of orient per session, about %s each.\n",
		plural(w.sessions, "session"), w.perSession(), plural(w.around, "read"), w.printsPerSession(), plural(tokens(w.wordsPerPrint()), "token"))
	fmt.Fprintf(&b, "Review: %s pushed%s, %d dropped. %s\n", plural(r.pushed, "commit"), byAgentText(r.byAgent), r.dropped, acceptedText(r, true))
	fmt.Fprintf(&b, "Friction: %s. Marks: %d #again, %d #wrong, %d #stale. memcore stats shows more.\n",
		plural(w.failed, "failed call"), w.marks[0], w.marks[1], w.marks[2])
	return b.String(), nil
}

// readLog returns the events of the use log, in the order of the file. It also returns the count of the lines
// that do not parse. Such a line is not an event, or its time is not RFC 3339. A blank line does not parse
// either. A missing log holds no event.
func readLog(repo string) ([]Event, int, error) {
	f, err := os.Open(filepath.Join(store.StateDir(repo), "log.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read the use log: %w", err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var events []Event
	broken := 0
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 { // only the end of the file reads nothing
			var e Event
			if json.Unmarshal(line, &e) != nil {
				broken++
			} else if _, terr := time.Parse(time.RFC3339, e.Time); terr != nil {
				broken++
			} else {
				events = append(events, e)
			}
		}
		if errors.Is(err, io.EOF) {
			return events, broken, nil
		}
		if err != nil {
			return nil, 0, fmt.Errorf("read the use log: %w", err)
		}
	}
}

// loggedCommits returns the commits that the review counts: those in made, pushed and dropped, sorted, once each.
func loggedCommits(events []Event) []string {
	var shas []string
	for _, e := range events {
		for _, list := range [][]string{e.Made, e.Pushed, e.Dropped} {
			shas = append(shas, list...)
		}
	}
	slices.Sort(shas)
	return slices.Compact(shas)
}

// shaRE matches a full commit id, in SHA-1 or SHA-256.
var shaRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// commitFacts reads the subject and the changed paths of each commit from git. It runs one git cat-file to find
// the commits that git lacks, and returns their count. Then it runs one git diff-tree for the rest. --always
// gives a commit that changes nothing its subject too. The output of diff-tree parts at NUL bytes, so every
// path stays exact.
func commitFacts(repo string, shas []string) (map[string]fact, int, error) {
	facts := map[string]fact{}
	missing := 0
	var asked []string
	for _, sha := range slices.Compact(slices.Sorted(slices.Values(shas))) {
		if shaRE.MatchString(sha) {
			asked = append(asked, sha)
		} else {
			missing++ // no commit of git has such a name
		}
	}
	if len(asked) == 0 {
		return facts, missing, nil
	}
	out, err := store.GitStdin(repo, strings.Join(asked, "\n")+"\n", "cat-file", "--batch-check")
	if err != nil {
		return nil, 0, fmt.Errorf("find the logged commits: %w", err)
	}
	have := map[string]bool{}
	var found []string
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[1] == "commit" {
			have[f[0]] = true
			found = append(found, f[0])
		} else {
			missing++
		}
	}
	if len(found) == 0 {
		return facts, missing, nil
	}
	out, err = store.GitStdin(repo, strings.Join(found, "\n")+"\n",
		"diff-tree", "--stdin", "-z", "-r", "--root", "--no-renames", "--name-status", "--always", "--format=%H%x00%s")
	if err != nil {
		return nil, 0, fmt.Errorf("read the logged commits: %w", err)
	}
	// Each commit prints its id and its subject, then a status letter and a path for each change.
	parts := strings.Split(out, "\x00")
	current := ""
	for i := 0; i < len(parts); {
		token := strings.TrimPrefix(parts[i], "\n")
		switch {
		case token == "":
			i++
		case current != "" && len(token) == 1 && i+1 < len(parts):
			f := facts[current]
			f.changes = append(f.changes, fileStatus{token, parts[i+1]})
			facts[current] = f
			i += 2
		case have[token] && i+1 < len(parts):
			current = token
			facts[current] = fact{subject: parts[i+1]}
			i += 2
		default:
			return nil, 0, fmt.Errorf("read the logged commits: git diff-tree printed %q", token)
		}
	}
	return facts, missing, nil
}

// measure computes the weeks 0 to 3 before now, newest first. It leaves out a week that ends before the log starts.
// facts holds what git says about the logged commits. A commit without facts counts as missing.
func measure(events []Event, facts map[string]fact, now time.Time) []week {
	start, ok := logStart(events)
	if !ok {
		return nil
	}
	var weeks []week
	for k := range weekCount {
		end := now.Add(-time.Duration(k) * weekLength)
		if end.Before(start) {
			break
		}
		weeks = append(weeks, week{end: end})
	}
	// The order of time decides the last print of orient, and what follows a push. Parallel calls append out of
	// that order. The log keeps whole seconds, so lines of one second keep the order of the file.
	lines := ordered(events)
	follow := follower(lines, now)
	// A revise or a forget of a memory after its push makes a mistake, even in a later week.
	changed := map[string][]logLine{}
	in := make([][]logLine, len(weeks))
	for i := range lines {
		l := lines[i]
		d := now.Sub(l.at)
		if d < 0 {
			continue // a line after the call counts nowhere
		}
		if (l.Command == "revise" || l.Command == "forget") && len(l.Made) > 0 && len(l.Paths) > 0 {
			changed[l.Paths[0]] = append(changed[l.Paths[0]], l)
		}
		if k := int(d / weekLength); k < len(weeks) {
			in[k] = append(in[k], l)
		}
	}
	for k := range weeks {
		weeks[k].add(in[k], facts, changed, follow)
	}
	return weeks
}

// ordered returns the lines of the log in the order of time, each with its place in that order.
func ordered(events []Event) []logLine {
	lines := make([]logLine, len(events))
	for i, e := range events {
		lines[i] = logLine{Event: e, at: eventTime(e)}
	}
	sort.SliceStable(lines, func(a, b int) bool { return lines[a].at.Before(lines[b].at) })
	for i := range lines {
		lines[i].place = i
	}
	return lines
}

// movesIn returns the committed moves of the log up to now, in the order of time.
func movesIn(lines []logLine, now time.Time) []logLine {
	var moves []logLine
	for _, l := range lines {
		if l.Command == "move" && l.Outcome == "ok" && len(l.Made) > 0 && len(l.Paths) == 2 && !l.at.After(now) {
			moves = append(moves, l)
		}
	}
	return moves
}

// rename carries a read of the path from to the path to, when the read lies between two places of the log.
type rename struct {
	from, to      string
	after, before int
}

// follower returns the path that a document read at a place of the log has now. Each later move of its path
// carries the read along. A dropped move undoes itself: the reads of its new path, between the move and the
// drop, go back to the old path.
func follower(lines []logLine, now time.Time) func(p string, place int) string {
	dropped := map[string]int{}
	for _, l := range lines {
		if l.Command == "drop" && l.Outcome == "ok" && !l.at.After(now) {
			for _, sha := range l.Dropped {
				dropped[sha] = l.place
			}
		}
	}
	var renames []rename
	for _, m := range movesIn(lines, now) {
		if d, ok := dropped[m.Made[0]]; ok {
			renames = append(renames, rename{from: m.Paths[1], to: m.Paths[0], after: m.place, before: d})
		} else {
			renames = append(renames, rename{from: m.Paths[0], to: m.Paths[1], after: -1, before: m.place})
		}
	}
	slices.SortStableFunc(renames, func(a, b rename) int { return a.before - b.before })
	return func(p string, place int) string {
		for _, r := range renames {
			if place > r.after && place < r.before && p == r.from {
				p = r.to
			}
		}
		return p
	}
}

// logLine is a line of the use log with its time, and its place in the order of time.
type logLine struct {
	Event
	at    time.Time
	place int
}

// add counts the lines of a week, oldest first. changed holds the committed revise and forget lines of each
// path. It covers the whole log up to the call.
func (w *week) add(lines []logLine, facts map[string]fact, changed map[string][]logLine, follow func(string, int) string) {
	lacked := map[string]bool{}
	lookup := func(sha string) (fact, bool) {
		f, ok := facts[sha]
		if !ok && !lacked[sha] {
			lacked[sha] = true
			w.review.missing++
		}
		return f, ok
	}
	sessions, printers := map[string]bool{}, map[string]bool{}
	reads, groups := map[string]int{}, map[string]int{}
	last := map[string]cost{}
	for _, l := range lines {
		e := l.Event
		w.lines++
		for _, list := range [][]string{e.Made, e.Pushed, e.Dropped} {
			for _, sha := range list {
				lookup(sha) // a logged commit that git lacks counts as missing, though no number needs it
			}
		}
		if e.Session != "" {
			sessions[e.Session] = true
		}
		if len(e.Made) > 0 && len(e.Paths) > 0 {
			t := &w.others
			if store.IsMemory(e.Paths[0]) {
				t = &w.memories
			}
			switch e.Command {
			case "remember":
				t.remembered++
			case "draft":
				t.drafted++
			case "revise":
				t.revised++
			case "forget":
				t.forgotten++
			}
		}
		if slices.Contains([]string{"remember", "revise", "forget", "draft", "move"}, e.Command) {
			for _, sha := range e.Made {
				w.review.created++
				if f, ok := lookup(sha); ok && strings.HasPrefix(f.subject, fixupSubject) {
					w.review.createdFixups++
				}
			}
		}
		switch {
		case e.Command == "push":
			w.review.push(l, lookup, changed)
		case e.Command == "drop" && e.Outcome == "ok":
			for _, sha := range e.Dropped {
				f, ok := lookup(sha)
				switch {
				case !ok:
				case strings.HasPrefix(f.subject, fixupSubject):
					w.review.dropped++
					w.review.droppedFixups++
				case isQueueSubject(f.subject):
					w.review.dropped++
				}
			}
		}
		if e.Session != "" {
			for _, p := range readsOf(e) {
				w.reads++
				if e.Hook == "tool" {
					w.around++
				}
				if p := follow(p, l.place); store.IsMemory(p) {
					reads[p]++
				}
			}
		}
		if len(e.Words) > 0 {
			if e.Session != "" {
				printers[e.Session] = true
				w.prints++
				for _, n := range e.Words {
					w.printed += n
				}
			}
			for name, n := range e.Words {
				c := cost{name: name, words: n, lines: -1}
				if l, ok := e.Lines[name]; ok {
					c.lines = l
				}
				last[name] = c
			}
		}
		if e.Outcome == "error" {
			w.failed++
			groups[e.Command+": "+normalize(e)]++
		}
		for i, m := range marks {
			if slices.Contains(e.Marks, m) {
				w.marks[i]++
			}
		}
	}
	w.sessions = len(sessions)
	w.printers = len(printers)
	w.top = largest(reads, topReads)
	w.errors = largest(groups, topErrors)
	w.moreErrors = len(groups) - len(w.errors)
	if c, ok := last["header"]; ok {
		w.costs = append(w.costs, c)
	}
	var names []string
	for name := range last {
		if name != "header" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		w.costs = append(w.costs, last[name])
	}
}

// push counts the queue commits of a push line, whatever its outcome, and the memories that it accepted.
// A memory that a committed revise or forget changed within 14 days after the push is an accepted mistake.
func (r *review) push(l logLine, lookup func(string) (fact, bool), changed map[string][]logLine) {
	memories := map[string]bool{}
	for _, sha := range l.Pushed {
		f, ok := lookup(sha)
		if !ok {
			continue
		}
		if isQueueSubject(f.subject) {
			r.pushed++
			if l.By == "agent" {
				r.byAgent++
			}
		}
		for _, c := range f.changes {
			if (c.status == "A" || c.status == "M") && store.IsMemory(c.path) {
				memories[c.path] = true
			}
		}
	}
	for p := range memories {
		r.accepted++
		for _, c := range changed[p] {
			if c.place > l.place && !c.at.After(l.at.Add(mistakeWindow)) {
				r.mistakes++
				break
			}
		}
	}
}

// isQueueSubject reports whether a subject is that of a commit that an agent added to the review queue.
func isQueueSubject(subject string) bool {
	for _, s := range queueSubjects {
		if strings.HasPrefix(subject, s) {
			return true
		}
	}
	return false
}

// eventTime returns the time of a line. readLog keeps only lines with a valid time.
func eventTime(e Event) time.Time {
	t, _ := time.Parse(time.RFC3339, e.Time)
	return t
}

// logStart returns the time of the oldest line, and false when the log has none.
func logStart(events []Event) (time.Time, bool) {
	var start time.Time
	for i, e := range events {
		if t := eventTime(e); i == 0 || t.Before(start) {
			start = t
		}
	}
	return start, len(events) > 0
}

// writeUnread writes the memories with no logged read in 30 days, or why it cannot list them yet.
// Then it names the time of the first read around memcore, because before it only show logged reads.
func writeUnread(b *strings.Builder, repo string, events []Event, now time.Time) error {
	start, _ := logStart(events)
	if start.After(now.Add(-unreadWindow)) {
		fmt.Fprintf(b, "\nMemories with no logged read in 30 days:\n  The use log starts on %s, so this needs 30 days of it.\n",
			start.UTC().Format("2006-01-02"))
	} else {
		paths, err := unread(repo, events, now)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "\nMemories with no logged read in 30 days: %d.\n", len(paths))
		for _, p := range paths[:min(len(paths), unreadList)] {
			fmt.Fprintf(b, "  %s\n", p)
		}
		if len(paths) > unreadList {
			fmt.Fprintf(b, "  and %d more\n", len(paths)-unreadList)
		}
	}
	var first time.Time
	for _, e := range events {
		if t := eventTime(e); e.Command == "hook" && e.Hook == "tool" && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	if first.IsZero() {
		b.WriteString("The log holds no read around memcore yet.\n")
	} else {
		fmt.Fprintf(b, "The log holds reads around memcore since %s.\n", stamp(first))
	}
	return nil
}

// unread returns the memories at HEAD that no read of the use log names in the 30 days before now. The list
// is sorted. A memory counts only when its addition is older than 30 days. The addition is the author date of
// the newest commit that adds the memory, since a fold keeps author dates. Every read counts, with a session
// or without one.
func unread(repo string, events []Event, now time.Time) ([]string, error) {
	since := now.Add(-unreadWindow)
	lines := ordered(events)
	follow := follower(lines, now)
	read := map[string]bool{}
	for _, l := range lines {
		if l.at.After(since) && !l.at.After(now) {
			for _, p := range readsOf(l.Event) {
				read[follow(p, l.place)] = true
			}
		}
	}
	out, err := store.Git(repo, "ls-tree", "-z", "-r", "--name-only", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("list the memories: %w", err)
	}
	added, err := additions(repo)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if at, ok := added[p]; ok && store.IsMemory(p) && !read[p] && at.Before(since) {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	return paths, nil
}

// additions returns the age of each path on HEAD: the author date of the commit that added its document. A
// rename keeps the date of the document, as git detects the rename, so a move keeps the age of a memory. A
// path that a later commit adds again starts a new age. The format starts with a NUL, so the empty field
// before each date tells it from a status.
func additions(repo string) (map[string]time.Time, error) {
	out, err := store.Git(repo, "log", "-z", "--reverse", "-M", "--diff-filter=AR", "--name-status", "--format=%x00%at", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("read the additions of the memories: %w", err)
	}
	added := map[string]time.Time{}
	var at time.Time
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		status := strings.TrimPrefix(parts[i], "\n") // a line break ends the date
		switch {
		case parts[i] == "" && i+1 < len(parts) && parts[i+1] != "":
			i++
			sec, err := strconv.ParseInt(parts[i], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("read the additions of the memories: git log printed %q", parts[i])
			}
			at = time.Unix(sec, 0)
		case status == "A" && i+1 < len(parts):
			i++
			added[parts[i]] = at
		case strings.HasPrefix(status, "R") && i+2 < len(parts):
			from, to := parts[i+1], parts[i+2]
			i += 2
			if was, ok := added[from]; ok {
				added[to] = was
				delete(added, from)
			} else {
				added[to] = at
			}
		}
	}
	return added, nil
}

// readsOf returns the reads of a line: the path of a successful show, or the paths that hook tool logged.
func readsOf(e Event) []string {
	switch {
	case e.Command == "show" && e.Outcome == "ok":
		return e.Paths
	case e.Command == "hook" && e.Hook == "tool":
		return e.Paths
	}
	return nil
}

// largest returns the n largest counts, largest first, ties by text, or nil when there are none.
func largest(counts map[string]int, n int) []ranked {
	var all []ranked
	for text, c := range counts {
		all = append(all, ranked{c, text})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].text < all[j].text
	})
	if len(all) > n {
		all = all[:n]
	}
	return all
}

var (
	commitRE = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	digitsRE = regexp.MustCompile(`[0-9]+`)
)

// normalize returns the error of a failed line with its variable parts replaced. So calls that failed the
// same way fall in one group. It replaces each path of the line, then each commit, then each run of digits.
func normalize(e Event) string {
	msg := e.Error
	paths := slices.Clone(e.Paths)
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, p := range paths {
		if p != "" {
			msg = strings.ReplaceAll(msg, p, "<path>")
		}
	}
	msg = commitRE.ReplaceAllStringFunc(msg, func(word string) string {
		if strings.ContainsAny(word, "0123456789") {
			return "<commit>"
		}
		return word
	})
	return digitsRE.ReplaceAllString(msg, "<n>")
}

// writeWeek writes the numbers of week k.
func writeWeek(b *strings.Builder, w week, k int) {
	fmt.Fprintf(b, "\nThe 7 days to %s:\n", stamp(w.end))
	if w.lines == 0 {
		b.WriteString("  No call of memcore.\n")
		return
	}
	m, o := w.memories, w.others
	fmt.Fprintf(b, "  Capture of memories: %d remembered, %d revised, %d forgotten. Other documents: %d drafted, %d revised, %d forgotten.\n",
		m.remembered, m.revised, m.forgotten, o.drafted, o.revised, o.forgotten)
	r := w.review
	fmt.Fprintf(b, "  Review: %s created, %s. %d pushed%s, %d dropped, %s.\n",
		plural(r.created, "commit"), ofThem(r.createdFixups), r.pushed, byAgentText(r.byAgent), r.dropped, ofThem(r.droppedFixups))
	// A push of weeks 0 and 1 may still gain mistakes.
	fmt.Fprintf(b, "  %s", acceptedText(r, time.Duration(k)*weekLength < mistakeWindow))
	if r.accepted > 0 {
		precision := int(math.Round(float64(r.accepted-r.mistakes) * 100 / float64(r.accepted)))
		fmt.Fprintf(b, " Precision: %d%%.", precision)
	}
	b.WriteString("\n")
	switch r.missing {
	case 0:
	case 1:
		b.WriteString("  1 logged commit is missing from git.\n")
	default:
		fmt.Fprintf(b, "  %d logged commits are missing from git.\n", r.missing)
	}
	fmt.Fprintf(b, "  Use: %s, %s, %.1f per session, %d of them around memcore.\n",
		plural(w.sessions, "session"), plural(w.reads, "read"), w.perSession(), w.around)
	writeCounts(b, w.top, 0)
	fmt.Fprintf(b, "  Context: orient printed %.1f times per session, %s each, about %s.\n",
		w.printsPerSession(), plural(w.wordsPerPrint(), "word"), plural(tokens(w.wordsPerPrint()), "token"))
	for _, c := range w.costs {
		fmt.Fprintf(b, "    %s: %s, about %s", c.name, plural(c.words, "word"), plural(tokens(c.words), "token"))
		if c.lines >= 0 {
			fmt.Fprintf(b, ", %s of %d", plural(c.lines, "line"), budget)
			if c.lines > budget {
				b.WriteString(", over the budget")
			}
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "  Friction: %s.\n", plural(w.failed, "failed call"))
	writeCounts(b, w.errors, w.moreErrors)
	fmt.Fprintf(b, "  Marks: %d #again, %d #wrong, %d #stale.\n", w.marks[0], w.marks[1], w.marks[2])
}

// acceptedText says how many memories the pushes accepted, and how many of these memories were mistakes.
// soFar says that the mistakes can still grow.
func acceptedText(r review, soFar bool) string {
	if r.accepted == 0 {
		return "Accepted memories: 0."
	}
	more := ""
	if soFar {
		more = " so far"
	}
	return fmt.Sprintf("Accepted memories: %d, with %s%s.", r.accepted, plural(r.mistakes, "mistake"), more)
}

// perSession returns the reads per session of a week, rounded to one decimal, or 0 without a session.
func (w week) perSession() float64 {
	if w.sessions == 0 {
		return 0
	}
	return math.Round(float64(w.reads)*10/float64(w.sessions)) / 10
}

// printsPerSession returns the prints of orient per session with a print, rounded to one decimal, or 0 without one.
func (w week) printsPerSession() float64 {
	if w.printers == 0 {
		return 0
	}
	return math.Round(float64(w.prints)*10/float64(w.printers)) / 10
}

// wordsPerPrint returns the words of one print of orient with a session, rounded half away from zero. It
// returns 0 when the week has no such print.
func (w week) wordsPerPrint() int {
	if w.prints == 0 {
		return 0
	}
	return int(math.Round(float64(w.printed) / float64(w.prints)))
}

// writeCounts writes one line for each count, with the numbers aligned, then the count of the rest.
func writeCounts(b *strings.Builder, counts []ranked, rest int) {
	width := 0
	for _, c := range counts {
		width = max(width, len(strconv.Itoa(c.n)))
	}
	for _, c := range counts {
		fmt.Fprintf(b, "      %*d  %s\n", width, c.n, c.text)
	}
	if rest > 0 {
		fmt.Fprintf(b, "      and %d more\n", rest)
	}
}

// ofThem says how many of the commits before it are fixups.
func ofThem(fixups int) string {
	if fixups == 1 {
		return "1 of them a fixup"
	}
	return fmt.Sprintf("%d of them fixups", fixups)
}

// byAgentText says how many of the pushed commits before it an agent pushed, after a comma, or nothing when none.
func byAgentText(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d of them by an agent", n)
}

// tokens estimates the tokens of English text from its words: words × 4 ÷ 3, rounded.
func tokens(words int) int {
	return int(math.Round(float64(words) * 4 / 3))
}

// plural writes a count with its noun, which takes an s unless the count is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// stamp writes a time in UTC to the minute, as stats prints it.
func stamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}
