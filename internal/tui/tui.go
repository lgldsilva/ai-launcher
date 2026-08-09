// Package tui provides the interactive frontend. It deliberately keeps
// state in launcher.LaunchConfig so the CLI and TUI share the same builder.
package tui

import (
	"errors"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"github.com/muesli/termenv"
)

// ErrCancelled is returned when the user quits the TUI without launching.
var ErrCancelled = errors.New("launcher cancelled")

// isWindows reports whether the host is Windows; it is a variable so tests
// can stub platform detection while running on Linux.
var isWindows = func() bool { return runtime.GOOS == config.PlatformWindows }

// detectContainerRuntime is a seam for the option toggle and its tests. The
// real lookup is cheap PATH inspection, while the TUI must still surface an
// unavailable explicit runtime instead of enabling a configuration that can
// never launch.
var detectContainerRuntime = container.DetectRuntime

// listContainerRuntimeStatuses is a seam for the runtime picker. The TUI
// only needs PATH availability while editing; RuntimeInfo remains a launch
// preflight check so a stopped daemon is reported at the point of use.
var listContainerRuntimeStatuses = container.ListRuntimeStatuses

// listDockerContexts is a seam for the context picker. Listing contexts is
// read-only; selecting one only writes the launch configuration and never
// changes Docker's global current context.
var listDockerContexts = container.ListDockerContexts

// goos reports the host platform for permission platform filtering; it is a
// variable so tests can exercise the filter for any target platform.
var goos = func() string { return runtime.GOOS }

// modeReadOnly is the canonical read-only mount label; the "ro" shorthand is
// also accepted (matched case-insensitively).
const (
	modeReadOnly    = config.MountReadOnlyLabel
	modeReadOnlyID  = config.MountReadOnly
	modeReadWriteID = config.MountReadWrite
	pointerEmpty    = "  "
	pointerSelected = "❯ "
	lineBreak       = "\n"
	keyEscape       = "esc"
	keyUp           = "up"
	keyUpLetter     = "k"
	keyDown         = "down"
	keyDownLetter   = "j"
	keyBackspace    = "backspace"
	keyEnter        = "enter"
)

type containerResourceKind string

const (
	resourceMemory  containerResourceKind = "container-memory"
	resourceCPU     containerResourceKind = "container-cpus"
	resourcePIDs    containerResourceKind = "container-pids"
	resourcePorts   containerResourceKind = "container-ports"
	resourceNetwork containerResourceKind = "container-network"
	resourceRuntime containerResourceKind = "container-runtime"
	resourceContext containerResourceKind = "container-context"
)

type containerResourceRow struct {
	name  string
	kind  containerResourceKind
	value string
	hint  string
}

type advancedOptionKind string

const (
	advancedNewWorkstream advancedOptionKind = "new-workstream-name"
	advancedWorkstream    advancedOptionKind = "workstream"
	advancedWorkspace     advancedOptionKind = "workspace"
	advancedProject       advancedOptionKind = "project"
	advancedExtraArgs     advancedOptionKind = "extra-args"
)

type advancedOptionRow struct {
	name  string
	kind  advancedOptionKind
	value string
	hint  string
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#b4befe"))
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))
	goodStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
	badStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8"))
)

// Hooks groups the optional persistence callbacks invoked by the TUI.
type Hooks struct {
	Save          func(launcher.LaunchConfig) error
	SaveProfile   func(name string, launch launcher.LaunchConfig) error
	ReviewCompose func(launcher.LaunchConfig) (*ComposeUpdateReview, error)
}

// ComposeUpdateChoice is the explicit decision made when the generated
// Compose document differs from the project-local document.
type ComposeUpdateChoice uint8

const (
	// KeepCompose preserves the existing project-local Compose file.
	KeepCompose ComposeUpdateChoice = iota + 1
	// ReplaceCompose replaces the project-local file with the generated one.
	ReplaceCompose
)

// ComposeUpdateReview is rendered inside the TUI before a Compose launch.
// Choose is called only after the operator selects Keep or Replace; it does
// not write the file itself, so the command flow remains the single writer.
type ComposeUpdateReview struct {
	Diff   string
	Choose func(ComposeUpdateChoice) error
}

