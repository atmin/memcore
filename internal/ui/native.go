//go:build (darwin || linux) && !nonative

package ui

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/crgimenes/glaze"
	"github.com/crgimenes/glaze/menu"
)

// RunHost runs serve, and gives it a function that opens a native window. The windows of glaze live on the
// main thread, so RunHost must run on the main goroutine, which main locked to the main thread. serve runs in
// another goroutine. When glaze cannot open a window, as on a Linux without WebKitGTK, the window falls back
// to Window: a Chrome app window or the default browser. MEMCORE_UI_WINDOW=chrome forces that fallback.
func RunHost(serve func(window func(url string) (stop func(), err error), quit, reopen <-chan struct{}) error) error {
	if os.Getenv("MEMCORE_UI_WINDOW") == "chrome" {
		return serve(Window, nil, nil)
	}
	runtime.LockOSThread()
	h := newHost()
	var served error
	go func() {
		served = serve(h.window, h.quit, h.reopen)
		close(h.done)
		if loop := h.getLoop(); loop != nil {
			loop.Terminate()
		}
	}()
	select {
	case url := <-h.first:
		if !h.openFirst(url) {
			<-h.done
			return served
		}
		loop := h.getLoop()
		h.setMenu(loop)
		// glaze stops the loop when its last window closes. The server lives on for its grace, and may open
		// another window, so the loop runs again until serve returns. A Dispatch waits in the main queue
		// between two runs.
		for {
			loop.Run()
			select {
			case <-h.done:
				return served
			default:
			}
		}
	case <-h.done:
		return served
	}
}

// host opens the native windows of one server.
type host struct {
	first    chan string // the URL of the first window, which the main thread opens before the loop runs
	opened   chan error  // the result of the first window
	done     chan struct{}
	quit     chan struct{} // closes when the person quits, so the server stops at once
	quitOnce sync.Once
	reopen   chan struct{} // gets a value when the person clicks the Dock icon with no window open

	// open opens a native window on the main thread, dispatch runs a function on the main thread, and
	// fallback opens the window of a browser. Tests replace them, so they run without AppKit.
	open     func(url string) (*nativeWindow, error)
	dispatch func(w *nativeWindow, f func())
	fallback func(url string) (stop func(), err error)
	delegate func(reopen func()) // installs the app delegate of memcore: keepDelegate
	warn     io.Writer

	mu     sync.Mutex
	loop   *nativeWindow // the first window, whose Run is the loop, and whose Dispatch reaches the main thread
	failed bool          // the first native window failed, so each window is a window of fallback
}

func newHost() *host {
	return &host{
		first: make(chan string), opened: make(chan error), done: make(chan struct{}), quit: make(chan struct{}),
		reopen:   make(chan struct{}, 1),
		open:     openNative,
		dispatch: func(w *nativeWindow, f func()) { w.Dispatch(f) },
		fallback: Window,
		delegate: keepDelegate,
		warn:     os.Stderr,
	}
}

func (h *host) getLoop() *nativeWindow {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.loop
}

// openFirst opens the first window on the main thread, and reports whether it opened. Then the window that
// asked for it gets the result.
func (h *host) openFirst(url string) bool {
	w, err := h.opened1(url)
	h.mu.Lock()
	if err != nil {
		h.failed = true
	} else {
		h.loop = w
	}
	h.mu.Unlock()
	h.opened <- err
	return err == nil
}

// opened1 opens one native window on the main thread. The window keeps the app delegate of memcore when it
// goes, and opening it keeps the delegate too.
func (h *host) opened1(url string) (*nativeWindow, error) {
	w, err := h.open(url)
	if err != nil {
		return nil, err
	}
	w.gone = h.keep
	h.keep()
	return w, nil
}

// keep installs the app delegate of memcore again, and asks for a window through reopen on a click on the
// Dock icon. It runs on the main thread.
func (h *host) keep() {
	h.delegate(func() {
		select {
		case h.reopen <- struct{}{}:
		default: // a window is already on its way
		}
	})
}

