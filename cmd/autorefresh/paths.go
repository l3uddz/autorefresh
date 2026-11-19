package main

import (
	"fmt"
	"os"
	"path/filepath"
)

var userConfigDirFunc = os.UserConfigDir

// defaultConfigDirectory returns the directory for files with no set path.
// If filename exists next to the binary, it returns the binary directory.
// Otherwise it returns the app directory in the user config directory and
// creates it. If that fails, it returns the binary directory.
func defaultConfigDirectory(app string, filename string) string {
	// binary path
	bcd := getBinaryPath()
	if _, err := os.Stat(filepath.Join(bcd, filename)); err == nil {
		// there is a config file in the binary path
		// so use this directory as the default
		return bcd
	}

	// config dir
	ucd, err := userConfigDirFunc()
	if err == nil && ucd != "" {
		acd := filepath.Join(ucd, app)
		mkErr := os.MkdirAll(acd, os.ModePerm)
		if mkErr == nil {
			return acd
		}

		fmt.Fprintf(os.Stderr, "%s: failed ensuring config directory %s: %v\n", app, acd, mkErr)
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "%s: unable to determine user config directory: %v\n", app, err)
	}

	return bcd
}

func getBinaryPath() string {
	// get current binary path
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		// get current working dir
		if dir, err = os.Getwd(); err != nil {
			panic(fmt.Sprintf("getwd: %v", err))
		}
	}

	return dir
}
