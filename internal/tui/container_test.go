package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/catalog"
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
	if model.sectionCount() != 6 {
		t.Fatalf("sectionCount() with docker = %d; want 6", model.sectionCount())
	}
	if model.containerIndex() != 4 {
		t.Fatalf("containerIndex() with docker = %d; want 4", model.containerIndex())
	}
	if !strings.Contains(model.View(), "Container") {
		t.Fatal("view does not render the container section")
	}
}

func TestDockerProfilesSectionHasNumericShortcut(t *testing.T) {
	stubWindows(t, false)
	global := config.DefaultGlobal()
	global.Profiles = map[string]config.Profile{"box": {Agent: "claude"}}
	model := NewModel(global, launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
	})
	if model.profilesIndex() != 6 || model.sectionCount() != 7 {
		t.Fatalf("docker profile layout = index %d, count %d; want 6, 7", model.profilesIndex(), model.sectionCount())
	}
	model = applyKey(t, model, runeKey("7"))
	if model.section != model.profilesIndex() {
		t.Fatalf("numeric jump to profiles = %d; want %d", model.section, model.profilesIndex())
	}
}

// The backend toggle in Options is tri-state: enabling the container disables
// the jail, disabling it restores the jail. The two never coexist.
func TestBackendToggleSwitchesJailAndContainer(t *testing.T) {
	stubWindows(t, false)
	origDetect := detectContainerRuntime
	detectContainerRuntime = func(string) (container.Runtime, error) { return container.DockerRuntime{}, nil }
	t.Cleanup(func() { detectContainerRuntime = origDetect })
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
	workspace, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if model.launch.Workspace != workspace {
		t.Fatalf("container workspace = %q; want current directory %q", model.launch.Workspace, workspace)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if model.launch.UseDocker {
		t.Fatal("toggling off the container should disable UseDocker")
	}
	if !model.launch.UseJail {
		t.Fatal("disabling the container must restore the jail default")
	}
}

func TestOptionsToggleContainerTmux(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:     true,
		Permissions:   map[string]bool{},
		ContainerTmux: config.TmuxSettings{},
	})
	model = applyKey(t, model, runeKey("4"))
	for index, row := range model.optionRows() {
		if row.name != "Container tmux" {
			continue
		}
		model.cursor = index
		model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
		if !model.launch.ContainerTmux.Enabled || !model.launch.Docker.Tmux {
			t.Fatalf("tmux toggle = %#v/%v; want enabled", model.launch.ContainerTmux, model.launch.Docker.Tmux)
		}
		return
	}
	t.Fatal("Container tmux option not found")
}

func TestContainerRunUsesCurrentDirectoryWhenWorkspaceIsEmpty(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseDocker:   true,
		UseMemory:   false,
		Permissions: map[string]bool{},
		Docker: container.RunConfig{
			Selection: container.Selection{Agents: []container.AgentInstall{{
				Command:  "claude",
				Kind:     container.InstallHostBinary,
				HostPath: "/opt/claude/bin/claude",
			}}},
		},
	})

	workspace, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if model.confirmRun(true) {
		t.Fatal("dry-run confirmRun should keep the TUI open")
	}
	if model.launch.Workspace != workspace {
		t.Fatalf("workspace = %q; want current directory %q", model.launch.Workspace, workspace)
	}
	if strings.Contains(model.status, "docker backend requires a workspace directory") {
		t.Fatalf("confirmRun still rejected the implicit workspace: %q", model.status)
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

func TestContainerStackChoicesIncludeNode(t *testing.T) {
	ids := containerStackIDs()
	for _, id := range ids {
		if id == "node" {
			model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
				UseDocker:   true,
				Permissions: map[string]bool{},
			})
			if !strings.Contains(model.View(), "Node") {
				t.Fatalf("container view does not render Node: %s", model.View())
			}
			return
		}
	}
	t.Fatalf("container stack choices = %v; want node", ids)
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
	last := len(model.stackIDs) + len(model.containerResourceRows()) - 1
	if model.cursor != last {
		t.Fatalf("cursor = %d; want wrap to last container row %d", model.cursor, last)
	}
}

