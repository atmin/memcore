// Package site holds the files of memco.re. Its tests run the install script against a fake release.
package site

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeMemcore is the executable of the fake release. It prints a version, as memcore version does.
const fakeMemcore = "#!/bin/sh\necho 'memcore v0.0.0-test'\n"

// release is a fake release on a test server, which counts the requests that it gets.
type release struct {
	url      string
	requests atomic.Int64
}

// serve starts a release with the assets, and SHA256SUMS with the given text.
func serve(t *testing.T, assets map[string]string, sums string) *release {
	t.Helper()
	r := &release{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		name := strings.TrimPrefix(req.URL.Path, "/")
		if name == "SHA256SUMS" {
			fmt.Fprint(w, sums)
			return
		}
		body, ok := assets[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	r.url = srv.URL
	return r
}

// sumLine returns the line of SHA256SUMS for an asset, in the format of sha256sum.
func sumLine(name, body string) string {
	return fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(body)), name)
}

// assetNames are the executables of a release: one build for each macOS processor, and two for each Linux processor.
var assetNames = []string{
	"memcore-darwin-arm64", "memcore-darwin-amd64",
	"memcore-linux-arm64-glibc", "memcore-linux-arm64-static",
	"memcore-linux-amd64-glibc", "memcore-linux-amd64-static",
}

// loaders are the glibc loaders that pick the glibc build, by processor.
var loaders = map[string]string{"arm64": "/lib/ld-linux-aarch64.so.1", "amd64": "/lib64/ld-linux-x86-64.so.2"}

// fullRelease returns the assets of all targets, and their SHA256SUMS.
func fullRelease() (map[string]string, string) {
	assets := map[string]string{}
	var sums strings.Builder
	for _, name := range assetNames {
		assets[name] = fakeMemcore
		sums.WriteString(sumLine(name, fakeMemcore))
	}
	return assets, sums.String()
}

