package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// stubWindows forces the platform detection to the given value for the test.
func stubWindows(t *testing.T, windows bool) {
	t.Helper()
	original := isWindows
	isWindows = func() bool { return windows }
	t.Cleanup(func() { isWindows = original })
}

func TestWindowsHidesJailToggleAndJailPermissions(t *testing.T) {
	stubWindows(t, true)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseJail:     true,
		Permissions: map[string]bool{"jail": true, "ssh": true},
	})
	if model.launch.UseJail {
		t.Fatal("NewModel must force the jail off on Windows")
	}
	for _, id := range model.permissionIDs {
		if id == "jail" || id == "ssh" || id == "gh" || id == "docker" || id == "gpu" {
			t.Errorf("jail-dependent permission %q offered on Windows", id)
		}
	}
	for _, row := range model.optionRows() {
		if strings.Contains(row.name, "Jail") {
			t.Fatalf("jail toggle offered on Windows: %#v", row)
		}
	}
	if view := model.View(); strings.Contains(view, "Jail / Sandbox") {
		t.Fatalf("options view shows the jail toggle on Windows: %s", view)
	}
}

func TestNonWindowsKeepsJailToggle(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	found := false
	for _, row := range model.optionRows() {
		if strings.Contains(row.name, "Jail") {
			found = true
		}
	}
	if !found {
		t.Fatal("jail toggle missing on a supported platform")
	}
}

func TestContinueRowSelectsAndLaunchesAiMemoryRun(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseJail:     true,
		UseMemory:   true,
		Permissions: map[string]bool{},
	})
	if !strings.Contains(model.View(), "Continuar última sessão") {
		t.Fatalf("agents view = %s; want the continue row", model.View())
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.launch.ContinueSession {
		t.Fatal("first Enter on the continue row did not select it")
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(model.result) == 0 {
		t.Fatal("second Enter on the continue row did not build a command")
	}
	joined := strings.Join(model.result, " ")
	if joined != "ai-jail ai-memory run" {
		t.Fatalf("continue argv = %q; want %q", joined, "ai-jail ai-memory run")
	}
}

func TestSelectingAnAgentClearsContinueSession(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		ContinueSession: true,
		UseMemory:       true,
		Permissions:     map[string]bool{},
	})
	model.cursor = 1
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.launch.ContinueSession {
		t.Fatal("selecting an agent must clear the continue selection")
	}
	if model.launch.Agent.Command == "" {
		t.Fatal("selecting an agent did not set the agent")
	}
}
