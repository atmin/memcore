package commands

import (
	"fmt"
	"io"
	"runtime/debug"
)

// Version is the version of memcore. A release sets it to its tag with
// -ldflags "-X github.com/atmin/memcore/internal/commands.Version=<tag>".
var Version = "dev"

// versionLine returns the line of memcore version: the version, then the short commit of the build when the
// build knows it, with ", modified" for a changed tree.
func versionLine(version string, info *debug.BuildInfo, ok bool) string {
	var revision, modified string
	if ok && info != nil {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
	}
	if revision == "" {
		return fmt.Sprintf("memcore %s\n", version)
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if modified == "true" {
		return fmt.Sprintf("memcore %s (%s, modified)\n", version, revision)
	}
	return fmt.Sprintf("memcore %s (%s)\n", version, revision)
}

// runVersion prints the version of memcore and the commit that it was built from.
func runVersion(env Env, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: memcore version")
	}
	info, ok := debug.ReadBuildInfo()
	if _, err := io.WriteString(env.Out, versionLine(Version, info, ok)); err != nil {
		return fmt.Errorf("print the version: %w", err)
	}
	return nil
}