// Model is the bubbletea model for the interactive launcher. All durable
// state lives in launch so the CLI and TUI share the same builder.
type Model struct {
	catalog catalog.Catalog
	launch  launcher.LaunchConfig
	hooks   Hooks
	// autoMounts are the home dotfile symlink targets escaping $HOME that the
	// jail needs mapped (ARCHITECTURE invariant 9). They are kept apart from
	// launch.Mounts because a profile replaces that list wholesale and they
	// have to survive it — see reapplyAutoMounts.
	autoMounts              []config.Mount
	agents                  []catalog.AgentStatus
	recentTop               string // command of the most-recently-used agent, if any
	permissionIDs           []string
	stackIDs                []string
	serviceIDs              []string
	profiles                map[string]config.Profile
	profileNames            []string
	section                 int
	cursor                  int
	mountInput              string
	mountMode               string
	mountDir                string
	mountEntries            []string
	mountCursor             int
	mountFilter             string // basename prefix used to filter browser rows
	mountTyped              bool
	inputActive             bool
	textInputActive         bool
	textInputKind           string
	textInputValue          string
	composeReview           *ComposeUpdateReview
	composeReviewOffset     int
	composeReviewAccepted   bool
	containerContexts       []string
	containerContextsLoaded bool
	contextCursor           int
	containerRuntimes       []container.RuntimeStatus
	containerRuntimesLoaded bool
	runtimeCursor           int
	paramTarget             config.Param
	helpOpen                bool
	width                   int
	height                  int
	status                  string
	result                  []string
	cancelled               bool
	opts                    Options
}

// NewModel builds the initial TUI model from the global catalog and the
// launch configuration derived from flags and the local config.
func NewModel(global config.Global, launch launcher.LaunchConfig) Model {
	c := catalog.New(global)
	if launch.Permissions == nil {
		launch.Permissions = make(map[string]bool)
	}
	recentTop := ""
	if len(global.RecentAgents) > 0 {
		recentTop = global.RecentAgents[0]
	}
	// Detected once, here, and never inside the event loop: this is a scan of
	// $HOME plus one EvalSymlinks per dotfile. The refused entries are dropped
	// because the launcher already reported them on stderr before the TUI
	// opened; re-listing them would only repeat warnings the operator has seen.
	autoMounts, _ := launcher.HomeSymlinkMounts(launch.HomeDir)
	model := Model{
		catalog:       c,
		launch:        launch,
		autoMounts:    autoMounts,
		agents:        visibleAgents(c.Agents(), global.RecentAgents, launch),
		recentTop:     recentTop,
		permissionIDs: make([]string, 0, len(global.Permissions)),
		stackIDs:      containerStackIDs(),
		serviceIDs:    containerServiceIDs(),
		profiles:      global.Profiles,
		profileNames:  config.ProfileNames(global),
		mountMode:     modeReadWriteID,
	}
	// ai-jail has no Windows build: hide the jail permission and everything
	// that requires it there. On macOS (including /Volumes) the jail stays
	// available — sandbox-exec is a supported backend. Permissions whose
	// Platforms list excludes this host (for example systemd-user on macOS)
	// are hidden as well.
	jailDependent := config.JailDependentIDs(global.Permissions)
	for _, permission := range global.Permissions {
		if isWindows() && jailDependent[permission.ID] {
			continue
		}
		if !config.PermissionSupportedOn(permission, goos()) {
			continue
		}
		model.permissionIDs = append(model.permissionIDs, permission.ID)
	}
	if isWindows() {
		model.launch.UseJail = false
	}
	// Put the cursor on the already-selected agent (not on "Continue"), so the
	// first Enter runs what the user is looking at instead of silently switching
	// to "Continue last session".
	model.cursor = cursorForLaunch(model.agents, model.launch)
	model.syncMemoryForAgent()
	model.syncYoloForAgent()
	model.status = model.sectionHint()
	return model
}

// Options holds display preferences and reload paths for the TUI.
type Options struct {
	NoColor       bool
	HighContrast  bool
	GlobalPath    string
	LocalPath     string
	NoLocalConfig bool
}

// Run starts the interactive TUI and returns the confirmed launch
// configuration, or ErrCancelled when the user quits.
func Run(global config.Global, launch launcher.LaunchConfig) (launcher.LaunchConfig, error) {
	return RunWithHooks(global, launch, Hooks{})
}

