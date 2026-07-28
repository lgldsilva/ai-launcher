package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// loadGlobalWithProfile parses a real global config file, which is how profiles
// reach the TUI in production. Building config.Profile values in Go skips the
// unmarshaler that applies the option defaults, so it would not exercise the
// path this test is about.
func loadGlobalWithProfile(t *testing.T, profilesYAML string) config.Global {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: \"2.0\"\nprofiles:\n"+profilesYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	return global
}

// loadProfile assigns m.launch.UseJail straight from profile.Options.Jail, so
// the TUI inherited the same defect as the CLI: a profile whose options block
// never names jail turned the sandbox off on load, with the status line still
// reporting a plain "Profile loaded". Fixing the defaults in the config parser
// has to fix this screen too, since it reads the same parsed value.
func TestLoadingAProfileWithoutJailKeyKeepsTheSandboxOn(t *testing.T) {
	stubWindows(t, false)
	global := loadGlobalWithProfile(t, "  partial:\n    agent: claude\n    options:\n      yolo: true\n")
	model := NewModel(global, launcher.LaunchConfig{UseJail: true, UseMemory: true, Permissions: map[string]bool{}})

	model = applyKey(t, model, runeKey("5"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})

	if !model.launch.UseJail {
		t.Error("UseJail = false; a profile that never names jail must not disable the sandbox")
	}
	if !model.launch.UseMemory {
		t.Error("UseMemory = false; a profile that never names memory must not disable it")
	}
	if !model.launch.Yolo {
		t.Error("Yolo = false; the profile's explicit toggle was lost")
	}
}

// The counterpart: a profile that does say jail: false is an operator decision
// stored in the trusted global config, and loading it must still turn the
// sandbox off.
func TestLoadingAProfileWithExplicitJailFalseStillDisablesTheSandbox(t *testing.T) {
	stubWindows(t, false)
	global := loadGlobalWithProfile(t,
		"  off:\n    agent: claude\n    options:\n      jail: false\n      memory: false\n")
	model := NewModel(global, launcher.LaunchConfig{UseJail: true, UseMemory: true, Permissions: map[string]bool{}})

	model = applyKey(t, model, runeKey("5"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})

	if model.launch.UseJail {
		t.Error("UseJail = true; an explicit jail: false must be honored")
	}
	if model.launch.UseMemory {
		t.Error("UseMemory = true; an explicit memory: false must be honored")
	}
}
