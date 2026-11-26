package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestApplyDefaultPathsKeepsSetPaths(t *testing.T) {
	stubExecutable(t, func() (string, error) {
		t.Fatal("resolved the binary directory although all paths are set")
		return "", nil
	})
	stubUserConfigDir(t, func() (string, error) {
		t.Fatal("resolved the user config directory although all paths are set")
		return "", nil
	})

	config, database, logFile := "/a/config.yml", "/b/autorefresh.db", "/c/activity.log"
	applyDefaultPaths(&config, &database, &logFile)

	if config != "/a/config.yml" || database != "/b/autorefresh.db" || logFile != "/c/activity.log" {
		t.Fatalf("expected the set paths to stay, got %q, %q, %q", config, database, logFile)
	}
}

func TestApplyDefaultPathsFillsEmptyPaths(t *testing.T) {
	setBinaryPath(t)
	configRoot := filepath.Join(t.TempDir(), "user-config")
	stubUserConfigDir(t, func() (string, error) {
		return configRoot, nil
	})

	var config, database, logFile string
	applyDefaultPaths(&config, &database, &logFile)

	dir := filepath.Join(configRoot, "autorefresh")
	want := map[string]string{
		filepath.Join(dir, "config.yml"):     config,
		filepath.Join(dir, "autorefresh.db"): database,
		filepath.Join(dir, "activity.log"):   logFile,
	}
	for expected, got := range want {
		if got != expected {
			t.Errorf("expected %s, got %s", expected, got)
		}
	}
}

// readConfigAsync runs readConfig in a goroutine and fails the test if it
// does not return within 5 seconds.
func readConfigAsync(t *testing.T, path string, interval time.Duration) ([]byte, error) {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := readConfig(context.Background(), path, interval)
		done <- result{data, err}
	}()
	select {
	case r := <-done:
		return r.data, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("readConfig did not return")
		return nil, nil
	}
}

func TestReadConfigWaitsForContent(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")

	go func() {
		time.Sleep(50 * time.Millisecond)
		// an empty file is not ready yet
		_ = os.WriteFile(path, nil, 0o600)
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte("plex: []\n"), 0o600)
	}()

	data, err := readConfigAsync(t, path, 10*time.Millisecond)
	if err != nil || string(data) != "plex: []\n" {
		t.Fatalf("readConfig = %q, %v, want the full file", data, err)
	}
}

// A file that appears during the wait is read only after it stops changing.
// Each wait of readConfig writes one more line, so the file changes between
// every two checks until it is complete.
func TestReadConfigWaitsUntilFileIsWritten(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	const full = "plex:\n  - name: a\n  - name: b\n  - name: c\n"
	lines := strings.SplitAfter(full, "\n")
	original := sleepFunc
	t.Cleanup(func() { sleepFunc = original })
	sleepFunc = func(context.Context, time.Duration) error {
		if len(lines) == 0 || lines[0] == "" {
			return nil
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Error(err)
			return nil
		}
		defer f.Close()
		f.WriteString(lines[0])
		lines = lines[1:]
		return nil
	}

	data, err := readConfig(context.Background(), path, time.Hour)
	if err != nil || string(data) != full {
		t.Fatalf("readConfig = %q, %v, want %q", data, err, full)
	}
}

