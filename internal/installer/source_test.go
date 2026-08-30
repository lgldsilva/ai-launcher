package installer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// The fixtures below model the two shapes a source_url recipe can hold. The
// installer one carries the exact self-detect deadlock that broke eight agent
// CLIs: it points its install target at the command path and refuses to run if
// something is already there.

const deadlockInstallerScript = "#!/bin/bash\n" +
	"TARGET_DIR=\"$(dirname \"$0\")\"\n" +
	"BINARY_PATH=\"$TARGET_DIR/tool\"\n" +
	"if [ -f \"$BINARY_PATH\" ]; then\n" +
	"  echo \"Notice: 'tool' is already installed at $BINARY_PATH.\"\n" +
	"  echo \"  rm \\\"$BINARY_PATH\\\"\"\n" +
	"  exit 0\n" +
	"fi\n" +
	"echo downloading release package...\n"

const managedWrapperScript = "#!/usr/bin/env bash\nexec managed-native-runner \"$@\"\n"

const workingBinaryScript = "#!/bin/sh\necho 7.8.9\n"

// sourceFixture serves a script over TLS (swappable mid-test, because a recipe
// changing upstream is exactly the case that must not be cached as "current")
// and records every command the installer tried to run.
type sourceFixture struct {
	client   *Installer
	target   string
	runs     [][]string // argv of every command the installer tried to run
	probeOut string
	probeErr error
	// probeFn replaces the fixed probe answer when a test needs the verdict to
	// depend on the file actually sitting at the probed path.
	probeFn       func(path string) (string, error)
	installEffect func(t *testing.T) error
	installErr    error
	pathHits      map[string]string // command -> resolved path for LookPath
	servedScript  *atomic.Value     // what the TLS server hands out right now
	sourceURL     string            // the TLS test server's URL, as a recipe would hold it
}

func newSourceFixture(t *testing.T, script string) *sourceFixture {
	t.Helper()
	var served atomic.Value
	served.Store(script)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(served.Load().(string)))
	}))
	t.Cleanup(func() { served.Store(script) })
	t.Cleanup(server.Close)
	root := t.TempDir()
	fixture := &sourceFixture{
		sourceURL:    server.URL,
		target:       filepath.Join(root, ".local", "bin", "tool"),
		probeOut:     workingBinaryScript,
		pathHits:     map[string]string{},
		servedScript: &served,
	}
	client := New(root)
	client.HTTPClient = server.Client()
	client.StatePath = filepath.Join(root, "state.json")
	client.GOOS = "linux"
	client.GOARCH = "amd64"
	client.LookPath = func(name string) (string, error) {
		if hit, ok := fixture.pathHits[name]; ok {
			return hit, nil
		}
		return "", errors.New("not found")
	}
	client.Run = func(_ context.Context, name string, args ...string) (string, error) {
		argv := append([]string{name}, args...)
		fixture.runs = append(fixture.runs, argv)
		if len(args) > 0 && args[len(args)-1] == "--version" {
			if fixture.probeFn != nil {
				return fixture.probeFn(name)
			}
			return fixture.probeOut, fixture.probeErr
		}
		// The bootstrapper invocation.
		if fixture.installEffect != nil {
			if err := fixture.installEffect(t); err != nil {
				return "installer failed\n", err
			}
		}
		if fixture.installErr != nil {
			return "installer reported a problem\n", fixture.installErr
		}
		return "installed\n", nil
	}
	fixture.client = client
	return fixture
}

// setRecipe changes what the server serves, simulating an upstream recipe edit.
func (f *sourceFixture) setRecipe(script string) { f.servedScript.Store(script) }

// writeExecutable puts an executable file at path, as a vendor installer would.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// #nosec G302 -- the fixture must be executable: it stands in for a vendor-installed CLI
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
}

// TestInstallSourceExecutesInstallerInsteadOfStoringIt is the core fix: a
// one-shot bootstrapper must be run, never left at the command path.
func TestInstallSourceExecutesInstallerInsteadOfStoringIt(t *testing.T) {
	fixture := newSourceFixture(t, deadlockInstallerScript)
	target := fixture.target
	fixture.installEffect = func(t *testing.T) error {
		writeExecutable(t, target, workingBinaryScript)
		return nil
	}

	result, err := fixture.client.InstallSource(context.Background(), "Tool", "tool", nil, target, fixture.sourceURL, false)
	if err != nil {
		t.Fatalf("InstallSource: %v", err)
	}
	if result.Status != "installed" {
		t.Errorf("Status = %q, want installed", result.Status)
	}
	if result.Version != "7.8.9" {
		t.Errorf("Version = %q, want the version the probe reported", result.Version)
	}
	stored, readErr := os.ReadFile(target) // #nosec G304 -- path created by this test
	if readErr != nil {
		t.Fatalf("target missing after install: %v", readErr)
	}
	if strings.Contains(string(stored), "already installed") {
		t.Fatal("the vendor installer was left at the command path — that is the deadlock")
	}
	// The bootstrapper ran from staging, which is never a PATH directory.
	var staged string
	for _, argv := range fixture.runs {
		if len(argv) == 2 && argv[1] != "--version" {
			staged = argv[1]
		}
	}
	if staged == "" {
		t.Fatalf("installer was never executed; runs=%v", fixture.runs)
	}
	if !strings.Contains(staged, filepath.Join(".local", "share", "ai-launcher", "staging")) {
		t.Errorf("installer ran from %q, want a staging dir outside PATH", staged)
	}
	if strings.HasPrefix(staged, filepath.Dir(target)) {
		t.Errorf("installer staged inside the command directory: %q", staged)
	}
}

