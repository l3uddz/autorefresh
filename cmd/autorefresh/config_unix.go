//go:build unix

package main

import (
	"os"
	"syscall"
)

// openConfigFile opens path with O_NONBLOCK so a regular file replaced by a
// FIFO between os.Stat and open cannot block startup before f.Stat checks it.
func openConfigFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
