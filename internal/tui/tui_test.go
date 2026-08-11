package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"github.com/muesli/termenv"
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

func TestReloadConfigPreservesSelection(t *testing.T) {
	stubWindows(t, false)
	launch := launcher.LaunchConfig{
		Agent:       config.Agent{Command: "codex"},
		UseJail:     true,
		UseMemory:   true,
		Permissions: map[string]bool{},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	model.opts.GlobalPath = ""

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update() returned %T; want Model", updated)
	}
	if got.launch.Agent.Command != "codex" {
		t.Fatalf("reload changed agent selection: %q", got.launch.Agent.Command)
	}
	if !strings.Contains(got.status, "reloaded") {
		t.Fatalf("expected reload status, got %q", got.status)
	}
}

func TestModelRendersAndNavigatesAllSections(t *testing.T) {
	stubWindows(t, false)
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
	// a opens the add panel; Enter from Mounts must RUN, not open add.
	model = applyKey(t, model, runeKey("a"))
	if !model.inputActive {
		t.Fatal("key a on Mounts did not open the add-folder panel")
	}
	if model.mountMode != "rw" {
		t.Fatalf("default mount mode = %q; want rw", model.mountMode)
	}
	model = applyKey(t, model, runeKey("/tmp/data"))
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
	if !strings.Contains(model.View(), "Looking in: "+root) {
		t.Fatalf("mount browser view = %s", model.View())
	}
	if !strings.Contains(model.View(), "Enter will add:") {
		t.Fatalf("mount browser missing Enter hint: %s", model.View())
	}
	if !strings.Contains(model.View(), "Keys:") {
		t.Fatalf("mount browser missing key legend: %s", model.View())
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
	// Entering a directory fills the path input so further typing continues
	// from there; it must not leave a bare letter in the field.
	wantInput := child + string(filepath.Separator)
	if model.mountInput != wantInput {
		t.Fatalf("l while browsing input = %q; want %q", model.mountInput, wantInput)
	}
}

func TestMountInputArrowRightWhileTypingEntersHighlightedDir(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "project")
	if err := os.Mkdir(child, 0o750); err != nil {
		t.Fatal(err)
	}
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.section = 2
	model = applyKey(t, model, runeKey("/"))
	// Type the root with a trailing slash so the browser lists its children
	// and filters by empty prefix; then highlight "project" and press →.
	model = applyKey(t, model, runeKey(root+"/"))
	for index, entry := range model.mountEntries {
		if entry == "project" {
			model.mountCursor = index
		}
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyRight})
	wantInput := child + string(filepath.Separator)
	if model.mountInput != wantInput {
		t.Fatalf("arrow right while typing = input %q; want %q", model.mountInput, wantInput)
	}
	if model.mountDir != child {
		t.Fatalf("arrow right while typing navigated to %q; want %q", model.mountDir, child)
	}
}

func TestMountPathTabCompletion(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha-app")
	alphaLib := filepath.Join(root, "alpha-lib")
	beta := filepath.Join(root, "beta")
	for _, dir := range []string{alpha, alphaLib, beta} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.section = 2
	model = applyKey(t, model, runeKey("/"))
	model = applyKey(t, model, runeKey(filepath.Join(root, "al")))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	// Two alpha-* matches share the prefix "alpha-".
	if !strings.HasPrefix(filepath.Base(strings.TrimSuffix(model.mountInput, string(filepath.Separator))), "alpha") &&
		!strings.Contains(model.mountInput, "alpha") {
		t.Fatalf("Tab completion input = %q; want alpha prefix under %s", model.mountInput, root)
	}
	if !strings.Contains(model.status, "2 matches") {
		t.Fatalf("status after partial complete = %q; want 2 matches", model.status)
	}
	// Finish unique: type enough to leave only alpha-app, then Tab.
	model.mountInput = filepath.Join(root, "alpha-a")
	model.mountTyped = true
	model.syncMountBrowserFromInput()
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyTab})
	want := alpha + string(filepath.Separator)
	if model.mountInput != want || model.mountDir != alpha {
		t.Fatalf("unique Tab complete: input=%q dir=%q; want input=%q dir=%q", model.mountInput, model.mountDir, want, alpha)
	}
}