// TestInstallSourceClearsStaleInstallerBeforeRunningIt is the self-heal of the
// incident: when the command path already holds an installer, running the
// bootstrapper with it still there re-triggers "already installed". The stale
// script must be gone before the installer runs.
func TestInstallSourceClearsStaleInstallerBeforeRunningIt(t *testing.T) {
	fixture := newSourceFixture(t, deadlockInstallerScript)
	target := fixture.target
	// The corrupted machine: an installer sitting where the command should be.
	writeExecutable(t, target, deadlockInstallerScript)

	deadlockSeen := false
	fixture.installEffect = func(t *testing.T) error {
		stored, err := os.ReadFile(target) // #nosec G304
		if err == nil && strings.Contains(string(stored), "already installed") {
			deadlockSeen = true // the vendor script would refuse to install here
		}
		writeExecutable(t, target, workingBinaryScript)
		return nil
	}

	result, err := fixture.client.InstallSource(context.Background(), "Tool", "tool", nil, target, fixture.sourceURL, false)
	if err != nil {
		t.Fatalf("InstallSource over a stale installer: %v", err)
	}
	if deadlockSeen {
		t.Fatal("the installer was run while its deadlock file still occupied the target")
	}
	if result.Status != "installed" || result.Version != "7.8.9" {
		t.Errorf("result = %#v, want installed 7.8.9", result)
	}
}

// TestInstallSourceResolvesExecutablePlacedElsewhere covers vendors that
// install into their own home directory instead of ~/.local/bin.
func TestInstallSourceResolvesExecutablePlacedElsewhere(t *testing.T) {
	fixture := newSourceFixture(t, deadlockInstallerScript)
	installedAt := filepath.Join(fixture.client.HomeDir, ".vendor", "bin", "tool")
	fixture.installEffect = func(t *testing.T) error {
		writeExecutable(t, installedAt, workingBinaryScript)
		return nil
	}
	fixture.pathHits["tool"] = installedAt

	result, err := fixture.client.InstallSource(context.Background(), "Tool", "tool", nil, fixture.target, fixture.sourceURL, false)
	if err != nil {
		t.Fatalf("InstallSource: %v", err)
	}
	if result.Path != installedAt {
		t.Errorf("Path = %q, want the resolved executable %q", result.Path, installedAt)
	}
	if _, err := os.Stat(fixture.target); !os.IsNotExist(err) {
		t.Errorf("no script should have been left at the configured target: %v", err)
	}
}

// TestInstallSourceAliasResolves covers Cursor Agent, whose bootstrapper links
// both `agent` and `cursor-agent`.
func TestInstallSourceAliasResolves(t *testing.T) {
	fixture := newSourceFixture(t, deadlockInstallerScript)
	aliasPath := filepath.Join(fixture.client.HomeDir, ".local", "share", "vendor", "agent")
	fixture.installEffect = func(t *testing.T) error {
		writeExecutable(t, aliasPath, workingBinaryScript)
		return nil
	}
	fixture.pathHits["agent"] = aliasPath

	result, err := fixture.client.InstallSource(context.Background(), "Cursor Agent", "cursor-agent", []string{"agent"}, fixture.target, fixture.sourceURL, false)
	if err != nil {
		t.Fatalf("InstallSource: %v", err)
	}
	if result.Path != aliasPath {
		t.Errorf("Path = %q, want alias resolution %q", result.Path, aliasPath)
	}
}

// TestInstallSourceKeepsGoingWhenInteractiveSetupFails models Devin: the
// bootstrapper exits non-zero because a login prompt got EOF, yet the binary it
// installed is perfectly usable. Judging by exit code alone would report a
// failure on a successful install.
func TestInstallSourceKeepsGoingWhenInteractiveSetupFails(t *testing.T) {
	fixture := newSourceFixture(t, deadlockInstallerScript)
	target := fixture.target
	fixture.installEffect = func(t *testing.T) error {
		writeExecutable(t, target, workingBinaryScript)
		return errors.New("exit status 1")
	}
	fixture.installErr = errors.New("login canceled")

	result, err := fixture.client.InstallSource(context.Background(), "Tool", "tool", nil, target, fixture.sourceURL, false)
	if err != nil {
		t.Fatalf("a declined login must not fail a completed install: %v", err)
	}
	if result.Status != "installed" || result.Version != "7.8.9" {
		t.Errorf("result = %#v, want installed 7.8.9", result)
	}
}

