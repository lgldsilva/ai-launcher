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
	runTUI = func(config.Global, launcher.LaunchConfig, tui.Hooks, string) (launcher.LaunchConfig, error) {
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
