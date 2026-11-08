//go:build !nonative

package ui

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/crgimenes/glaze"
	"github.com/ebitengine/purego/objc"
)

// The app delegate of memcore. glaze installs its own delegate with its first window, and clears
// NSApplication.delegate when that window goes. So memcore installs its own delegate after each window opens and
// after each window goes, and it answers the click on the Dock icon for the whole life of the server.
var (
	delegateOnce sync.Once
	delegate     objc.ID
	delegateErr  error
	reopenFunc   atomic.Pointer[func()]
)

// keepDelegate installs the app delegate of memcore, when NSApplication has another delegate or none. reopen
// runs when the person clicks the Dock icon with no window open. It runs on the main thread.
func keepDelegate(reopen func()) {
	delegateOnce.Do(func() {
		// glaze loads Cocoa, where NSResponder lives. It already did for a window, and Init does nothing then.
		if err := glaze.Init(); err != nil {
			delegateErr = fmt.Errorf("load Cocoa: %w", err)
			fmt.Fprintf(os.Stderr, "memcore ui: %v, so a click on the Dock icon opens no window\n", delegateErr)
			return
		}
		class, err := objc.RegisterClass("MemcoreAppDelegate", objc.GetClass("NSResponder"), nil, nil, []objc.MethodDef{
			{
				// As glaze answers: the loop of the host decides when the process ends, not AppKit.
				Cmd: objc.RegisterName("applicationShouldTerminateAfterLastWindowClosed:"),
				Fn:  func(self objc.ID, _cmd objc.SEL, sender objc.ID) bool { return false },
			},
			{
				Cmd: objc.RegisterName("applicationShouldHandleReopen:hasVisibleWindows:"),
				Fn: func(self objc.ID, _cmd objc.SEL, sender objc.ID, visible bool) bool {
					if f := reopenFunc.Load(); f != nil && !visible {
						// The window opens through the server, which waits on the main thread. This callback holds
						// the main thread, so the request goes to another goroutine, and the callback returns.
						go (*f)()
					}
					return true
				},
			},
		})
		if err != nil {
			delegateErr = fmt.Errorf("register the app delegate of memcore: %w", err)
			fmt.Fprintf(os.Stderr, "memcore ui: %v, so a click on the Dock icon opens no window\n", delegateErr)
			return
		}
		delegate = objc.ID(class).Send(objc.RegisterName("new"))
	})
	if delegateErr != nil {
		return
	}
	reopenFunc.Store(&reopen)
	app := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	if app.Send(objc.RegisterName("delegate")) != delegate {
		app.Send(objc.RegisterName("setDelegate:"), delegate)
	}
}
