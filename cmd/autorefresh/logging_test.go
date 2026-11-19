package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestNewLoggerLevelFollowsVerbosity(t *testing.T) {
	tests := []struct {
		verbosity int
		want      zerolog.Level
	}{
		{verbosity: 0, want: zerolog.InfoLevel},
		{verbosity: 1, want: zerolog.DebugLevel},
		{verbosity: 2, want: zerolog.TraceLevel},
		{verbosity: 5, want: zerolog.TraceLevel},
	}

	for _, tt := range tests {
		logger := newLogger(filepath.Join(t.TempDir(), "activity.log"), tt.verbosity)
		if got := logger.GetLevel(); got != tt.want {
			t.Errorf("verbosity %d: expected level %s, got %s", tt.verbosity, tt.want, got)
		}
	}
}

func TestNewLoggerWritesPlainTextToLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	logger := newLogger(path, 0)

	logger.Info().Str("item", "Movie").Msg("Refreshed item")
	logger.Debug().Msg("Hidden at info level")

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	got := string(content)
	if !strings.Contains(got, "INF Refreshed item item=Movie") {
		t.Fatalf("expected the info line in the log file, got %q", got)
	}
	if strings.Contains(got, "Hidden at info level") {
		t.Fatalf("expected no debug line at verbosity 0, got %q", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("expected no color codes in the log file, got %q", got)
	}
}