func TestContainerViewShowsResources(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
		Docker: container.RunConfig{
			MemoryLimit:  "4g",
			CPULimit:     "2.0",
			PIDsLimit:    512,
			ExposedPorts: []container.PortMapping{{Host: 3000, Internal: 3000}},
			NetworkName:  "bridge",
		},
	})
	view := model.View()
	for _, want := range []string{"Resources", "Memory", "4g", "512m / 2g", "CPU", "2.0", "1.0 cores", "PIDs", "512", "integer, e.g. 256", "Ports", "3000:3000", "3000:3000[,udp]", "Network", "bridge", "bridge / host"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestContainerResourceEditorShowsValueAndFormatOnShortScreen(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
	})
	model.section = model.containerIndex()
	model.height = 12
	model.cursor = len(model.stackIDs)
	model.startContainerResourceInput()
	model.textInputValue = "512m"

	view := model.View()
	for _, want := range []string{
		"[5 Container]",
		"Memory limit: 512m",
		"Format: positive integer",
		"512m or 2g",
		"Enter apply · Esc cancel · r RUN after closing this editor",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("short editor view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(model.sectionHint(), "after closing the editor") {
		t.Fatalf("sectionHint() = %q; want the run workflow", model.sectionHint())
	}
}

func TestDockerContextEditorListsAndSelectsContext(t *testing.T) {
	stubWindows(t, false)
	original := listDockerContexts
	listDockerContexts = func() ([]string, error) {
		return []string{"default", "colima", "remote-builder"}, nil
	}
	t.Cleanup(func() { listDockerContexts = original })

	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
		Docker:      container.RunConfig{Runtime: container.DockerRuntime{}},
	})
	model.section = model.containerIndex()
	model.cursor = len(model.stackIDs) + len(model.containerResourceRows()) - 1
	model.startContainerResourceInput()
	if !strings.Contains(model.View(), "Contexts (↑/↓)") || !strings.Contains(model.View(), "remote-builder") {
		t.Fatalf("context editor does not show the picker:\n%s", model.View())
	}
	// Choices are (current), default, colima, remote-builder.
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.textInputValue != "colima" {
		t.Fatalf("context selection = %q; want colima", model.textInputValue)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.textInputActive || model.launch.ContainerContext != "colima" || model.launch.Docker.Context != "colima" {
		t.Fatalf("committed context = %#v; want colima", model.launch)
	}
}

func TestDockerContextEditorAllowsManualEntryWhenListFails(t *testing.T) {
	stubWindows(t, false)
	original := listDockerContexts
	listDockerContexts = func() ([]string, error) { return nil, errors.New("daemon unavailable") }
	t.Cleanup(func() { listDockerContexts = original })

	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
		Docker:      container.RunConfig{Runtime: container.DockerRuntime{}},
	})
	model.section = model.containerIndex()
	model.cursor = len(model.stackIDs) + len(model.containerResourceRows()) - 1
	model.startContainerResourceInput()
	if !strings.Contains(model.status, "contexts unavailable") {
		t.Fatalf("status = %q; want context-list diagnostic", model.status)
	}
	model.textInputValue = "remote-builder"
	model.commitTextInput()
	if model.textInputActive || model.launch.ContainerContext != "remote-builder" {
		t.Fatalf("manual context entry was not committed: %#v", model.launch)
	}
}

func TestContainerRuntimeEditorListsAndSelectsPodman(t *testing.T) {
	stubWindows(t, false)
	originalStatuses := listContainerRuntimeStatuses
	listContainerRuntimeStatuses = func() []container.RuntimeStatus {
		return []container.RuntimeStatus{
			{Name: "docker", Available: true},
			{Name: "podman", Available: true},
			{Name: "nerdctl", Available: false},
		}
	}
	t.Cleanup(func() { listContainerRuntimeStatuses = originalStatuses })
	originalDetect := detectContainerRuntime
	detectContainerRuntime = func(preference string) (container.Runtime, error) {
		if preference == "podman" {
			return container.PodmanRuntime{}, nil
		}
		return container.DockerRuntime{}, nil
	}
	t.Cleanup(func() { detectContainerRuntime = originalDetect })

	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:        true,
		ContainerRuntime: config.ContainerRuntimeAuto,
		ContainerContext: "colima",
		Permissions:      map[string]bool{},
		Docker: container.RunConfig{
			Runtime: container.DockerRuntime{},
			Context: "colima",
		},
	})
	model.section = model.containerIndex()
	// Resources are memory, CPU, PIDs, ports, network, runtime, context.
	model.cursor = len(model.stackIDs) + 5
	model.startContainerResourceInput()
	if !strings.Contains(model.View(), "Runtimes (↑/↓)") || !strings.Contains(model.View(), "podman ✓") || !strings.Contains(model.View(), "nerdctl (unavailable)") {
		t.Fatalf("runtime editor does not show availability:\n%s", model.View())
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.textInputValue != "podman" {
		t.Fatalf("runtime selection = %q; want podman", model.textInputValue)
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.textInputActive || model.launch.ContainerRuntime != "podman" || model.launch.Docker.Runtime.Name() != "podman" {
		t.Fatalf("committed runtime = %#v; want podman", model.launch)
	}
	if model.launch.ContainerContext != "" || model.launch.Docker.Context != "" {
		t.Fatalf("incompatible Docker context survived Podman selection: %#v", model.launch)
	}
}

func TestContainerRuntimeEditorRejectsUnavailableSelection(t *testing.T) {
	stubWindows(t, false)
	originalStatuses := listContainerRuntimeStatuses
	listContainerRuntimeStatuses = func() []container.RuntimeStatus {
		return []container.RuntimeStatus{{Name: "docker", Available: true}, {Name: "podman", Available: false}, {Name: "nerdctl", Available: false}}
	}
	t.Cleanup(func() { listContainerRuntimeStatuses = originalStatuses })
	originalDetect := detectContainerRuntime
	detectContainerRuntime = func(string) (container.Runtime, error) { return nil, errors.New("podman is not available in PATH") }
	t.Cleanup(func() { detectContainerRuntime = originalDetect })

	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:        true,
		ContainerRuntime: "podman",
		Permissions:      map[string]bool{},
		Docker:           container.RunConfig{Runtime: container.DockerRuntime{}},
	})
	model.section = model.containerIndex()
	model.cursor = len(model.stackIDs) + 5
	model.startContainerResourceInput()
	model.commitTextInput()
	if !model.textInputActive || !strings.Contains(model.status, "podman is not available") {
		t.Fatalf("unavailable runtime commit = active:%v status:%q", model.textInputActive, model.status)
	}
}

