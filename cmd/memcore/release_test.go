package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// pinned matches a uses: line that names a full commit sha.
var pinned = regexp.MustCompile(`^\s*(- )?uses: [\w./-]+@[0-9a-f]{40}( #.*)?$`)

// workflow reads a workflow of the repo, and checks that every action in it is pinned to a commit.
func workflow(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../.github/workflows/" + name)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	uses := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "uses:") {
			uses++
			if !pinned.MatchString(line) {
				t.Errorf("%s: the action is not pinned to a commit sha: %q", name, line)
			}
		}
	}
	if uses == 0 {
		t.Errorf("%s uses no action", name)
	}
	return text
}

func TestReleaseWorkflow(t *testing.T) {
	text := workflow(t, "release.yml")
	for _, want := range []string{
		"tags: ['v*']",
		"CGO_ENABLED=0",
		"darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64",
		"-tags nonative", "-glibc", "-static",
		"SHA256SUMS",
		versionFlag + "${GITHUB_REF_NAME}",
		"actions/checkout@", "actions/setup-go@",
		"go test ./...",
		"gh release create",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release.yml holds no %q", want)
		}
	}
	checkHomebrewJob(t, text)
}

// checkHomebrewJob checks the job publish-homebrew: its dependency, its tag gate, and its steps in order. The tap
// token is read once, by the tap checkout alone, after the formula is rendered and checked and before it is published.
func checkHomebrewJob(t *testing.T, text string) {
	t.Helper()
	m := regexp.MustCompile(`(?ms)^  publish-homebrew:\n(.*?)(?:^  \S|\z)`).FindStringSubmatch(text)
	if m == nil {
		t.Fatal("release.yml has no job publish-homebrew")
	}
	job := m[1]
	for _, pattern := range []string{
		`(?m)^    needs: \[?release\]?\s*$`,
		`(?m)^    if: .*!\s*contains\(github\.ref_name,\s*'-'\)`,
		`(?m)^    permissions:\n      contents: read\s*$`,
		`(?m)^          GH_TOKEN: \$\{\{ github\.token \}\}\s*$`,
	} {
		if !regexp.MustCompile(pattern).MatchString(job) {
			t.Errorf("the job publish-homebrew does not match %s", pattern)
		}
	}

	const token = "secrets.HOMEBREW_TAP_TOKEN"
	if n := strings.Count(text, token); n != 1 || !strings.Contains(job, token) {
		t.Errorf("release.yml reads %s %d times, want once, in publish-homebrew", token, n)
	}
	for _, step := range strings.Split(job, "\n      - ") {
		if strings.Contains(step, "secrets.") && !(strings.Contains(step, "actions/checkout@") && strings.Contains(step, "repository: atmin/homebrew-tap")) {
			t.Errorf("a step besides the tap checkout reads a secret:\n%s", step)
		}
	}

	rest := job
	for _, part := range []string{".github/scripts/homebrew-formula", "ruby -c", token, ".github/scripts/publish-homebrew-formula"} {
		i := strings.Index(rest, part)
		if i < 0 {
			t.Fatalf("publish-homebrew has no %q after the steps before it", part)
		}
		rest = rest[i+len(part):]
	}
}

func TestPagesWorkflow(t *testing.T) {
	text := workflow(t, "pages.yml")
	for _, want := range []string{
		"actions/checkout@",
		"site/index.html", "site/install", "internal/ui/static/icon.svg",
		"path: _site",
		"actions/configure-pages@", "actions/upload-pages-artifact@", "actions/deploy-pages@",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("pages.yml holds no %q", want)
		}
	}
}

const homebrewScript = "../../.github/scripts/homebrew-formula"

// homebrewTargets are the executables of the formula, with the Homebrew selectors of each.
var homebrewTargets = []struct{ os, cpu, asset, arch string }{
	{"macos", "arm", "memcore-darwin-arm64", "arm64"},
	{"macos", "intel", "memcore-darwin-amd64", "x86_64"},
	{"linux", "arm", "memcore-linux-arm64-glibc", "arm64"},
	{"linux", "intel", "memcore-linux-amd64-glibc", "x86_64"},
}

// homebrewDigest is the fixture digest of target i. Each target has its own.
func homebrewDigest(i int) string { return fmt.Sprintf("%064x", 0xabc000+i) }

