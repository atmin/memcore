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
)

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
	sessions         int
	reads            int      // the reads with a session
	around           int      // the reads with a session that hook tool logged
	top              []ranked // the most-read memories, most first, ties by path
	words            int      // the words that orient printed per session, rounded
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
	for _, w := range measure(events, now) {
		writeWeek(&b, w)
	}
	if _, err := io.WriteString(env.Out, b.String()); err != nil {
		return fmt.Errorf("write the stats: %w", err)
	}
	return nil
}

// readLog returns the events of the use log, in the order of the file, and the count of the lines that do not
// parse: a line that is not an event, a blank line included, or whose time is not RFC 3339. A missing log holds no event.
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

// measure computes the weeks 0 to 3 before now, newest first. It leaves out a week that ends before the log starts.
func measure(events []Event, now time.Time) []week {
	times := make([]time.Time, len(events))
	var start time.Time
	for i, e := range events {
		times[i], _ = time.Parse(time.RFC3339, e.Time) // readLog keeps only lines with a valid time
		if i == 0 || times[i].Before(start) {
			start = times[i]
		}
	}
	if len(events) == 0 {
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
	// The order of time decides the last print of orient, and parallel calls append out of that order.
	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return times[order[a]].Before(times[order[b]]) })
	in := make([][]Event, len(weeks))
	for _, i := range order {
		d := now.Sub(times[i])
		if d < 0 {
			continue // a line after the call belongs to no week
		}
		if k := int(d / weekLength); k < len(weeks) {
			in[k] = append(in[k], events[i])
		}
	}
	for k := range weeks {
		weeks[k].add(in[k])
	}
	return weeks
}

// add counts the events of a week, oldest first.
func (w *week) add(events []Event) {
	sessions, printers := map[string]bool{}, map[string]bool{}
	reads, groups := map[string]int{}, map[string]int{}
	last := map[string]cost{}
	printed := 0
	for _, e := range events {
		w.lines++
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
		if e.Session != "" {
			for _, p := range readsOf(e) {
				w.reads++
				if e.Hook == "tool" {
					w.around++
				}
				if store.IsMemory(p) {
					reads[p]++
				}
			}
		}
		if len(e.Words) > 0 {
			if e.Session != "" {
				printers[e.Session] = true
				for _, n := range e.Words {
					printed += n
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
	if len(printers) > 0 {
		w.words = int(math.Round(float64(printed) / float64(len(printers))))
	}
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

// normalize returns the error of a failed line with its variable parts replaced, so that calls that failed
// the same way fall in one group: each path of the line, then each commit, then each run of digits.
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

// writeWeek writes the numbers of one week.
func writeWeek(b *strings.Builder, w week) {
	fmt.Fprintf(b, "\nThe 7 days to %s:\n", stamp(w.end))
	if w.lines == 0 {
		b.WriteString("  No call of memcore.\n")
		return
	}
	m, o := w.memories, w.others
	fmt.Fprintf(b, "  Capture of memories: %d remembered, %d revised, %d forgotten. Other documents: %d drafted, %d revised, %d forgotten.\n",
		m.remembered, m.revised, m.forgotten, o.drafted, o.revised, o.forgotten)
	perSession := 0.0
	if w.sessions > 0 {
		perSession = math.Round(float64(w.reads)*10/float64(w.sessions)) / 10
	}
	fmt.Fprintf(b, "  Use: %s, %s, %.1f per session, %d of them around memcore.\n",
		plural(w.sessions, "session"), plural(w.reads, "read"), perSession, w.around)
	writeCounts(b, w.top, 0)
	fmt.Fprintf(b, "  Context: orient printed %s per session, about %s.\n", plural(w.words, "word"), plural(tokens(w.words), "token"))
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
