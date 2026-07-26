package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubCreateTempFailure forces the temporary-file creation step to fail with
// a deterministic error, independent of filesystem permissions — a chmod-based
// read-only directory does not fail under root or on Windows.
func stubCreateTempFailure(t *testing.T) {
	t.Helper()
	original := createTempFile
	createTempFile = func(string, string) (*os.File, error) {
		return nil, errors.New("forced temp-file failure")
	}
	t.Cleanup(func() { createTempFile = original })
}

func TestLoadGlobalRejectsUnreadableDirectoryAndUnsupportedVersion(t *testing.T) {
	tempDir := t.TempDir()
	if got, err := LoadGlobal(""); err != nil || got.Version != CurrentVersion {
		t.Fatalf("LoadGlobal(empty) = %#v, %v; want defaults", got, err)
	}
	if _, err := LoadGlobal(tempDir); err == nil || !strings.Contains(err.Error(), "read global config") {
		t.Fatalf("LoadGlobal(directory) error = %v; want read error", err)
	}
	path := filepath.Join(tempDir, "global.yaml")
	if err := writeFile(path, "version: '99'\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadGlobal(path); err == nil || !strings.Contains(err.Error(), "unsupported config version") {
		t.Fatalf("LoadGlobal(unsupported) = %#v, %v", got, err)
	}
}

func TestLoadLocalRejectsUnreadableDirectoryAndMalformedYaml(t *testing.T) {
	tempDir := t.TempDir()
	if got, err := LoadLocal(""); err != nil || got.Version != CurrentVersion {
		t.Fatalf("LoadLocal(empty) = %#v, %v; want defaults", got, err)
	}
	if _, err := LoadLocal(tempDir); err == nil || !strings.Contains(err.Error(), "read local config") {
		t.Fatalf("LoadLocal(directory) error = %v; want read error", err)
	}
	path := filepath.Join(tempDir, "local.yaml")
	if err := writeFile(path, "options: [\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadLocal(path); err == nil || !strings.Contains(err.Error(), "parse local config") {
		t.Fatalf("LoadLocal(malformed) = %#v, %v", got, err)
	}
}

func TestSaveLocalRejectsEmptyAndDirectoryDestination(t *testing.T) {
	if err := SaveLocal("", DefaultLocal()); err == nil {
		t.Fatal("empty path should fail")
	}
	destination := t.TempDir()
	if err := SaveLocal(destination, DefaultLocal()); err == nil || !strings.Contains(err.Error(), "replace local config") {
		t.Fatalf("SaveLocal(directory) error = %v; want replace error", err)
	}
	parentFile := filepath.Join(t.TempDir(), "parent-file")
	if err := writeFile(parentFile, "not a directory"); err != nil {
		t.Fatal(err)
	}
	if err := SaveLocal(filepath.Join(parentFile, "config.yaml"), DefaultLocal()); err == nil || !strings.Contains(err.Error(), "create config directory") {
		t.Fatalf("SaveLocal(file parent) error = %v; want mkdir error", err)
	}
	stubCreateTempFailure(t)
	if err := SaveLocal(filepath.Join(t.TempDir(), "config.yaml"), DefaultLocal()); err == nil || !strings.Contains(err.Error(), "create temporary config") {
		t.Fatalf("SaveLocal(temp-file failure) error = %v; want temp-file error", err)
	}
}

func TestSaveLocalInitializesNilPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.yaml")
	if err := SaveLocal(path, Local{Agent: "claude", Options: Options{Jail: true, Memory: true}}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLocal(path)
	if err != nil || got.Permissions == nil {
		t.Fatalf("nil permissions round-trip = %#v, %v", got, err)
	}
}

func writeFile(path, value string) error {
	return os.WriteFile(path, []byte(value), 0o600)
}

