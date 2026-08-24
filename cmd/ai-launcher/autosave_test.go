package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"github.com/lgldsilva/ai-launcher/internal/tui"
)

// stubRunTUI replaces the TUI with a stub that confirms the given selection,
// and returns a restore function.
func stubRunTUI(t *testing.T, confirmed launcher.LaunchConfig) func() {
	t.Helper()
	previous := runTUI
	runTUI = func(config.Global, launcher.LaunchConfig, tui.Hooks, string, tui.Options) (launcher.LaunchConfig, error) {
		return confirmed, nil
	}
	return func() { runTUI = previous }
}

// Pressing r in the TUI runs the confirmed selection — and that is exactly the
// selection the operator wants back on the next open. The run must autosave it
// (with provenance, so the trust boundary honors it) instead of requiring a
// separate Ctrl+S.
func TestTuiRunAutosavesTheConfirmedSelection(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseJail:   false,
		UseMemory: false,
		Yolo:      true,
	}
	restore := stubRunTUI(t, confirmed)
	defer restore()
	req := &launchRequest{
		opts:         cliOptions{globalPath: globalPath, localPath: localPath},
		global:       global,
		local:        local,
		launchConfig: launcher.LaunchConfig{Agent: config.Agent{Command: "custom-cli"}, UseJail: true},
		errOut:       &bytes.Buffer{},
	}
	proceed, err := req.confirmSelection("")
	if err != nil || !proceed {
		t.Fatalf("confirmSelection() = %t, %v; want proceed", proceed, err)
	}
	saved, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if saved.Options.Jail || !saved.Options.Yolo {
		t.Fatalf("saved options = jail:%t yolo:%t; the confirmed selection was not autosaved", saved.Options.Jail, saved.Options.Yolo)
	}
	reloaded, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !config.LocalConfigTrusted(reloaded, localPath) {
		t.Error("the autosave must record provenance, or the next open refuses the file")
	}
}

// Regression: --save (autosave included) must not silently drop the raw
// tri-state network-isolation choice — see
// LaunchConfig.ContainerNetworkInternal's doc comment for why this field
// exists separately from the already-resolved Docker.NetworkInternal.
func TestTuiRunAutosavesContainerNetworkInternal(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	networkInternal := true
	confirmed := launcher.LaunchConfig{
		Agent:                    config.Agent{Command: "custom-cli"},
		UseDocker:                true,
		ContainerNetworkInternal: &networkInternal,
	}
	restore := stubRunTUI(t, confirmed)
	defer restore()
	req := &launchRequest{
		opts:         cliOptions{globalPath: globalPath, localPath: localPath},
		global:       global,
		local:        local,
		launchConfig: launcher.LaunchConfig{Agent: config.Agent{Command: "custom-cli"}},
		errOut:       &bytes.Buffer{},
	}
	proceed, err := req.confirmSelection("")
	if err != nil || !proceed {
		t.Fatalf("confirmSelection() = %t, %v; want proceed", proceed, err)
	}
	saved, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if saved.Options.ContainerNetworkInternal == nil || !*saved.Options.ContainerNetworkInternal {
		t.Fatalf("saved container_network_internal = %#v; want explicit true", saved.Options.ContainerNetworkInternal)
	}
}

// TestTuiRunAutosavesContainerHostGateway is the regression guard for the
// persistence gap ContainerNetworkInternal was deliberately built to avoid:
// before this fix, --save/--save-profile silently dropped an operator's
// explicit ContainerHostGateway choice because LaunchConfig only carried the
// already-resolved Docker.AddHostGateway bool, never the raw tri-state.
func TestTuiRunAutosavesContainerHostGateway(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	hostGateway := false
	confirmed := launcher.LaunchConfig{
		Agent:                config.Agent{Command: "custom-cli"},
		UseDocker:            true,
		ContainerHostGateway: &hostGateway,
	}
	restore := stubRunTUI(t, confirmed)
	defer restore()
	req := &launchRequest{
		opts:         cliOptions{globalPath: globalPath, localPath: localPath},
		global:       global,
		local:        local,
		launchConfig: launcher.LaunchConfig{Agent: config.Agent{Command: "custom-cli"}},
		errOut:       &bytes.Buffer{},
	}
	proceed, err := req.confirmSelection("")
	if err != nil || !proceed {
		t.Fatalf("confirmSelection() = %t, %v; want proceed", proceed, err)
	}
	saved, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if saved.Options.ContainerHostGateway == nil || *saved.Options.ContainerHostGateway {
		t.Fatalf("saved container_host_gateway = %#v; want explicit false", saved.Options.ContainerHostGateway)
	}
}

// Selecting the container backend in the TUI has to leave a concrete project
// directory behind — Docker needs the same-path mount and WORKDIR explicitly,
// where the jail gets the cwd implicitly. What it must NOT do is write that
// derived path into options.workspace: that is an ai-memory scope name, and a
// saved absolute path there came back on the next jail launch as
// `ai-memory run --workspace /abs/path`, minting a path-shaped phantom
// workspace in the memory database.
func TestTuiContainerDefaultsProjectDirToCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	restoreDir := chdir(t, dir)
	defer restoreDir()
	wantProjectDir := mustGetwd()

	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	restoreTUI := stubRunTUI(t, launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseDocker: true,
		UseMemory: false,
	})
	defer restoreTUI()

	req := &launchRequest{
		opts:         cliOptions{globalPath: globalPath, localPath: localPath},
		global:       global,
		local:        local,
		launchConfig: launcher.LaunchConfig{Agent: config.Agent{Command: "custom-cli"}, UseJail: true},
		errOut:       &bytes.Buffer{},
	}
	proceed, err := req.confirmSelection("")
	if err != nil || !proceed {
		t.Fatalf("confirmSelection() = %t, %v; want proceed", proceed, err)
	}
	if req.launchConfig.ProjectDir != wantProjectDir {
		t.Fatalf("project dir = %q; want current directory %q", req.launchConfig.ProjectDir, wantProjectDir)
	}
	if req.launchConfig.Workspace != "" {
		t.Fatalf("workspace = %q; the container project directory must not become an ai-memory scope", req.launchConfig.Workspace)
	}
	saved, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() after container selection: %v", err)
	}
	if saved.Options.Workspace != "" {
		t.Fatalf("saved workspace = %q; want the derived project directory left unpersisted", saved.Options.Workspace)
	}
}

