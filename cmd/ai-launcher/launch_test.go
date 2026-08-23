package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

func TestDecideLaunchAction(t *testing.T) {
	if decideLaunchAction(true) != actionPrint {
		t.Fatal("dry-run must print instead of executing")
	}
	if decideLaunchAction(false) != actionExecute {
		t.Fatal("a confirmed launch (Enter in the TUI or any CLI call) must execute")
	}
}

func TestFinalizeResolvesMemoryWhenJailAndMemoryOn(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "ai-memory")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil { // #nosec G306 -- PATH stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var errOut bytes.Buffer
	got := finalizeLaunchConfig(launcher.LaunchConfig{UseJail: true, UseMemory: true}, config.DefaultGlobal(), t.TempDir(), &errOut)
	// macOS resolves /var to /private/var; canonicalize for the assertion.
	want, err := filepath.EvalSymlinks(stub)
	if err != nil {
		t.Fatal(err)
	}
	if got.MemoryExecutable != want {
		t.Fatalf("MemoryExecutable = %q; want %q", got.MemoryExecutable, want)
	}
}

func TestContinueFlagBuildsHarnessLessAiMemoryRun(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--continue", "--no-jail", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.TrimSpace(out) != "ai-memory run" {
		t.Fatalf("--continue dry-run = %q; want %q", out, "ai-memory run")
	}
}

func TestWorkspaceProjectAndWorkstreamForwarding(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--no-jail",
		"--workspace", "acme", "--project", "billing", "--workstream", "release-1", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-memory run --workspace acme --project billing --workstream release-1 opencode --executable " + stubPath(t, "custom-cli")
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q", out, want)
	}
}

func TestNewWorkstreamStillCreates(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--no-jail", "--new", "fresh", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.TrimSpace(out) != "ai-memory run --new fresh opencode --executable "+stubPath(t, "custom-cli") {
		t.Fatalf("--new dry-run = %q", out)
	}
}

func TestCliLaunchUsesJailExecProgrammaticMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	globalPath, localPath, mounts := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --no-docker " + execMountArgv(t, "custom-cli") + " " + defaultMountArgv(mounts) + " " + stubPath(t, "custom-cli")
	if strings.TrimSpace(out) != want {
		t.Fatalf("CLI dry-run = %q; want %q", out, want)
	}
}

func TestJailFlagsFromLocalConfigMapToAiJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Unsaved jail_flags are refused by the trust gate; a launcher-saved
	// selection is trusted operator input and must still emit the flags.
	globalPath, localPath, mounts := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	lockdown, statusBar := true, false
	saved := launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseJail:   true,
		UseMemory: false,
		JailFlags: config.JailFlags{
			Lockdown:  &lockdown,
			StatusBar: &statusBar,
			Browser:   "soft",
			Mask:      []string{"/etc/secrets"},
		},
	}
	if err := saveLocalSelection(globalPath, true, localPath, local, saved); err != nil {
		t.Fatalf("saveLocalSelection() error = %v", err)
	}
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --no-docker --lockdown --no-status-bar --mask /etc/secrets --browser=soft " + execMountArgv(t, "custom-cli") + " " + defaultMountArgv(mounts) + " " + stubPath(t, "custom-cli")
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q", out, want)
	}
}

func TestV115PermissionFlagsMapToAiJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	globalPath, localPath, mounts := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath,
		"--display", "--pictures", "--tailscale", "--systemd-user", "--mise", "--worktree", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --no-docker --display --pictures --tailscale --systemd-user --mise --worktree " + execMountArgv(t, "custom-cli") + " " +
		defaultMountArgv(mounts) + " " + stubPath(t, "custom-cli")
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q", out, want)
	}
}

func TestLocalMountsReplaceDefaultMounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Pre-flight stats every mount, so the fixture path has to exist.
	// t.TempDir lives under /var or /tmp on most hosts; unsaved local mounts of
	// those trees are refused by the trust gate, so the selection is saved first
	// (operator provenance) — the same path the TUI --save flow takes.
	data := t.TempDir()
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	saved := launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseJail:   true,
		UseMemory: false,
		Mounts:    []config.Mount{{Path: data, Mode: "ro"}},
	}
	if err := saveLocalSelection(globalPath, true, localPath, local, saved); err != nil {
		t.Fatalf("saveLocalSelection() error = %v", err)
	}
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --no-docker " + execMountArgv(t, "custom-cli") + " --map " + data + " " + stubPath(t, "custom-cli")
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q (local mounts replace default_mounts)", out, want)
	}
}

