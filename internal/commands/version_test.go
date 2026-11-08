package commands

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionLine(t *testing.T) {
	info := func(settings ...string) *debug.BuildInfo {
		bi := &debug.BuildInfo{}
		for i := 0; i+1 < len(settings); i += 2 {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
		}
		return bi
	}
	tests := []struct {
		name    string
		version string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"no build info", "dev", nil, false, "memcore dev\n"},
		{"build info without a revision", "dev", info(), true, "memcore dev\n"},
		{"dev with a revision", "dev", info("vcs.revision", "7c3718e0123456789abcdef", "vcs.modified", "false"), true, "memcore dev (7c3718e)\n"},
		{"a release of a changed tree", "v0.1.0", info("vcs.revision", "abcdef0123456789", "vcs.modified", "true"), true, "memcore v0.1.0 (abcdef0, modified)\n"},
		{"a short revision stays whole", "dev", info("vcs.revision", "abc"), true, "memcore dev (abc)\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionLine(tt.version, tt.info, tt.ok); got != tt.want {
				t.Errorf("versionLine = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	home, _, _ := newMemory(t)
	out, err := run(t, home, "", "version")
	if err != nil || out != "memcore dev\n" {
		t.Errorf("version: %v, output %q, want %q", err, out, "memcore dev\n")
	}
	out, err = run(t, home, "", "version", "x")
	if err == nil || !strings.Contains(err.Error(), "memcore help version shows the input") {
		t.Errorf("version x: %v, output %q, want an input error", err, out)
	}
	out, err = run(t, home, "", "help")
	if err != nil || !strings.Contains(out, "  memcore version ") {
		t.Errorf("help: %v, output %q, want a line of version", err, out)
	}
}
