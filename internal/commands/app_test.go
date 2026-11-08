package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeApp makes the application of the window testable on one machine: the system, the executable that the
// launcher runs, and an lsregister that records its arguments in a file. It returns the home and that file.
func fakeApp(t *testing.T, system string, lsExit int) (home, lsCalls string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MEMCORE_APP_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("MEMCORE_REPO", "")
	bin := t.TempDir()
	// The fake memcore records its arguments, PATH and MEMCORE_REPO, with shell builtins only.
	exe := filepath.Join(bin, "memcore")
	writeExec(t, exe, "#!/bin/sh\nprintf '%s\\n' \"$*\" \"$PATH\" \"${MEMCORE_REPO-unset}\" > \"$HOME/ran\"\n")
	lsCalls = filepath.Join(bin, "lsregister-calls")
	ls := filepath.Join(bin, "lsregister")
	writeExec(t, ls, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+shellQuote(lsCalls)+"\nexit "+string(rune('0'+lsExit))+"\n")
	os0, ls0, exe0 := appOS, lsregister, appExecutable
	appOS, lsregister = system, ls
	appExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { appOS, lsregister, appExecutable = os0, ls0, exe0 })
	return home, lsCalls
}

func writeExec(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
}

// launch runs a launcher with sh, a bare environment and the home, and returns what the fake memcore recorded.
func launch(t *testing.T, launcher, home string) []string {
	t.Helper()
	cmd := exec.Command("/bin/sh", launcher)
	cmd.Env = []string{"HOME=" + home}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run the launcher: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(home, "ran"))
	if err != nil {
		t.Fatalf("the launcher ran no memcore: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func modeT(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestUIInstall(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	for _, tt := range []struct {
		name, system, repo string
		appDir, dataDir    bool // set MEMCORE_APP_DIR or XDG_DATA_HOME
	}{
		{name: "macOS", system: "darwin"},
		{name: "macOS with MEMCORE_REPO", system: "darwin", repo: "/repos/my memory"},
		{name: "macOS with MEMCORE_APP_DIR", system: "darwin", appDir: true},
		{name: "Linux", system: "linux"},
		{name: "Linux with MEMCORE_REPO", system: "linux", repo: "/repos/it's mine"},
		{name: "Linux with XDG_DATA_HOME", system: "linux", dataDir: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, lsCalls := fakeApp(t, tt.system, 0)
			t.Setenv("MEMCORE_REPO", tt.repo)
			apps := filepath.Join(home, "Applications")
			if tt.appDir {
				apps = filepath.Join(t.TempDir(), "apps")
				t.Setenv("MEMCORE_APP_DIR", apps)
			}
			data := filepath.Join(home, ".local", "share")
			if tt.dataDir {
				data = filepath.Join(t.TempDir(), "data")
				t.Setenv("XDG_DATA_HOME", data)
			}
			for round := 1; round <= 2; round++ { // a second install replaces the first
				if out, err := run(t, home, "", "ui", "--install"); err != nil {
					t.Fatalf("ui --install, round %d: %v\n%s", round, err, out)
				}
			}
			wantRepo := tt.repo
			if wantRepo == "" {
				wantRepo = "unset"
			}
			var launcher string
			switch tt.system {
			case "darwin":
				app := filepath.Join(apps, "memcore.app")
				launcher = filepath.Join(app, "Contents", "MacOS", "memcore")
				plist := readT(t, filepath.Join(app, "Contents", "Info.plist"))
				for _, want := range []string{"<string>re.memco.memcore</string>", "<key>CFBundleExecutable</key><string>memcore</string>", "<key>CFBundleIconFile</key><string>memcore</string>"} {
					if !strings.Contains(plist, want) {
						t.Errorf("Info.plist holds no %s:\n%s", want, plist)
					}
				}
				if strings.Contains(plist, "LSEnvironment") {
					t.Errorf("Info.plist sets an environment:\n%s", plist)
				}
				if icns := readT(t, filepath.Join(app, "Contents", "Resources", "memcore.icns")); !strings.HasPrefix(icns, "icns") {
					t.Errorf("memcore.icns starts with %q", icns[:min(4, len(icns))])
				}
				if got, want := readT(t, lsCalls), "-f "+app+"\n-f "+app+"\n"; got != want {
					t.Errorf("lsregister got %q, want %q", got, want)
				}
				if entries, _ := os.ReadDir(apps); len(entries) != 1 {
					t.Errorf("%s holds %d entries, want only memcore.app", apps, len(entries))
				}
			case "linux":
				launcher = filepath.Join(data, "memcore", "launch")
				entry := readT(t, filepath.Join(data, "applications", "memcore.desktop"))
				if want := "\nExec=" + desktopExec(launcher) + "\n"; !strings.Contains(entry, want) {
					t.Errorf("the desktop entry holds no %q:\n%s", want, entry)
				}
				if !strings.Contains(entry, "\nIcon=memcore\n") {
					t.Errorf("the desktop entry names no icon:\n%s", entry)
				}
				if png := readT(t, filepath.Join(data, "icons", "hicolor", "256x256", "apps", "memcore.png")); !strings.HasPrefix(png, "\x89PNG") {
					t.Error("memcore.png is no PNG")
				}
				if _, err := os.Stat(lsCalls); err == nil {
					t.Error("Linux ran lsregister")
				}
			}
			if m := modeT(t, launcher); m != 0o755 {
				t.Errorf("the launcher has mode %v", m)
			}
			ran := launch(t, launcher, home)
			if want := []string{"ui", os.Getenv("PATH"), wantRepo}; strings.Join(ran, "\n") != strings.Join(want, "\n") {
				t.Errorf("the launcher ran memcore with %q, want %q", ran, want)
			}
			if tt.system == "darwin" {
				if _, err := os.Stat(filepath.Join(home, "Library", "Logs", "memcore.log")); err != nil {
					t.Errorf("the launcher wrote no log: %v", err)
				}
			}
		})
	}

	t.Run("lsregister fails", func(t *testing.T) {
		home, _ := fakeApp(t, "darwin", 1)
		out, err := run(t, home, "", "ui", "--install")
		if err != nil {
			t.Fatalf("ui --install: %v\n%s", err, out)
		}
		if !strings.Contains(out, "lsregister failed") {
			t.Errorf("no warning about lsregister:\n%s", out)
		}
	})

	t.Run("a foreign memcore.app stays", func(t *testing.T) {
		for name, plist := range map[string]string{
			"another identifier":              "<plist><dict><key>CFBundleIdentifier</key><string>com.example.memcore</string></dict></plist>",
			"the identifier in a comment":     "<plist><dict><!-- <key>CFBundleIdentifier</key><string>re.memco.memcore</string> --><key>CFBundleIdentifier</key><string>com.example.memcore</string></dict></plist>",
			"the identifier in a nested dict": "<plist><dict><key>Other</key><dict><key>CFBundleIdentifier</key><string>re.memco.memcore</string></dict></dict></plist>",
			"no Info.plist":                   "",
		} {
			t.Run(name, func(t *testing.T) {
				home, lsCalls := fakeApp(t, "darwin", 0)
				contents := filepath.Join(home, "Applications", "memcore.app", "Contents")
				mkdirT(t, contents)
				if plist != "" {
					writeT(t, contents, "Info.plist", plist)
				}
				for _, flag := range []string{"--install", "--uninstall"} {
					out, err := run(t, home, "", "ui", flag)
					if err == nil || !strings.Contains(err.Error(), "memcore did not write") {
						t.Errorf("ui %s: %v, want a refusal\n%s", flag, err, out)
					}
				}
				if plist != "" && readT(t, filepath.Join(contents, "Info.plist")) != plist {
					t.Error("the foreign Info.plist changed")
				}
				want := 0
				if plist != "" {
					want = 1
				}
				if entries, _ := os.ReadDir(contents); len(entries) != want {
					t.Errorf("the foreign bundle holds %d entries, want %d", len(entries), want)
				}
				if _, err := os.Stat(lsCalls); err == nil {
					t.Error("a refusal ran lsregister")
				}
			})
		}
	})
}