func TestMountPathFiltersBrowserWhileTyping(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "keepme")
	drop := filepath.Join(root, "dropme")
	for _, dir := range []string{keep, drop} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.section = 2
	model = applyKey(t, model, runeKey("/"))
	model = applyKey(t, model, runeKey(filepath.Join(root, "ke")))
	if model.mountFilter != "ke" {
		t.Fatalf("filter = %q; want ke", model.mountFilter)
	}
	if !containsString(model.mountEntries, "keepme") {
		t.Fatalf("entries = %#v; want keepme", model.mountEntries)
	}
	if containsString(model.mountEntries, "dropme") {
		t.Fatalf("entries = %#v; dropme must be filtered out", model.mountEntries)
	}
}

func TestMountPathExpandsTildeOnAdd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.section = 2
	model = applyKey(t, model, runeKey("/"))
	model = applyKey(t, model, runeKey("~/projects"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	want := filepath.Join(home, "projects")
	if len(model.launch.Mounts) != 1 || model.launch.Mounts[0].Path != want {
		t.Fatalf("mounts = %#v; want %q", model.launch.Mounts, want)
	}
}

func TestMountCtrlTTogglesMode(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.section = 2
	model = applyKey(t, model, runeKey("a"))
	if !model.inputActive {
		t.Fatal("key a on Mounts did not open the add-folder panel")
	}
	if model.mountMode != "rw" {
		t.Fatalf("mode = %q; want rw", model.mountMode)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlT})
	if model.mountMode != "ro" {
		t.Fatalf("mode after Ctrl+T = %q; want ro", model.mountMode)
	}
}

func TestMountsSectionShowsHowToAddHint(t *testing.T) {
	// Tips must be visible even when Mounts is not the focused section.
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.section = 0
	view := model.View()
	for _, want := range []string{"Tips", "add a folder", "a  or  +  or  /", "r                    RUN"} {
		if !strings.Contains(view, want) {
			t.Fatalf("mounts block missing %q:\n%s", want, view)
		}
	}
}

func TestEnterOnMountsDoesNotRunOrOpenAdd(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Name: "Shell", Command: "sh"},
		Executable:  sh,
		UseJail:     false,
		UseMemory:   false,
		Permissions: map[string]bool{},
		Mounts:      []config.Mount{{Path: "/tmp", Mode: "rw"}},
	})
	model.section = 2
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.inputActive {
		t.Fatal("Enter on Mounts must not open add panel (use a)")
	}
	if len(model.result) != 0 {
		t.Fatal("Enter on Mounts must not run (use r)")
	}
	model = applyKey(t, model, runeKey("r"))
	if len(model.result) == 0 {
		t.Fatalf("r on Mounts should run; status=%q", model.status)
	}
}

func TestRunBlockedByPreflightStaysInTUI(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Name: "Claude Code", Command: "claude"},
		UseJail:     true, // requires ai-jail on PATH — force missing via empty LookPath in validator... use missing mount instead
		UseMemory:   false,
		Permissions: map[string]bool{},
		Mounts:      []config.Mount{{Path: "/this/path/does/not/exist-ai-launcher-test", Mode: "rw"}},
	})
	model.section = 0
	model.agents = []catalog.AgentStatus{{
		Agent: config.Agent{Name: "Claude Code", Command: "claude"}, Path: "/bin/claude", Installed: true,
	}}
	model.cursor = 1
	model = applyKey(t, model, runeKey("r"))
	if len(model.result) != 0 {
		t.Fatal("r must not quit/launch when pre-flight fails")
	}
	if !strings.Contains(model.status, "Cannot run") && !strings.Contains(model.status, "mount-not-found") {
		t.Fatalf("status = %q; want pre-flight failure message", model.status)
	}
}

func TestVisibleAgentsInstalledOnlySortedByRecent(t *testing.T) {
	all := []catalog.AgentStatus{
		{Agent: config.Agent{Name: "A", Command: "aaa"}, Installed: true},
		{Agent: config.Agent{Name: "B", Command: "bbb"}, Installed: false},
		{Agent: config.Agent{Name: "C", Command: "ccc"}, Installed: true},
		{Agent: config.Agent{Name: "D", Command: "ddd"}, Installed: true},
	}
	got := visibleAgents(all, []string{"ddd", "missing", "aaa"}, launcher.LaunchConfig{})
	if len(got) != 3 {
		t.Fatalf("visible = %#v; want 3 installed agents", got)
	}
	if got[0].Agent.Command != "ddd" || got[1].Agent.Command != "aaa" || got[2].Agent.Command != "ccc" {
		t.Fatalf("order = %s,%s,%s; want ddd,aaa,ccc", got[0].Agent.Command, got[1].Agent.Command, got[2].Agent.Command)
	}
}

