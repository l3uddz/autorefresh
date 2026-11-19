package main

import (
	"fmt"
	"os"
	"path/filepath"
)

var (
	userConfigDirFunc = os.UserConfigDir
	executableFunc    = os.Executable
)

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

// getBinaryPath returns the directory of the running binary. It uses the
// executable path, because os.Args[0] has no directory when a PATH lookup
// starts the binary. If the executable path is unknown, it returns the
// working directory.
func getBinaryPath() string {
	if exe, err := executableFunc(); err == nil {
		return filepath.Dir(exe)
	}

	dir, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("getwd: %v", err))
	}

	return dir
}