// RunWithHooks is Run with optional save hooks invoked on Ctrl+S (local
// config) and Ctrl+P (named profile in the global config).
func RunWithHooks(global config.Global, launch launcher.LaunchConfig, hooks Hooks) (launcher.LaunchConfig, error) {
	return RunWithMessage(global, launch, hooks, "", Options{})
}

// RunWithMessage is RunWithHooks with an initial status line. It is used to
// surface a launch failure when the TUI is re-opened for recovery: the
// operator sees why the last run failed and can adjust (toggle New
// workstream / ai-memory off) and retry with r, or quit, without restarting
// ai-launcher. An empty message keeps the default section hint.
func RunWithMessage(global config.Global, launch launcher.LaunchConfig, hooks Hooks, message string, opts Options) (launcher.LaunchConfig, error) {
	model := NewModel(global, launch)
	model.hooks = hooks
	model.opts = opts
	if msg := strings.TrimSpace(message); msg != "" {
		model.status = msg
	}
	applyDisplayOptions(&model)
	final, err := newProgram(model).Run()
	if err != nil {
		return launch, err
	}
	model, ok := final.(Model)
	if !ok || model.cancelled || len(model.result) == 0 {
		return launch, ErrCancelled
	}
	return model.launch, nil
}

// newProgram builds the bubbletea program for the TUI.
//
// The alternate screen buffer is mandatory: View() renders the whole screen
// (agents, permissions, mounts, options, profiles and a live command preview)
// and its line count changes as the user toggles options or adds mounts. In
// the default (inline) mode bubbletea repaints in place by moving the cursor
// up by the previous frame's line count; when the new frame has a different
// number of lines the repaint desyncs and the layout drifts down the screen
// — the symptom seen on short Linux terminals. The alt screen gives a
// fixed-size canvas with a full repaint every frame, so neither the variable
// line count nor a view taller than the terminal scrolls the layout.
func newProgram(model tea.Model) *tea.Program {
	return tea.NewProgram(model, tea.WithAltScreen())
}

// applyDisplayOptions configures lipgloss according to the user's preferences.
func applyDisplayOptions(m *Model) {
	if m.opts.NoColor {
		lipgloss.SetColorProfile(termenv.Ascii)
		return
	}
	if m.opts.HighContrast {
		// Force a small, high-contrast ANSI palette.
		lipgloss.SetColorProfile(termenv.ANSI256)
	}
}

// Init implements tea.Model; the TUI has no startup command.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model, handling window size and key events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = value.Width, value.Height
	case tea.KeyMsg:
		if m.helpOpen {
			m.helpOpen = false
			return m, nil
		}
		if m.composeReview != nil {
			return m.handleComposeReviewKey(value)
		}
		if m.inputActive {
			return m.updateMountInput(value)
		}
		if m.textInputActive {
			return m.updateTextInput(value)
		}
		return m.handleMainKey(value)
	}
	return m, nil
}

// handleMainKey dispatches key presses when no text input is active.
func (m Model) handleMainKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c", "q", keyEscape:
		m.cancelled = true
		return m, tea.Quit
	case "tab", "ctrl+j":
		m.cycleSection(1)
	case "shift+tab", "ctrl+k":
		m.cycleSection(-1)
	case "1", "2", "3", "4", "5", "6", "7":
		m.jumpToSection(key.String())
	case keyUp, keyUpLetter:
		m.moveCursor(-1)
	case keyDown, keyDownLetter:
		m.moveCursor(1)
	case "space", " ":
		m.handleSpaceKey()
	case "/", "a", "+":
		m.handleAddMountKey()
	case keyBackspace:
		m.removeMount()
	case "d", "ctrl+d":
		m.confirmRun(true)
	case "r", "ctrl+enter":
		// Explicit RUN — never bound to plain Enter on Agent (that only selects).
		if m.confirmRun(false) {
			return m, tea.Quit
		}
	case "?":
		m.helpOpen = true
	case "ctrl+s":
		m.saveLocal()
	case "ctrl+p":
		m.startProfileInput()
	case "ctrl+l":
		return m.reloadConfig()
	case keyEnter:
		m.handleEnterKey()
	}
	return m, nil
}

// cycleSection moves to the next (delta 1) or previous (delta -1) section,
// wrapping around, and resets the cursor and the footer hint.
func (m *Model) cycleSection(delta int) {
	count := m.sectionCount()
	m.section = (m.section + delta + count) % count
	m.cursor = 0
	m.status = m.sectionHint()
}