// homebrewSums returns the lines of a SHA256SUMS file with one digest per target.
func homebrewSums() []string {
	var lines []string
	for i, target := range homebrewTargets {
		lines = append(lines, homebrewDigest(i)+"  "+target.asset)
	}
	return lines
}

// runHomebrewFormula runs the generator on the tag and the lines of a checksums file.
func runHomebrewFormula(t *testing.T, tag string, sums []string) (stdout, stderr string, err error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SHA256SUMS")
	if err := os.WriteFile(path, []byte(strings.Join(sums, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	cmd := exec.Command(homebrewScript, tag, path)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

// TestHomebrewFormula runs every success and rejection case of the generator, so one -run pattern covers them.
func TestHomebrewFormula(t *testing.T) {
	t.Run("pins each target", testHomebrewFormulaPinsEachTarget)
	t.Run("rejects input", testHomebrewFormulaRejectsInput)
}

func testHomebrewFormulaPinsEachTarget(t *testing.T) {
	for _, tt := range []struct {
		tag, urlTag string
		// version is the formula version as Ruby data. It is empty when only the URL is checked.
		version string
	}{
		{"v1.0", "v1.0", `"1.0"`},
		{"v0.10.3", "v0.10.3", `"0.10.3"`},
		{"v7", "v7", `"7"`},
		{`v1.0"#{x}\`, "v1.0%22%23%7Bx%7D%5C", `"1.0\"\#{x}\\"`},
		{"vé1.0", "v%C3%A91.0", ""},
	} {
		t.Run(tt.tag, func(t *testing.T) {
			out, stderr, err := runHomebrewFormula(t, tt.tag, homebrewSums())
			if err != nil {
				t.Fatalf("homebrew-formula: %v\n%s", err, stderr)
			}
			if tt.version != "" && !strings.Contains(out, "version "+tt.version+"\n") {
				t.Errorf("the formula holds no version %s:\n%s", tt.version, out)
			}
			macos, linux, _ := strings.Cut(out, "on_linux do")
			for i, target := range homebrewTargets {
				section := macos
				if target.os == "linux" {
					section = linux
				}
				_, rest, ok := strings.Cut(section, "on_"+target.cpu+" do")
				if !ok {
					t.Fatalf("no on_%s block for %s:\n%s", target.cpu, target.os, out)
				}
				// The block runs up to the next one, or to the end of the section.
				block, _, _ := strings.Cut(rest, "on_")
				for _, want := range []string{
					"/releases/download/" + tt.urlTag + "/" + target.asset + `"`,
					`sha256 "` + homebrewDigest(i) + `"`,
					"depends_on arch: :" + target.arch,
				} {
					if !strings.Contains(block, want) {
						t.Errorf("the %s %s block holds no %q:\n%s", target.os, target.cpu, want, out)
					}
				}
				if n := strings.Count(out, homebrewDigest(i)); n != 1 {
					t.Errorf("digest %d is in the formula %d times, want 1", i, n)
				}
			}
		})
	}
}

func testHomebrewFormulaRejectsInput(t *testing.T) {
	good := homebrewSums()
	for _, tt := range []struct {
		name, tag string
		sums      []string
	}{
		{"empty tag", "", good},
		{"no v", "1.0", good},
		{"only v", "v", good},
		{"prerelease", "v1.0-rc.1", good},
		{"slash", "v1/2", good},
		{"space", "v1.0 x", good},
		{"newline", "v1.0\nx", good},
		{"control character", "v1.0\x01", good},
		{"no digests", "v1.0", nil},
		{"missing digest", "v1.0", good[1:]},
		{"duplicate digest", "v1.0", append(append([]string{}, good...), good[0])},
		{"short digest", "v1.0", append([]string{"abc  " + homebrewTargets[0].asset}, good[1:]...)},
		{"non-hex digest", "v1.0", append([]string{strings.Repeat("g", 64) + "  " + homebrewTargets[0].asset}, good[1:]...)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, stderr, err := runHomebrewFormula(t, tt.tag, tt.sums)
			if err == nil || out != "" || stderr == "" {
				t.Errorf("want an error with a reason and no formula, got err %v, stdout %q, stderr %q", err, out, stderr)
			}
		})
	}
}