// fakeUname returns a folder that holds a uname that prints the system and the processor.
func fakeUname(t *testing.T, system, processor string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n-s) echo %s ;;\n-m) echo %s ;;\nesac\n", system, processor)
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// script returns the install script.
func script(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("install")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// install pipes the script into the shell, with only the environment that the test sets. The loaders of
// the glibc build are looked up under root, so the test decides which Linux build the script picks.
func install(shell string, text []byte, home, url, path, root string) (string, error) {
	cmd := exec.Command(shell)
	cmd.Stdin = bytes.NewReader(text)
	cmd.Env = []string{"HOME=" + home, "MEMCORE_RELEASE_URL=" + url, "PATH=" + path, "MEMCORE_INSTALL_ROOT=" + root}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		cmd.Env = append(cmd.Env, "TMPDIR="+tmp)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// staged returns the staging files that an install left in a folder.
func staged(t *testing.T, bin string) []string {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(bin, ".memcore.*"))
	if err != nil {
		t.Fatal(err)
	}
	return left
}

func TestInstall(t *testing.T) {
	tests := []struct {
		system, processor string
		loader            string // the loader that exists under the root, if any
		asset             string
	}{
		{"Darwin", "arm64", "", "memcore-darwin-arm64"},
		{"Darwin", "x86_64", "", "memcore-darwin-amd64"},
		{"Linux", "aarch64", loaders["arm64"], "memcore-linux-arm64-glibc"},
		{"Linux", "aarch64", "", "memcore-linux-arm64-static"},
		{"Linux", "x86_64", loaders["amd64"], "memcore-linux-amd64-glibc"},
		{"Linux", "x86_64", "", "memcore-linux-amd64-static"},
		{"Linux", "x86_64", loaders["arm64"], "memcore-linux-amd64-static"},
	}
	for _, tt := range tests {
		t.Run(tt.system+" "+tt.processor+" "+tt.loader, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tt.loader != "" {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, tt.loader)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, tt.loader), nil, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			// Only the asset of the target holds a working executable, so a wrong choice fails the version.
			assets, sums := map[string]string{}, ""
			for _, name := range assetNames {
				body := "#!/bin/sh\nexit 3\n"
				if name == tt.asset {
					body = fakeMemcore
				}
				assets[name] = body
				sums += sumLine(name, body)
			}
			r := serve(t, assets, sums)
			home := t.TempDir()
			out, err := install("sh", script(t), home, r.url, fakeUname(t, tt.system, tt.processor)+":"+os.Getenv("PATH"), root)
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			bin := filepath.Join(home, ".local", "bin")
			info, err := os.Stat(filepath.Join(bin, "memcore"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o755 {
				t.Errorf("mode = %v, want 0755", info.Mode().Perm())
			}
			if !strings.Contains(out, "memcore v0.0.0-test") {
				t.Errorf("the output holds no version:\n%s", out)
			}
			if left := staged(t, bin); len(left) != 0 {
				t.Errorf("staging files left: %v", left)
			}
		})
	}
}

func TestInstallRefuses(t *testing.T) {
	assets, sums := fullRelease()
	tests := []struct {
		name, system, processor string
		assets                  map[string]string
		sums                    string
		folder                  bool // ~/.local/bin/memcore is a folder
	}{
		{"a wrong digest", "Linux", "x86_64", assets, sumLine("memcore-linux-amd64-static", "another executable"), false},
		{"a target missing from SHA256SUMS", "Linux", "x86_64", assets, sumLine("memcore-darwin-arm64", fakeMemcore), false},
		{"a 404 for the asset", "Linux", "x86_64", map[string]string{}, sums, false},
		{"an unsupported system", "FreeBSD", "amd64", assets, sums, false},
		{"an unsupported processor", "Linux", "i686", assets, sums, false},
		{"an executable that does not run", "Linux", "x86_64", map[string]string{"memcore-linux-amd64-static": "#!/bin/sh\nexit 3\n"}, sumLine("memcore-linux-amd64-static", "#!/bin/sh\nexit 3\n"), false},
		{"memcore is a folder", "Linux", "x86_64", assets, sums, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := serve(t, tt.assets, tt.sums)
			home := t.TempDir()
			bin := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			const old = "#!/bin/sh\necho 'memcore v0.0.0-old'\n"
			existing := filepath.Join(bin, "memcore")
			if tt.folder {
				existing = filepath.Join(bin, "memcore", "file")
				if err := os.Mkdir(filepath.Join(bin, "memcore"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(existing, []byte(old), 0o755); err != nil {
				t.Fatal(err)
			}
			out, err := install("sh", script(t), home, r.url, fakeUname(t, tt.system, tt.processor)+":"+os.Getenv("PATH"), t.TempDir())
			if err == nil {
				t.Fatalf("install succeeded:\n%s", out)
			}
			if !strings.Contains(out, "memcore install:") {
				t.Errorf("the output names no failure of the install:\n%s", out)
			}
			if got, _ := os.ReadFile(existing); string(got) != old {
				t.Errorf("the existing memcore changed to %q", got)
			}
			if entries, _ := os.ReadDir(filepath.Join(bin, "memcore")); tt.folder && len(entries) != 1 {
				t.Errorf("the folder memcore holds %d entries, want 1", len(entries))
			}
			if left := staged(t, bin); len(left) != 0 {
				t.Errorf("staging files left: %v", left)
			}
		})
	}
}

func TestInstallPrefixes(t *testing.T) {
	text := script(t)
	end := bytes.LastIndex(text, []byte("\n}"))
	if end < 0 {
		t.Fatal("the script has no closing } line")
	}
	end++ // the length of the longest prefix without the last }
	shells := []string{"sh"}
	if _, err := exec.LookPath("dash"); err == nil {
		shells = append(shells, "dash")
	}
	assets, sums := fullRelease()
	r := serve(t, assets, sums)
	home := t.TempDir()
	path := fakeUname(t, "Linux", "x86_64") + ":" + os.Getenv("PATH")

	type job struct {
		shell string
		n     int
	}
	jobs := make(chan job)
	var mu sync.Mutex
	var ran []string
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				out, _ := install(j.shell, text[:j.n], home, r.url, path, home)
				if strings.Contains(out, "memcore v0.0.0-test") {
					mu.Lock()
					ran = append(ran, fmt.Sprintf("%s at %d bytes", j.shell, j.n))
					mu.Unlock()
				}
			}
		}()
	}
	for _, shell := range shells {
		for n := 1; n <= end; n++ {
			jobs <- job{shell, n}
		}
	}
	close(jobs)
	wg.Wait()

	if len(ran) != 0 {
		t.Errorf("prefixes installed memcore: %v", ran)
	}
	if n := r.requests.Load(); n != 0 {
		t.Errorf("the prefixes made %d requests, want 0", n)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("the prefixes wrote %d entries into HOME", len(entries))
	}
}

func TestInstallPath(t *testing.T) {
	for _, onPath := range []bool{true, false} {
		t.Run(fmt.Sprintf("on PATH %v", onPath), func(t *testing.T) {
			t.Parallel()
			assets, sums := fullRelease()
			r := serve(t, assets, sums)
			home := t.TempDir()
			path := fakeUname(t, "Linux", "x86_64") + ":" + os.Getenv("PATH")
			if onPath {
				path = filepath.Join(home, ".local", "bin") + ":" + path
			}
			out, err := install("sh", script(t), home, r.url, path, t.TempDir())
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			if mentions := strings.Contains(out, "PATH"); mentions == onPath {
				t.Errorf("on PATH %v, the output mentions PATH: %v\n%s", onPath, mentions, out)
			}
			if !onPath && !strings.Contains(out, "~/.local/bin") {
				t.Errorf("the output does not name ~/.local/bin:\n%s", out)
			}
		})
	}
}
