//go:build unix

package main

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadConfigFileRejectsFIFOWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "config.yml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigFile(fifo); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("readConfigFile(fifo) = %v, want not a regular file", err)
	}
}
