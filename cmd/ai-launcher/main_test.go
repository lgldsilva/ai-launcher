package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
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
// The launcher canonicalizes the executable (macOS resolves /var to
// /private/var), so the expectation must be canonical too.
func stubPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("stub %q is not on PATH: %v", name, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
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
    supports_memory: true
    supports_yolo: true
    # A recipe so docker-mode tests have an install path (script agent).
    source_url: https://example.com/custom-cli.sh
    allow_unverified: true
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
    supports_memory: true
    supports_yolo: false
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

// TestNoLocalConfigIgnoresWorkspaceFile proves that --no-local-config skips
// the workspace .ai-launch.yaml entirely: a file that disables the sandbox is
// treated as absent, so the global default jail stays on and the launch
// succeeds without the explicit --no-jail normally required by the trust gate.
func TestNoLocalConfigIgnoresWorkspaceFile(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--agent", "codex", "--no-local-config", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; --no-local-config must ignore the local file", err)
	}
	if !strings.Contains(out, "ai-jail") {
		t.Fatalf("dry-run = %q; want the global default jail applied", out)
	}

	// Without the flag the same invocation must be refused by the trust boundary.
	_, err = runDryRun(t, "--config", globalPath, "--local-config", localPath, "--agent", "codex", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "local config disables the sandbox") {
		t.Fatalf("without --no-local-config error = %v; want trust refusal", err)
	}
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

// A typo in the local config is invisible today: the unknown key never takes
// effect and the next save erases it. The launch must still proceed — a config
// written by a newer launcher has to keep working — but stderr has to name the
// key so the operator can see what was ignored.
func TestUnknownLocalConfigKeyWarnsAndContinues(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: codex\noptions:\n  jail: false\n  memory: false\n  container_host_gatway: false\n")
	var out, errOut bytes.Buffer
	err := run([]string{"--config", globalPath, "--local-config", localPath,
		"--agent", "codex", "--no-jail", "--no-memory", "--dry-run"},
		strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("run() error = %v; an unknown key must not fail the launch", err)
	}
	if !strings.Contains(errOut.String(), "warning:") ||
		!strings.Contains(errOut.String(), "options.container_host_gatway") {
		t.Fatalf("stderr %q; want a warning naming the unknown key", errOut.String())
	}
	if !strings.Contains(out.String(), "codex") {
		t.Fatalf("dry-run = %q; want the launch to proceed", out.String())
	}
}

// No --no-jail here on purpose: the `review` profile declares its own options
// block, so it — not the workspace file — owns the jail toggle. Passing
// --no-jail used to be required only because the profile's value leaked into
// the trust check and got attributed to .ai-launch.yaml.
func TestProfileFlagLayersOverLocalConfig(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: other-cli\noptions:\n  jail: false\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--profile", "review", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(out, "custom-cli --model v1") {
		t.Fatalf("dry-run = %q; want profile agent and param applied over local config", out)
	}
}

