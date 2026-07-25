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
	for _, want := range []string{"Agent", "Permissions", "Mounts", "Options", "Preview:"} {
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
	if len(model.launch.Mounts) != 1 || !strings.Contains(model.status, "already") {
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
	if !strings.Contains(model.View(), "Browser: "+root) {
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

func TestMountInputTypesNavigationLettersWhileTyping(t *testing.T) {
	for _, letter := range []string{"l", "j", "k", "h"} {
		t.Run(letter, func(t *testing.T) {
			model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
			model.section = 2
			model = applyKey(t, model, runeKey("/"))
			model = applyKey(t, model, runeKey("/ho"))
			if !model.mountTyped {
				t.Fatal("typing a path did not set mountTyped")
			}
			model = applyKey(t, model, runeKey(letter))
			if want := "/ho" + letter; model.mountInput != want {
				t.Fatalf("mount input after typing %q = %q; want %q", letter, model.mountInput, want)
			}
		})
	}
}

func TestMountInputLetterNavigatesWhenNotTyping(t *testing.T) {
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
	for index, entry := range model.mountEntries {
		if entry == "project" {
			model.mountCursor = index
		}
	}
	model = applyKey(t, model, runeKey("l"))
	if model.mountDir != child {
		t.Fatalf("l navigation directory = %q; want %q", model.mountDir, child)
	}
	if model.mountInput != "" {
		t.Fatalf("l while browsing must not type into the input: %q", model.mountInput)
	}
}

func TestMountInputArrowRightWhileTypingDoesNotAppendOrNavigate(t *testing.T) {
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
	model = applyKey(t, model, runeKey("/ho"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if model.mountInput != "/ho" {
		t.Fatalf("arrow right while typing changed the input to %q", model.mountInput)
	}
	if model.mountDir != root {
		t.Fatalf("arrow right while typing navigated to %q", model.mountDir)
	}
}

func TestModelSaveAndHelpShortcuts(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	saved := false
	model.hooks.Save = func(launcher.LaunchConfig) error {
		saved = true
		return nil
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !saved || !strings.Contains(model.status, "saved") {
		t.Fatalf("ctrl+s result: saved=%t status=%q", saved, model.status)
	}
	model = applyKey(t, model, runeKey("?"))
	if !model.helpOpen || !strings.Contains(model.View(), "help") {
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
	model.cursor = 1 // first agent row; row 0 is "continue last session"
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(model.result) == 0 {
		t.Fatal("Enter on the already-selected agent did not build a command")
	}
}

func TestModelLoadsProfileFromTheProfilesSection(t *testing.T) {
	global := config.DefaultGlobal()
	if err := config.SetProfile(&global, "review", config.Profile{
		Agent:   "custom-cli",
		Mounts:  []config.Mount{{Path: "/reference", Mode: "read-only"}},
		Options: &config.Options{Yolo: true, ParamValues: map[string]string{"model": "v1"}},
	}); err != nil {
		t.Fatal(err)
	}
	model := NewModel(global, launcher.LaunchConfig{UseJail: true, Permissions: map[string]bool{}})
	if model.sectionCount() != 5 {
		t.Fatalf("sectionCount() = %d; want 5 with profiles", model.sectionCount())
	}
	if !strings.Contains(model.View(), "Profiles") {
		t.Fatal("view does not render the profiles section")
	}
	model = applyKey(t, model, runeKey("5"))
	if model.section != 4 {
		t.Fatalf("numeric jump to profiles = %d", model.section)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if model.launch.Agent.Command != "custom-cli" || !model.launch.Yolo || model.launch.ParamValues["model"] != "v1" {
		t.Fatalf("profile not applied: %#v", model.launch)
	}
	if len(model.launch.Mounts) != 1 || model.launch.Mounts[0].Path != "/reference" {
		t.Fatalf("profile mounts = %#v", model.launch.Mounts)
	}
	if !strings.Contains(model.status, "Profile loaded") {
		t.Fatalf("status = %q", model.status)
	}
}

func TestModelEditsDeclaredParamValues(t *testing.T) {
	launch := launcher.LaunchConfig{
		Agent: config.Agent{Command: "kimi", Params: []config.Param{{Name: "query", Flag: "--query", TakesValue: true}}},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	model.section = 3
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	for range 3 {
		model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	}
	if model.cursor != len(model.optionRows()) {
		t.Fatalf("cursor = %d; want first param row", model.cursor)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.textInputActive || model.textInputKind != "param" {
		t.Fatal("enter on a param row did not open the param input")
	}
	model = applyKey(t, model, runeKey("oi"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.launch.ParamValues["query"] != "oi" {
		t.Fatalf("param values = %#v", model.launch.ParamValues)
	}
	if !strings.Contains(model.View(), "query: oi (--query)") {
		t.Fatalf("options view does not show the param value: %s", model.View())
	}

	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyBackspace})
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyBackspace})
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := model.launch.ParamValues["query"]; ok {
		t.Fatalf("empty param input should clear the value: %#v", model.launch.ParamValues)
	}
}

func TestModelSavesProfileWithCtrlP(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Agent: config.Agent{Command: "claude"}, Permissions: map[string]bool{}})
	var savedName string
	model.hooks.SaveProfile = func(name string, _ launcher.LaunchConfig) error {
		savedName = name
		return nil
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlP})
	if !model.textInputActive || model.textInputKind != "profile" {
		t.Fatal("ctrl+p did not open the profile name input")
	}
	model = applyKey(t, model, runeKey("review"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if savedName != "review" || !strings.Contains(model.status, "Profile saved") {
		t.Fatalf("save profile: name=%q status=%q", savedName, model.status)
	}
	if len(model.profileNames) != 1 || model.profileNames[0] != "review" || model.sectionCount() != 5 {
		t.Fatalf("profiles after save = %#v", model.profileNames)
	}
}