// The empty-path guard must fire before any filesystem work: without it the
// save proceeds into a temp file in the current directory and fails later
// with a misleading rename error.
func TestSaveFunctionsRejectEmptyPathWithAClearMessage(t *testing.T) {
	if err := SaveGlobal("", DefaultGlobal()); err == nil || !strings.Contains(err.Error(), "path is empty") {
		t.Fatalf("SaveGlobal(empty) error = %v; want \"path is empty\"", err)
	}
	if err := SaveLocal("", DefaultLocal()); err == nil || !strings.Contains(err.Error(), "path is empty") {
		t.Fatalf("SaveLocal(empty) error = %v; want \"path is empty\"", err)
	}
	if err := SaveRecentAgents("", []string{"claude"}); err == nil || !strings.Contains(err.Error(), "path is empty") {
		t.Fatalf("SaveRecentAgents(empty) error = %v; want \"path is empty\"", err)
	}
}

// Each writeGlobalAtomically failure stage must report its own step, and a
// failed save must not leak the temporary file into the config directory.
func TestSaveGlobalReportsEachWriteStageAndCleansUp(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := writeFile(blocker, "file"); err != nil {
		t.Fatal(err)
	}
	if err := SaveGlobal(filepath.Join(blocker, "config.yaml"), DefaultGlobal()); err == nil || !strings.Contains(err.Error(), "create global config directory") {
		t.Fatalf("SaveGlobal(file parent) error = %v; want mkdir error", err)
	}

	scratch := t.TempDir()
	destination := filepath.Join(scratch, "target")
	if err := os.MkdirAll(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := SaveGlobal(destination, DefaultGlobal()); err == nil || !strings.Contains(err.Error(), "replace global config") {
		t.Fatalf("SaveGlobal(directory) error = %v; want replace error", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(scratch, ".ai-launch-global-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("failed save leaked temporary files: %#v", leftovers)
	}

	stubCreateTempFailure(t)
	if err := SaveGlobal(filepath.Join(t.TempDir(), "config.yaml"), DefaultGlobal()); err == nil || !strings.Contains(err.Error(), "create temporary global config") {
		t.Fatalf("SaveGlobal(temp-file failure) error = %v; want temp-file error", err)
	}
}

func TestSaveLocalFailureDoesNotLeakTemporaryFiles(t *testing.T) {
	scratch := t.TempDir()
	destination := filepath.Join(scratch, "target")
	if err := os.MkdirAll(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := SaveLocal(destination, DefaultLocal()); err == nil {
		t.Fatal("SaveLocal(directory) = nil; want error")
	}
	leftovers, err := filepath.Glob(filepath.Join(scratch, ".ai-launch-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("failed save leaked temporary files: %#v", leftovers)
	}
}

// A missing local config is a first-run situation, not an error: the safe
// defaults come back with a nil error.
func TestLoadLocalFallsBackToDefaultsForAMissingFile(t *testing.T) {
	got, err := LoadLocal(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadLocal(missing) error = %v", err)
	}
	if got.Version != CurrentVersion || got.Agent != "claude" || !got.Options.Jail || !got.Options.Memory {
		t.Fatalf("LoadLocal(missing) = %#v; want safe defaults", got)
	}
}

func TestSaveRecentAgentsRejectsUnreadableAndMalformedDocuments(t *testing.T) {
	if err := SaveRecentAgents(t.TempDir(), []string{"claude"}); err == nil || !strings.Contains(err.Error(), "read global config") {
		t.Fatalf("SaveRecentAgents(directory) error = %v; want read error", err)
	}
	malformed := filepath.Join(t.TempDir(), "global.yaml")
	if err := writeFile(malformed, "agents: [\n"); err != nil {
		t.Fatal(err)
	}
	if err := SaveRecentAgents(malformed, []string{"claude"}); err == nil || !strings.Contains(err.Error(), "parse global config") {
		t.Fatalf("SaveRecentAgents(malformed) error = %v; want parse error", err)
	}
}