// jumpToSection switches directly to a numbered section when it exists.
func (m *Model) jumpToSection(key string) {
	if section := int(key[0] - '1'); section < m.sectionCount() {
		m.section = section
		m.cursor = 0
		m.status = m.sectionHint()
	}
}

// handleSpaceKey selects the highlighted agent in the Agent section and
// toggles the current row everywhere else.
func (m *Model) handleSpaceKey() {
	if m.section == 0 {
		m.selectHighlightedAgent()
	} else {
		m.toggleCurrent()
	}
}

// handleAddMountKey opens the mount browser from the Mounts section; a / +
// are obvious "add" keys and / is the historical shortcut.
func (m *Model) handleAddMountKey() {
	if m.section == 2 {
		m.startMountBrowser()
	}
}

// handleEnterKey confirms the local action of the active section; Enter never
// launches from the Agent section (that is what r is for):
//   - Agent: SELECT only (stay open)
//   - Options: edit advanced launch fields or catalog params
//   - Profiles: load
//   - elsewhere: no-op (use r to run)
func (m *Model) handleEnterKey() {
	container := m.containerIndex()
	services := m.servicesIndex()
	profiles := m.profilesIndex()
	switch m.section {
	case 0:
		m.selectHighlightedAgent()
	case 3:
		fixed := len(m.optionRows())
		advanced := len(m.advancedOptionRows())
		if m.cursor >= fixed && m.cursor < fixed+advanced {
			m.startAdvancedOptionInput()
		} else if m.cursor >= fixed+advanced {
			m.startParamInput()
		}
	case container:
		if m.cursor < len(m.stackIDs) {
			m.toggleStack()
		} else {
			m.startContainerResourceInput()
		}
	case services:
		m.startServicePortInput()
	case profiles:
		if m.cursor < len(m.profileNames) {
			m.loadProfile(m.profileNames[m.cursor])
		}
	}
}

// containerIndex returns the layout index of the Container section, or -1
// when the docker backend is off. It is 4 when present, after Options (3).
func (m Model) containerIndex() int {
	if !m.launch.UseDocker {
		return -1
	}
	return 4
}

// servicesIndex returns the layout index of the Services section, or -1 when
// the docker backend is off. Services follow Container and precede Profiles.
func (m Model) servicesIndex() int {
	if !m.launch.UseDocker {
		return -1
	}
	return 5
}

// profilesIndex returns the layout index of the Profiles section, or -1 when
// no profile exists. It shifts from 4 to 6 when Docker sections are present.
func (m Model) profilesIndex() int {
	if len(m.profileNames) == 0 {
		return -1
	}
	if m.launch.UseDocker {
		return 6
	}
	return 4
}

// sectionHint returns the always-visible footer for the active section so the
// user does not have to guess which keys apply.
func (m Model) sectionHint() string {
	if m.textInputActive {
		return "Editing value · Enter apply · Esc cancel · r RUN after closing the editor"
	}
	base := "[r] RUN · [?] help · [q] quit"
	container := m.containerIndex()
	services := m.servicesIndex()
	profiles := m.profilesIndex()
	switch m.section {
	case 0:
		return "Agent · ↑/↓ · Space/Enter SELECT · " + base
	case 1:
		return "Permissions · Space on/off · Tab next · " + base
	case 2:
		return "Mounts · a/+// ADD folder · Space ro/rw · Backspace remove · " + base
	case 3:
		return "Options · Space toggle (Container docker included) · Enter edit scope/workstream/args/params · " + base
	default:
		if m.section == container {
			return "Container · Space toggle stacks · Enter edit resources/ports · Tab next · " + base
		}
		if m.section == services {
			return "Services · Space add/remove · Enter edit ports · Tab next · " + base
		}
		if m.section == profiles {
			return "Profiles · Enter/Space load · " + base
		}
		return base
	}
}

// sectionCount reports how many sections the layout currently has: the
// container section exists only while the docker backend is active, and the
// profiles section only when the global config saves at least one.
func (m Model) sectionCount() int {
	count := 4
	if m.launch.UseDocker {
		count += 2
	}
	if len(m.profileNames) > 0 {
		count++
	}
	return count
}