func TestVisibleAgentsKeepsSelectedEvenIfMissing(t *testing.T) {
	all := []catalog.AgentStatus{
		{Agent: config.Agent{Name: "Missing", Command: "gone"}, Installed: false},
		{Agent: config.Agent{Name: "OK", Command: "ok"}, Installed: true},
	}
	got := visibleAgents(all, nil, launcher.LaunchConfig{Agent: config.Agent{Command: "gone"}})
	if len(got) != 2 || got[0].Agent.Command != "gone" {
		t.Fatalf("visible = %#v; want selected missing agent first plus installed", got)
	}
}

func TestMountBrowserListsSymlinkedDirectories(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real-dir")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link-dir")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.mountDir = root
	model.mountFilter = ""
	model.refreshMountEntries()
	if !containsString(model.mountEntries, "link-dir") {
		t.Fatalf("entries = %#v; want symlink dir listed", model.mountEntries)
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

func TestComposeReviewIsShownInsideTUIBeforeRun(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Command: "custom-cli"},
		UseDocker:   true,
		Workspace:   "/workspace",
		Services:    []string{"redis"},
		Permissions: map[string]bool{},
		Docker: container.RunConfig{Selection: container.Selection{Agents: []container.AgentInstall{{
			Command: "custom-cli",
			Kind:    container.InstallScript,
			Script:  "echo install",
		}}}},
	})
	chosen := ComposeUpdateChoice(0)
	model.hooks.ReviewCompose = func(launcher.LaunchConfig) (*ComposeUpdateReview, error) {
		return &ComposeUpdateReview{
			Diff: "--- current\n+++ generated\n-    - 16379:6379\n+    - 6379:6379\n",
			Choose: func(choice ComposeUpdateChoice) error {
				chosen = choice
				return nil
			},
		}, nil
	}
	if model.confirmRun(false) {
		t.Fatal("confirmRun should stop for Compose review")
	}
	if model.composeReview == nil || !strings.Contains(model.View(), "16379:6379") {
		t.Fatalf("Compose review was not rendered: %q", model.View())
	}
	model = applyKey(t, model, runeKey("m"))
	if chosen != KeepCompose || model.composeReview != nil || len(model.result) == 0 {
		t.Fatalf("keep review result: choice=%d review=%v result=%v", chosen, model.composeReview != nil, model.result)
	}
}

// The compose review screen is part of an English UI: the scroll indicators
// and the key legend must not leak Portuguese.
func TestComposeReviewViewUsesEnglish(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	model.height = 10
	model.composeReview = &ComposeUpdateReview{Diff: strings.Repeat(" unchanged\n", 19) + " unchanged"}
	model.composeReviewOffset = 4
	view := model.composeReviewView()
	for _, want := range []string{"↑ 4 line(s) above", "line(s) below", "[m] keep current", "[s] replace with generated", "[Esc] cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("compose review missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"linha(s)", "manter", "substituir", "rolar", "cancelar"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("compose review still renders Portuguese %q:\n%s", unwanted, view)
		}
	}
}

func TestDockerSectionHintsAndHelpDescribeAllBackendControls(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
	})
	model.section = model.containerIndex()
	if hint := model.sectionHint(); !strings.Contains(hint, "resources/ports") || !strings.Contains(hint, "toggle stacks") {
		t.Fatalf("container hint = %q", hint)
	}
	model.section = model.servicesIndex()
	if hint := model.sectionHint(); !strings.Contains(hint, "add/remove") || !strings.Contains(hint, "Enter edit ports") {
		t.Fatalf("services hint = %q", hint)
	}
	model.section = 3
	if hint := model.sectionHint(); !strings.Contains(hint, "Container docker included") {
		t.Fatalf("options hint = %q", hint)
	}
	model.helpOpen = true
	help := model.View()
	for _, want := range []string{"Container", "Services", "resources", "ports", "Compose YAML"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q:\n%s", want, help)
		}
	}
}

