package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