// TestInstallSourceRestoresTargetWhenInstallerProducesNothing is the
// transactional guarantee: a failed escalation must not leave the machine one
// step worse than it found it.
func TestInstallSourceRestoresTargetWhenInstallerProducesNothing(t *testing.T) {
	fixture := newSourceFixture(t, deadlockInstallerScript)
	target := fixture.target
	writeExecutable(t, target, "#!/bin/sh\necho 1.0.0\n")

	fixture.installErr = errors.New("exit status 1") // installs nothing
	_, err := fixture.client.InstallSource(context.Background(), "Tool", "tool", nil, target, fixture.sourceURL, true)
	if err == nil {
		t.Fatal("want an error when the installer produces no runnable command")
	}
	if !strings.Contains(err.Error(), "no runnable") {
		t.Errorf("error = %v, want it to name the missing executable", err)
	}
	stored, readErr := os.ReadFile(target) // #nosec G304
	if readErr != nil || !strings.Contains(string(stored), "1.0.0") {
		t.Fatalf("previous executable was not restored: %q, %v", stored, readErr)
	}
}

// TestInstallSourceReportsInstallerOutputOnFailure keeps the diagnosis in the
// error instead of a bare exit code.
func TestInstallSourceReportsInstallerOutputOnFailure(t *testing.T) {
	fixture := newSourceFixture(t, deadlockInstallerScript)
	fixture.installErr = errors.New("exit status 127")
	fixture.probeOut = ""
	fixture.probeErr = errors.New("exit status 127")
	_, err := fixture.client.InstallSource(context.Background(), "Tool", "tool", nil, fixture.target, fixture.sourceURL, true)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "installer reported a problem") {
		t.Errorf("error = %v, want captured installer output", err)
	}
}

// TestInstallSourceWrapperStaysStored is the ai-memory case: a managed wrapper
// that IS the command must keep being stored, not executed once and discarded.
func TestInstallSourceWrapperStaysStored(t *testing.T) {
	fixture := newSourceFixture(t, managedWrapperScript)
	fixture.probeOut = "ai-memory 1.34.0\n"

	result, err := fixture.client.InstallSource(context.Background(), "ai-memory", "ai-memory", nil, fixture.target, fixture.sourceURL, false)
	if err != nil {
		t.Fatalf("InstallSource wrapper: %v", err)
	}
	if result.Path != fixture.target {
		t.Errorf("Path = %q, want the wrapper stored at the command path", result.Path)
	}
	stored, readErr := os.ReadFile(fixture.target) // #nosec G304
	if readErr != nil || string(stored) != managedWrapperScript {
		t.Fatalf("wrapper contents changed: %q, %v", stored, readErr)
	}
	for _, argv := range fixture.runs {
		if len(argv) == 2 && !strings.HasSuffix(argv[1], "--version") {
			t.Fatalf("a managed wrapper must not be executed as an installer: %v", argv)
		}
	}
	if result.Version != "1.34.0" {
		t.Errorf("Version = %q, want 1.34.0", result.Version)
	}
}

// TestInstallSourceSecondRunIsCurrentWithoutReinstalling proves idempotence is
// behaviour-based: an installer recipe must re-verify, not trust byte identity.
func TestInstallSourceSecondRunIsCurrentWithoutReinstalling(t *testing.T) {
	fixture := newSourceFixture(t, managedWrapperScript)
	fixture.probeOut = "ai-memory 1.34.0\n"
	first, err := fixture.client.InstallSource(context.Background(), "ai-memory", "ai-memory", nil, fixture.target, fixture.sourceURL, false)
	if err != nil || first.Status != "installed" {
		t.Fatalf("first = %#v, err=%v", first, err)
	}
	runsBefore := len(fixture.runs)
	second, err := fixture.client.InstallSource(context.Background(), "ai-memory", "ai-memory", nil, fixture.target, fixture.sourceURL, false)
	if err != nil || second.Status != "current" {
		t.Fatalf("second = %#v, err=%v", second, err)
	}
	if len(fixture.runs) != runsBefore+1 {
		t.Errorf("expected exactly one probe on the current path, got %d runs", len(fixture.runs)-runsBefore)
	}
}

