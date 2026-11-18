package commands

import (
	"strings"
	"testing"
	"time"
)

func TestAutoPushDelay(t *testing.T) {
	tests := []struct {
		name, value string // value "" leaves the key unset
		delay       time.Duration
		on, fails   bool
	}{
		{name: "unset"},
		{name: "a day", value: "24h", delay: 24 * time.Hour, on: true},
		{name: "no delay", value: "0s", on: true},
		{name: "a negative delay", value: "-1h", fails: true},
		{name: "no duration", value: "soon", fails: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, repo, _ := newMemory(t)
			if tt.value != "" {
				gitT(t, repo, "config", "memcore.autopush", tt.value)
			}
			delay, on, err := autoPushDelay(repo)
			if tt.fails {
				if err == nil || !strings.Contains(err.Error(), "memcore.autopush") || !strings.Contains(err.Error(), tt.value) {
					t.Fatalf("autoPushDelay = %v, want an error that names the key and the value %q", err, tt.value)
				}
				if on {
					t.Error("a setting that memcore cannot read turned the automatic push on")
				}
				return
			}
			if err != nil || delay != tt.delay || on != tt.on {
				t.Errorf("autoPushDelay = %v, %v, %v; want %v, %v", delay, on, err, tt.delay, tt.on)
			}
		})
	}
}
