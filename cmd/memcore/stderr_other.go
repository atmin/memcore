//go:build !darwin

package main

import "os"

// redirectStderr does nothing outside macOS, where no bundle runs memcore.
func redirectStderr(*os.File) error { return nil }
