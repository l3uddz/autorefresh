package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
)

// applyDefaultPaths sets each empty path to a file in the default config
// directory. It resolves that directory only if a path is empty, because
// defaultConfigDirectory can create a directory. A container that sets all
// paths then never touches $HOME.
func applyDefaultPaths(config, database, logFile *string) {
	if *config != "" && *database != "" && *logFile != "" {
		return
	}

	dir := defaultConfigDirectory("autorefresh", "config.yml")
	if *config == "" {
		*config = filepath.Join(dir, "config.yml")
	}
	if *database == "" {
		*database = filepath.Join(dir, "autorefresh.db")
	}
	if *logFile == "" {
		*logFile = filepath.Join(dir, "activity.log")
	}
}

// Tests replace these functions.
var (
	sleepFunc    = time.Sleep  // waits between two checks of the config file
	readFileFunc = os.ReadFile // reads the config file
)

// readConfig returns the content of the config file at path. If the file
// is missing or empty, it checks again after each interval until the file
// has content. A container can then start before the user creates
// config.yml. A file that appears during the wait can still be in writing,
// so readConfig reads it only after its size and modification time stay
// the same for one interval. If the file changes during the read, it waits
// again.
func readConfig(path string, interval time.Duration) ([]byte, error) {
	waited := false
	var seen os.FileInfo // the last state of the file with content
	for {
		info, err := os.Stat(path)
		switch {
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return nil, err
		case err == nil && !info.Mode().IsRegular():
			// Docker creates a directory for a bind mount of a missing file.
			return nil, fmt.Errorf("%s is not a regular file", path)
		case err == nil && info.Size() > 0:
			if !waited || sameState(seen, info) {
				data, err := readFileFunc(path)
				if err != nil {
					return nil, err
				}
				if after, err := os.Stat(path); err == nil && len(data) > 0 && sameState(info, after) {
					if waited {
						log.Info().Str("path", path).Msg("Found config")
					}
					return data, nil
				}
			}
			seen = info
		}

		if !waited {
			log.Warn().
				Str("path", path).
				Dur("interval", interval).
				Msg("Config is missing, empty, or changing, waiting for it")
			waited = true
		}
		sleepFunc(interval)
	}
}

// sameState reports whether a and b have the same size and modification
// time. A nil a is never the same.
func sameState(a, b os.FileInfo) bool {
	return a != nil && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