func TestExplicitFlagsOverrideProfile(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: other-cli\noptions:\n  jail: false\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath,
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

// --no-jail is genuinely required here: the `minimal` profile declares no
// options block, so the workspace file still owns the toggles — and a file
// disabling the sandbox is exactly what the trust boundary refuses without an
// explicit opt-in.
func TestProfileWithoutOptionsKeepsLocalOptions(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: other-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--no-jail", "--profile", "minimal", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	// The point is that the memory wrapper survived the profile, not where the
	// harness sits: --executable is emitted before it so ai-jail can parse the
	// chain, which is asserted by the argv-order tests instead.
	if !strings.HasPrefix(out, "ai-memory run") || !strings.Contains(out, "opencode") {
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

func TestProfileFromLaunchPersistsContainerRuntime(t *testing.T) {
	networkInternal := true
	profile := profileFromLaunch(launcher.LaunchConfig{
		Agent:                    config.Agent{Command: "claude"},
		UseDocker:                true,
		ContainerRuntime:         "podman",
		ContainerNetworkInternal: &networkInternal,
		Docker: container.RunConfig{
			Runtime:      container.PodmanRuntime{},
			MemoryLimit:  "4g",
			CPULimit:     "2.0",
			PIDsLimit:    512,
			ExposedPorts: []container.PortMapping{{Host: 3000, Internal: 3000}},
			NetworkName:  "bridge",
		},
	})
	if profile.Options == nil || profile.Options.ContainerRuntime != "podman" {
		t.Fatalf("profile = %#v; want container_runtime podman", profile)
	}
	if profile.Options.ContainerMemory != "4g" || profile.Options.ContainerCPUs != "2.0" || profile.Options.ContainerPIDs != 512 || len(profile.Options.ContainerPorts) != 1 || profile.Options.ContainerNetwork != "bridge" {
		t.Fatalf("profile resources = %#v", profile.Options)
	}
	// Regression: --save-profile must not silently drop the raw tri-state
	// network-isolation choice the way ContainerHostGateway's resolved-only
	// representation does — see LaunchConfig.ContainerNetworkInternal's doc.
	if profile.Options.ContainerNetworkInternal == nil || !*profile.Options.ContainerNetworkInternal {
		t.Fatalf("profile.Options.ContainerNetworkInternal = %#v; want explicit true", profile.Options.ContainerNetworkInternal)
	}
}

func TestServiceFlagsAreValidatedAndShownByDryRun(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath,
		"--no-jail", "--no-memory", "--service", "redis", "--service", "mongo", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(out, "services: mongo redis") {
		t.Fatalf("dry-run = %q; want canonical service selection", out)
	}

	if _, err := runDryRun(t, "--config", globalPath, "--local-config", localPath,
		"--no-jail", "--no-memory", "--service", "not-a-service", "--dry-run"); err == nil || !strings.Contains(err.Error(), "unknown service") {
		t.Fatalf("unknown service error = %v; want validation failure", err)
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

func TestDoctorReturnsErrorWhenUpstreamIsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := runDryRun(t, "--doctor")
	if err == nil {
		t.Fatal("run(--doctor) = nil; want error when upstream tools are missing")
	}
}

func TestDoctorReturnsErrorWhenUpstreamIsTooOld(t *testing.T) {
	binDir := t.TempDir()
	for _, name := range []string{"ai-jail", "ai-memory"} {
		path := filepath.Join(binDir, name)
		script := "#!/bin/sh\necho \"" + name + " version 1.0.0\"\n"
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil { // #nosec G306 -- test stub
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := runDryRun(t, "--doctor")
	if err == nil {
		t.Fatal("run(--doctor) = nil; want error when upstream tools are below the floor")
	}
}

// stubUpstreamVersions puts ai-jail and ai-memory stubs reporting the given
// versions at the front of PATH.
func stubUpstreamVersions(t *testing.T, versions map[string]string) string {
	t.Helper()
	binDir := t.TempDir()
	for name, want := range versions {
		path := filepath.Join(binDir, name)
		script := "#!/bin/sh\necho \"" + name + " version " + want + "\"\n"
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil { // #nosec G306 -- test stub
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Point HOME at an empty directory so the managed native runner probe finds
	// nothing. Without this the report grows a third row read from whichever
	// ~/.local/share/ai-launcher/bin the developer happens to have, and a test
	// about PATH versions would fail on the state of a file it never mentions.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// stubManagedNativeRunner writes a fake managed ai-memory reporting version
// under home, so a test can exercise the runner row without owning the whole
// probe. home comes from stubUpstreamVersions rather than the environment:
// reading it back out of HOME would be taking a path from a mutable global to
// build a path to write to, which is the shape gosec flags and is no easier to
// read than passing it.
func stubManagedNativeRunner(t *testing.T, home, version string) {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "ai-launcher", "bin")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"ai-memory " + version + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ai-memory"), []byte(script), 0o700); err != nil { // #nosec G306 -- test stub
		t.Fatal(err)
	}
}

func TestDoctorSucceedsWhenUpstreamMeetsFloor(t *testing.T) {
	// Inside the validated range on both ends: at the floor, below the ceiling.
	stubUpstreamVersions(t, map[string]string{
		"ai-jail":   config.MinAIJailVersion,
		"ai-memory": config.MinAIMemoryVersion,
	})
	out, err := runDryRun(t, "--doctor")
	if err != nil {
		t.Fatalf("run(--doctor) error = %v; want success when upstream tools meet the floor", err)
	}
	if !strings.Contains(out, config.MinAIJailVersion) {
		t.Fatalf("--doctor output = %q; want the detected upstream version", out)
	}
	if strings.Contains(out, "untested") {
		t.Fatalf("--doctor output = %q; a version inside the range must not be reported as untested", out)
	}
}

// An upstream above the validated range is reported, not refused: it may well
// work, and refusing would strand every operator the day upstream ships a
// release. The exit code is what proves the difference between this and a
// floor violation.
func TestDoctorWarnsWithoutFailingAboveTheTestedCeiling(t *testing.T) {
	stubUpstreamVersions(t, map[string]string{"ai-jail": "99.0.0", "ai-memory": "99.0.0"})
	out, err := runDryRun(t, "--doctor")
	if err != nil {
		t.Fatalf("run(--doctor) error = %v; a newer upstream must warn, not block", err)
	}
	if !strings.Contains(out, "ai-jail-version-untested") {
		t.Fatalf("--doctor output = %q; want the untested code for ai-jail", out)
	}
	// The advice has to say what the report means and against what range, or
	// it is a bare code the operator cannot act on. It deliberately no longer
	// carries a per-release case study: the ai-jail 1.18.0 one outlived its
	// own accuracy and named a release now below the floor.
	if !strings.Contains(out, "has not been validated") || !strings.Contains(out, config.MinAIJailVersion) {
		t.Fatalf("--doctor output = %q; want the validated-range advice naming the floor", out)
	}
}

// The managed native runner has its own upgrade lifecycle, so a current
// ai-memory on PATH says nothing about the copy Environment() exports as
// AI_MEMORY_NATIVE_BIN. --doctor reports it on its own line or the operator
// never learns that `--upgrade` is owed.
func TestDoctorReportsAStaleManagedNativeRunner(t *testing.T) {
	home := stubUpstreamVersions(t, map[string]string{
		"ai-jail":   config.MinAIJailVersion,
		"ai-memory": config.UntestedAIMemoryVersion,
	})
	stubManagedNativeRunner(t, home, "1.24.0")

	out, err := runDryRun(t, "--doctor")
	if err == nil {
		t.Fatalf("--doctor output = %q; a runner below the floor is a failure, not a note", out)
	}
	if !strings.Contains(out, "ai-memory (managed) 1.24.0") {
		t.Fatalf("--doctor output = %q; want the managed runner on its own line", out)
	}
}

func TestLaunchFailureHintWorkstreamConflict(t *testing.T) {
	got := launchFailureHint(`server returned 409 Conflict: {"error":"workstream is already active: owned by host:1 until 2099"}`)
	if !strings.Contains(got, "workstream") || !strings.Contains(got, "--new") {
		t.Fatalf("hint = %q; want workstream recovery tips", got)
	}
	// The upstream lease expires within 90 seconds (docs/managed-workstreams.md),
	// not the stale "~1-2 min" the hint used to claim.
	if !strings.Contains(got, "90 seconds") {
		t.Fatalf("hint = %q; want the 90-second lease window from the ai-memory docs", got)
	}
	got = launchFailureHint("certificate not valid for name")
	if !strings.Contains(got, "TLS") && !strings.Contains(got, "memory_server_url") {
		t.Fatalf("TLS hint = %q", got)
	}
}

func TestLaunchFailureHintAuthRejection(t *testing.T) {
	got := launchFailureHint("Error: server returned 401 Unauthorized: auth required")
	if !strings.Contains(got, "401") || !strings.Contains(got, "memory_auth_token") {
		t.Fatalf("401 hint = %q; want memory_auth_token guidance", got)
	}
}

func TestLaunchFailureStatusWrapsHint(t *testing.T) {
	got := launchFailureStatus(`server returned 409 Conflict: {"error":"workstream is already active"}`)
	if !strings.Contains(got, "Last launch failed") || !strings.Contains(got, "r to retry") || !strings.Contains(got, "90 seconds") {
		t.Fatalf("status = %q; want retry call-to-action + the hint", got)
	}
}

func TestIsWorkstreamConflict(t *testing.T) {
	if !isWorkstreamConflict(`Caused by: server returned 409 Conflict: {"error":"workstream is already active: owned by host:1 until 2099"}`) {
		t.Fatal("expected the 409 active-workstream text to be detected")
	}
	if isWorkstreamConflict("server returned 401 Unauthorized: auth required") {
		t.Fatal("401 must not be classified as a workstream conflict")
	}
	if isWorkstreamConflict("some unrelated exit status 1") {
		t.Fatal("generic failure must not be classified as a workstream conflict")
	}
}

// TestWorkstreamConflictDetectedThroughExecuteErrorShape proves the end-to-end
// chain after the executor fix: execute() wraps the PTY error as
// "failed to start <agent>: <waitErr>\ncommand: ...\nhint: ...", and the
// executor appends "last output:" with the child's stderr. The 409 detector
// and the workstream hint must still match through that full wrapping — this
// is what arms --new on recovery.
func TestWorkstreamConflictDetectedThroughExecuteErrorShape(t *testing.T) {
	execErr := errors.New("exit status 1\nlast output:\nserver returned 409 Conflict: {\"error\":\"workstream is already active: owned by host:1\"}")
	wrapped := fmt.Errorf("failed to start pi: %w\ncommand: ai-jail ... ai-memory run pi\n%s", execErr, launchFailureHint(execErr.Error()))
	if !isWorkstreamConflict(wrapped.Error()) {
		t.Fatalf("isWorkstreamConflict missed the real execute() error shape:\n%s", wrapped.Error())
	}
	if hint := launchFailureHint(wrapped.Error()); !strings.Contains(hint, "90 seconds") {
		t.Fatalf("hint = %q; want the 90s workstream recovery tip", hint)
	}
}

// TestAgentWithoutMemorySupportDisablesMemory proves that selecting an agent
// with supports_memory: false automatically turns ai-memory off, instead of
// building an unsupported "ai-memory run <harness>" command.
func TestAgentWithoutMemorySupportDisablesMemory(t *testing.T) {
	stubToolsOnPath(t, "cursor-agent", "ai-jail", "ai-memory")
	dir := t.TempDir()
	globalYAML := `agents:
  - name: Cursor
    command: cursor-agent
    supports_memory: false
permissions:
  - id: jail
    name: Jail
    default: true
`
	globalPath := filepath.Join(dir, "global.yaml")
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	out, err := runDryRunWithErrOut(t, []string{"--config", globalPath, "--agent", "cursor-agent", "--dry-run"}, &errOut)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(out, "ai-memory") {
		t.Fatalf("dry-run = %q; ai-memory must be disabled for cursor-agent", out)
	}
	if !strings.Contains(errOut.String(), "does not support ai-memory") {
		t.Fatalf("stderr = %q; want warning about unsupported memory", errOut.String())
	}
}

// TestAgentWithUnsupportedRunHarnessDisablesMemory proves that an agent whose
// catalog entry claims memory support but names a run_harness that ai-memory
// does not accept also gets memory disabled automatically.
func TestAgentWithUnsupportedRunHarnessDisablesMemory(t *testing.T) {
	stubToolsOnPath(t, "cursor-agent", "ai-jail", "ai-memory")
	dir := t.TempDir()
	globalYAML := `agents:
  - name: Cursor
    command: cursor-agent
    supports_memory: true
    memory:
      run_harness: cursor-agent
permissions:
  - id: jail
    name: Jail
    default: true
`
	globalPath := filepath.Join(dir, "global.yaml")
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	out, err := runDryRunWithErrOut(t, []string{"--config", globalPath, "--agent", "cursor-agent", "--dry-run"}, &errOut)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(out, "ai-memory") {
		t.Fatalf("dry-run = %q; ai-memory must be disabled for unsupported harness", out)
	}
	if !strings.Contains(errOut.String(), "does not accept harness") {
		t.Fatalf("stderr = %q; want warning about unsupported harness", errOut.String())
	}
}

// TestAgentWithoutYoloSupportDisablesYolo proves that passing --yolo for an
// agent with supports_yolo: false does not append a raw --yolo argument.
func TestAgentWithoutYoloSupportDisablesYolo(t *testing.T) {
	stubToolsOnPath(t, "cursor-agent", "ai-jail", "ai-memory")
	dir := t.TempDir()
	globalYAML := `agents:
  - name: Cursor
    command: cursor-agent
    supports_memory: false
    supports_yolo: false
permissions:
  - id: jail
    name: Jail
    default: true
`
	globalPath := filepath.Join(dir, "global.yaml")
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	out, err := runDryRunWithErrOut(t, []string{"--config", globalPath, "--agent", "cursor-agent", "--yolo", "--dry-run"}, &errOut)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(out, "--yolo") {
		t.Fatalf("dry-run = %q; --yolo must be disabled for cursor-agent", out)
	}
	if !strings.Contains(errOut.String(), "does not support --yolo") {
		t.Fatalf("stderr = %q; want warning about unsupported yolo", errOut.String())
	}
}

// runDryRunWithErrOut is like runDryRun but captures stderr for assertions.
func runDryRunWithErrOut(t *testing.T, args []string, errOut *bytes.Buffer) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, strings.NewReader(""), &out, errOut)
	return out.String(), err
}