func TestShortDockerLayoutShowsOnlyTheActiveSection(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
	})
	model.height = 12
	view := model.View()
	if !strings.Contains(view, "Agent") || !strings.Contains(view, "compact view") {
		t.Fatalf("short Docker view = %q; want active section and compact hint", view)
	}
	if strings.Contains(view, "\nServices\n") {
		t.Fatalf("short Docker view rendered inactive sections and will clip the TUI: %q", view)
	}
}

func TestShortDockerLayoutFollowsContainerSection(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
	})
	model.height = 12
	model.section = model.containerIndex()
	model.cursor = len(model.stackIDs) + len(model.containerResourceRows())
	view := model.View()
	if !strings.Contains(view, "Container") || !strings.Contains(view, "Runtime:") {
		t.Fatalf("short Container view = %q; want the active container controls", view)
	}
	if strings.Contains(view, "\nAgent\n") {
		t.Fatalf("short Container view rendered the inactive Agent section: %q", view)
	}
}

func TestShortLayoutShowsRunFailureStatus(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
	})
	model.height = 12
	model.section = model.containerIndex()
	model.status = "Cannot run — fix these first:\n  · docker runtime is unavailable"

	view := model.View()
	for _, want := range []string{"Cannot run", "docker runtime is unavailable", "[5 Container]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("short failure view missing %q:\n%s", want, view)
		}
	}
}

func TestNewModelInitializesNilPermissions(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{})
	if model.launch.Permissions == nil {
		t.Fatal("NewModel left permissions map nil")
	}
}

// TestModelDisablesMemoryForUnsupportedAgent proves that selecting an agent
// with SupportsMemory=false automatically turns ai-memory off in the TUI.
func TestModelDisablesMemoryForUnsupportedAgent(t *testing.T) {
	stubWindows(t, false)
	global := config.DefaultGlobal()
	global.Agents = append(global.Agents, config.Agent{Name: "Cursor", Command: "cursor-agent", SupportsMemory: false})
	launch := launcher.LaunchConfig{
		Agent:       config.Agent{Command: "claude", SupportsMemory: true},
		UseJail:     true,
		UseMemory:   true,
		Permissions: map[string]bool{},
	}
	model := NewModel(global, launch)
	if !model.launch.UseMemory {
		t.Fatal("initial model must have memory enabled for claude")
	}
	// Add cursor-agent to the visible list and move the cursor to it.
	model.agents = append(model.agents, catalog.AgentStatus{
		Agent: config.Agent{Name: "Cursor", Command: "cursor-agent", SupportsMemory: false},
		Path:  "/bin/cursor-agent", Installed: true,
	})
	model.cursor = len(model.agents)
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.launch.UseMemory {
		t.Fatal("selecting cursor-agent must disable ai-memory")
	}
	if !strings.Contains(model.status, "ai-memory disabled") {
		t.Fatalf("status = %q; want ai-memory disabled message", model.status)
	}
}

// TestModelDisablesYoloForUnsupportedAgent proves that selecting an agent with
// SupportsYolo=false automatically turns --yolo off in the TUI.
func TestModelDisablesYoloForUnsupportedAgent(t *testing.T) {
	stubWindows(t, false)
	global := config.DefaultGlobal()
	global.Agents = append(global.Agents, config.Agent{Name: "Cursor", Command: "cursor-agent", SupportsMemory: false, SupportsYolo: false})
	launch := launcher.LaunchConfig{
		Agent:       config.Agent{Command: "claude", SupportsMemory: true, SupportsYolo: true},
		UseJail:     true,
		UseMemory:   true,
		Yolo:        true,
		Permissions: map[string]bool{},
	}
	model := NewModel(global, launch)
	if !model.launch.Yolo {
		t.Fatal("initial model must have yolo enabled for claude")
	}
	model.agents = append(model.agents, catalog.AgentStatus{
		Agent: config.Agent{Name: "Cursor", Command: "cursor-agent", SupportsMemory: false, SupportsYolo: false},
		Path:  "/bin/cursor-agent", Installed: true,
	})
	model.cursor = len(model.agents)
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.launch.Yolo {
		t.Fatal("selecting cursor-agent must disable --yolo")
	}
	if !strings.Contains(model.status, "--yolo disabled") {
		t.Fatalf("status = %q; want --yolo disabled message", model.status)
	}
}

