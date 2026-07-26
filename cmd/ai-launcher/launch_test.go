package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecideLaunchAction(t *testing.T) {
	if decideLaunchAction(true) != actionPrint {
		t.Fatal("dry-run must print instead of executing")
	}
	if decideLaunchAction(false) != actionExecute {
		t.Fatal("a confirmed launch (Enter in the TUI or any CLI call) must execute")
	}
}

func TestContinueFlagBuildsHarnessLessAiMemoryRun(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--continue", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.TrimSpace(out) != "ai-memory run" {
		t.Fatalf("--continue dry-run = %q; want %q", out, "ai-memory run")
	}
}

func TestWorkspaceProjectAndWorkstreamForwarding(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath,
		"--workspace", "acme", "--project", "billing", "--workstream", "release-1", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-memory run --workspace acme --project billing --workstream release-1 custom-cli"
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q", out, want)
	}
}

func TestNewWorkstreamStillCreates(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--new", "fresh", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.TrimSpace(out) != "ai-memory run --new fresh custom-cli" {
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
	want := "ai-jail --exec " + defaultMountArgv(mounts) + " custom-cli"
	if strings.TrimSpace(out) != want {
		t.Fatalf("CLI dry-run = %q; want %q", out, want)
	}
}

func TestJailFlagsFromLocalConfigMapToAiJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	globalPath, localPath, mounts := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n  jail_flags:\n    lockdown: true\n    status_bar: false\n    browser: soft\n    mask: [/etc/secrets]\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --lockdown --no-status-bar --mask /etc/secrets --browser=soft " + defaultMountArgv(mounts) + " custom-cli"
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q", out, want)
	}
}

func TestLocalMountsReplaceDefaultMounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\nmounts:\n  - path: /data\n    mode: ro\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --map /data custom-cli"
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q (local mounts replace default_mounts)", out, want)
	}
}

func TestMissingDefaultMountsAreSkipped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	want := "ai-jail --exec --rw-map " + existing + " custom-cli"
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want missing default mounts skipped → %q", out, want)
	}
	if strings.Contains(out, missing) {
		t.Fatalf("dry-run = %q; must not mount missing path %s", out, missing)
	}
}

func TestMountFlagsReplaceDefaultMounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--rw-map", "/custom", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --rw-map /custom custom-cli"
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
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
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
