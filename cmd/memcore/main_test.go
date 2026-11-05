package main

import (
	"errors"
	"testing"
)

func TestWorkingDir(t *testing.T) {
	fails := func() (string, error) { return "", errors.New("getwd: no such file or directory") }
	works := func() (string, error) { return "/work", nil }
	tests := []struct {
		name    string
		args    []string
		getwd   func() (string, error)
		want    string
		wantErr bool
	}{
		{"hook", []string{"tool"}, fails, "", false},
		{"orient", nil, fails, "", true},
		{"hook", []string{"prompt"}, fails, "", true},
		{"hook", []string{"tool"}, works, "/work", false},
		{"orient", nil, works, "/work", false},
	}
	for _, tt := range tests {
		got, err := workingDir(tt.name, tt.args, tt.getwd)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("workingDir(%q, %q) = %q, %v, want %q and an error: %v", tt.name, tt.args, got, err, tt.want, tt.wantErr)
		}
	}
}
