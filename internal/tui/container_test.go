package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// The Container section exists only while the docker backend is on; with it
// off, the section count stays at the pre-docker layout so nothing shifts.
func TestContainerSectionOnlyExistsWithDocker(t *testing.T) {
	stubWindows(t, false)
	launch := launcher.LaunchConfig{UseJail: true, UseMemory: true, Permissions: map[string]bool{}}
	model := NewModel(config.DefaultGlobal(), launch)
	if model.sectionCount() != 4 {
		t.Fatalf("sectionCount() without docker = %d; want 4", model.sectionCount())
	}
	if model.containerIndex() != -1 {
		t.Fatalf("containerIndex() without docker = %d; want -1", model.containerIndex())
	}

	launch.UseDocker = true
	launch.UseJail = false
	model = NewModel(config.DefaultGlobal(), launch)
	if model.sectionCount() != 5 {
		t.Fatalf("sectionCount() with docker = %d; want 5", model.sectionCount())
	}
	if model.containerIndex() != 4 {
		t.Fatalf("containerIndex() with docker = %d; want 4", model.containerIndex())
	}
	if !strings.Contains(model.View(), "Container") {
		t.Fatal("view does not render the container section")
	}
}

// The backend toggle in Options is tri-state: enabling the container disables
// the jail, disabling it restores the jail. The two never coexist.
func TestBackendToggleSwitchesJailAndContainer(t *testing.T) {
	stubWindows(t, false)
	launch := launcher.LaunchConfig{UseJail: true, UseMemory: true, Permissions: map[string]bool{}}
	model := NewModel(config.DefaultGlobal(), launch)

	// Navigate to the Options section and find the container toggle row.
	model = applyKey(t, model, runeKey("4")) // options is the 4th section (index 3)
	if model.section != 3 {
		t.Fatalf("jump to options = %d; want 3", model.section)
	}
	// Move the cursor to the container row (index 1 in optionRows).
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.launch.UseDocker {
		t.Fatal("UseDocker should be false initially")
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if !model.launch.UseDocker {
		t.Fatal("toggling the container option should enable UseDocker")
	}
	if model.launch.UseJail {
		t.Fatal("enabling the container must disable the jail (mutually exclusive)")
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if model.launch.UseDocker {
		t.Fatal("toggling off the container should disable UseDocker")
	}
	if !model.launch.UseJail {
		t.Fatal("disabling the container must restore the jail default")
	}
}

// Toggling a stack in the Container section updates the docker selection, and
// the saved list stays canonical (sorted) for the image tag.
func TestToggleStackUpdatesSelection(t *testing.T) {
	stubWindows(t, false)
	launch := launcher.LaunchConfig{
		UseDocker:   true,
		UseJail:     false,
		UseMemory:   true,
		Permissions: map[string]bool{},
		Docker: container.RunConfig{
			Selection: container.Selection{Stacks: []string{"go"}},
		},
	}
	model := NewModel(config.DefaultGlobal(), launch)

	// Jump to the container section (index 4, the 5th section) and toggle the
	// first stack (Go, already selected) off, then the second (Python) on.
	model = applyKey(t, model, runeKey("5"))
	if model.section != 4 {
		t.Fatalf("jump to container = %d; want 4", model.section)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if len(model.launch.Docker.Selection.Stacks) != 0 {
		t.Fatalf("stacks after toggling off go = %v; want empty", model.launch.Docker.Selection.Stacks)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if len(model.launch.Docker.Selection.Stacks) != 1 || model.launch.Docker.Selection.Stacks[0] != "python" {
		t.Fatalf("stacks after toggling on python = %v; want [python]", model.launch.Docker.Selection.Stacks)
	}
	if !strings.Contains(model.View(), "[✓] Python") {
		t.Fatal("view does not render the selected python stack")
	}
}

// Toggling a stack sorts the resulting selection, so the image tag does not
// churn when the same stacks are selected in a different order.
func TestToggleStackKeepsSelectionCanonical(t *testing.T) {
	stubWindows(t, false)
	launch := launcher.LaunchConfig{
		UseDocker:   true,
		UseMemory:   true,
		Permissions: map[string]bool{},
		Docker: container.RunConfig{
			Selection: container.Selection{Stacks: []string{"go"}},
		},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	model = applyKey(t, model, runeKey("5"))
	// python is at index 1 (after go); toggle it on.
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	stacks := model.launch.Docker.Selection.Stacks
	if len(stacks) != 2 || stacks[0] != "go" || stacks[1] != "python" {
		t.Fatalf("stacks = %v; want sorted [go python]", stacks)
	}
}

// The Enter key toggles a stack in the container section too (Space is not
// the only way), matching the Profiles behavior.
func TestEnterTogglesStack(t *testing.T) {
	stubWindows(t, false)
	launch := launcher.LaunchConfig{
		UseDocker:   true,
		UseMemory:   true,
		Permissions: map[string]bool{},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	model = applyKey(t, model, runeKey("5"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(model.launch.Docker.Selection.Stacks) != 1 || model.launch.Docker.Selection.Stacks[0] != "go" {
		t.Fatalf("stacks after Enter = %v; want [go]", model.launch.Docker.Selection.Stacks)
	}
}

// Moving the cursor in the container section wraps within the stack list.
func TestContainerCursorWraps(t *testing.T) {
	stubWindows(t, false)
	launch := launcher.LaunchConfig{
		UseDocker:   true,
		UseMemory:   true,
		Permissions: map[string]bool{},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	model = applyKey(t, model, runeKey("5"))
	// Move up from the first stack: should wrap to the last.
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyUp})
	if model.cursor != len(model.stackIDs)-1 {
		t.Fatalf("cursor = %d; want wrap to last stack %d", model.cursor, len(model.stackIDs)-1)
	}
}

// H2: switching agents with docker active must re-plan the image selection
// (kind follows the new agent), or the image would be built for the old one.
func TestSelectAgentUpdatesDockerSelection(t *testing.T) {
	stubWindows(t, false)
	launch := launcher.LaunchConfig{
		UseDocker:   true,
		UseMemory:   true,
		Permissions: map[string]bool{},
		Docker: container.RunConfig{
			Selection: container.Selection{Stacks: []string{"go"}},
		},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	// Move the cursor to a script agent (claude) and select it.
	for i, agent := range model.agents {
		if agent.Agent.Command == "claude" {
			// cursor 0 is "continue"; agent rows start at 1.
			for model.cursor < i+1 {
				model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
			}
			break
		}
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(model.launch.Docker.Selection.Agents) != 1 {
		t.Fatalf("docker selection agents = %d; want 1 after selecting claude", len(model.launch.Docker.Selection.Agents))
	}
	got := model.launch.Docker.Selection.Agents[0]
	if got.Command != "claude" {
		t.Fatalf("selection agent = %q; want claude", got.Command)
	}
	if got.Kind != container.InstallScript {
		t.Fatalf("selection kind = %s; want script (claude has source_url)", got.Kind)
	}
}