// TestInstallSourceRepairsACommandMaskedByAnInstaller is the end-to-end of the
// incident: a wrapper-classified recipe whose stored file now answers with
// installer usage must be repaired rather than reported as current.
func TestInstallSourceRepairsACommandMaskedByAnInstaller(t *testing.T) {
	// Recipe text looks like a wrapper, but what is on disk behaves like an
	// installer: the state a machine reaches when something else wrote the
	// bootstrapper to the command path.
	fixture := newSourceFixture(t, managedWrapperScript)
	writeExecutable(t, fixture.target, deadlockInstallerScript)
	target := fixture.target
	fixture.installEffect = func(t *testing.T) error {
		writeExecutable(t, target, workingBinaryScript)
		return nil
	}
	// The probe reflects the file at the path, as a real probe would.
	fixture.probeFn = func(path string) (string, error) {
		body, err := os.ReadFile(path) // #nosec G304
		if err != nil {
			return "", err
		}
		if strings.Contains(string(body), "already installed") {
			return "Notice: 'tool' is already installed at " + path + ".\n", nil
		}
		return "7.8.9\n", nil
	}

	result, err := fixture.client.InstallSource(context.Background(), "Tool", "tool", nil, target, fixture.sourceURL, false)
	if err != nil {
		t.Fatalf("InstallSource: %v", err)
	}
	if result.Status != "installed" || result.Version != "7.8.9" {
		t.Errorf("result = %#v, want a repaired install reporting 7.8.9", result)
	}
}

// TestInstallSourceDoesNotRerunInstallerForAHealthyCommand protects the
// expensive and side-effecting path: a bootstrapper that already produced its
// binary must not be run again on the next install. Re-running would re-fetch a
// whole release, and for vendors with a pre-existence check (Antigravity) the
// script would then refuse and the install would be reported as broken when the
// command is in fact fine.
func TestInstallSourceDoesNotRerunInstallerForAHealthyCommand(t *testing.T) {
	fixture := newSourceFixture(t, deadlockInstallerScript)
	target := fixture.target
	fixture.installEffect = func(t *testing.T) error {
		writeExecutable(t, target, workingBinaryScript)
		return nil
	}
	// The probe reflects whatever is at the path, as a real probe does.
	fixture.probeFn = func(path string) (string, error) {
		body, err := os.ReadFile(path) // #nosec G304
		if err != nil {
			return "", err
		}
		if strings.Contains(string(body), "already installed") {
			return "Notice: 'tool' is already installed at " + path + ".\n", nil
		}
		return "7.8.9\n", nil
	}

	first, err := fixture.client.InstallSource(context.Background(), "Tool", "tool", nil, target, fixture.sourceURL, false)
	if err != nil || first.Status != "installed" {
		t.Fatalf("first = %#v, err=%v", first, err)
	}
	installRuns := len(fixture.runs)

	second, err := fixture.client.InstallSource(context.Background(), "Tool", "tool", nil, target, fixture.sourceURL, false)
	if err != nil {
		t.Fatalf("second InstallSource: %v", err)
	}
	if second.Status != "current" {
		t.Errorf("Status = %q, want current", second.Status)
	}
	if second.Version != "7.8.9" {
		t.Errorf("Version = %q, want the probed version", second.Version)
	}
	if len(fixture.runs) != installRuns+1 {
		t.Errorf("runs grew by %d, want exactly one more (the probe, not a re-install)", len(fixture.runs)-installRuns)
	}
	for _, argv := range fixture.runs[installRuns:] {
		if len(argv) == 2 && argv[1] != "--version" {
			t.Errorf("the bootstrapper was re-run on a healthy command: %v", argv)
		}
	}
}

// TestInstallSourceReinstallsWhenTheRecipeMovesOn keeps idempotence from
// turning into a stale cache: a wrapper whose upstream bytes changed must be
// written again, not reported as current. This is the case the old byte-equality
// fast path already handled, so losing it would silently stop updates.
func TestInstallSourceReinstallsWhenTheRecipeMovesOn(t *testing.T) {
	fixture := newSourceFixture(t, managedWrapperScript)
	fixture.probeOut = "ai-memory 1.34.0\n"
	first, err := fixture.client.InstallSource(context.Background(), "ai-memory", "ai-memory", nil, fixture.target, fixture.sourceURL, false)
	if err != nil || first.Status != "installed" {
		t.Fatalf("first = %#v, err=%v", first, err)
	}

	fixture.setRecipe("#!/usr/bin/env bash\nexec managed-native-runner-v2 \"$@\"\n")
	second, err := fixture.client.InstallSource(context.Background(), "ai-memory", "ai-memory", nil, fixture.target, fixture.sourceURL, false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if second.Status != "installed" {
		t.Errorf("Status = %q, want installed (a changed recipe is never current)", second.Status)
	}
	stored, err := os.ReadFile(fixture.target) // #nosec G304
	if err != nil || !strings.Contains(string(stored), "v2") {
		t.Errorf("target not updated to the new recipe: %q, %v", stored, err)
	}
}