const publishScript = "../../.github/scripts/publish-homebrew-formula"

const (
	tapBranch = "trunk"
	botAuthor = "github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>"
)

// tapFiles are the files of the tap besides the formula.
var tapFiles = map[string]string{"README.md": "the tap\n", "Formula/other.rb": "class Other < Formula\nend\n"}

// fixtureFormula stands in for the generated formula: a version line, and a body that tells two formulas of one
// version apart.
type fixtureFormula struct{ version, body string }

func (f fixtureFormula) text() string {
	return fmt.Sprintf("class Memcore < Formula\n  version %q\n  # %s\nend\n", f.version, f.body)
}

// gitIn runs git through store.Git, and fails the test on an error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := store.Git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// put writes a file, and the directories above it.
func put(t *testing.T, path, content string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// tap is a bare remote with a clone for the job, and a clone for a competing publisher.
type tap struct{ remote, checkout, peer string }

// commitPeer commits files to the tap from the clone of the peer.
func commitPeer(t *testing.T, peer string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		put(t, filepath.Join(peer, name), content, 0o644)
	}
	gitIn(t, peer, "add", ".")
	gitIn(t, peer, "commit", "-m", "peer")
	gitIn(t, peer, "push", "origin", "HEAD:"+tapBranch)
}

// newTap makes a tap that holds tapFiles and the formula.
func newTap(t *testing.T, formula string) tap {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	tp := tap{filepath.Join(root, "remote.git"), filepath.Join(root, "checkout"), filepath.Join(root, "peer")}
	gitIn(t, root, "init", "--bare", "-b", tapBranch, tp.remote)
	gitIn(t, root, "clone", tp.remote, tp.peer)
	gitIn(t, tp.peer, "config", "user.name", "Peer")
	gitIn(t, tp.peer, "config", "user.email", "peer@example.com")
	gitIn(t, tp.peer, "checkout", "-B", tapBranch)
	files := map[string]string{"Formula/memcore.rb": formula}
	for name, content := range tapFiles {
		files[name] = content
	}
	commitPeer(t, tp.peer, files)
	gitIn(t, root, "clone", tp.remote, tp.checkout)
	return tp
}

// at returns the content of a file of the tap at rev.
func (tp tap) at(t *testing.T, rev, name string) string {
	t.Helper()
	return gitIn(t, tp.remote, "show", rev+":"+name)
}

func (tp tap) head(t *testing.T) string {
	t.Helper()
	return gitIn(t, tp.remote, "rev-parse", "refs/heads/"+tapBranch)
}

// publication is one run of the publish script.
type publication struct {
	candidate fixtureFormula
	// latest holds the tags that successive lookups of gh report, and the last one repeats. None fails the lookup.
	latest []string
	// peers are the versions that competing runs publish, one at each push of the job. A version with the suffix
	// +latest also becomes the latest release.
	peers  []string
	gitDir bool   // the environment points GIT_DIR at another repo
	tag    string // the tag to publish, when it is not v and the version of the candidate
}

// hookMarker and the fixture root are the reserved trailing arguments of the pre-push hook. TestPublishHook reads
// nothing from the environment, so an ordinary go test never acts on a repo.
const hookMarker = "memcore-homebrew-hook"

