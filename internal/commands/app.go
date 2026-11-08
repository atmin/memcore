package commands

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atmin/memcore/internal/ui"
)

// appID is the bundle identifier of memcore.app. --install and --uninstall touch only a bundle that names it.
const appID = "re.memco.memcore"

var (
	// appOS is the system that the application is for. Tests replace it to test both systems on one machine.
	appOS = runtime.GOOS
	// lsregister registers an application with LaunchServices, so Spotlight and the Finder know it at once.
	lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
	// appExecutable returns the memcore that the launcher runs: this executable, with its links resolved.
	appExecutable = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("find the executable of memcore: %w", err)
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return "", fmt.Errorf("find the executable of memcore: %w", err)
		}
		return exe, nil
	}
)

// plistID returns the CFBundleIdentifier of the root dictionary of an Info.plist in XML, or "". It skips
// comments and nested dictionaries, so only the real identifier counts.
func plistID(b []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	var stack []string // the open elements
	var key, text string
	var after bool // the last element of the root dictionary was the key CFBundleIdentifier
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			text = ""
		case xml.CharData:
			text += string(t)
		case xml.EndElement:
			root := len(stack) == 3 && stack[0] == "plist" && stack[1] == "dict"
			stack = stack[:max(len(stack)-1, 0)]
			if !root {
				continue
			}
			switch {
			case after && t.Name.Local == "string":
				return strings.TrimSpace(text)
			case t.Name.Local == "key":
				key = strings.TrimSpace(text)
				after = key == "CFBundleIdentifier"
			default:
				after = false
			}
		}
	}
}

// installApp writes the application of the window: memcore.app on macOS, or a desktop entry on Linux. The
// application runs a launcher, which runs memcore ui with the PATH of this call, and with MEMCORE_REPO when
// this call has it set.
func installApp(env Env) error {
	exe, err := appExecutable()
	if err != nil {
		return err
	}
	h, err := home()
	if err != nil {
		return err
	}
	switch appOS {
	case "darwin":
		return installBundle(env, h, exe)
	case "linux":
		return installDesktop(env, h, exe)
	}
	return fmt.Errorf("the application of the window works on macOS and Linux only, not on %s", appOS)
}

// uninstallApp removes what installApp wrote.
func uninstallApp(env Env) error {
	h, err := home()
	if err != nil {
		return err
	}
	var removed []string
	switch appOS {
	case "darwin":
		removed, err = uninstallBundle(env, h)
	case "linux":
		removed, err = uninstallDesktop(h)
	default:
		return fmt.Errorf("the application of the window works on macOS and Linux only, not on %s", appOS)
	}
	for _, p := range removed {
		fmt.Fprintf(env.Out, "Removed %s.\n", tilde(p))
	}
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		fmt.Fprintln(env.Out, "The application of the window is not installed, so there is nothing to remove.")
	}
	return nil
}

// launcher returns the text of the script that the application runs. log names the file of its output, or "".
func launcher(exe, log string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# The launcher of the window of memcore. memcore ui --install wrote it.\n")
	fmt.Fprintf(&b, "export PATH=%s\n", shellQuote(os.Getenv("PATH")))
	if repo := os.Getenv("MEMCORE_REPO"); repo != "" {
		fmt.Fprintf(&b, "export MEMCORE_REPO=%s\n", shellQuote(repo))
	}
	if log == "" {
		fmt.Fprintf(&b, "exec %s ui\n", shellQuote(exe))
		return b.String()
	}
	fmt.Fprintf(&b, "mkdir -p \"$HOME/%s\"\n", filepath.Dir(log))
	fmt.Fprintf(&b, "exec %s ui >>\"$HOME/%s\" 2>&1\n", shellQuote(exe), log)
	return b.String()
}

// appDir returns the folder of memcore.app: MEMCORE_APP_DIR, or ~/Applications.
func appDir(h string) string {
	if d := os.Getenv("MEMCORE_APP_DIR"); d != "" {
		return d
	}
	return filepath.Join(h, "Applications")
}

// ownBundle reports whether a bundle exists at app, and refuses one that memcore did not write.
func ownBundle(app string) (bool, error) {
	if _, err := os.Lstat(app); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("read %s: %w", tilde(app), err)
	}
	b, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read the Info.plist of %s: %w", tilde(app), err)
	}
	if plistID(b) != appID {
		return false, inputErrf("%s exists, and memcore did not write it: move it away, then run memcore ui --install again", tilde(app))
	}
	return true, nil
}