// window opens a native window at url, and returns the function that closes it. A native window that fails
// falls back to a window of the browser.
func (h *host) window(url string) (func(), error) {
	h.mu.Lock()
	loop, failed := h.loop, h.failed
	h.mu.Unlock()
	if failed {
		return h.fallback(url)
	}
	if loop == nil {
		h.first <- url
		if err := <-h.opened; err != nil {
			return h.fellBack(url, err)
		}
		return h.closer(h.getLoop()), nil
	}
	type made struct {
		w   *nativeWindow
		err error
	}
	ch := make(chan made, 1)
	h.dispatch(loop, func() {
		w, err := h.opened1(url)
		ch <- made{w, err}
	})
	m := <-ch
	if m.err != nil {
		return h.fellBack(url, m.err)
	}
	return h.closer(m.w), nil
}

// fellBack warns that a native window failed, and opens the window of the browser instead.
func (h *host) fellBack(url string, err error) (func(), error) {
	fmt.Fprintf(h.warn, "memcore ui: the native window failed, so the window falls back to a browser: %v\n", err)
	return h.fallback(url)
}

// setMenu installs the menu of the application. AppKit takes the shortcuts Cmd+W, Cmd+Q, Cmd+C and the rest
// from the menu, so without it they do nothing. It runs on the main thread, before the loop runs.
func (h *host) setMenu(w *nativeWindow) {
	quit := func() { h.quitOnce.Do(func() { close(h.quit) }) }
	_, err := menu.Set([]menu.Item{
		{Title: "memcore", Submenu: []menu.Item{
			{Title: "Hide memcore", Shortcut: "cmd+h", Selector: "hide:"},
			{Separator: true},
			{Title: "Quit memcore", Shortcut: "cmd+q", OnClick: quit},
		}},
		{Title: "Edit", Submenu: []menu.Item{
			{Title: "Undo", Shortcut: "cmd+z", Selector: "undo:"},
			{Title: "Redo", Shortcut: "cmd+shift+z", Selector: "redo:"},
			{Separator: true},
			{Title: "Cut", Shortcut: "cmd+x", Selector: "cut:"},
			{Title: "Copy", Shortcut: "cmd+c", Selector: "copy:"},
			{Title: "Paste", Shortcut: "cmd+v", Selector: "paste:"},
			{Title: "Select All", Shortcut: "cmd+a", Selector: "selectAll:"},
		}},
		{Title: "Window", Submenu: []menu.Item{
			{Title: "Minimize", Shortcut: "cmd+m", Selector: "performMiniaturize:"},
			{Title: "Close", Shortcut: "cmd+w", Selector: "performClose:"},
		}},
	}, menu.Options{Window: w.Window()})
	if err != nil {
		fmt.Fprintf(h.warn, "memcore ui: the menu failed, so its shortcuts do nothing: %v\n", err)
	}
}

// closer returns the function that closes a window from any goroutine.
func (h *host) closer(w *nativeWindow) func() {
	return func() {
		done := make(chan struct{})
		h.dispatch(w, func() {
			w.destroy()
			close(done)
		})
		<-done
	}
}

// nativeWindow is a window of glaze that frees its web view once: when the person closes it, or when the
// server closes it.
type nativeWindow struct {
	glaze.WebView
	once sync.Once
	gone func() // runs on the main thread after the web view is freed
}

// destroy frees the web view once. Before the web view exists, it does nothing, and keeps its one call.
func (w *nativeWindow) destroy() {
	if w.WebView == nil {
		return
	}
	w.once.Do(func() {
		w.Destroy()
		if w.gone != nil {
			w.gone()
		}
	})
}

// openNative opens one window of glaze at url. It runs on the main thread.
func openNative(url string) (*nativeWindow, error) {
	if png, err := IconPNG(512); err == nil {
		glaze.SetAppIcon(png) // ErrIconUnsupported on Linux, where the desktop entry names the icon
	}
	w := &nativeWindow{}
	view, err := glaze.NewWithOptions(glaze.Options{
		NoBridge:        true, // the page talks to the server over HTTP, never to the process
		HideUntilLoaded: true,
		// A link with target="_blank", such as a link out of a document, opens in the default browser.
		OnNewWindow: func(link string) {
			if err := OpenExternal(link); err != nil {
				fmt.Fprintf(os.Stderr, "memcore ui: %v\n", err)
			}
		},
		// The page closes its event stream, so the server counts one window less. destroy frees the web view.
		OnClose: func() { w.destroy() },
	})
	if err != nil {
		return nil, fmt.Errorf("open a native window: %w", err)
	}
	w.WebView = view
	w.SetTitle("memcore")
	w.SetSize(1200, 800, glaze.HintNone)
	w.Navigate(url)
	w.Raise()
	return w, nil
}
