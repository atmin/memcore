package commands

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests of this package set the process environment, so t.Parallel cannot run them. TestMain runs each
// top-level test in its own process instead, MEMCORE_TEST_PROCS at a time.
const (
	procsVar = "MEMCORE_TEST_PROCS"
	childVar = "MEMCORE_TEST_CHILD"
	logMagic = "# test log\n" // the first line of a file of -test.testlogfile
	framing  = "\x16"         // the byte that -test.v=test2json puts before a line of the testing package
)

// procsFlags holds what decides whether the tests run in one process.
type procsFlags struct {
	child   bool
	procs   int
	cover   bool
	run     string
	skip    string
	shuffle string
	single  []string // the values of the flags that need one process, such as -test.list
}

// oneProcessFlags are the flags that need one process when they are set.
var oneProcessFlags = []string{"test.list", "test.bench", "test.fuzz", "test.cpuprofile", "test.memprofile",
	"test.blockprofile", "test.mutexprofile", "test.trace", "test.coverprofile"}

// oneProcess reports whether the tests run in this process, as without the runner.
func (f procsFlags) oneProcess() bool {
	if f.child || f.procs == 1 || f.cover || strings.Contains(f.run, "/") || f.skip != "" {
		return true
	}
	if f.shuffle != "" && f.shuffle != "off" {
		return true
	}
	for _, v := range f.single {
		if v != "" {
			return true
		}
	}
	return false
}

// procsOf reads MEMCORE_TEST_PROCS: empty gives def, and anything but a positive integer is an error.
func procsOf(value string, def int) (int, error) {
	if value == "" {
		return def, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s is %q, not a positive integer", procsVar, value)
	}
	return n, nil
}

// listPattern returns the pattern of -test.list for the pattern of -test.run.
func listPattern(run string) string {
	if run == "" {
		return "."
	}
	return run
}

// childArgs returns the arguments of the child that runs one test: the pattern of the test first, so that no
// "--" or positional argument hides it, then args without -test.run, and -test.testlogfile with the suffix of
// the child.
func childArgs(args []string, name string, n int) []string {
	out := []string{"-test.run=^" + regexp.QuoteMeta(name) + "$"}
	for i := 0; i < len(args); i++ {
		flagName, value, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if !strings.HasPrefix(args[i], "-") || (flagName != "test.run" && flagName != "test.testlogfile") {
			out = append(out, args[i])
			continue
		}
		if !hasValue && i+1 < len(args) {
			i++
			value = args[i]
		}
		if flagName == "test.testlogfile" {
			out = append(out, "-test.testlogfile="+value+"."+strconv.Itoa(n))
		}
	}
	return out
}

// trimResult removes the final PASS or FAIL line of the output of a child, with or without framing.
func trimResult(out []byte) []byte {
	s := strings.TrimSuffix(string(out), "\n")
	i := strings.LastIndexByte(s, '\n')
	if last := strings.TrimPrefix(s[i+1:], framing); last != "PASS" && last != "FAIL" {
		return out
	}
	return []byte(s[:i+1])
}

// deadlineOf returns the deadline of a run that starts at start: only a positive timeout sets one.
func deadlineOf(start time.Time, timeout time.Duration) (time.Time, bool) {
	if timeout <= 0 {
		return time.Time{}, false
	}
	return start.Add(timeout), true
}

// controlLines are the lines of a test log for the variables that the runner reads before the log starts.
const controlLines = "getenv " + procsVar + "\ngetenv " + childVar + "\n"

// mergeLogs writes the test log of the run to dst: one header, the control lines, then the lines of the logs
// of the children. It removes the logs of the children.
func mergeLogs(dst string, parts []string) error {
	var b strings.Builder
	b.WriteString(logMagic + controlLines)
	for _, p := range parts {
		data, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read the test log %s: %w", p, err)
		}
		b.WriteString(strings.TrimPrefix(string(data), logMagic))
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("remove the test log %s: %w", p, err)
		}
	}
	if err := os.WriteFile(dst, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write the test log %s: %w", dst, err)
	}
	return nil
}

// recordControls appends the control lines to the test log of a run in one process.
func recordControls() {
	logfile := flagValue("test.testlogfile")
	if logfile == "" {
		return
	}
	f, err := os.OpenFile(logfile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return // no log, so the cache of go test keeps nothing
	}
	defer f.Close()
	f.WriteString(controlLines)
}

