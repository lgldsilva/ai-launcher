package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// stubToolsOnPath puts executable stubs for the harness and the upstream CLIs
// at the front of PATH. Pre-flight validation resolves all three, and --dry-run
// now runs it, so a fixture pointing at binaries that do not exist would fail
// for the wrong reason. It also keeps the suite hermetic: it never depends on
// ai-jail or ai-memory being installed on the machine running the tests.
func stubToolsOnPath(t *testing.T, names ...string) string {
	t.Helper()
	binDir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test stub must be executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return binDir
}

// stubPath resolves a stub installed by stubToolsOnPath. A resolvable harness
// is invoked through its absolute path, so that is what the argv carries.
func stubPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("stub %q is not on PATH: %v", name, err)
	}
	return path
}

// execMountArgv is the read-only map of the directory holding the resolved
// harness. --executable names a host path that has to exist inside the sandbox,
// so the launcher mounts its directory.
func execMountArgv(t *testing.T, name string) string {
	t.Helper()
	return "--map " + filepath.Dir(stubPath(t, name))
}

// writeTestConfigs creates a global config with a custom harness and two
// profiles plus a local config. The returned mounts are three existing
// directories used as default_mounts so tests stay independent of the host OS
// layout (production defaults come from config.DefaultMountCandidates).
func writeTestConfigs(t *testing.T, localYAML string) (globalPath, localPath string, defaultMounts []string) {
	t.Helper()
	stubToolsOnPath(t, "custom-cli", "other-cli", "codex", "ai-jail", "ai-memory")
	dir := t.TempDir()
	defaultMounts = []string{
		filepath.Join(dir, "storage"),
		filepath.Join(dir, "storage", "Projetos"),
		filepath.Join(dir, "storage", "cache"),
	}
	for _, mount := range defaultMounts {
		if err := os.MkdirAll(mount, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	globalYAML := `agents:
  - name: Custom
    command: custom-cli
    yolo_flag: --custom-yolo
    # custom-cli is a wrapper: ai-memory run only accepts its fixed harness
    # list, so the catalog declares which of those it maps onto.
    memory:
      run_harness: opencode
    params:
      - name: model
        flag: --model
        takes_value: true
  - name: Codex
    command: codex
    params:
      - name: model
        flag: --model
        takes_value: true
permissions:
  - id: jail
    name: Jail
    default: true
default_mounts:
  - ` + defaultMounts[0] + `
  - ` + defaultMounts[1] + `
  - ` + defaultMounts[2] + `
profiles:
  review:
    agent: custom-cli
    options:
      jail: false
      memory: false
      param_values:
        model: v1
  minimal:
    agent: custom-cli
`
	globalPath = filepath.Join(dir, "global.yaml")
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	localPath = filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte(localYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return globalPath, localPath, defaultMounts
}

// defaultMountArgv formats the fixture default mounts as ai-jail --rw-map args.
func defaultMountArgv(mounts []string) string {
	parts := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		parts = append(parts, "--rw-map "+mount)
	}
	return strings.Join(parts, " ")
}

func runDryRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(args, strings.NewReader(""), &out, &errOut)
	return out.String(), err
}

func TestCorruptLocalConfigWarnsAndContinues(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "options: [not, valid")
	var out, errOut bytes.Buffer
	err := run([]string{"--config", globalPath, "--local-config", localPath,
		"--agent", "codex", "--no-jail", "--no-memory", "--dry-run"},
		strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("run() error = %v; a corrupt local config must degrade like a corrupt global one", err)
	}
	if !strings.Contains(errOut.String(), "warning:") || !strings.Contains(errOut.String(), "local config") {
		t.Fatalf("stderr %q; want a warning naming the local config parse failure", errOut.String())
	}
	if !strings.Contains(out.String(), "codex") {
		t.Fatalf("dry-run = %q; want the launch to proceed with defaults", out.String())
	}
}

func TestProfileFlagLayersOverLocalConfig(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: other-cli\noptions:\n  jail: false\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--no-jail", "--profile", "review", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(out, "custom-cli --model v1") {
		t.Fatalf("dry-run = %q; want profile agent and param applied over local config", out)
	}
}

