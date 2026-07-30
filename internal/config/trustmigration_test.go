package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// legacyGlobal is a schema-2.0 global config: trusted_local_configs is a list
// of bare SHA-256 strings, with no path bound to any of them. It also carries
// the keys an operator would hate to lose, so the assertions below can prove
// the migration keeps them.
const legacyGlobal = `version: "2.0"
memory_server_url: https://memory.example.internal
memory_auth_token: s3cr3t
recent_agents:
  - claude
  - codex
profiles:
  review:
    agent: claude
    options:
      jail: true
      memory: true
trusted_local_configs:
  - 5f2b1c4d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b2c
  - 0011223344556677889900aabbccddeeff00112233445566778899aabbccddee
`

// A 2.0 global config must still load. LoadGlobal falls back to DefaultGlobal on
// any parse error, so rejecting the legacy scalar form would cost the operator
// their --add agents, profiles, MRU list and memory token — a far worse
// regression than the trust records they were always going to have to re-save.
func TestLegacyGlobalConfigStillLoadsWithAllKeysIntact(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	if err := os.WriteFile(globalPath, []byte(legacyGlobal), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadGlobal(globalPath)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v; a schema-2.0 file must remain loadable", err)
	}
	if loaded.MemoryServerURL != "https://memory.example.internal" {
		t.Errorf("MemoryServerURL = %q; the migration dropped a configured key", loaded.MemoryServerURL)
	}
	if loaded.MemoryAuthToken != "s3cr3t" {
		t.Error("MemoryAuthToken was lost migrating from 2.0")
	}
	if len(loaded.RecentAgents) != 2 || loaded.RecentAgents[0] != "claude" {
		t.Errorf("RecentAgents = %#v; want the file's list", loaded.RecentAgents)
	}
	if _, ok := loaded.Profiles["review"]; !ok {
		t.Errorf("Profiles = %#v; the saved profile was lost", loaded.Profiles)
	}
	if len(loaded.TrustedLocalConfigs) != 2 {
		t.Fatalf("TrustedLocalConfigs = %#v; want both legacy rows read", loaded.TrustedLocalConfigs)
	}
	for i, entry := range loaded.TrustedLocalConfigs {
		if entry.Path != "" {
			t.Errorf("entry %d Path = %q; a legacy bare hash binds no path", i, entry.Path)
		}
		if entry.Hash == "" {
			t.Errorf("entry %d Hash is empty; the legacy hash should survive the read", i)
		}
	}
}

// Reading the legacy rows is not honoring them. A bare hash proves the bytes,
// not the file, so a cloned checkout carrying the same .ai-launch.yaml must not
// inherit the trust its author recorded under 2.0.
func TestLegacyBareHashDoesNotGrantTrust(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	localPath := filepath.Join(dir, ".ai-launch.yaml")
	body := []byte("version: \"2.0\"\nagent: claude\noptions:\n  jail: false\n")
	if err := os.WriteFile(localPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	hash, err := localConfigHash(localPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := "version: \"2.0\"\ntrusted_local_configs:\n  - " + hash + "\n"
	if err := os.WriteFile(globalPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadGlobal(globalPath)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if LocalConfigTrusted(loaded, localPath) {
		t.Fatal("a schema-2.0 bare hash must not grant trust: it binds no path, " +
			"so identical bytes in any checkout would inherit it")
	}
}

// Re-saving is the documented way back: one --save rewrites the record in the
// path-bound form and the file is honored again.
func TestResavingUpgradesALegacyRecordAndDropsTheStaleRow(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	localPath := filepath.Join(dir, ".ai-launch.yaml")
	if err := os.WriteFile(localPath, []byte("agent: claude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := localConfigHash(localPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := "version: \"2.0\"\ntrusted_local_configs:\n  - " + hash + "\n"
	if err := os.WriteFile(globalPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RecordTrustedLocalConfig(globalPath, localPath); err != nil {
		t.Fatalf("RecordTrustedLocalConfig() error = %v", err)
	}
	loaded, err := LoadGlobal(globalPath)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if !LocalConfigTrusted(loaded, localPath) {
		t.Fatal("after --save the local config must be trusted again")
	}
	if len(loaded.TrustedLocalConfigs) != 1 {
		t.Fatalf("TrustedLocalConfigs = %#v; the stale bare-hash row must be dropped on save",
			loaded.TrustedLocalConfigs)
	}
	if loaded.TrustedLocalConfigs[0].Path == "" {
		t.Error("the rewritten record must bind a canonical path")
	}
	if got := loaded.Version; got != CurrentVersion {
		t.Errorf("version = %q; a save must stamp %q", got, CurrentVersion)
	}
}

// Every version in LoadableVersions must actually load, and anything else must
// be refused by name. This is the guard against bumping CurrentVersion without
// teaching the reader about the version it replaces — the mistake that would
// lock operators out of their own global config on upgrade.
func TestLoadableVersionsAreAcceptedAndOthersAreRefused(t *testing.T) {
	for _, version := range append([]string{""}, LoadableVersions...) {
		if err := ValidateVersion(version); err != nil {
			t.Errorf("ValidateVersion(%q) = %v; want nil", version, err)
		}
		if err := ValidateVersion("  " + version + "\t"); err != nil {
			t.Errorf("ValidateVersion(%q) with surrounding space = %v; want nil", version, err)
		}
	}
	if !slices.Contains(LoadableVersions, "2.0") {
		t.Error("LoadableVersions must keep 2.0 readable: the 2.1 schema change is read-compatible")
	}
	if !slices.Contains(LoadableVersions, CurrentVersion) {
		t.Error("LoadableVersions must contain CurrentVersion")
	}

	err := ValidateVersion("99.0")
	if err == nil {
		t.Fatal("ValidateVersion(\"99.0\") = nil; an unknown schema must be refused")
	}
	for _, loadable := range LoadableVersions {
		if !strings.Contains(err.Error(), loadable) {
			t.Errorf("error %q does not name the supported version %q", err, loadable)
		}
	}
}
