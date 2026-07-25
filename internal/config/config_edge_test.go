package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if err := SaveLocal("/proc/config.yaml", DefaultLocal()); err == nil || !strings.Contains(err.Error(), "create temporary config") {
		t.Fatalf("SaveLocal(proc) error = %v; want temp-file error", err)
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
