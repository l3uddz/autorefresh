//go:build !unix

package main

import "os"

func openConfigFile(path string) (*os.File, error) {
	return os.Open(path)
}
