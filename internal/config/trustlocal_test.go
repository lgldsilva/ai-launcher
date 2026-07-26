package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A launcher-saved local config proves its provenance through the recorded
// hash; a byte of difference (a hand edit, a git pull) revokes it.
func TestLocalConfigTrustRoundTrip(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	localPath := filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte("options:\n  jail: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	global := Global{}
	if LocalConfigTrusted(global, localPath) {
		t.Fatal("an unrecorded file must not be trusted")
	}
	if err := RecordTrustedLocalConfig(globalPath, localPath); err != nil {
		t.Fatalf("RecordTrustedLocalConfig() error = %v", err)
	}
	loaded, err := LoadGlobal(globalPath)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if !LocalConfigTrusted(loaded, localPath) {
		t.Fatal("a launcher-saved file must be trusted on the next launch")
	}
	if err := os.WriteFile(localPath, []byte("options:\n  jail: false\n# edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if LocalConfigTrusted(loaded, localPath) {
		t.Fatal("editing the file after the save must revoke the trust")
	}
}

// Recording twice deduplicates, and other keys in the global file survive.
func TestRecordTrustedLocalConfigDedupesAndPreservesKeys(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	localPath := filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte("options:\n  jail: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte("memory_server_url: https://example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := RecordTrustedLocalConfig(globalPath, localPath); err != nil {
			t.Fatalf("RecordTrustedLocalConfig() error = %v", err)
		}
	}
	loaded, err := LoadGlobal(globalPath)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if len(loaded.TrustedLocalConfigs) != 1 {
		t.Fatalf("TrustedLocalConfigs = %#v; the same hash recorded twice must dedupe", loaded.TrustedLocalConfigs)
	}
	if loaded.MemoryServerURL != "https://example.test" {
		t.Fatalf("MemoryServerURL = %q; existing keys must survive the partial write", loaded.MemoryServerURL)
	}
	if err := RecordTrustedLocalConfig(globalPath, filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("hashing a nonexistent local config must fail")
	}
}