func TestContainerResourceInputValidatesAndUpdates(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
	})

	model.cursor = len(model.stackIDs)
	model.startContainerResourceInput()
	if model.textInputKind != string(resourceMemory) || model.textInputValue != "" {
		t.Fatalf("memory input = kind %q value %q", model.textInputKind, model.textInputValue)
	}
	model.textInputValue = "1m"
	model.commitTextInput()
	if !model.textInputActive || !strings.Contains(model.status, "6m") {
		t.Fatalf("invalid memory commit state: active=%v status=%q", model.textInputActive, model.status)
	}
	model.textInputValue = "4g"
	model.commitTextInput()
	if model.textInputActive || model.launch.Docker.MemoryLimit != "4g" {
		t.Fatalf("valid memory commit = active:%v value:%q", model.textInputActive, model.launch.Docker.MemoryLimit)
	}

	model.cursor = len(model.stackIDs) + 3 // Ports
	model.startContainerResourceInput()
	model.textInputValue = "3000:3000,5353:53/udp"
	model.commitTextInput()
	if len(model.launch.Docker.ExposedPorts) != 2 || model.launch.Docker.ExposedPorts[1].Protocol != "udp" {
		t.Fatalf("published ports = %#v", model.launch.Docker.ExposedPorts)
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

// The Container section lists the selected agent's shared config dirs (rw),
// so the mount map is visible: which dirs are shared and that the agent only
// sees its own.
func TestSharedConfigViewListsAgentDirs(t *testing.T) {
	stubWindows(t, false)
	// The fixture paths do not exist on the host; stub the probe so the panel
	// renders them.
	origExists := container.ExistsOnHost
	container.ExistsOnHost = func(string) bool { return true }
	t.Cleanup(func() { container.ExistsOnHost = origExists })
	launch := launcher.LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		HomeDir:     "/home/tester",
		UseDocker:   true,
		UseMemory:   true,
		Permissions: map[string]bool{},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	// containerView writes into the builder; capture it.
	var b strings.Builder
	model.containerView(&b)
	out := b.String()
	for _, want := range []string{"Shared config (rw)", "/home/tester/.claude", "/home/tester/.claude.json", "/home/tester/.claude/projects"} {
		if !strings.Contains(out, want) {
			t.Errorf("containerView missing %q:\n%s", want, out)
		}
	}
}

// No config dirs → the panel says so instead of rendering empty.
func TestSharedConfigViewEmpty(t *testing.T) {
	stubWindows(t, false)
	launch := launcher.LaunchConfig{
		Agent:       config.Agent{Command: "totally-unknown"},
		HomeDir:     "/home/tester",
		UseDocker:   true,
		UseMemory:   true,
		Permissions: map[string]bool{},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	var b strings.Builder
	model.containerView(&b)
	if !strings.Contains(b.String(), "no shared config dirs") {
		t.Fatalf("containerView for unknown agent = %q; want the empty note", b.String())
	}
}

func TestContainerViewShowsActiveRuntime(t *testing.T) {
	launch := launcher.LaunchConfig{
		Agent:            config.Agent{Command: "totally-unknown"},
		UseDocker:        true,
		ContainerRuntime: "podman",
		Docker: container.RunConfig{
			Runtime: container.PodmanRuntime{},
		},
	}
	model := NewModel(config.DefaultGlobal(), launch)
	var b strings.Builder
	model.containerView(&b)
	if got := b.String(); !strings.Contains(got, "Runtime: podman (--container-runtime)") {
		t.Fatalf("containerView = %q; want active Podman runtime", got)
	}
}

func TestLoadContainerProfileRestoresBackendRuntimeAndSelection(t *testing.T) {
	origDetect := detectContainerRuntime
	detectContainerRuntime = func(string) (container.Runtime, error) { return container.PodmanRuntime{}, nil }
	t.Cleanup(func() { detectContainerRuntime = origDetect })
	global := config.DefaultGlobal()
	global.Profiles = map[string]config.Profile{
		"box": {
			Agent: "claude",
			Options: &config.Options{
				Jail:             false,
				Docker:           true,
				ContainerRuntime: "podman",
				Stacks:           []string{"go"},
				ContainerMemory:  "4g",
				ContainerCPUs:    "2.0",
				ContainerPIDs:    512,
				ContainerPorts:   []config.PortMapping{{Host: 3000, Internal: 3000}},
				ContainerNetwork: "bridge",
			},
		},
	}
	model := NewModel(global, launcher.LaunchConfig{UseJail: true, Permissions: map[string]bool{}})
	model.loadProfile("box")
	if !model.launch.UseDocker || model.launch.UseJail {
		t.Fatalf("profile backend = docker:%v jail:%v; want docker only", model.launch.UseDocker, model.launch.UseJail)
	}
	if model.launch.ContainerRuntime != "podman" || model.launch.Docker.Runtime.Name() != "podman" {
		t.Fatalf("profile runtime = %q/%v; want podman", model.launch.ContainerRuntime, model.launch.Docker.Runtime)
	}
	if len(model.launch.Docker.Selection.Stacks) != 1 || model.launch.Docker.Selection.Stacks[0] != "go" {
		t.Fatalf("profile stacks = %#v; want [go]", model.launch.Docker.Selection.Stacks)
	}
	if model.launch.Docker.MemoryLimit != "4g" || model.launch.Docker.CPULimit != "2.0" || model.launch.Docker.PIDsLimit != 512 || len(model.launch.Docker.ExposedPorts) != 1 || model.launch.Docker.NetworkName != "bridge" {
		t.Fatalf("profile resources = %#v", model.launch.Docker)
	}
	if len(model.launch.Docker.Selection.Agents) != 1 || model.launch.Docker.Selection.Agents[0].Command != "claude" {
		t.Fatalf("profile agents = %#v; want claude", model.launch.Docker.Selection.Agents)
	}
}

func TestLoadContainerProfileKeepsStateWhenRuntimeUnavailable(t *testing.T) {
	origDetect := detectContainerRuntime
	detectContainerRuntime = func(string) (container.Runtime, error) { return nil, errors.New("podman missing") }
	t.Cleanup(func() { detectContainerRuntime = origDetect })
	global := config.DefaultGlobal()
	global.Profiles = map[string]config.Profile{
		"box": {Agent: "claude", Options: &config.Options{Docker: true, ContainerRuntime: "podman"}},
	}
	model := NewModel(global, launcher.LaunchConfig{Agent: config.Agent{Command: "codex"}, UseJail: true, Permissions: map[string]bool{}})
	model.loadProfile("box")
	if model.launch.UseDocker || model.launch.Agent.Command != "codex" {
		t.Fatalf("failed profile load changed state: docker=%v agent=%q", model.launch.UseDocker, model.launch.Agent.Command)
	}
	if !strings.Contains(model.status, "Profile runtime unavailable") {
		t.Fatalf("status = %q; want runtime failure", model.status)
	}
}

func TestContainerToggleShowsRuntimeFailure(t *testing.T) {
	origDetect := detectContainerRuntime
	detectContainerRuntime = func(string) (container.Runtime, error) { return nil, errors.New("podman missing") }
	t.Cleanup(func() { detectContainerRuntime = origDetect })
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{UseJail: true, Permissions: map[string]bool{}})
	model = applyKey(t, model, runeKey("4"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if model.launch.UseDocker {
		t.Fatal("container toggle should remain off when runtime detection fails")
	}
	if !strings.Contains(model.status, "Container runtime unavailable") {
		t.Fatalf("status = %q; want runtime failure", model.status)
	}
}

// writeInstallState records a verified install for command under home so
// container.AgentVersion resolves its pinned tag in tests.
func writeInstallState(t *testing.T, home, command, tag string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "ai-launch")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir install-state: %v", err)
	}
	state := `{"installs":{"` + command + `":{"repository":"acme/` + command + `","tag":"` + tag + `","path":"/home/tester/.local/bin/` + command + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "install-state.json"), []byte(state), 0o600); err != nil {
		t.Fatalf("write install-state: %v", err)
	}
}

