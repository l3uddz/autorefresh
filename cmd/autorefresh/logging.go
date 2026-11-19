package main

import (
	"io"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

// newLogger returns a logger that writes to console and to the rotated log
// file at path. verbosity 1 selects debug and more selects trace. Each event
// goes to both writers, even if the console write fails.
func newLogger(console io.Writer, path string, verbosity int) zerolog.Logger {
	logger := log.Output(zerolog.MultiLevelWriter(zerolog.ConsoleWriter{
		TimeFormat: time.Stamp,
		Out:        console,
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