func TestModelCanExecuteFromTheAgentSection(t *testing.T) {
	// Use a real PATH binary so in-TUI pre-flight validation passes.
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}
	launch := launcher.LaunchConfig{
		Agent:       config.Agent{Name: "Shell", Command: "sh"},
		Executable:  sh,
		UseJail:     false,
		UseMemory:   false,
		Permissions: map[string]bool{},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	model.agents = []catalog.AgentStatus{{
		Agent: config.Agent{Name: "Shell", Command: "sh"}, Path: sh, Installed: true, ResolvedCommand: "sh",
	}}
	model.cursor = 1
	model.section = 0
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.launch.Agent.Command != "sh" {
		t.Fatalf("launch agent = %q; want sh", model.launch.Agent.Command)
	}
	if len(model.result) != 0 {
		t.Fatal("Enter must not launch")
	}
	model = applyKey(t, model, runeKey("r"))
	if len(model.result) == 0 {
		t.Fatalf("r did not build a command; status=%q", model.status)
	}
}

// Regression: Selected is Claude but cursor is on oc (recent-sorted list or
// arrow navigation without Space). r must RUN Selected, never the highlight.
func TestRunUsesSelectedAgentNotCursorHighlight(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}
	trueBin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true not on PATH")
	}
	// Use sh as the Selected harness so pre-flight LookPath succeeds in CI;
	// OpenCode under the cursor is a different real PATH binary (true).
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Name: "Claude Code", Command: "sh"},
		Executable:  sh,
		UseJail:     false,
		UseMemory:   false,
		Permissions: map[string]bool{},
	})
	model.agents = []catalog.AgentStatus{
		{Agent: config.Agent{Name: "Claude Code", Command: "sh"}, Path: sh, Installed: true, ResolvedCommand: "sh"},
		{Agent: config.Agent{Name: "OpenCode", Command: "oc"}, Path: trueBin, Installed: true, ResolvedCommand: "oc"},
	}
	model.cursor = 2 // highlight OpenCode without Space/Enter select
	model.section = 0

	model = applyKey(t, model, runeKey("r"))
	if len(model.result) == 0 {
		t.Fatalf("r did not launch; status=%q", model.status)
	}
	if model.launch.Agent.Command != "sh" {
		t.Fatalf("r overwrote Selected with cursor: agent=%q; want sh", model.launch.Agent.Command)
	}
	joined := strings.Join(model.result, " ")
	if strings.Contains(joined, "oc") {
		t.Fatalf("argv launched highlighted oc instead of Selected: %q", joined)
	}
	if !strings.Contains(joined, "sh") {
		t.Fatalf("argv = %q; want selected sh harness", joined)
	}
}

func TestEnterOnPiSelectsWithoutClosing(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}
	// "pi" here is simulated with sh so pre-flight can pass when r is pressed.
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Name: "Other", Command: "other"},
		Executable:  sh,
		UseJail:     false,
		UseMemory:   false,
		Permissions: map[string]bool{},
	})
	model.agents = []catalog.AgentStatus{
		{Agent: config.Agent{Name: "Other", Command: "other"}, Path: sh, Installed: true, ResolvedCommand: "other"},
		{Agent: config.Agent{Name: "Pi", Command: "sh", Aliases: []string{"pi"}}, Path: sh, Installed: true, ResolvedCommand: "sh"},
	}
	model.cursor = 2
	model.section = 0
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.launch.ContinueSession {
		t.Fatal("Enter on Pi must not activate Continue last session")
	}
	if model.launch.Agent.Command != "sh" {
		t.Fatalf("after Enter agent = %q; want sh (simulated pi)", model.launch.Agent.Command)
	}
	if len(model.result) != 0 {
		t.Fatal("Enter on Pi must NOT launch (must not set result / quit)")
	}
	if !strings.Contains(model.status, "press r to RUN") {
		t.Fatalf("status = %q; want hint to press r", model.status)
	}
	if !strings.Contains(model.View(), "Selected: Pi") {
		t.Fatalf("view missing Selected Pi:\n%s", model.View())
	}
	model = applyKey(t, model, runeKey("r"))
	if len(model.result) == 0 {
		t.Fatalf("r did not launch; status=%q", model.status)
	}
	joined := strings.Join(model.result, " ")
	if !strings.Contains(joined, "sh") {
		t.Fatalf("argv = %q; want sh harness", joined)
	}
}

