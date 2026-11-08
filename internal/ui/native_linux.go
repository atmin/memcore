//go:build !nonative

package ui

// keepDelegate does nothing on Linux, which has no Dock icon to click.
func keepDelegate(func()) {}
