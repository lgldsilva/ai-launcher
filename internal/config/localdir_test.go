package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalConfigFallbackAndDirectorySelection(t *testing.T) {
	project := t.TempDir()
	legacy := LegacyLocalConfigPath(project)
	newPath := LocalConfigPath(project)
	if err := os.MkdirAll(filepath.Dir(newPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("agent: codex\noptions:\n  docker: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadLocal(newPath)
	if err != nil {
		t.Fatalf("LoadLocal(new path) error = %v", err)
	}
	if loaded.Agent != "codex" || !loaded.Options.Docker {
		t.Fatalf("LoadLocal(new path) = %#v; want the legacy fallback", loaded)
	}
	loaded, err = LoadLocal(LocalConfigDir(project))
	if err != nil {
		t.Fatalf("LoadLocal(directory) error = %v", err)
	}
	if loaded.Agent != "codex" {
		t.Fatalf("LoadLocal(directory) agent = %q; want codex", loaded.Agent)
	}

	if err := os.WriteFile(newPath, []byte("agent: claude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadLocal(legacy)
	if err != nil {
		t.Fatalf("LoadLocal(legacy with new path) error = %v", err)
	}
	if loaded.Agent != "claude" {
		t.Fatalf("LoadLocal(legacy with new path) agent = %q; new config must win", loaded.Agent)
	}
}

func TestLocalConfigMigrationWritesNewPathAndBackup(t *testing.T) {
	project := t.TempDir()
	legacy := LegacyLocalConfigPath(project)
	if err := os.WriteFile(legacy, []byte("agent: codex\noptions:\n  jail: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SaveLocalResult(legacy, Local{Agent: "claude", Options: Options{Jail: true}})
	if err != nil {
		t.Fatalf("SaveLocalResult() error = %v", err)
	}
	if !result.Migrated || result.Path != LocalConfigPath(project) || result.BackupPath != legacy+".bak" {
		t.Fatalf("SaveLocalResult() = %#v; want the directory path and .bak migration", result)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy config stat error = %v; want the old path renamed", err)
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("backup stat error = %v", err)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("new config stat error = %v", err)
	}
	loaded, err := LoadLocal(result.Path)
	if err != nil {
		t.Fatalf("LoadLocal(migrated) error = %v", err)
	}
	if loaded.Agent != "claude" || !loaded.Options.Jail {
		t.Fatalf("migrated config = %#v; want the saved selection", loaded)
	}
}

func TestResolveLocalConfigPathPrefersNewAndDefaultsToNew(t *testing.T) {
	project := t.TempDir()
	if got := ResolveLocalConfigPath(project); got != LocalConfigPath(project) {
		t.Fatalf("missing config path = %q; want %q", got, LocalConfigPath(project))
	}
	if err := os.WriteFile(LegacyLocalConfigPath(project), []byte("agent: claude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveLocalConfigPath(project); got != LegacyLocalConfigPath(project) {
		t.Fatalf("legacy config path = %q; want %q", got, LegacyLocalConfigPath(project))
	}
	if err := os.MkdirAll(filepath.Dir(LocalConfigPath(project)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LocalConfigPath(project), []byte("agent: codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveLocalConfigPath(project); got != LocalConfigPath(project) {
		t.Fatalf("new config path = %q; want %q", got, LocalConfigPath(project))
	}
}

func TestLocalConfigTrustUsesFallbackFileWhenNewPathIsExplicit(t *testing.T) {
	project := t.TempDir()
	legacy := LegacyLocalConfigPath(project)
	newPath := LocalConfigPath(project)
	if err := os.WriteFile(legacy, []byte("agent: claude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := localConfigHash(legacy)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalLocalPath(legacy)
	if err != nil {
		t.Fatal(err)
	}
	global := Global{TrustedLocalConfigs: []TrustedLocalEntry{{Path: canonical, Hash: hash}}}
	if !LocalConfigTrusted(global, newPath) {
		t.Fatal("an explicit new path falling back to the legacy file must use the legacy trust record")
	}
}

func TestSaveLocalAcceptsNotYetCreatedLauncherDirectory(t *testing.T) {
	project := t.TempDir()
	result, err := SaveLocalResult(LocalConfigDir(project), DefaultLocal())
	if err != nil {
		t.Fatalf("SaveLocalResult(directory) error = %v", err)
	}
	if result.Path != LocalConfigPath(project) {
		t.Fatalf("written path = %q; want %q", result.Path, LocalConfigPath(project))
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("new directory config was not written: %v", err)
	}
}

func TestSaveLocalMigrationUsesNextBackupWhenBakExists(t *testing.T) {
	project := t.TempDir()
	legacy := LegacyLocalConfigPath(project)
	if err := os.WriteFile(legacy, []byte("agent: codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy+".bak", []byte("previous backup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := SaveLocalResult(legacy, DefaultLocal())
	if err != nil {
		t.Fatalf("SaveLocalResult() error = %v", err)
	}
	if !strings.HasSuffix(result.BackupPath, ".bak.1") {
		t.Fatalf("backup path = %q; want a non-destructive suffix", result.BackupPath)
	}
}
