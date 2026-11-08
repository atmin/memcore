//go:build !nonative

package ui

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/ebitengine/purego/objc"
)

// AppKit runs only on the main thread. init locks the main goroutine to it, and TestMain serves onMain there
// while the tests run in another goroutine.
func init() { runtime.LockOSThread() }

var onMain = make(chan func())

func TestMain(m *testing.M) {
	done := make(chan int)
	go func() { done <- m.Run() }()
	for {
		select {
		case f := <-onMain:
			f()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// mainThread runs f on the main thread, and waits for it.
func mainThread(f func()) {
	ran := make(chan struct{})
	onMain <- func() {
		defer close(ran)
		f()
	}
	<-ran
}

// TestKeepDelegate installs the app delegate of memcore, and sends it the messages of AppKit: a click on the
// Dock icon with and without a visible window.
func TestKeepDelegate(t *testing.T) {
	called := make(chan struct{}, 2)
	var app objc.ID
	var current objc.ID
	mainThread(func() {
		keepDelegate(func() { called <- struct{}{} })
		keepDelegate(func() { called <- struct{}{} }) // a second install keeps the one delegate
		app = objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
		current = app.Send(objc.RegisterName("delegate"))
	})
	if delegateErr != nil {
		t.Fatal(delegateErr)
	}
	if current != delegate {
		t.Fatalf("NSApplication.delegate = %v, want the delegate of memcore %v", current, delegate)
	}
	reopen := objc.RegisterName("applicationShouldHandleReopen:hasVisibleWindows:")
	send := func(visible bool) (answer bool) {
		mainThread(func() { answer = objc.Send[bool](delegate, reopen, app, visible) })
		return answer
	}
	if !send(true) {
		t.Error("the delegate refused a reopen with a visible window")
	}
	select {
	case <-called:
		t.Error("a reopen with a visible window asked for a window")
	case <-time.After(100 * time.Millisecond):
	}
	if !send(false) {
		t.Error("the delegate refused a reopen without a visible window")
	}
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Error("a reopen without a visible window asked for no window")
	}
	var terminate bool
	mainThread(func() {
		terminate = objc.Send[bool](delegate, objc.RegisterName("applicationShouldTerminateAfterLastWindowClosed:"), app)
	})
	if terminate {
		t.Error("the delegate ends the process after the last window")
	}
}
