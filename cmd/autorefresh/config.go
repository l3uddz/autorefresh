package main

import (
	"errors"
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

// waitForConfig opens the config file at path. If the file is missing or
// empty, it checks again after each interval until the file has content.
// A container can then start before the user creates config.yml.
func waitForConfig(path string, interval time.Duration) (*os.File, error) {
	waited := false
	for {
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Size() > 0:
			if waited {
				log.Info().Str("path", path).Msg("Found config")
			}
			return os.Open(path)
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}

		if !waited {
			log.Warn().
				Str("path", path).
				Dur("interval", interval).
				Msg("Config is missing or empty, waiting for it")
			waited = true
		}
		time.Sleep(interval)
	}
}