// catalogAgent returns one agent from the default catalog (e.g. kilo, the
// release-recipe agent these tests exercise).
func catalogAgent(t *testing.T, command string) config.Agent {
	t.Helper()
	for _, agent := range config.DefaultGlobal().Agents {
		if agent.Command == command {
			return agent
		}
	}
	t.Fatalf("agent %q missing from the default catalog", command)
	return config.Agent{}
}

// Switching to a release-recipe agent with docker active must keep a pinned
// version: an empty one fails Selection.Validate ("release installs require a
// pinned version") and the launch becomes unrecoverable in-process.
func TestSelectAgentPinsReleaseVersion(t *testing.T) {
	stubWindows(t, false)
	home := t.TempDir()
	writeInstallState(t, home, "kilo", "1.2.3")
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		HomeDir:     home,
		Permissions: map[string]bool{},
	})
	// Put the release-recipe agent under the cursor directly: kilo is not
	// required to be installed on the test host.
	model.agents = []catalog.AgentStatus{{Agent: catalogAgent(t, "kilo"), Installed: true, Path: "/home/tester/.local/bin/kilo"}}
	model.section = 0
	model.cursor = 1
	model.selectHighlightedAgent()
	if len(model.launch.Docker.Selection.Agents) != 1 {
		t.Fatalf("docker selection agents = %d; want 1", len(model.launch.Docker.Selection.Agents))
	}
	got := model.launch.Docker.Selection.Agents[0]
	if got.Kind != container.InstallRelease {
		t.Fatalf("selection kind = %s; want release (kilo has a release recipe)", got.Kind)
	}
	if got.Version != "1.2.3" {
		t.Fatalf("selection version = %q; want the pinned 1.2.3 from the install-state", got.Version)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("selection does not validate: %v", err)
	}
}