// runPublish runs the script on the checkout of the tap, with a fake gh ahead on PATH. The pre-push hook in the
// clone runs TestPublishHook, which lets the peers publish while the job waits at its push.
func runPublish(t *testing.T, tp tap, p publication) (output string, pushes int, err error) {
	t.Helper()
	script, err := filepath.Abs(publishScript)
	if err != nil {
		t.Fatal(err)
	}
	test, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(tp.checkout)
	dir := filepath.Join(root, "fake")
	put(t, filepath.Join(dir, "peers"), strings.Join(p.peers, "\n")+"\n", 0o644)
	if len(p.latest) > 0 {
		put(t, filepath.Join(dir, "latest"), strings.Join(p.latest, "\n")+"\n", 0o644)
	}
	put(t, filepath.Join(dir, "bin/gh"), `#!/bin/sh
f="$FAKE_DIR/latest"
[ -s "$f" ] || { echo "fake gh: no latest release" >&2; exit 1; }
head -n 1 "$f"
if [ "$(sed -n '$=' "$f")" -gt 1 ]; then sed 1d "$f" >"$f.next" && mv "$f.next" "$f"; fi
exit 0
`, 0o755)
	put(t, filepath.Join(tp.checkout, ".git/hooks/pre-push"), fmt.Sprintf("#!/bin/sh\nexec %q -test.run='^TestPublishHook$' -- %s %q >&2\n", test, hookMarker, root), 0o755)
	candidate := put(t, filepath.Join(dir, "candidate.rb"), p.candidate.text(), 0o644)
	tag := p.tag
	if tag == "" {
		tag = "v" + p.candidate.version
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, script, tag, candidate, tp.checkout)
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_TOKEN=builtin-read-token", "FAKE_DIR="+dir)
	var sentinel, sentinelHead string
	if p.gitDir {
		sentinel = t.TempDir()
		gitIn(t, sentinel, "init")
		gitIn(t, sentinel, "-c", "user.name=Sentinel", "-c", "user.email=sentinel@example.com", "commit", "--allow-empty", "-m", "sentinel")
		sentinelHead = gitIn(t, sentinel, "rev-parse", "HEAD")
		cmd.Dir = sentinel
		cmd.Env = append(cmd.Env, "GIT_DIR="+filepath.Join(sentinel, ".git"))
	}
	out, err := cmd.CombinedOutput()
	if p.gitDir && (gitIn(t, sentinel, "rev-parse", "HEAD") != sentinelHead || gitIn(t, sentinel, "status", "--porcelain") != "") {
		t.Error("the job changed the repo of GIT_DIR")
	}
	log, _ := os.ReadFile(filepath.Join(dir, "pushes"))
	return string(out), len(log), err
}

// TestPublishHook is the pre-push hook of runPublish. It runs only with the trailing arguments hookMarker and the
// fixture root, and takes every path from that root. At the nth push of the job it publishes the nth version of the
// file peers from the peer clone, when there is one.
func TestPublishHook(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-2] != hookMarker {
		t.Skip("runs only as the hook of TestPublishHomebrewFormula")
	}
	root := args[len(args)-1]
	dir, peer := filepath.Join(root, "fake"), filepath.Join(root, "peer")
	log, _ := os.ReadFile(filepath.Join(dir, "pushes"))
	put(t, filepath.Join(dir, "pushes"), string(log)+".", 0o644)
	list, _ := os.ReadFile(filepath.Join(dir, "peers"))
	peers := strings.Fields(string(list))
	if len(log) >= len(peers) {
		return
	}
	version, latest := strings.CutSuffix(peers[len(log)], "+latest")
	gitIn(t, peer, "fetch", "origin")
	gitIn(t, peer, "reset", "--hard", "origin/"+tapBranch)
	commitPeer(t, peer, map[string]string{"Formula/memcore.rb": fixtureFormula{version, "peer"}.text()})
	if latest {
		put(t, filepath.Join(dir, "latest"), "v"+version+"\n", 0o644)
	}
}

