package main

import (
	"os"
	"syscall"
)

// redirectStderr points file descriptor 2 at f, so a panic of the runtime and the default logger of the HTTP
// server reach the log too, not only what memcore writes to os.Stderr.
func redirectStderr(f *os.File) error { return syscall.Dup2(int(f.Fd()), 2) }
