package main

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

// newLogger returns a logger that writes to stderr and to the rotated log
// file at path. verbosity 1 selects debug and more selects trace.
func newLogger(path string, verbosity int) zerolog.Logger {
	logger := log.Output(io.MultiWriter(zerolog.ConsoleWriter{
		TimeFormat: time.Stamp,
		Out:        os.Stderr,
	}, zerolog.ConsoleWriter{
		TimeFormat: time.Stamp,
		Out: &lumberjack.Logger{
			Filename:   path,
			MaxSize:    5,
			MaxAge:     14,
			MaxBackups: 5,
		},
		NoColor: true,
	}))

	switch {
	case verbosity == 1:
		return logger.Level(zerolog.DebugLevel)
	case verbosity > 1:
		return logger.Level(zerolog.TraceLevel)
	default:
		return logger.Level(zerolog.InfoLevel)
	}
}