// Without a verified install on the host the version falls back to the same
// stable placeholder the CLI uses, so release installs still validate.
func TestSelectAgentFallsBackToRecipeVersion(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		HomeDir:     t.TempDir(),
		Permissions: map[string]bool{},
	})
	model.agents = []catalog.AgentStatus{{Agent: catalogAgent(t, "kilo"), Installed: true, Path: "/home/tester/.local/bin/kilo"}}
	model.section = 0
	model.cursor = 1
	model.selectHighlightedAgent()
	got := model.launch.Docker.Selection.Agents[0]
	if got.Version != "0.0.0-recipe" {
		t.Fatalf("selection version = %q; want the 0.0.0-recipe fallback", got.Version)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("selection does not validate: %v", err)
	}
}

// Loading a docker profile for a release-recipe agent must pin the version
// too — the profile path used to plan the install with an empty version.
func TestLoadProfilePinsReleaseVersion(t *testing.T) {
	origDetect := detectContainerRuntime
	detectContainerRuntime = func(string) (container.Runtime, error) { return container.DockerRuntime{}, nil }
	t.Cleanup(func() { detectContainerRuntime = origDetect })
	home := t.TempDir()
	writeInstallState(t, home, "kilo", "2.0.0")
	global := config.DefaultGlobal()
	global.Profiles = map[string]config.Profile{
		"box": {Agent: "kilo", Options: &config.Options{Docker: true}},
	}
	model := NewModel(global, launcher.LaunchConfig{HomeDir: home, Permissions: map[string]bool{}})
	model.loadProfile("box")
	if !model.launch.UseDocker {
		t.Fatal("profile did not enable the docker backend")
	}
	if len(model.launch.Docker.Selection.Agents) != 1 {
		t.Fatalf("profile agents = %#v; want kilo", model.launch.Docker.Selection.Agents)
	}
	got := model.launch.Docker.Selection.Agents[0]
	if got.Kind != container.InstallRelease || got.Version != "2.0.0" {
		t.Fatalf("profile install = kind %s version %q; want release 2.0.0", got.Kind, got.Version)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("profile selection does not validate: %v", err)
	}
}