func TestPublishHomebrewFormula(t *testing.T) {
	v9, v10, v11, v12 := fixtureFormula{"0.9.0", "tap"}, fixtureFormula{"0.10.0", "tap"}, fixtureFormula{"0.11.0", "tap"}, fixtureFormula{"0.12.0", "tap"}
	for _, tt := range []struct {
		name     string
		tap      string // the formula in the tap
		run      publication
		fail     bool
		pushed   bool     // the job commits to the tap
		want     string   // the formula in the tap at the end
		mentions []string // what a failure says
	}{
		{name: "newer", tap: v10.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			pushed: true, want: v11.text()},
		{name: "newer with GIT_DIR set", tap: v10.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}, gitDir: true},
			pushed: true, want: v11.text()},
		{name: "identical", tap: v11.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			want: v11.text()},
		{name: "stale, the tap holds the latest", tap: v12.text(), run: publication{candidate: v10, latest: []string{"v0.12.0"}},
			want: v12.text()},
		{name: "stale, the tap is behind the latest", tap: v9.text(), run: publication{candidate: v10, latest: []string{"v0.12.0"}},
			want: v9.text()},
		{name: "conflicting", tap: fixtureFormula{"0.11.0", "edited"}.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			fail: true, want: fixtureFormula{"0.11.0", "edited"}.text()},
		{name: "downgrade", tap: v12.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			fail: true, want: v12.text(), mentions: []string{"0.11.0", "0.12.0"}},
		{name: "unreadable", tap: "not a formula\n", run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			fail: true, want: "not a formula\n"},
		{name: "tap version is empty", tap: fixtureFormula{"", "tap"}.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			fail: true, want: fixtureFormula{"", "tap"}.text()},
		{name: "tap version ends in an escaped newline", tap: fixtureFormula{"0.10.0\n", "tap"}.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			fail: true, want: fixtureFormula{"0.10.0\n", "tap"}.text()},
		{name: "tap version has an escaped space", tap: fixtureFormula{"0.10 0", "tap"}.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			fail: true, want: fixtureFormula{"0.10 0", "tap"}.text()},
		{name: "tap version has an escaped control character", tap: fixtureFormula{"0.10\x01", "tap"}.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			fail: true, want: fixtureFormula{"0.10\x01", "tap"}.text()},
		{name: "tap version has a hyphen", tap: fixtureFormula{"0.10-1", "tap"}.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			fail: true, want: fixtureFormula{"0.10-1", "tap"}.text()},
		{name: "tap version has a slash", tap: fixtureFormula{"0.10/1", "tap"}.text(), run: publication{candidate: v11, latest: []string{"v0.11.0"}},
			fail: true, want: fixtureFormula{"0.10/1", "tap"}.text()},
		{name: "candidate version is empty", tap: v10.text(), run: publication{candidate: fixtureFormula{"", "new"}, tag: "v0.11.0", latest: []string{"v0.11.0"}},
			fail: true, want: v10.text()},
		{name: "candidate version ends in an escaped newline", tap: v10.text(),
			run:  publication{candidate: fixtureFormula{"0.11.0\n", "new"}, tag: "v0.11.0", latest: []string{"v0.11.0"}},
			fail: true, want: v10.text()},
		{name: "identical formulas with an escaped newline", tap: fixtureFormula{"0.11.0\n", "new"}.text(),
			run:  publication{candidate: fixtureFormula{"0.11.0\n", "new"}, tag: "v0.11.0", latest: []string{"v0.11.0"}},
			fail: true, want: fixtureFormula{"0.11.0\n", "new"}.text()},
		{name: "gh fails", tap: v10.text(), run: publication{candidate: v11},
			fail: true, want: v10.text()},
		{name: "latest lags", tap: v10.text(), run: publication{candidate: v12, latest: []string{"v0.11.0"}},
			fail: true, want: v10.text()},
		{name: "latest catches up", tap: v10.text(), run: publication{candidate: v12, latest: []string{"v0.11.0", "v0.12.0"}},
			pushed: true, want: v12.text()},
		{name: "a peer publishes an older version first", tap: v10.text(),
			run:    publication{candidate: v12, latest: []string{"v0.12.0"}, peers: []string{"0.11.0"}},
			pushed: true, want: v12.text()},
		{name: "a peer publishes an older version first with GIT_DIR set", tap: v10.text(),
			run:    publication{candidate: v12, latest: []string{"v0.12.0"}, peers: []string{"0.11.0"}, gitDir: true},
			pushed: true, want: v12.text()},
		{name: "a peer publishes a newer version first", tap: v10.text(),
			run:  publication{candidate: v11, latest: []string{"v0.11.0"}, peers: []string{"0.12.0+latest"}},
			want: fixtureFormula{"0.12.0", "peer"}.text()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tp := newTap(t, tt.tap)
			start := tp.head(t)
			output, _, err := runPublish(t, tp, tt.run)
			if (err != nil) != tt.fail {
				t.Errorf("the job failed: %v, want %v\n%s", err != nil, tt.fail, output)
			}
			for _, version := range tt.mentions {
				if !strings.Contains(output, version) {
					t.Errorf("the failure does not mention %s:\n%s", version, output)
				}
			}
			final := tp.head(t)
			if got := tp.at(t, final, "Formula/memcore.rb"); got != strings.TrimSpace(tt.want) {
				t.Errorf("the tap ends with\n%s\nwant\n%s", got, tt.want)
			}
			for name, content := range tapFiles {
				if got := tp.at(t, final, name); got != strings.TrimSpace(content) {
					t.Errorf("%s is now %q", name, got)
				}
			}
			if _, err := store.Git(tp.remote, "merge-base", "--is-ancestor", start, final); err != nil {
				t.Errorf("the tap did not move forward: %v", err)
			}
			if author := gitIn(t, tp.remote, "log", "-1", "--format=%an <%ae>", final); (author == botAuthor) != tt.pushed {
				t.Errorf("the head of the tap is by %q, the job should push: %v", author, tt.pushed)
			}
			if tt.pushed {
				if names := gitIn(t, tp.remote, "diff", "--name-only", final+"^", final); names != "Formula/memcore.rb" {
					t.Errorf("the job changed %q, want only Formula/memcore.rb", names)
				}
			}
		})
	}

	// A committed symlink must not let the job write outside the checkout.
	for _, link := range []string{"Formula", "Formula/memcore.rb"} {
		t.Run("symlinked "+link, func(t *testing.T) {
			tp := newTap(t, v10.text())
			outside := t.TempDir()
			external := put(t, filepath.Join(outside, "memcore.rb"), v10.text(), 0o644)
			target := outside
			if link != "Formula" {
				target = external
			}
			gitIn(t, tp.peer, "rm", "-r", "-q", "Formula")
			if err := os.MkdirAll(filepath.Join(tp.peer, filepath.Dir(link)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(tp.peer, link)); err != nil {
				t.Fatal(err)
			}
			gitIn(t, tp.peer, "add", ".")
			gitIn(t, tp.peer, "commit", "-m", "symlink")
			gitIn(t, tp.peer, "push", "origin", "HEAD:"+tapBranch)
			start := tp.head(t)

			output, _, err := runPublish(t, tp, publication{candidate: v11, latest: []string{"v0.11.0"}})
			if err == nil {
				t.Errorf("the job succeeded on a symlinked %s:\n%s", link, output)
			}
			if got, _ := os.ReadFile(external); string(got) != v10.text() {
				t.Errorf("the job wrote the file outside the tap:\n%s", got)
			}
			if tp.head(t) != start {
				t.Error("the job moved the tap")
			}
		})
	}

	t.Run("repeated collisions end the job", func(t *testing.T) {
		tp := newTap(t, v10.text())
		peers := strings.Fields("0.10.1 0.10.2 0.10.3 0.10.4 0.10.5 0.10.6 0.10.7 0.10.8")
		output, pushes, err := runPublish(t, tp, publication{candidate: v12, latest: []string{"v0.12.0"}, peers: peers})
		if err == nil {
			t.Errorf("the job succeeded against peers that win every race:\n%s", output)
		}
		if pushes < 2 || pushes > 4 {
			t.Errorf("the job pushed %d times, want a retry and at most 4", pushes)
		}
		if author := gitIn(t, tp.remote, "log", "-1", "--format=%an <%ae>", tp.head(t)); author == botAuthor {
			t.Error("the job won a race that the peers should win")
		}
	})
}

