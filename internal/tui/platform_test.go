package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/catalog"
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

func TestMacOSKeepsJailAndPermissions(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseJail:     true,
		Permissions: map[string]bool{"ssh": true, "gh": true, "docker": true},
	})
	if !model.launch.UseJail {
		t.Fatal("macOS must keep jail enabled")
	}
	if !model.launch.Permissions["ssh"] || !model.launch.Permissions["gh"] {
		t.Fatalf("ssh/gh must remain: %#v", model.launch.Permissions)
	}
	foundJail := false
	for _, row := range model.optionRows() {
		if strings.Contains(row.name, "Jail") {
			foundJail = true
		}
	}
	if !foundJail {
		t.Fatal("Jail / Sandbox option missing on macOS")
	}
	seen := map[string]bool{}
	for _, id := range model.permissionIDs {
		seen[id] = true
	}
	for _, want := range []string{"ssh", "gh", "docker"} {
		if !seen[want] {
			t.Errorf("permission %q missing on macOS TUI", want)
		}
	}
	view := model.View()
	if !strings.Contains(view, "SSH access") {
		t.Fatalf("view missing SSH access:\n%s", view)
	}
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

// stubPATHBin puts a LookPath-resolvable name on PATH so pre-flight succeeds in
// CI images that lack ai-memory / ai-jail. Uses a symlink to `true` so we never
// write a world-executable file (gosec G302/G306).
func stubPATHBin(t *testing.T, name string) {
	t.Helper()
	trueBin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true not on PATH")
	}
	dir := t.TempDir()
	if err := os.Symlink(trueBin, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestContinueRowSelectsAndLaunchesAiMemoryRun(t *testing.T) {
	// confirmRun validates PATH for ai-memory; CI images often lack the binary.
	stubPATHBin(t, "ai-memory")
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseJail:     false,
		UseMemory:   true,
		Permissions: map[string]bool{},
	})
	if !strings.Contains(model.View(), "Continue last session") {
		t.Fatalf("agents view = %s; want the continue row", model.View())
	}
	model.cursor = 0
	model.section = 0
	// Enter only selects; r runs.
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.launch.ContinueSession {
		t.Fatal("Enter on the continue row did not select it")
	}
	if len(model.result) != 0 {
		t.Fatal("Enter must not launch; only select")
	}
	model = applyKey(t, model, runeKey("r"))
	if len(model.result) == 0 {
		t.Fatalf("r on continue did not build a command; status=%q", model.status)
	}
	joined := strings.Join(model.result, " ")
	if joined != "ai-memory run" {
		t.Fatalf("continue argv = %q; want %q", joined, "ai-memory run")
	}
}

func TestSelectingAnAgentClearsContinueSession(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		ContinueSession: true,
		UseMemory:       false,
		UseJail:         false,
		Permissions:     map[string]bool{},
	})
	model.agents = []catalog.AgentStatus{{
		Agent: config.Agent{Name: "Pi", Command: "sh"}, Path: sh, Installed: true, ResolvedCommand: "sh",
	}}
	model.cursor = 1
	model.section = 0
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.launch.ContinueSession {
		t.Fatal("selecting an agent must clear the continue selection")
	}
	if model.launch.Agent.Command != "sh" {
		t.Fatalf("selected agent = %q; want sh", model.launch.Agent.Command)
	}
	if len(model.result) != 0 {
		t.Fatal("Enter must select only; r launches")
	}
	model = applyKey(t, model, runeKey("r"))
	if len(model.result) == 0 {
		t.Fatalf("r must launch; status=%q", model.status)
	}
	if !strings.Contains(strings.Join(model.result, " "), "sh") {
		t.Fatalf("launch argv = %q; want sh", model.result)
	}
}
