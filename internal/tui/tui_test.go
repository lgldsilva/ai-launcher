package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

func applyKey(t *testing.T, model Model, key tea.KeyMsg) Model {
	t.Helper()
	updated, _ := model.Update(key)
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update() returned %T; want Model", updated)
	}
	return got
}

func runeKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

func TestModelRendersAndNavigatesAllSections(t *testing.T) {
	launch := launcher.LaunchConfig{
		Agent:       config.Agent{Command: "codex"},
		UseJail:     true,
		UseMemory:   true,
		Permissions: map[string]bool{},
		Mounts:      []config.Mount{{Path: "/workspace", Mode: "ro"}},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	view := model.View()
	for _, want := range []string{"Agente", "Permissões", "Mounts", "Opções", "Preview:"} {
		if !strings.Contains(view, want) {
			t.Errorf("initial view does not contain %q: %s", want, view)
		}
	}

	for range 3 {
		model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	}
	if model.section != 3 || model.cursor != 0 {
		t.Fatalf("after three tabs section=%d cursor=%d; want options at cursor 0", model.section, model.cursor)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if model.launch.UseJail {
		t.Fatal("space in options section did not toggle jail")
	}
	model = applyKey(t, model, runeKey("2"))
	if model.section != 1 {
		t.Fatalf("numeric section jump = %d; want permissions", model.section)
	}
}

func TestModelAddsAndRejectsDuplicateMounts(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.section = 2
	model = applyKey(t, model, runeKey("/"))
	model = applyKey(t, model, runeKey("/tmp/data"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	if len(model.launch.Mounts) != 1 || model.launch.Mounts[0].Path != "/tmp/data" || model.launch.Mounts[0].Mode != "rw" {
		t.Fatalf("mount after input = %#v; want one rw mount", model.launch.Mounts)
	}

	model = applyKey(t, model, runeKey("/"))
	model = applyKey(t, model, runeKey("/tmp/data"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(model.launch.Mounts) != 1 || !strings.Contains(model.status, "já está") {
		t.Fatalf("duplicate mount handling: mounts=%#v status=%q", model.launch.Mounts, model.status)
	}
}

func TestModelNavigatesMountBrowser(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "project")
	if err := os.Mkdir(child, 0o750); err != nil {
		t.Fatal(err)
	}
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.section = 2
	model = applyKey(t, model, runeKey("/"))
	model.mountDir = root
	model.refreshMountEntries()
	if !strings.Contains(model.View(), "Navegador: "+root) {
		t.Fatalf("mount browser view = %s", model.View())
	}
	for index, entry := range model.mountEntries {
		if entry == "project" {
			model.mountCursor = index
		}
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if model.mountDir != child {
		t.Fatalf("right navigation directory = %q; want %q", model.mountDir, child)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if model.mountDir != root {
		t.Fatalf("left navigation directory = %q; want %q", model.mountDir, root)
	}
	for index, entry := range model.mountEntries {
		if entry == "project" {
			model.mountCursor = index
		}
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(model.launch.Mounts) != 1 || model.launch.Mounts[0].Path != child {
		t.Fatalf("selected mount = %#v; want %q", model.launch.Mounts, child)
	}
}

func TestModelSaveAndHelpShortcuts(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	saved := false
	model.save = func(launcher.LaunchConfig) error {
		saved = true
		return nil
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !saved || !strings.Contains(model.status, "salva") {
		t.Fatalf("ctrl+s result: saved=%t status=%q", saved, model.status)
	}
	model = applyKey(t, model, runeKey("?"))
	if !model.helpOpen || !strings.Contains(model.View(), "ajuda") {
		t.Fatal("help shortcut did not open help view")
	}
	model = applyKey(t, model, runeKey("x"))
	if model.helpOpen {
		t.Fatal("any key should close help view")
	}
}

func TestNewModelInitializesNilPermissions(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{})
	if model.launch.Permissions == nil {
		t.Fatal("NewModel left permissions map nil")
	}
}

func TestModelCanExecuteFromTheAgentSection(t *testing.T) {
	launch := launcher.LaunchConfig{Agent: config.Agent{Command: "claude"}, Permissions: map[string]bool{}}
	model := NewModel(config.DefaultGlobal(), launch)
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(model.result) == 0 {
		t.Fatal("Enter on the already-selected agent did not build a command")
	}
}