func TestAgentListShowsSelectedMarker(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Name: "Pi", Command: "pi"},
		Permissions: map[string]bool{},
	})
	model.agents = []catalog.AgentStatus{
		{Agent: config.Agent{Name: "Pi", Command: "pi"}, Installed: true},
	}
	model.cursor = 1
	view := model.View()
	if !strings.Contains(view, "Selected: Pi (pi)") {
		t.Fatalf("missing Selected line:\n%s", view)
	}
	if !strings.Contains(view, "[●]") {
		t.Fatalf("missing selected marker:\n%s", view)
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
		Agent: config.Agent{Command: "kimi", Params: []config.Param{{Name: "query", Flag: "--prompt", TakesValue: true}}},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	model.section = 3
	model.cursor = 0 // NewModel parks cursor on the selected agent; reset for options.
	for range len(model.optionRows()) + len(model.advancedOptionRows()) {
		model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	}
	if model.cursor != len(model.optionRows())+len(model.advancedOptionRows()) {
		t.Fatalf("cursor = %d; want first param row", model.cursor)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.textInputActive || !model.textInputKind.isParam() {
		t.Fatal("enter on a param row did not open the param input")
	}
	model = applyKey(t, model, runeKey("oi"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.launch.ParamValues["query"] != "oi" {
		t.Fatalf("param values = %#v", model.launch.ParamValues)
	}
	if !strings.Contains(model.View(), "query: oi (--prompt)") {
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

func TestModelEditsAdvancedLaunchInputs(t *testing.T) {
	launch := launcher.LaunchConfig{
		Agent: config.Agent{
			Command:        "codex",
			SupportsMemory: true,
			SupportsYolo:   true,
		},
		UseMemory:   true,
		Permissions: map[string]bool{},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	model.section = 3

	edit := func(t *testing.T, index int, value string) {
		t.Helper()
		model.cursor = len(model.optionRows()) + index
		model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
		if !model.textInputActive {
			t.Fatalf("advanced row %d did not open editor", index)
		}
		model = applyKey(t, model, runeKey(value))
		model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
		if model.textInputActive {
			t.Fatalf("advanced row %d editor stayed open: %q", index, model.status)
		}
	}

	edit(t, 0, "feature-x")
	edit(t, 1, "resume-42")
	edit(t, 2, "/workspaces/acme")
	edit(t, 3, "billing")
	edit(t, 4, `--model "sonnet 4" --verbose`)

	if model.launch.NewWorkstream != "feature-x" || model.launch.Workstream != "resume-42" ||
		model.launch.Workspace != "/workspaces/acme" || model.launch.Project != "billing" {
		t.Fatalf("advanced launch inputs = %#v", model.launch)
	}
	wantArgs := []string{"--model", "sonnet 4", "--verbose"}
	if !reflect.DeepEqual(model.launch.ExtraArgs, wantArgs) {
		t.Fatalf("extra args = %#v; want %#v", model.launch.ExtraArgs, wantArgs)
	}
	view := model.View()
	for _, want := range []string{
		"New workstream name: feature-x",
		"Resume workstream: resume-42",
		"Memory workspace: /workspaces/acme",
		"Memory project: billing",
		`Extra agent args: --model 'sonnet 4' --verbose`,
		"quotes supported",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("options view missing %q:\n%s", want, view)
		}
	}

	model.cursor = len(model.optionRows()) + 4
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model.textInputValue = `--model "unterminated`
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.textInputActive || !strings.Contains(model.status, "Invalid extra args") {
		t.Fatalf("invalid extra args did not stay editable: active=%t status=%q", model.textInputActive, model.status)
	}
	if !strings.Contains(model.View(), "Invalid extra args") {
		t.Fatalf("invalid extra args was not visible in the editor view:\n%s", model.View())
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEscape})
}

func TestModelSavesProfileWithCtrlP(t *testing.T) {
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Agent: config.Agent{Command: "claude"}, Permissions: map[string]bool{}})
	var savedName string
	model.hooks.SaveProfile = func(name string, _ launcher.LaunchConfig) error {
		savedName = name
		return nil
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlP})
	if !model.textInputActive || !model.textInputKind.isProfile() {
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

// TestNewProgramEnablesAltScreen guards the cross-platform rendering fix: the
// TUI must start in the alternate screen buffer, otherwise a View taller than
// the terminal (or whose line count changes between frames) drifts down the
// screen instead of repainting in place — the bug seen on short Linux
// terminals. startupOptions is unexported on *tea.Program, so the first bit
// (withAltScreen = 1 << iota = 1) is read via reflection.
func TestNewProgramEnablesAltScreen(t *testing.T) {
	p := newProgram(NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}}))

	field := reflect.ValueOf(p).Elem().FieldByName("startupOptions")
	if !field.IsValid() {
		t.Skip("tea.Program.startupOptions not found; bubbletea API changed — re-check alt screen")
	}
	// #nosec G103 -- reading one known unexported int16 field on a value we just built, for a regression test.
	opts := *(*int16)(unsafe.Pointer(field.UnsafeAddr()))
	if opts&1 == 0 {
		t.Fatal("newProgram must be created with tea.WithAltScreen() to keep the layout stable on short terminals")
	}
}