// A writer can change the file between the last check and the read. The
// bytes of that read do not match the checked state, so readConfig waits for
// the file to stop changing and reads it again.
func TestReadConfigRereadsFileChangedDuringRead(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("plex:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const full = "plex:\n  - name: a\n"
	originalSleep, originalRead := sleepFunc, readFileFunc
	t.Cleanup(func() { sleepFunc, readFileFunc = originalSleep, originalRead })
	sleepFunc = func(context.Context, time.Duration) error { return nil }
	reads := 0
	readFileFunc = func(name string) ([]byte, error) {
		reads++
		data, err := os.ReadFile(name)
		if reads == 1 {
			// The writer finishes after the first read.
			if err := os.WriteFile(name, []byte(full), 0o600); err != nil {
				t.Error(err)
			}
		}
		return data, err
	}

	data, err := readConfig(context.Background(), path, time.Hour)
	if err != nil || string(data) != full || reads != 2 {
		t.Fatalf("readConfig = %q, %v after %d reads, want %q after 2", data, err, reads, full)
	}
}

// A signal during the wait for a missing config ends the wait, so the
// process can stop.
func TestReadConfigStopsWhenContextEnds(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() {
		_, err := readConfig(ctx, path, time.Hour)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("readConfig error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readConfig did not return after the context ended")
	}
}

// A path that cannot be checked, such as a path through a regular file,
// fails at once instead of a wait for a file that can never appear.
func TestReadConfigFailsOnStatError(t *testing.T) {
	captureLog(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := readConfigAsync(t, filepath.Join(file, "config.yml"), time.Hour); err == nil {
		t.Fatal("readConfig = nil error, want the error of the path")
	}
}

func TestReadConfigReturnsReadError(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("plex: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := readFileFunc
	t.Cleanup(func() { readFileFunc = original })
	errRead := errors.New("read failed")
	readFileFunc = func(string) ([]byte, error) { return nil, errRead }

	if _, err := readConfigAsync(t, path, time.Hour); !errors.Is(err, errRead) {
		t.Fatalf("readConfig error = %v, want the read error", err)
	}
}

// If the file vanishes between Stat and ReadFile (e.g. an atomic replace),
// readConfig waits instead of aborting startup.
func TestReadConfigWaitsWhenFileVanishesBeforeRead(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("plex: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalSleep, originalRead := sleepFunc, readFileFunc
	t.Cleanup(func() { sleepFunc, readFileFunc = originalSleep, originalRead })
	sleepFunc = func(context.Context, time.Duration) error { return nil }
	reads := 0
	readFileFunc = func(name string) ([]byte, error) {
		reads++
		if reads == 1 {
			return nil, fs.ErrNotExist
		}
		return os.ReadFile(name)
	}

	data, err := readConfig(context.Background(), path, time.Hour)
	if err != nil || string(data) != "plex: []\n" || reads != 2 {
		t.Fatalf("readConfig = %q, %v after %d reads, want content after 2 reads", data, err, reads)
	}
}

// If a file becomes empty while waiting and later gets the same size and
// modification time again, readConfig still waits for one quiet interval.
func TestReadConfigResetsSeenWhenFileBecomesEmpty(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	fixedTime := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	writeFixed := func(content string) {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, fixedTime, fixedTime); err != nil {
			t.Fatal(err)
		}
	}

	originalSleep := sleepFunc
	t.Cleanup(func() { sleepFunc = originalSleep })
	sleeps := 0
	sleepFunc = func(context.Context, time.Duration) error {
		sleeps++
		switch sleeps {
		case 1:
			// File appears with content; triggers seen = state.
			writeFixed("plex: []\n")
		case 2:
			// File becomes empty; must clear seen.
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		case 3:
			// File reappears with identical size/mtime; must wait one more interval.
			writeFixed("plex: []\n")
		}
		return nil
	}

	data, err := readConfig(context.Background(), path, time.Hour)
	if err != nil || string(data) != "plex: []\n" {
		t.Fatalf("readConfig = %q, %v", data, err)
	}
	if sleeps != 4 {
		t.Fatalf("sleeps = %d, want 4 (must wait one quiet interval after reappearance)", sleeps)
	}
}

func TestReadConfigReadsExistingFileAtOnce(t *testing.T) {
	buf := captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("plex: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	data, err := readConfigAsync(t, path, time.Hour)
	if err != nil || string(data) != "plex: []\n" || time.Since(start) > time.Second {
		t.Fatalf("readConfig = %q, %v after %v, want the file at once", data, err, time.Since(start))
	}
	if buf.Len() != 0 {
		t.Errorf("readConfig logged %q, want nothing", buf.String())
	}
}

// Docker creates a directory for a bind mount of a missing file. The error
// must say so, not report invalid YAML.
func TestReadConfigRejectsDirectory(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := readConfigAsync(t, path, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("readConfig error = %v, want not a regular file", err)
	}
}

func TestReadConfigRejectsOversizedFile(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, make([]byte, maxConfigBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	originalRead := readFileFunc
	t.Cleanup(func() { readFileFunc = originalRead })
	readFileFunc = func(string) ([]byte, error) {
		t.Fatal("readFileFunc was called for an oversized config file")
		return nil, nil
	}

	_, err := readConfigAsync(t, path, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "larger than 1 MiB") {
		t.Fatalf("readConfig error = %v, want larger than 1 MiB", err)
	}
}

func TestReadConfigFileCapsReadAndValidatesType(t *testing.T) {
	dir := t.TempDir()
	if _, err := readConfigFile(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("readConfigFile(dir) = %v, want not a regular file", err)
	}
	big := filepath.Join(dir, "big.yml")
	if err := os.WriteFile(big, make([]byte, maxConfigBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigFile(big); err == nil || !strings.Contains(err.Error(), "larger than 1 MiB") {
		t.Fatalf("readConfigFile(big) = %v, want larger than 1 MiB", err)
	}
	ok := filepath.Join(dir, "ok.yml")
	if err := os.WriteFile(ok, []byte("plex: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readConfigFile(ok)
	if err != nil || string(data) != "plex: []\n" {
		t.Fatalf("readConfigFile(ok) = %q, %v", data, err)
	}
}

// captureLog sends the global logger to a buffer until the test ends. The
// caller reads the buffer only after every logging goroutine is done.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := log.Logger
	// Some tests log from several goroutines.
	log.Logger = zerolog.New(zerolog.SyncWriter(&buf))
	t.Cleanup(func() {
		log.Logger = original
	})
	return &buf
}
