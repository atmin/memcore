//go:build (darwin || linux) && !nonative

package ui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRunHostFallback(t *testing.T) {
	t.Setenv("MEMCORE_UI_WINDOW", "chrome")
	called := false
	err := RunHost(func(window func(string) (func(), error), quit, reopen <-chan struct{}) error {
		called = true
		if quit != nil || reopen != nil {
			t.Error("the fallback gets a channel of the menu or the Dock")
		}
		if reflect.ValueOf(window).Pointer() != reflect.ValueOf(Window).Pointer() {
			t.Error("RunHost passes another function than Window")
		}
		return nil
	})
	if err != nil || !called {
		t.Errorf("RunHost: %v, called %v", err, called)
	}
}

// fakeHost returns a host whose native windows fail when fail says so, and whose fallback counts its windows.
func fakeHost(fail func(url string) bool) (h *host, fellBack *[]string, warned *strings.Builder) {
	h = newHost()
	fellBack, warned = new([]string), new(strings.Builder)
	h.open = func(url string) (*nativeWindow, error) {
		if fail(url) {
			return nil, errors.New("no display")
		}
		return &nativeWindow{}, nil
	}
	h.dispatch = func(_ *nativeWindow, f func()) { f() }
	h.delegate = func(func()) {}
	h.fallback = func(url string) (func(), error) {
		*fellBack = append(*fellBack, url)
		return nil, nil
	}
	h.warn = warned
	return h, fellBack, warned
}

func TestHostFallback(t *testing.T) {
	for _, tt := range []struct {
		name     string
		fail     string // the URL whose native window fails
		fellBack []string
	}{
		{"the first window fails", "first", []string{"first", "second"}},
		{"a later window fails", "second", []string{"second"}},
		{"no window fails", "", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, fellBack, warned := fakeHost(func(url string) bool { return url == tt.fail })
			// The main thread of RunHost opens the first window.
			go func() { h.openFirst(<-h.first) }()
			for _, url := range []string{"first", "second"} {
				if _, err := h.window(url); err != nil {
					t.Fatalf("window(%s): %v", url, err)
				}
			}
			if strings.Join(*fellBack, ",") != strings.Join(tt.fellBack, ",") {
				t.Errorf("the fallback opened %q, want %q", *fellBack, tt.fellBack)
			}
			if wantWarn := tt.fail != ""; strings.Contains(warned.String(), "falls back to a browser") != wantWarn {
				t.Errorf("warned %q, want a warning: %v", warned.String(), wantWarn)
			}
		})
	}
}
