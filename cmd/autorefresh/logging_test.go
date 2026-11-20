package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		logger := newLogger(io.Discard, filepath.Join(t.TempDir(), "activity.log"), tt.verbosity)
		if got := logger.GetLevel(); got != tt.want {
			t.Errorf("verbosity %d: expected level %s, got %s", tt.verbosity, tt.want, got)
		}
	}
}

func TestNewLoggerWritesPlainTextToLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	logger := newLogger(io.Discard, path, 0)

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

func TestNewLoggerWritesReadableDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	logger := newLogger(io.Discard, path, 0)

	logger.Info().Dur("duration", 11*time.Minute+3814*time.Millisecond).Msg("Run summary")

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "duration=11m3.814s") {
		t.Fatalf("expected a readable duration in the log file, got %q", content)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("console closed")
}

func TestNewLoggerWritesLogFileWhenConsoleFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	logger := newLogger(failingWriter{}, path, 0)

	logger.Info().Msg("Still logged")

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "Still logged") {
		t.Fatalf("expected the line in the log file, got %q", content)
	}
}