// saveLocal persists the current selection to .ai-launcher/config.yaml via the hook.
func (m *Model) saveLocal() {
	if m.hooks.Save == nil {
		m.status = "Saving is not available in this mode"
	} else if err := m.hooks.Save(m.launch); err != nil {
		m.status = "Save failed: " + err.Error()
	} else {
		m.status = "Selection saved to .ai-launcher/config.yaml"
	}
}

// startProfileInput opens the text input that names a new saved profile.
func (m *Model) startProfileInput() {
	if m.hooks.SaveProfile == nil {
		m.status = "Saving profiles is not available in this mode"
		return
	}
	m.textInputKind = "profile"
	m.textInputValue = ""
	m.textInputActive = true
	m.status = "Profile name · Enter saves · Esc cancels"
}

// optionRow is one fixed toggle row in the options section; catalog-declared
// param rows follow them.
type optionRow struct {
	name   string
	on     bool
	toggle func(m *Model)
}

// optionRows returns the fixed option toggles in display order. The jail
// toggle is hidden on Windows, where ai-jail is unavailable, and --fresh is
// hidden with the memory layer off, where it would toggle a no-op.
func (m *Model) optionRows() []optionRow {
	rows := make([]optionRow, 0, 4)
	if !isWindows() {
		rows = append(rows, optionRow{name: "Jail / Sandbox", on: m.launch.UseJail && !m.launch.UseDocker, toggle: func(m *Model) { m.launch.UseJail = !m.launch.UseJail }})
	}
	rows = append(rows,
		optionRow{name: "Container", on: m.launch.UseDocker, toggle: func(m *Model) {
			// Tri-state backend: the container replaces the jail (design
			// decision 25), so enabling docker disables the jail and vice
			// versa — never both. Disabling docker restores the jail default.
			if m.launch.UseDocker {
				m.launch.UseDocker = false
				m.launch.UseJail = true
			} else {
				if m.launch.Docker.Runtime == nil {
					runtime, err := detectContainerRuntime(config.EffectiveContainerRuntime(m.launch.ContainerRuntime))
					if err != nil {
						m.status = "Container runtime unavailable: " + err.Error()
						return
					}
					m.launch.Docker.Runtime = runtime
				}
				m.launch.UseDocker = true
				m.launch.UseJail = false
				m.launch = ensureDockerWorkspace(m.launch)
			}
		}},
		optionRow{name: config.AIMemoryCommand, on: m.launch.UseMemory, toggle: func(m *Model) { m.launch.UseMemory = !m.launch.UseMemory }},
		optionRow{name: "New workstream", on: m.launch.NewWorkstream != "", toggle: toggleWorkstreamOption},
	)
	if m.launch.UseDocker {
		rows = append(rows, optionRow{name: "Container tmux", on: m.launch.ContainerTmux.Enabled, toggle: func(m *Model) {
			m.launch.ContainerTmux.Enabled = !m.launch.ContainerTmux.Enabled
			m.launch.Docker.Tmux = m.launch.ContainerTmux.Enabled
			if m.launch.ContainerTmux.Enabled {
				m.status = "tmux enabled: host config will be mounted read-only"
			} else {
				m.status = "tmux disabled"
			}
		}})
	}
	if m.launch.Agent.SupportsYolo {
		rows = append(rows, optionRow{name: "--yolo", on: m.launch.Yolo, toggle: func(m *Model) { m.launch.Yolo = !m.launch.Yolo }})
	}
	if m.launch.UseMemory {
		rows = append(rows, optionRow{name: "--fresh", on: m.launch.Fresh, toggle: func(m *Model) { m.launch.Fresh = !m.launch.Fresh }})
	}
	return rows
}

// toggleWorkstreamOption flips the new-workstream toggle, seeding a default
// name when enabling it.
func toggleWorkstreamOption(m *Model) {
	if m.launch.NewWorkstream == "" {
		m.launch.NewWorkstream = "new-workstream"
	} else {
		m.launch.NewWorkstream = ""
	}
}

