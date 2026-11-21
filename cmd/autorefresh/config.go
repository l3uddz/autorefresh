package main

import (
	"context"
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
	sleepFunc    = sleepContext // waits between two checks of the config file
	readFileFunc = os.ReadFile  // reads the config file
)

// sleepContext waits for d. It returns the error of ctx if ctx ends first.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// readConfig returns the content of the config file at path. If the file
// is missing or empty, it checks again after each interval until the file
// has content. A container can then start before the user creates
// config.yml. A file that appears during the wait can still be in writing,
// so readConfig reads it only after its size and modification time stay
// the same for one interval. If the file changes during the read, it waits
// again. If ctx ends during a wait, readConfig returns the error of ctx.
func readConfig(ctx context.Context, path string, interval time.Duration) ([]byte, error) {
	waited := false
	var seen os.FileInfo // the last state of the file with content
	for {
		data, state, err := checkConfig(path, waited, seen)
		if err != nil {
			return nil, err
		}
		if data != nil {
			if waited {
				log.Info().Str("path", path).Msg("Found config")
			}
			return data, nil
		}
		if state != nil {
			seen = state
		}

		if !waited {
			log.Warn().
				Str("path", path).
				Dur("interval", interval).
				Msg("Config is missing, empty, or changing, waiting for it")
			waited = true
		}
		if err := sleepFunc(ctx, interval); err != nil {
			return nil, err
		}
	}
}

// checkConfig checks the config file at path once. It returns the content
// of the file if the file is ready: it has content, it did not change during
// the read, and after a wait it has the same state as seen. Otherwise it
// returns no content, and the state of the file if the file has content.
func checkConfig(path string, waited bool, seen os.FileInfo) (data []byte, state os.FileInfo, err error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, nil
	case err != nil:
		return nil, nil, err
	case !info.Mode().IsRegular():
		// Docker creates a directory for a bind mount of a missing file.
		return nil, nil, fmt.Errorf("%s is not a regular file", path)
	case info.Size() == 0:
		return nil, nil, nil
	case waited && !sameState(seen, info):
		return nil, info, nil
	}

	data, err = readFileFunc(path)
	if err != nil {
		return nil, nil, err
	}
	if after, err := os.Stat(path); err != nil || len(data) == 0 || !sameState(info, after) {
		return nil, info, nil
	}
	return data, info, nil
}

// sameState reports whether a and b have the same size and modification
// time. A nil a is never the same.
func sameState(a, b os.FileInfo) bool {
	return a != nil && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
