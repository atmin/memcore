package ui

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// These variables find and start the programs of a window. Tests replace them.
var (
	goos     = runtime.GOOS
	lookPath = exec.LookPath
	stat     = os.Stat
	command  = exec.Command
	// macBrowsers are the Chromium browsers of macOS, under /Applications or ~/Applications.
	macBrowsers = []string{
		"Google Chrome.app/Contents/MacOS/Google Chrome",
		"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"Brave Browser.app/Contents/MacOS/Brave Browser",
		"Chromium.app/Contents/MacOS/Chromium",
	}
	// linuxBrowsers are the names of the Chromium browsers of Linux, on PATH.
	linuxBrowsers = []string{"google-chrome", "google-chrome-stable", "microsoft-edge", "brave-browser", "chromium", "chromium-browser"}
)

// Window opens url in a window. With a Chromium browser, it starts an app window: no tabs and no address bar,
// with a temporary profile. Its stop function ends that browser and removes the profile. Without one, it opens
// the default browser, which the server cannot close, so stop is nil.
func Window(url string) (stop func(), err error) {
	if bin := browser(); bin != "" {
		profile, err := os.MkdirTemp("", "memcore-ui-")
		if err != nil {
			return nil, fmt.Errorf("create the profile of the window: %w", err)
		}
		cmd := command(bin, "--app="+url, "--user-data-dir="+profile, "--no-first-run", "--no-default-browser-check")
		if err := cmd.Start(); err != nil {
			os.RemoveAll(profile)
			return nil, fmt.Errorf("start %s: %w", filepath.Base(bin), err)
		}
		exited := make(chan struct{})
		go func() {
			cmd.Wait()
			close(exited)
		}()
		return func() { end(cmd, exited, profile) }, nil
	}
	opener := "xdg-open"
	if goos == "darwin" {
		opener = "open"
	}
	if out, err := command(opener, url).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("open the default browser with %s: %w: %s", opener, err, out)
	}
	return nil, nil
}

// end asks a browser to quit, kills it after 5 s, and removes its profile.
func end(cmd *exec.Cmd, exited chan struct{}, profile string) {
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		cmd.Process.Kill()
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-exited
	}
	os.RemoveAll(profile)
}

// browser returns the path of a Chromium browser, or "" when none is installed.
func browser() string {
	switch goos {
	case "darwin":
		home, _ := os.UserHomeDir()
		for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
			for _, b := range macBrowsers {
				p := filepath.Join(root, b)
				if info, err := stat(p); err == nil && !info.IsDir() {
					return p
				}
			}
		}
	case "linux":
		for _, name := range linuxBrowsers {
			if p, err := lookPath(name); err == nil {
				return p
			}
		}
	}
	return ""
}

// OpenExternal opens a link of the page in the default browser, as a target="_blank" link does in a browser.
// Only http, https and mailto links open, so a page never starts a local file or another scheme.
func OpenExternal(link string) error {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
		return fmt.Errorf("open %q: only http, https and mailto links open outside the window", link)
	}
	opener := "xdg-open"
	if goos == "darwin" {
		opener = "open"
	}
	cmd := command(opener, link)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s with %s: %w", link, opener, err)
	}
	go cmd.Wait()
	return nil
}