// advancedOptionRows exposes the non-boolean launch inputs that used to be
// available only through flags or YAML. Keeping them as explicit rows makes
// the TUI the single place where an operator can inspect and edit the complete
// memory scope and native argument configuration.
func (m *Model) advancedOptionRows() []advancedOptionRow {
	return []advancedOptionRow{
		{name: "New workstream name", kind: advancedNewWorkstream, value: m.launch.NewWorkstream, hint: "empty disables the toggle"},
		{name: "Resume workstream", kind: advancedWorkstream, value: m.launch.Workstream, hint: "name passed to ai-memory"},
		{name: "Memory workspace", kind: advancedWorkspace, value: m.launch.Workspace, hint: "ai-memory workspace scope"},
		{name: "Memory project", kind: advancedProject, value: m.launch.Project, hint: "ai-memory project scope"},
		{name: "Extra agent args", kind: advancedExtraArgs, value: formatExtraArgs(m.launch.ExtraArgs), hint: "shell-style; quotes supported"},
	}
}

func (m *Model) advancedOption(kind advancedOptionKind) (advancedOptionRow, bool) {
	for _, row := range m.advancedOptionRows() {
		if row.kind == kind {
			return row, true
		}
	}
	return advancedOptionRow{}, false
}

func (m Model) advancedOptionKindActive() bool {
	switch advancedOptionKind(m.textInputKind) {
	case advancedNewWorkstream, advancedWorkstream, advancedWorkspace, advancedProject, advancedExtraArgs:
		return true
	default:
		return false
	}
}

// formatExtraArgs produces an editable representation that round-trips
// through launcher.SplitArgs while keeping ordinary flags compact.
func formatExtraArgs(args []string) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
			return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-", r)
		}) < 0 {
			parts[i] = arg
			continue
		}
		parts[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return strings.Join(parts, " ")
}

// View implements tea.Model and renders the whole screen. When the terminal
// is shorter than the complete Docker layout, it switches to the active
// section instead of letting the alternate screen clip the top and footer.

func (m *Model) moveCursor(delta int) {
	limit := len(m.agents) + 1 // +1 for the "continue last session" row
	container := m.containerIndex()
	services := m.servicesIndex()
	profiles := m.profilesIndex()
	switch m.section {
	case 1:
		limit = len(m.permissionIDs)
	case 2:
		limit = len(m.launch.Mounts)
	case 3:
		limit = len(m.optionRows()) + len(m.advancedOptionRows()) + len(m.launch.Agent.Params)
	case container:
		limit = len(m.stackIDs) + len(m.containerResourceRows())
	case services:
		limit = len(m.serviceIDs)
	case profiles:
		limit = len(m.profileNames)
	}
	if limit == 0 {
		return
	}
	m.cursor = (m.cursor + delta + limit) % limit
}

func (m *Model) togglePermission() {
	if m.section != 1 || m.cursor >= len(m.permissionIDs) {
		return
	}
	id := m.permissionIDs[m.cursor]
	permission, ok := m.catalog.Permission(id)
	if !ok || permission.Locked {
		return
	}
	m.launch.Permissions[id] = !m.launch.Permissions[id]
	m.launch.Permissions = m.catalog.NormalizePermissions(m.launch.Permissions)
	m.status = "Dependencies normalized automatically"
}

func (m *Model) toggleCurrent() {
	container := m.containerIndex()
	services := m.servicesIndex()
	profiles := m.profilesIndex()
	switch m.section {
	case 1:
		m.togglePermission()
	case 2:
		if m.cursor < len(m.launch.Mounts) {
			if strings.EqualFold(m.launch.Mounts[m.cursor].Mode, modeReadOnlyID) || strings.EqualFold(m.launch.Mounts[m.cursor].Mode, modeReadOnly) {
				m.launch.Mounts[m.cursor].Mode = modeReadWriteID
			} else {
				m.launch.Mounts[m.cursor].Mode = modeReadOnlyID
			}
			m.status = "Mount mode toggled"
		}
	case 3:
		m.toggleOption()
	case container:
		if m.cursor < len(m.stackIDs) {
			m.toggleStack()
		} else {
			m.startContainerResourceInput()
		}
	case services:
		m.toggleService()
	case profiles:
		if m.cursor < len(m.profileNames) {
			m.loadProfile(m.profileNames[m.cursor])
		}
	}
}

// toggleStack adds or removes the stack under the cursor from the docker
// image selection. The selection is re-normalized so the saved stack list
// stays canonical (sorted, deduplicated) for the image tag.
func (m *Model) toggleStack() {
	if m.section != m.containerIndex() || m.cursor >= len(m.stackIDs) {
		return
	}
	id := m.stackIDs[m.cursor]
	selected := m.selectedStacks()
	if selected[id] {
		delete(selected, id)
	} else {
		selected[id] = true
	}
	ids := make([]string, 0, len(selected))
	for stackID := range selected {
		ids = append(ids, stackID)
	}
	normalized, err := container.ValidStackIDs(ids)
	if err != nil {
		m.status = "Stack selection invalid: " + err.Error()
		return
	}
	m.launch.Docker.Selection.Stacks = normalized
	m.status = "Stack toggled"
}