// TestNativeBuilds builds memcore with CGO_ENABLED=0 for each target of a release. Each build holds glaze, the
// native window, and the build with -tags nonative holds none.
func TestNativeBuilds(t *testing.T) {
	for _, tt := range []struct {
		goos, goarch, tags string
		glaze              bool
	}{
		{"darwin", "arm64", "", true},
		{"darwin", "amd64", "", true},
		{"linux", "arm64", "", true},
		{"linux", "amd64", "", true},
		{"linux", "amd64", "nonative", false},
	} {
		t.Run(tt.goos+"/"+tt.goarch+" "+tt.tags, func(t *testing.T) {
			t.Parallel()
			bin := filepath.Join(t.TempDir(), "memcore")
			build := exec.Command("go", "build", "-tags", tt.tags, "-o", bin, ".")
			build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+tt.goos, "GOARCH="+tt.goarch)
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}
			out, err := exec.Command("go", "version", "-m", bin).CombinedOutput()
			if err != nil {
				t.Fatalf("go version -m: %v\n%s", err, out)
			}
			if got := strings.Contains(string(out), "github.com/crgimenes/glaze"); got != tt.glaze {
				t.Errorf("the build holds glaze: %v, want %v\n%s", got, tt.glaze, out)
			}
		})
	}
}