func TestUIUninstall(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	for _, system := range []string{"darwin", "linux"} {
		t.Run(system, func(t *testing.T) {
			home, lsCalls := fakeApp(t, system, 0)
			if out, err := run(t, home, "", "ui", "--install"); err != nil {
				t.Fatalf("ui --install: %v\n%s", err, out)
			}
			// A file of another program in the data folder stays.
			other := filepath.Join(home, ".local", "share", "applications", "other.desktop")
			writeT(t, filepath.Dir(other), "other.desktop", "[Desktop Entry]\n")
			out, err := run(t, home, "", "ui", "--uninstall")
			if err != nil {
				t.Fatalf("ui --uninstall: %v\n%s", err, out)
			}
			var gone []string
			switch system {
			case "darwin":
				app := filepath.Join(home, "Applications", "memcore.app")
				gone = []string{app}
				if got, want := readT(t, lsCalls), "-f "+app+"\n-u "+app+"\n"; got != want {
					t.Errorf("lsregister got %q, want %q", got, want)
				}
			case "linux":
				data := filepath.Join(home, ".local", "share")
				gone = []string{filepath.Join(data, "memcore"), filepath.Join(data, "applications", "memcore.desktop"),
					filepath.Join(data, "icons", "hicolor", "256x256", "apps", "memcore.png")}
			}
			for _, p := range gone {
				if _, err := os.Stat(p); err == nil {
					t.Errorf("%s stays", p)
				}
				if !strings.Contains(out, "Removed "+tilde(p)) {
					t.Errorf("the output names no %s:\n%s", tilde(p), out)
				}
			}
			if _, err := os.Stat(other); err != nil {
				t.Errorf("the desktop entry of another program is gone: %v", err)
			}
			out, err = run(t, home, "", "ui", "--uninstall")
			if err != nil || !strings.Contains(out, "nothing to remove") {
				t.Errorf("a second ui --uninstall: %v\n%s", err, out)
			}
		})
	}
}