// installBundle writes memcore.app in a temporary folder beside it, then replaces the old bundle with it.
func installBundle(env Env, h, exe string) error {
	dir := appDir(h)
	app := filepath.Join(dir, "memcore.app")
	if _, err := ownBundle(app); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", tilde(dir), err)
	}
	tmp, err := os.MkdirTemp(dir, ".memcore.app-")
	if err != nil {
		return fmt.Errorf("create the application: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := writeBundle(tmp, exe); err != nil {
		return err
	}
	if err := os.RemoveAll(app); err != nil {
		return fmt.Errorf("remove the old application: %w", err)
	}
	if err := os.Rename(tmp, app); err != nil {
		return fmt.Errorf("move the application into place: %w", err)
	}
	if out, err := exec.Command(lsregister, "-f", app).CombinedOutput(); err != nil {
		fmt.Fprintf(env.Err, "memcore ui: lsregister failed, so Spotlight may find the application later: %v: %s\n", err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(env.Out, "Wrote %s. Spotlight and the Finder open it as memcore.\n", tilde(app))
	return nil
}

// writeBundle writes the files of memcore.app into the folder app.
func writeBundle(app, exe string) error {
	if err := os.Chmod(app, 0o755); err != nil {
		return fmt.Errorf("create the application: %w", err)
	}
	macos := filepath.Join(app, "Contents", "MacOS")
	resources := filepath.Join(app, "Contents", "Resources")
	for _, d := range []string{macos, resources} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create the application: %w", err)
		}
	}
	icns, err := ui.ICNS()
	if err != nil {
		return err
	}
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{filepath.Join(macos, "memcore"), []byte(launcher(exe, "Library/Logs/memcore.log")), 0o755},
		{filepath.Join(resources, "memcore.icns"), icns, 0o644},
		{filepath.Join(app, "Contents", "Info.plist"), []byte(fmt.Sprintf(infoPlist, appID, html.EscapeString(bundleVersion()))), 0o644},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, f.data, f.mode); err != nil {
			return fmt.Errorf("write %s of the application: %w", filepath.Base(f.path), err)
		}
	}
	return nil
}

// bundleVersion returns the version of memcore without its v, or 0.0.0 for a build from source.
func bundleVersion() string {
	if Version == "dev" || Version == "" {
		return "0.0.0"
	}
	return strings.TrimPrefix(Version, "v")
}

// infoPlist is the Info.plist of memcore.app. It sets no environment: the launcher sets it.
const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>memcore</string>
	<key>CFBundleDisplayName</key><string>memcore</string>
	<key>CFBundleIdentifier</key><string>%s</string>
	<key>CFBundleExecutable</key><string>memcore</string>
	<key>CFBundleIconFile</key><string>memcore</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>%s</string>
	<key>CFBundleVersion</key><string>1</string>
	<key>LSMinimumSystemVersion</key><string>12.0</string>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
`

// uninstallBundle unregisters and removes memcore.app, and returns it when it removed it.
func uninstallBundle(env Env, h string) ([]string, error) {
	app := filepath.Join(appDir(h), "memcore.app")
	exists, err := ownBundle(app)
	if err != nil || !exists {
		return nil, err
	}
	if out, err := exec.Command(lsregister, "-u", app).CombinedOutput(); err != nil {
		fmt.Fprintf(env.Err, "memcore ui: lsregister failed, so Spotlight may list the application for a while: %v: %s\n", err, strings.TrimSpace(string(out)))
	}
	if err := os.RemoveAll(app); err != nil {
		return nil, fmt.Errorf("remove %s: %w", tilde(app), err)
	}
	return []string{app}, nil
}

// dataDir returns the data folder of XDG: XDG_DATA_HOME when it is an absolute path, or ~/.local/share.
func dataDir(h string) string {
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return d
	}
	return filepath.Join(h, ".local", "share")
}

// desktopFiles returns the launcher, the desktop entry and the icon of the application on Linux.
func desktopFiles(h string) (launch, entry, icon string) {
	data := dataDir(h)
	return filepath.Join(data, "memcore", "launch"), filepath.Join(data, "applications", "memcore.desktop"),
		filepath.Join(data, "icons", "hicolor", "256x256", "apps", "memcore.png")
}

// installDesktop writes the launcher, the desktop entry and the icon of the window on Linux.
func installDesktop(env Env, h, exe string) error {
	launch, entry, icon := desktopFiles(h)
	if strings.ContainsAny(launch, "\n\r") {
		return fmt.Errorf("the path %q holds a line break, which a desktop entry cannot hold", launch)
	}
	png, err := ui.IconPNG(256)
	if err != nil {
		return err
	}
	text := "[Desktop Entry]\nType=Application\nName=memcore\nComment=Review, push, browse and search the memory repo\n" +
		"Exec=" + desktopExec(launch) + "\nIcon=memcore\nTerminal=false\nCategories=Development;\n"
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{launch, []byte(launcher(exe, "")), 0o755},
		{icon, png, 0o644},
		{entry, []byte(text), 0o644},
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", tilde(filepath.Dir(f.path)), err)
		}
		if err := writeAtomic(f.path, f.data); err != nil {
			return err
		}
		if err := os.Chmod(f.path, f.mode); err != nil {
			return fmt.Errorf("set the mode of %s: %w", tilde(f.path), err)
		}
		fmt.Fprintf(env.Out, "Wrote %s.\n", tilde(f.path))
	}
	return nil
}

// desktopExec quotes a path for the Exec key of a desktop entry. The quoting rule of the key comes first, and
// the escape rule of a string value after it, so a reader undoes them in the other order. % starts a field
// code, so it doubles.
func desktopExec(path string) string {
	quoted := `"` + strings.NewReplacer(`"`, `\"`, "`", "\\`", `$`, `\$`, `\`, `\\`).Replace(path) + `"`
	return strings.NewReplacer(`\`, `\\`, `%`, `%%`).Replace(quoted)
}

// uninstallDesktop removes the files of installDesktop, and the folder of the launcher when it is empty.
func uninstallDesktop(h string) ([]string, error) {
	launch, entry, icon := desktopFiles(h)
	var removed []string
	for _, p := range []string{entry, icon, launch} {
		if err := os.Remove(p); err == nil {
			removed = append(removed, p)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return removed, fmt.Errorf("remove %s: %w", tilde(p), err)
		}
	}
	dir := filepath.Dir(launch)
	if err := os.Remove(dir); err == nil {
		removed = append(removed, dir)
	}
	return removed, nil
}