// A failed autosave never blocks a launch: it warns and proceeds.
func TestTuiRunAutosaveFailureWarnsButProceeds(t *testing.T) {
	restore := stubRunTUI(t, launcher.LaunchConfig{Agent: config.Agent{Command: "custom-cli"}})
	defer restore()
	// A regular file where the config directory should be makes every save
	// fail deterministically, under any user or OS.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	req := &launchRequest{
		opts:   cliOptions{globalPath: filepath.Join(blocker, "global.yaml"), localPath: filepath.Join(blocker, "local.yaml")},
		errOut: &errOut,
	}
	proceed, err := req.confirmSelection("")
	if err != nil || !proceed {
		t.Fatalf("confirmSelection() = %t, %v; a save failure must not block the launch", proceed, err)
	}
	if !strings.Contains(errOut.String(), "warning:") {
		t.Fatalf("errOut = %q; want a warning naming the failed save", errOut.String())
	}
}

// stubRunTUIAfterProfile replaces the TUI with a stub that loads a profile
// (reporting it through the ProfileLoaded hook, as loadProfile does) and then
// confirms the given selection.
func stubRunTUIAfterProfile(t *testing.T, loaded, confirmed launcher.LaunchConfig) func() {
	t.Helper()
	previous := runTUI
	runTUI = func(_ config.Global, _ launcher.LaunchConfig, hooks tui.Hooks, _ string, _ tui.Options) (launcher.LaunchConfig, error) {
		if hooks.ProfileLoaded != nil {
			hooks.ProfileLoaded(loaded)
		}
		return confirmed, nil
	}
	return func() { runTUI = previous }
}

// Loading a profile replaces the whole options block, and the interactive
// launch autosaves. Together they deleted every option the workspace file
// declared and the profile omitted — here stacks and container_memory — on a
// launch the operator never asked to save. The autosave must leave the file's
// options alone when the profile ran unedited.
func TestTuiRunKeepsLocalOptionsAfterUneditedProfileLoad(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: false\n  memory: false\n  stacks: [go]\n  container_memory: 4g\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	// What `options: {yolo: true}` alone produces once loadProfile has run.
	profileSelection := launcher.LaunchConfig{
		Agent: config.Agent{Command: "custom-cli"},
		Yolo:  true,
	}
	restore := stubRunTUIAfterProfile(t, profileSelection, profileSelection)
	defer restore()
	req := &launchRequest{
		opts:         cliOptions{globalPath: globalPath, localPath: localPath},
		global:       global,
		local:        local,
		launchConfig: launcher.LaunchConfig{Agent: config.Agent{Command: "custom-cli"}},
		errOut:       &bytes.Buffer{},
	}
	if proceed, err := req.confirmSelection(""); err != nil || !proceed {
		t.Fatalf("confirmSelection() = %t, %v; want proceed", proceed, err)
	}
	saved, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if len(saved.Options.Stacks) != 1 || saved.Options.Stacks[0] != "go" {
		t.Errorf("saved stacks = %v; the profile omits stacks, so the file's own value must survive", saved.Options.Stacks)
	}
	if saved.Options.ContainerMemory != "4g" {
		t.Errorf("saved container_memory = %q; want the file's own 4g", saved.Options.ContainerMemory)
	}
	if saved.Options.Yolo {
		t.Error("the profile's yolo must not be written to the workspace file either — the block is kept whole")
	}
	if saved.Agent != "custom-cli" {
		t.Errorf("saved agent = %q; the agent selection still autosaves", saved.Agent)
	}
}

// The exception is narrow: once the operator edits an option after loading a
// profile, the selection is theirs and autosaves in full.
func TestTuiRunAutosavesOptionsEditedAfterProfileLoad(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: false\n  memory: false\n  stacks: [go]\n  container_memory: 4g\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	profileSelection := launcher.LaunchConfig{
		Agent: config.Agent{Command: "custom-cli"},
		Yolo:  true,
	}
	edited := profileSelection
	edited.UseMemory = true // an options toggle after the load
	restore := stubRunTUIAfterProfile(t, profileSelection, edited)
	defer restore()
	req := &launchRequest{
		opts:         cliOptions{globalPath: globalPath, localPath: localPath},
		global:       global,
		local:        local,
		launchConfig: launcher.LaunchConfig{Agent: config.Agent{Command: "custom-cli"}},
		errOut:       &bytes.Buffer{},
	}
	if proceed, err := req.confirmSelection(""); err != nil || !proceed {
		t.Fatalf("confirmSelection() = %t, %v; want proceed", proceed, err)
	}
	saved, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if !saved.Options.Memory || !saved.Options.Yolo {
		t.Fatalf("saved options = memory:%t yolo:%t; an edited selection autosaves in full",
			saved.Options.Memory, saved.Options.Yolo)
	}
	if len(saved.Options.Stacks) != 0 || saved.Options.ContainerMemory != "" {
		t.Errorf("saved stacks = %v, container_memory = %q; the edited selection owns the block",
			saved.Options.Stacks, saved.Options.ContainerMemory)
	}
}
