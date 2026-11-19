package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigDirectoryUsesBinaryDirWhenConfigFileIsThere(t *testing.T) {
	binDir := setBinaryPath(t)
	if err := os.WriteFile(filepath.Join(binDir, "config.yml"), nil, 0o644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	stubUserConfigDir(t, func() (string, error) {
		t.Fatal("resolved the user config directory although the binary directory has a config file")
		return "", nil
	})

	if dir := defaultConfigDirectory("autorefresh", "config.yml"); dir != binDir {
		t.Fatalf("expected binary dir %s, got %s", binDir, dir)
	}
}

func TestGetBinaryPathUsesExecutableForPathLaunch(t *testing.T) {
	binDir := setBinaryPath(t)

	// A PATH lookup gives os.Args[0] without a directory.
	original := os.Args[0]
	os.Args[0] = "autorefresh"
	t.Cleanup(func() {
		os.Args[0] = original
	})

	if dir := getBinaryPath(); dir != binDir {
		t.Fatalf("expected binary dir %s, got %s", binDir, dir)
	}
}

func TestGetBinaryPathFallsBackToWorkingDir(t *testing.T) {
	stubExecutable(t, func() (string, error) {
		return "", errors.New("executable unknown")
	})

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}

	if dir := getBinaryPath(); dir != wd {
		t.Fatalf("expected working dir %s, got %s", wd, dir)
	}
}

func TestDefaultConfigDirectoryUsesUserConfigDirWhenAvailable(t *testing.T) {
	setBinaryPath(t)

	configRoot := filepath.Join(t.TempDir(), "user-config")
	stubUserConfigDir(t, func() (string, error) {
		return configRoot, nil
	})

	dir := defaultConfigDirectory("autorefresh", "config.yml")
	want := filepath.Join(configRoot, "autorefresh")
	if dir != want {
		t.Fatalf("expected %s, got %s", want, dir)
	}

	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected config directory %s to exist: %v", want, err)
	}
}

func TestDefaultConfigDirectoryFallsBackWhenUserConfigDirUnavailable(t *testing.T) {
	expected := setBinaryPath(t)

	stubUserConfigDir(t, func() (string, error) {
		return "", errors.New("missing home")
	})

	if dir := defaultConfigDirectory("autorefresh", "config.yml"); dir != expected {
		t.Fatalf("expected fallback to binary dir %s, got %s", expected, dir)
	}
}

func TestDefaultConfigDirectoryFallsBackWhenConfigDirCannotBeCreated(t *testing.T) {
	expected := setBinaryPath(t)

	tempFile := filepath.Join(t.TempDir(), "config-root")
	if err := os.WriteFile(tempFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	stubUserConfigDir(t, func() (string, error) {
		return tempFile, nil
	})

	if dir := defaultConfigDirectory("autorefresh", "config.yml"); dir != expected {
		t.Fatalf("expected fallback to binary dir %s, got %s", expected, dir)
	}
}

func stubUserConfigDir(t *testing.T, fn func() (string, error)) {
	t.Helper()
	original := userConfigDirFunc
	userConfigDirFunc = fn
	t.Cleanup(func() {
		userConfigDirFunc = original
	})
}

func setBinaryPath(t *testing.T) string {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	stubExecutable(t, func() (string, error) {
		return filepath.Join(binDir, "autorefresh"), nil
	})

	return binDir
}

func stubExecutable(t *testing.T, fn func() (string, error)) {
	t.Helper()
	original := executableFunc
	executableFunc = fn
	t.Cleanup(func() {
		executableFunc = original
	})
}