func TestUIInstallNoRepo(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, _ := fakeApp(t, "darwin", 0)
	t.Setenv("MEMCORE_REPO", filepath.Join(home, "no-such-repo"))
	for _, flag := range []string{"--install", "--uninstall"} {
		if out, err := run(t, home, "", "ui", flag); err != nil {
			t.Errorf("ui %s without a memory repo: %v\n%s", flag, err, out)
		}
	}
}

func TestUIFlags(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	home, _ := fakeApp(t, "darwin", 0)
	for _, args := range [][]string{{"ui", "--install", "--uninstall"}, {"ui", "--install", "--serve"}, {"ui", "--uninstall", "x"}} {
		if _, err := run(t, home, "", args...); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%q: %v, want a usage error", args, err)
		}
	}
	t.Run("another system", func(t *testing.T) {
		home, _ := fakeApp(t, "windows", 0)
		if _, err := run(t, home, "", "ui", "--install"); err == nil || !strings.Contains(err.Error(), "macOS and Linux") {
			t.Errorf("ui --install on windows: %v", err)
		}
	})
}

func TestDesktopExec(t *testing.T) {
	for _, tt := range []struct{ path, want string }{
		{"/home/a/.local/share/memcore/launch", `"/home/a/.local/share/memcore/launch"`},
		{"/home/a b/launch", `"/home/a b/launch"`},
		{`/home/a"b/launch`, `"/home/a\\"b/launch"`},
		{"/home/a$b/launch", `"/home/a\\$b/launch"`},
		{"/home/a`b/launch", "\"/home/a\\\\`b/launch\""},
		{`/home/a\b/launch`, `"/home/a\\\\b/launch"`},
		{"/home/100%/launch", `"/home/100%%/launch"`},
	} {
		if got := desktopExec(tt.path); got != tt.want {
			t.Errorf("desktopExec(%q) = %s, want %s", tt.path, got, tt.want)
		}
	}
}

func TestPlistID(t *testing.T) {
	for _, tt := range []struct{ name, plist, want string }{
		{"the Info.plist of memcore", fmt.Sprintf(infoPlist, appID, "1.0.0"), appID},
		{"a comment", "<plist><dict><!-- <key>CFBundleIdentifier</key><string>re.memco.memcore</string> --></dict></plist>", ""},
		{"a nested dict", "<plist><dict><key>A</key><dict><key>CFBundleIdentifier</key><string>re.memco.memcore</string></dict></dict></plist>", ""},
		{"a key between", "<plist><dict><key>CFBundleIdentifier</key><key>B</key><string>re.memco.memcore</string></dict></plist>", ""},
		{"not XML", "{}", ""},
	} {
		if got := plistID([]byte(tt.plist)); got != tt.want {
			t.Errorf("%s: plistID = %q, want %q", tt.name, got, tt.want)
		}
	}
}