func TestExplicitFlagsOverrideProfile(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: other-cli\noptions:\n  jail: false\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--no-jail",
		"--profile", "review", "--agent", "codex", "--param", "model=v2", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(out, "custom-cli") || !strings.Contains(out, "codex") {
		t.Fatalf("dry-run = %q; want --agent to override the profile agent", out)
	}
	if strings.Contains(out, "--model v1") {
		t.Fatalf("dry-run = %q; want --param to override the profile param value", out)
	}
}

func TestProfileWithoutOptionsKeepsLocalOptions(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: other-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--no-jail", "--profile", "minimal", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.HasPrefix(out, "ai-memory run opencode") {
		t.Fatalf("dry-run = %q; want local memory option retained by a profile without options", out)
	}
}

func TestSaveProfilePersistsMergedSelectionWithoutLaunching(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--no-jail",
		"--save-profile", "saved", "--param", "model=v9", "--yolo")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(out, `profile "saved" saved`) {
		t.Fatalf("output = %q; want save confirmation", out)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := global.Profiles["saved"]
	if !ok {
		t.Fatalf("profiles = %#v; want saved", config.ProfileNames(global))
	}
	if profile.Agent != "custom-cli" || profile.Options == nil || !profile.Options.Yolo || profile.Options.ParamValues["model"] != "v9" {
		t.Fatalf("saved profile = %#v", profile)
	}
}

func TestListAndDeleteProfiles(t *testing.T) {
	globalPath, _, _ := writeTestConfigs(t, "agent: custom-cli\n")
	out, err := runDryRun(t, "--config", globalPath, "--list-profiles")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(out, "minimal") || !strings.Contains(out, "review") || !strings.Contains(out, "agent: custom-cli") {
		t.Fatalf("list output = %q", out)
	}

	out, err = runDryRun(t, "--config", globalPath, "--delete-profile", "review")
	if err != nil || !strings.Contains(out, `profile "review" deleted`) {
		t.Fatalf("delete output = %q, err = %v", out, err)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if names := config.ProfileNames(global); len(names) != 1 || names[0] != "minimal" {
		t.Fatalf("profiles after delete = %#v", names)
	}
	if _, err = runDryRun(t, "--config", globalPath, "--delete-profile", "review"); err == nil {
		t.Fatal("deleting a missing profile = nil; want error")
	}
}

func TestListProfilesWithNoneSaved(t *testing.T) {
	globalPath, _, _ := writeTestConfigs(t, "agent: custom-cli\n")
	if _, err := runDryRun(t, "--config", globalPath, "--delete-profile", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := runDryRun(t, "--config", globalPath, "--delete-profile", "minimal"); err != nil {
		t.Fatal(err)
	}
	out, err := runDryRun(t, "--config", globalPath, "--list-profiles")
	if err != nil || !strings.Contains(out, "no profiles saved") {
		t.Fatalf("empty list output = %q, err = %v", out, err)
	}
}

func TestUnknownProfileReturnsError(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\n")
	if _, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--profile", "nope", "--dry-run"); err == nil || !strings.Contains(err.Error(), `profile "nope" not found`) {
		t.Fatalf("run() error = %v; want unknown profile error", err)
	}
}

func TestInvalidParamFlagReturnsError(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\n")
	if _, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--param", "novalue", "--dry-run"); err == nil || !strings.Contains(err.Error(), "expected name=value") {
		t.Fatalf("run() error = %v; want invalid --param error", err)
	}
}

func TestVersionFlagPrintsBuildMetadata(t *testing.T) {
	out, err := runDryRun(t, "--version")
	if err != nil {
		t.Fatalf("run(--version) error = %v", err)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || !strings.Contains(trimmed, "ai-launcher") {
		t.Fatalf("--version output = %q; want a non-empty line containing \"ai-launcher\"", out)
	}
	want := "ai-launcher " + version + " (" + commit + ", " + date + ")"
	if trimmed != want {
		t.Fatalf("--version output = %q; want %q", trimmed, want)
	}
}

func TestLaunchFailureHintWorkstreamConflict(t *testing.T) {
	got := launchFailureHint(`server returned 409 Conflict: {"error":"workstream is already active: owned by host:1 until 2099"}`)
	if !strings.Contains(got, "workstream") || !strings.Contains(got, "--new") {
		t.Fatalf("hint = %q; want workstream recovery tips", got)
	}
	got = launchFailureHint("certificate not valid for name")
	if !strings.Contains(got, "TLS") && !strings.Contains(got, "memory_server_url") {
		t.Fatalf("TLS hint = %q", got)
	}
}