func flagValue(name string) string {
	if f := flag.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

// runInProcs runs the top-level tests in child processes. It returns ran false when the tests must run in
// this process, through m.Run.
func runInProcs() (ran bool, code int) {
	start := time.Now()
	if os.Getenv(childVar) != "" {
		return false, 0
	}
	procs, err := procsOf(os.Getenv(procsVar), runtime.GOMAXPROCS(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 2
	}
	f := procsFlags{procs: procs, cover: testing.CoverMode() != "", run: flagValue("test.run"),
		skip: flagValue("test.skip"), shuffle: flagValue("test.shuffle")}
	for _, name := range oneProcessFlags {
		if v := flagValue(name); v != "" {
			f.single = append(f.single, v)
		}
	}
	if f.oneProcess() {
		return false, 0
	}
	timeout, _ := flag.Lookup("test.timeout").Value.(flag.Getter).Get().(time.Duration)
	ctx := context.Background()
	if deadline, ok := deadlineOf(start, timeout); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	names, err := listTests(ctx, listPattern(f.run))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 2
	}
	if len(names) <= 1 {
		return false, 0
	}
	r := children{
		names:    names,
		procs:    procs,
		args:     os.Args[1:],
		failfast: flagValue("test.failfast") == "true",
		timeout:  timeout,
		out:      os.Stdout,
	}
	code = r.run(ctx)
	if logfile := flagValue("test.testlogfile"); logfile != "" {
		var parts []string
		for i := range names {
			parts = append(parts, logfile+"."+strconv.Itoa(i))
		}
		if err := mergeLogs(logfile, parts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	return true, code
}

var testName = regexp.MustCompile(`^(Test|Fuzz|Example)[^\s]*$`)

// listTests returns the top-level tests that pattern selects, through -test.list of this binary.
func listTests(ctx context.Context, pattern string) ([]string, error) {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.list="+pattern)
	cmd.Env = append(os.Environ(), childVar+"=1")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list the tests: %w", err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if testName.MatchString(line) {
			names = append(names, line)
		}
	}
	return names, nil
}

// children runs one child process for each name, procs at a time, and writes the output of each child whole.
type children struct {
	names    []string
	procs    int
	args     []string
	failfast bool
	timeout  time.Duration
	out      io.Writer
}

func (r children) run(ctx context.Context) int {
	var mu sync.Mutex
	failed := false
	next := make(chan int)
	go func() {
		for i := range r.names {
			next <- i
		}
		close(next)
	}()
	var wg sync.WaitGroup
	for range min(r.procs, len(r.names)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				var out bytes.Buffer
				cmd := r.child(ctx, i, &out)
				mu.Lock()
				// The check and the start share the lock, so no child starts after a failure under -failfast.
				if (r.failfast && failed) || ctx.Err() != nil {
					mu.Unlock()
					continue
				}
				err := cmd.Start()
				mu.Unlock()
				if err == nil {
					err = cmd.Wait()
				}
				mu.Lock()
				r.out.Write(trimResult(out.Bytes()))
				var exit *exec.ExitError
				switch {
				case ctx.Err() != nil:
					fmt.Fprintf(r.out, "%s timed out after %v, the timeout of the whole run\n", r.names[i], r.timeout)
				case err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1):
					fmt.Fprintf(r.out, "%s: the child process failed: %v\n", r.names[i], err)
				}
				if err != nil || ctx.Err() != nil {
					failed = true
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	prefix := ""
	if flagValue("test.v") == "test2json" {
		prefix = framing
	}
	if failed || ctx.Err() != nil {
		fmt.Fprint(r.out, prefix+"FAIL\n")
		return 1
	}
	fmt.Fprint(r.out, prefix+"PASS\n")
	return 0
}

// child returns the command that runs the test names[i] in a child process, with its output, standard error
// included, in out.
func (r children) child(ctx context.Context, i int, out *bytes.Buffer) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], childArgs(r.args, r.names[i], i)...)
	cmd.Env = append(os.Environ(), childVar+"=1")
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 10 * time.Second // a process that the test left behind may hold the output open
	return cmd
}

func TestListPattern(t *testing.T) {
	for run, want := range map[string]string{"": ".", "TestFold": "TestFold", "TestHelp|TestOrient": "TestHelp|TestOrient"} {
		if got := listPattern(run); got != want {
			t.Errorf("listPattern(%q) = %q, want %q", run, got, want)
		}
	}
}

func TestChildArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"the name replaces -test.run", []string{"-test.v=true", "-test.run=Test(A|B)", "-test.count=1"},
			[]string{"-test.run=^TestA$", "-test.v=true", "-test.count=1"}},
		{"a -test.run with its value apart", []string{"-test.run", "TestA", "-test.v=true"},
			[]string{"-test.run=^TestA$", "-test.v=true"}},
		{"the log file gets the suffix of the child", []string{"-test.testlogfile=/tmp/log", "-test.timeout=10m0s"},
			[]string{"-test.run=^TestA$", "-test.testlogfile=/tmp/log.3", "-test.timeout=10m0s"}},
		{"the pattern comes before -- and positional arguments", []string{"-test.v=true", "--", "extra"},
			[]string{"-test.run=^TestA$", "-test.v=true", "--", "extra"}},
		{"no -test.run", nil, []string{"-test.run=^TestA$"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := 0
			if strings.Contains(c.name, "suffix") {
				n = 3
			}
			if got := childArgs(c.args, "TestA", n); !slices.Equal(got, c.want) {
				t.Errorf("childArgs = %q, want %q", got, c.want)
			}
		})
	}
	if got := childArgs(nil, "TestA.b", 0); got[0] != `-test.run=^TestA\.b$` {
		t.Errorf("a name with a dot gives %q, want the dot quoted", got[0])
	}
}

func TestOneProcess(t *testing.T) {
	many := procsFlags{procs: 8}
	cases := []struct {
		name string
		f    procsFlags
		want bool
	}{
		{"many procs", many, false},
		{"a pattern without /", procsFlags{procs: 8, run: "TestHelp|TestOrient"}, false},
		{"shuffle off", procsFlags{procs: 8, shuffle: "off"}, false},
		{"a child", procsFlags{procs: 8, child: true}, true},
		{"one proc", procsFlags{procs: 1}, true},
		{"coverage", procsFlags{procs: 8, cover: true}, true},
		{"a pattern with /", procsFlags{procs: 8, run: "TestFold/an_uncommitted"}, true},
		{"skip", procsFlags{procs: 8, skip: "TestFold"}, true},
		{"shuffle on", procsFlags{procs: 8, shuffle: "on"}, true},
		{"a flag that needs one process", procsFlags{procs: 8, single: []string{"cpu.out"}}, true},
	}
	for _, c := range cases {
		if got := c.f.oneProcess(); got != c.want {
			t.Errorf("%s: oneProcess = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestProcs(t *testing.T) {
	for _, c := range []struct {
		value string
		want  int
	}{{"", 6}, {"1", 1}, {"8", 8}} {
		if got, err := procsOf(c.value, 6); err != nil || got != c.want {
			t.Errorf("procsOf(%q) = %d, %v, want %d", c.value, got, err, c.want)
		}
	}
	for _, value := range []string{"0", "-2", "x", "1.5"} {
		if _, err := procsOf(value, 6); err == nil || !strings.Contains(err.Error(), procsVar) {
			t.Errorf("procsOf(%q): error = %v, want one that names %s", value, err, procsVar)
		}
	}
}

func TestTrimResult(t *testing.T) {
	for in, want := range map[string]string{
		"=== RUN   TestA\n--- PASS: TestA (0.00s)\nPASS\n":         "=== RUN   TestA\n--- PASS: TestA (0.00s)\n",
		"--- FAIL: TestA (0.00s)\nFAIL\n":                          "--- FAIL: TestA (0.00s)\n",
		framing + "--- PASS: TestA (0.00s)\n" + framing + "PASS\n": framing + "--- PASS: TestA (0.00s)\n",
		"PASS\n":                     "",
		"panic: boom\ngoroutine 1\n": "panic: boom\ngoroutine 1\n",
		"":                           "",
	} {
		if got := string(trimResult([]byte(in))); got != want {
			t.Errorf("trimResult(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMergeLogs(t *testing.T) {
	dir := t.TempDir()
	parts := []string{dir + "/log.0", dir + "/log.1", dir + "/log.2"}
	writeT(t, dir, "log.0", logMagic+"getenv HOME\nopen /a\n")
	writeT(t, dir, "log.1", logMagic+"stat /b\n")
	if err := mergeLogs(dir+"/log", parts); err != nil { // log.2 is missing, as for a child that never started
		t.Fatal(err)
	}
	want := logMagic + controlLines + "getenv HOME\nopen /a\nstat /b\n"
	if got := readT(t, dir+"/log"); got != want {
		t.Errorf("the merged log = %q, want %q", got, want)
	}
	for _, p := range parts {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
}

func TestDeadline(t *testing.T) {
	start := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	if d, ok := deadlineOf(start, time.Minute); !ok || !d.Equal(start.Add(time.Minute)) {
		t.Errorf("a minute gives %v, %v", d, ok)
	}
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, ok := deadlineOf(start, timeout); ok {
			t.Errorf("%v gives a deadline", timeout)
		}
	}
}