func TestMissingDefaultMountsAreSkipped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubToolsOnPath(t, "custom-cli", "ai-jail")
	dir := t.TempDir()
	existing := filepath.Join(dir, "present")
	if err := os.MkdirAll(existing, 0o750); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "absent")
	globalYAML := `agents:
  - name: Custom
    command: custom-cli
permissions:
  - id: jail
    name: Jail
    default: true
default_mounts:
  - ` + existing + `
  - ` + missing + `
`
	globalPath := filepath.Join(dir, "global.yaml")
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte("agent: custom-cli\noptions:\n  jail: true\n  memory: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --no-docker " + execMountArgv(t, "custom-cli") + " --rw-map " + existing + " " + stubPath(t, "custom-cli")
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want missing default mounts skipped → %q", out, want)
	}
	if strings.Contains(out, missing) {
		t.Fatalf("dry-run = %q; must not mount missing path %s", out, missing)
	}
}

func TestMountFlagsReplaceDefaultMounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	custom := t.TempDir()
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--rw-map", custom, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --no-docker " + execMountArgv(t, "custom-cli") + " --rw-map " + custom + " " + stubPath(t, "custom-cli")
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q (flag mounts replace default_mounts)", out, want)
	}
}

func TestHomeSymlinkTargetsAreAutoMounted(t *testing.T) {
	fakeHome := t.TempDir()
	outside := t.TempDir()
	cacheTarget := filepath.Join(outside, "cache")
	if err := os.MkdirAll(cacheTarget, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cacheTarget, filepath.Join(fakeHome, ".cache")); err != nil {
		t.Fatal(err)
	}
	// Broken and inside-home symlinks must be ignored.
	if err := os.Symlink(filepath.Join(outside, "gone"), filepath.Join(fakeHome, ".broken")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fakeHome, ".config"), filepath.Join(fakeHome, ".inside")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	// macOS resolves /var to /private/var; canonicalize for the assertion.
	resolvedTarget, err := filepath.EvalSymlinks(cacheTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--rw-map "+resolvedTarget) {
		t.Fatalf("dry-run = %q; want auto mount of symlink target %s", out, resolvedTarget)
	}
	if strings.Contains(out, "gone") || strings.Contains(out, ".inside") {
		t.Fatalf("dry-run = %q; broken and inside-home symlinks must be skipped", out)
	}
}

func TestSymlinkAutoMountsSkippedWithoutJail(t *testing.T) {
	fakeHome := t.TempDir()
	outside := t.TempDir()
	cacheTarget := filepath.Join(outside, "cache")
	if err := os.MkdirAll(cacheTarget, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cacheTarget, filepath.Join(fakeHome, ".cache")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--no-jail", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(out, cacheTarget) {
		t.Fatalf("dry-run = %q; auto mounts require the jail", out)
	}
}

func TestSymlinkedProjectJailConfigDisablesHideConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	target := filepath.Join(dir, "ai-jail.toml")
	if err := os.WriteFile(target, []byte("# jail config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".ai-jail")); err != nil {
		t.Fatal(err)
	}
	restore := chdir(t, dir)
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	// A checkout-controlled .ai-jail symlink is refused by the trust boundary
	// unless the selection was saved by the operator (provenance), so save first.
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if err := saveLocalSelection(globalPath, true, localPath, local, launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseJail:   true,
		UseMemory: false,
	}); err != nil {
		t.Fatalf("saveLocalSelection() error = %v", err)
	}
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	restore()
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(out, "--no-hide-config") {
		t.Fatalf("dry-run = %q; want --no-hide-config for a symlinked .ai-jail", out)
	}
}

func TestRealProjectJailConfigKeepsHideConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ai-jail"), []byte("# jail config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := chdir(t, dir)
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	restore()
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(out, "hide-config") {
		t.Fatalf("dry-run = %q; a real .ai-jail file keeps the ai-jail default mask", out)
	}
}

// Disabling the project's config mask because .ai-jail is a symlink is a
// policy downgrade: it must be announced, naming the file and the effect,
// not applied silently. Saved selections are trusted, so the warning path is
// reached after recording provenance.
func TestSymlinkedProjectJailConfigWarnsWhenDisablingHideConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	target := filepath.Join(dir, "ai-jail.toml")
	if err := os.WriteFile(target, []byte("# jail config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".ai-jail")); err != nil {
		t.Fatal(err)
	}
	restore := chdir(t, dir)
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if err := saveLocalSelection(globalPath, true, localPath, local, launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseJail:   true,
		UseMemory: false,
	}); err != nil {
		t.Fatalf("saveLocalSelection() error = %v", err)
	}
	var out, errOut bytes.Buffer
	err = run([]string{"--config", globalPath, "--local-config", localPath, "--dry-run"}, strings.NewReader(""), &out, &errOut)
	restore()
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	warning := errOut.String()
	if !strings.Contains(warning, ".ai-jail") || !strings.Contains(warning, "hide-config") {
		t.Fatalf("stderr = %q; want a warning naming .ai-jail and hide-config", warning)
	}
}

// chdir switches the working directory for the duration of a test and
// returns the restore function. run() resolves the project .ai-jail from the
// process working directory, so the test must actually chdir.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	}
}
