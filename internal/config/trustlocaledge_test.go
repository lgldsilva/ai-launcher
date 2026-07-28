package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forceTempFileFailure makes the atomic-write step fail deterministically. A
// read-only directory would not do it: the suite also runs as root in CI, where
// the mode bits are ignored.
func forceTempFileFailure(t *testing.T) {
	t.Helper()
	original := createTempFile
	createTempFile = func(string, string) (*os.File, error) {
		return nil, errors.New("no temporary files today")
	}
	t.Cleanup(func() { createTempFile = original })
}

// The trust record is what lets the launcher honor an operator's own saved
// selection, so a failure to write it must be reported rather than swallowed:
// silently losing the record turns the next launch into a refusal the operator
// cannot explain.
func TestRecordTrustedLocalConfigReportsWriteFailures(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte("options:\n  jail: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("empty global path", func(t *testing.T) {
		if err := RecordTrustedLocalConfig("", localPath); err == nil {
			t.Fatal("RecordTrustedLocalConfig(\"\") = nil; there is nowhere to record the hash")
		}
	})

	t.Run("unparseable global config", func(t *testing.T) {
		globalPath := filepath.Join(t.TempDir(), "global.yaml")
		if err := os.WriteFile(globalPath, []byte("profiles: [unterminated\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := RecordTrustedLocalConfig(globalPath, localPath)
		if err == nil {
			t.Fatal("RecordTrustedLocalConfig() = nil; a global config that cannot be parsed must not be overwritten")
		}
		if !strings.Contains(err.Error(), "parse global config") {
			t.Errorf("error = %v; want it to name the parse failure", err)
		}
	})

	t.Run("temporary file cannot be created", func(t *testing.T) {
		forceTempFileFailure(t)
		globalPath := filepath.Join(t.TempDir(), "global.yaml")
		if err := RecordTrustedLocalConfig(globalPath, localPath); err == nil {
			t.Fatal("RecordTrustedLocalConfig() = nil; the atomic write failed")
		}
	})
}

// trustedLocalConfigsMax caps the list so a long history of saves cannot grow
// the global config without bound; the newest entries are the ones to keep,
// because they correspond to the files still on disk.
func TestRecordTrustedLocalConfigCapsTheHistoryKeepingTheNewest(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	localPath := filepath.Join(dir, "local.yaml")

	for i := range trustedLocalConfigsMax + 10 {
		body := "options:\n  jail: false\n# save " + strings.Repeat("x", i) + "\n"
		if err := os.WriteFile(localPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := RecordTrustedLocalConfig(globalPath, localPath); err != nil {
			t.Fatalf("RecordTrustedLocalConfig() error = %v", err)
		}
	}

	loaded, err := LoadGlobal(globalPath)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if len(loaded.TrustedLocalConfigs) != trustedLocalConfigsMax {
		t.Fatalf("TrustedLocalConfigs = %d entries; want the list capped at %d",
			len(loaded.TrustedLocalConfigs), trustedLocalConfigsMax)
	}
	// The file on disk is the last one written, so it must still be trusted.
	if !LocalConfigTrusted(loaded, localPath) {
		t.Fatal("the most recently saved config fell out of the capped list")
	}
}

// The recorded list is read back from a hand-editable YAML document, so entries
// that are not strings have to be skipped rather than crashing the launch.
func TestTrustedHashesIgnoreNonStringEntries(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	localPath := filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte("options:\n  jail: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte("trusted_local_configs:\n  - 12345\n  - deadbeef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordTrustedLocalConfig(globalPath, localPath); err != nil {
		t.Fatalf("RecordTrustedLocalConfig() error = %v", err)
	}
	loaded, err := LoadGlobal(globalPath)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	for _, hash := range loaded.TrustedLocalConfigs {
		if hash == "12345" {
			t.Fatal("a non-string entry was carried over as a hash")
		}
	}
	if !LocalConfigTrusted(loaded, localPath) {
		t.Fatal("the newly recorded hash was lost")
	}
}

// A missing or unreadable local config is simply untrusted. Anything else would
// mean a file the launcher cannot read could still pass the boundary.
func TestLocalConfigTrustedIsFalseWhenTheFileCannotBeRead(t *testing.T) {
	global := Global{TrustedLocalConfigs: []string{"whatever"}}
	if LocalConfigTrusted(global, filepath.Join(t.TempDir(), "absent.yaml")) {
		t.Error("a missing local config must not be trusted")
	}
	if LocalConfigTrusted(global, t.TempDir()) {
		t.Error("a directory must not be trusted")
	}
}

// SaveLocal and SaveGlobal are the atomic 0600 writers behind invariant 3.
// Their failure paths matter because a partial write leaves a config the next
// launch would read.
func TestAtomicSaversReportFailuresInsteadOfWritingPartially(t *testing.T) {
	t.Run("empty paths", func(t *testing.T) {
		if err := SaveLocal("", DefaultLocal()); err == nil {
			t.Error("SaveLocal(\"\") = nil")
		}
		if err := SaveGlobal("", DefaultGlobal()); err == nil {
			t.Error("SaveGlobal(\"\") = nil")
		}
	})

	t.Run("temporary file cannot be created", func(t *testing.T) {
		forceTempFileFailure(t)
		dir := t.TempDir()
		if err := SaveLocal(filepath.Join(dir, "local.yaml"), DefaultLocal()); err == nil {
			t.Error("SaveLocal() = nil; the atomic write failed")
		}
		if err := SaveGlobal(filepath.Join(dir, "global.yaml"), DefaultGlobal()); err == nil {
			t.Error("SaveGlobal() = nil; the atomic write failed")
		}
		if err := SaveRecentAgents(filepath.Join(dir, "global.yaml"), []string{"claude"}); err == nil {
			t.Error("SaveRecentAgents() = nil; the atomic write failed")
		}
	})

	t.Run("target directory is a file", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SaveLocal(filepath.Join(blocker, "local.yaml"), DefaultLocal()); err == nil {
			t.Error("SaveLocal() = nil; the parent path is a regular file")
		}
		if err := SaveGlobal(filepath.Join(blocker, "global.yaml"), DefaultGlobal()); err == nil {
			t.Error("SaveGlobal() = nil; the parent path is a regular file")
		}
	})
}

// SaveLocal writes 0600 because the local config can carry workspace names and
// paths, and the global one carries the ai-memory bearer token (invariant 3).
func TestSaversWriteUserOnlyPermissions(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "local.yaml")
	globalPath := filepath.Join(dir, "global.yaml")
	if err := SaveLocal(localPath, DefaultLocal()); err != nil {
		t.Fatal(err)
	}
	if err := SaveGlobal(globalPath, DefaultGlobal()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{localPath, globalPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %#o; want 0600", filepath.Base(path), perm)
		}
	}
}

// forceClosedTempFile hands the writers a real temp file that is already
// closed, so the write and close steps fail while Name() still resolves. It
// exercises the branches between "temp file created" and "rename", which a
// creation failure jumps straight past.
func forceClosedTempFile(t *testing.T) {
	t.Helper()
	original := createTempFile
	createTempFile = func(dir, pattern string) (*os.File, error) {
		file, err := original(dir, pattern)
		if err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return file, nil
	}
	t.Cleanup(func() { createTempFile = original })
}

// A write that fails midway must not leave the previous config replaced by a
// truncated one. The writers report the failure and the rename never happens,
// so whatever was on disk before is still there.
func TestAtomicSaversLeaveThePreviousFileIntactOnAWriteFailure(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "local.yaml")
	globalPath := filepath.Join(dir, "global.yaml")
	previous := []byte("agent: claude\n")
	for _, path := range []string{localPath, globalPath} {
		if err := os.WriteFile(path, previous, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	forceClosedTempFile(t)
	if err := SaveLocal(localPath, DefaultLocal()); err == nil {
		t.Error("SaveLocal() = nil; writing to a closed file must fail")
	}
	if err := SaveGlobal(globalPath, DefaultGlobal()); err == nil {
		t.Error("SaveGlobal() = nil; writing to a closed file must fail")
	}

	for _, path := range []string{localPath, globalPath} {
		after, err := os.ReadFile(path) // #nosec G304 -- the path is the test's own temp file
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(previous) {
			t.Errorf("%s = %q; a failed save must not replace the previous file", filepath.Base(path), after)
		}
	}
}

// The atomic writers finish with os.Rename. Renaming a file over a directory
// fails, and the error has to reach the caller — the save silently doing
// nothing is how a selection gets lost between launches.
func TestAtomicSaversReportAFailedRename(t *testing.T) {
	dir := t.TempDir()
	occupied := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(occupied, "child"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := SaveLocal(occupied, DefaultLocal()); err == nil {
		t.Error("SaveLocal() = nil; the target path is a non-empty directory")
	}
	if err := SaveGlobal(occupied, DefaultGlobal()); err == nil {
		t.Error("SaveGlobal() = nil; the target path is a non-empty directory")
	}
}

// A global config path that cannot be read for a reason other than "absent"
// (here: it is a directory) must abort the partial update. Treating every read
// error as "no file yet" would replace the operator's catalog with a stub.
func TestPartialUpdatesAbortOnAnUnreadableGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte("options:\n  jail: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	asDirectory := filepath.Join(dir, "global-as-dir")
	if err := os.MkdirAll(asDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := RecordTrustedLocalConfig(asDirectory, localPath); err == nil {
		t.Error("RecordTrustedLocalConfig() = nil; the global config could not be read")
	}
	if err := SaveRecentAgents(asDirectory, []string{"claude"}); err == nil {
		t.Error("SaveRecentAgents() = nil; the global config could not be read")
	}
}

// MemoryRunHarnesses is what memory-harness-unsupported prints as "accepted:".
// An empty or aliased copy would make that error unactionable, and a caller
// mutating the returned slice would corrupt the package's own source of truth.
func TestMemoryRunHarnessesIsANonEmptyDefensiveCopy(t *testing.T) {
	harnesses := MemoryRunHarnesses()
	if len(harnesses) == 0 {
		t.Fatal("MemoryRunHarnesses() = empty; the error message would name nothing")
	}
	for _, harness := range harnesses {
		if !SupportsMemoryRunHarness(harness) {
			t.Errorf("%q is listed but SupportsMemoryRunHarness says no", harness)
		}
	}
	harnesses[0] = "clobbered"
	if !SupportsMemoryRunHarness(MemoryRunHarnesses()[0]) {
		t.Fatal("mutating the returned slice changed the package's list")
	}
}

// SaveRecentAgents updates one key in place. A global config it cannot parse
// must abort rather than replace the operator's catalog with a two-key file.
func TestSaveRecentAgentsRefusesToClobberAnUnparseableConfig(t *testing.T) {
	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	original := []byte("agents: [unterminated\n")
	if err := os.WriteFile(globalPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveRecentAgents(globalPath, []string{"claude"}); err == nil {
		t.Fatal("SaveRecentAgents() = nil; an unparseable config must not be rewritten")
	}
	after, err := os.ReadFile(globalPath) // #nosec G304 -- the path is the test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("global config = %q; it must be left untouched", after)
	}
}
