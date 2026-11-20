package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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

func TestWaitForConfigWaitsForContent(t *testing.T) {
	captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yml")

	go func() {
		time.Sleep(50 * time.Millisecond)
		// an empty file is not ready yet
		_ = os.WriteFile(path, nil, 0o600)
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte("plex: []\n"), 0o600)
	}()

	done := make(chan error, 1)
	go func() {
		file, err := waitForConfig(path, 10*time.Millisecond)
		if err == nil {
			info, statErr := file.Stat()
			if statErr == nil && info.Size() == 0 {
				err = errors.New("opened the config file while it was empty")
			}
			file.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitForConfig did not return after the config file got content")
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