// TestApplyDisplayOptionsSetsColorProfile guards the --no-color and
// --high-contrast flag wiring: the TUI must force lipgloss into the requested
// color profile so the rest of the rendering stays consistent.
func TestApplyDisplayOptionsSetsColorProfile(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{Permissions: map[string]bool{}})
	original := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	model.opts.NoColor = true
	applyDisplayOptions(&model)
	if got := lipgloss.ColorProfile(); got != termenv.Ascii {
		t.Fatalf("NoColor profile = %v; want Ascii", got)
	}

	model.opts.NoColor = false
	model.opts.HighContrast = true
	applyDisplayOptions(&model)
	if got := lipgloss.ColorProfile(); got != termenv.ANSI256 {
		t.Fatalf("HighContrast profile = %v; want ANSI256", got)
	}
}

// TestCurrentSectionMatchesLegacyIndexHelpers guards currentSection() (the
// single place that now resolves m.section to a sectionKind) against
// drifting from containerIndex/servicesIndex/profilesIndex, the low-level
// helpers it wraps, across every combination of docker/profiles presence.
func TestCurrentSectionMatchesLegacyIndexHelpers(t *testing.T) {
	stubWindows(t, false)
	cases := []struct {
		name      string
		useDocker bool
		profiles  map[string]config.Profile
	}{
		{name: "no docker, no profiles", useDocker: false, profiles: nil},
		{name: "docker, no profiles", useDocker: true, profiles: nil},
		{name: "no docker, with profiles", useDocker: false, profiles: map[string]config.Profile{"box": {Agent: "claude"}}},
		{name: "docker, with profiles", useDocker: true, profiles: map[string]config.Profile{"box": {Agent: "claude"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			global := config.DefaultGlobal()
			global.Profiles = tc.profiles
			model := NewModel(global, launcher.LaunchConfig{
				UseDocker:   tc.useDocker,
				Permissions: map[string]bool{},
			})
			containerIdx := model.containerIndex()
			servicesIdx := model.servicesIndex()
			profilesIdx := model.profilesIndex()
			for section := 0; section < model.sectionCount(); section++ {
				model.section = section
				want := sectionNone
				switch section {
				case 0:
					want = sectionAgent
				case 1:
					want = sectionPermissions
				case 2:
					want = sectionMounts
				case 3:
					want = sectionOptions
				case containerIdx:
					want = sectionContainer
				case servicesIdx:
					want = sectionServices
				case profilesIdx:
					want = sectionProfiles
				}
				if got := model.currentSection(); got != want {
					t.Fatalf("%s: section %d -> currentSection() = %v, want %v (container=%d services=%d profiles=%d)",
						tc.name, section, got, want, containerIdx, servicesIdx, profilesIdx)
				}
			}
		})
	}
}