// toggleService adds or removes the service under the cursor and keeps the
// persisted selection canonical for later Compose generation.
func (m *Model) toggleService() {
	if m.section != m.servicesIndex() || m.cursor >= len(m.serviceIDs) {
		return
	}
	id := m.serviceIDs[m.cursor]
	selected := m.selectedServices()
	if selected[id] {
		delete(selected, id)
	} else {
		selected[id] = true
	}
	ids := make([]string, 0, len(selected))
	for serviceID := range selected {
		ids = append(ids, serviceID)
	}
	normalized, err := container.ValidServiceIDs(ids)
	if err != nil {
		m.status = "Service selection invalid: " + err.Error()
		return
	}
	m.launch.Services = normalized
	m.status = "Service toggled"
}

func (m *Model) startServicePortInput() {
	if m.section != m.servicesIndex() || m.cursor >= len(m.serviceIDs) {
		return
	}
	id := m.serviceIDs[m.cursor]
	service, ok := container.ServiceByID(id)
	if !ok {
		return
	}
	m.textInputKind = "service-ports:" + id
	m.textInputValue = servicePortInputValue(service, m.launch.ContainerServicePorts)
	m.textInputActive = true
	m.status = "Editing " + service.Name + " ports · " + servicePortEditHint() + " · Enter confirms · Esc cancels"
}

// toggleOption flips one of the fixed option toggles, or opens the text input
// when the cursor is on an advanced launch field or catalog-declared param.
func (m *Model) toggleOption() {
	rows := m.optionRows()
	advanced := m.advancedOptionRows()
	if m.cursor >= len(rows) && m.cursor < len(rows)+len(advanced) {
		m.startAdvancedOptionInput()
		return
	}
	if m.cursor >= len(rows)+len(advanced) {
		m.startParamInput()
		return
	}
	previousStatus := m.status
	rows[m.cursor].toggle(m)
	// The container toggle can refuse activation when its selected runtime is
	// unavailable. Preserve that diagnostic instead of replacing it with the
	// generic success message below.
	if m.status != previousStatus && strings.HasPrefix(m.status, "Container runtime unavailable:") {
		return
	}
	// Turning the jail on here reaches a state the launcher never prepared:
	// applyJailAutoDetection only runs when the jail was already on at startup,
	// so a --no-jail launch that the operator re-enables would otherwise emit an
	// argv without the auto-mounts invariant 9 requires.
	m.reapplyAutoMounts()
	m.status = "Option toggled"
}

// startParamInput opens the text input for the param row under the cursor.
func (m *Model) startParamInput() {
	index := m.cursor - len(m.optionRows()) - len(m.advancedOptionRows())
	params := m.launch.Agent.Params
	if index < 0 || index >= len(params) {
		return
	}
	m.paramTarget = params[index]
	m.textInputKind = "param"
	m.textInputValue = m.launch.ParamValues[m.paramTarget.Name]
	m.textInputActive = true
	m.status = "Editing " + m.paramTarget.Name + " (" + m.paramTarget.Flag + ") · Enter confirms · Esc cancels"
}

// startAdvancedOptionInput opens the editor for a scope, workstream, or
// native-agent-argument field under the cursor.
func (m *Model) startAdvancedOptionInput() {
	index := m.cursor - len(m.optionRows())
	rows := m.advancedOptionRows()
	if index < 0 || index >= len(rows) {
		return
	}
	row := rows[index]
	m.textInputKind = string(row.kind)
	m.textInputValue = row.value
	m.textInputActive = true
	m.status = "Editing " + row.name + " · Enter confirms · Esc cancels"
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func copyStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func copyServicePortMappings(values map[string][]config.PortMapping) map[string][]config.PortMapping {
	if values == nil {
		return nil
	}
	clone := make(map[string][]config.PortMapping, len(values))
	for serviceID, mappings := range values {
		clone[serviceID] = append([]config.PortMapping(nil), mappings...)
	}
	return clone
}
