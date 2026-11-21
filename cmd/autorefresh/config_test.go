package main

import (
	"bytes"
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
		data, err := readConfig(path, interval)
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
	sleepFunc = func(time.Duration) {
		if len(lines) == 0 || lines[0] == "" {
			return
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		f.WriteString(lines[0])
		lines = lines[1:]
	}

	data, err := readConfig(path, time.Hour)
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
	sleepFunc = func(time.Duration) {}
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

	data, err := readConfig(path, time.Hour)
	if err != nil || string(data) != full || reads != 2 {
		t.Fatalf("readConfig = %q, %v after %d reads, want %q after 2", data, err, reads, full)
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
