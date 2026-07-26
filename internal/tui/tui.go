// Package tui provides the interactive frontend. It deliberately keeps
// state in launcher.LaunchConfig so the CLI and TUI share the same builder.
package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// ErrCancelled is returned when the user quits the TUI without launching.
var ErrCancelled = errors.New("launcher cancelled")

// isWindows reports whether the host is Windows; it is a variable so tests
// can stub platform detection while running on Linux.
var isWindows = func() bool { return runtime.GOOS == "windows" }

// goos reports the host platform for permission platform filtering; it is a
// variable so tests can exercise the filter for any target platform.
var goos = func() string { return runtime.GOOS }

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#b4befe"))
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))
	goodStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
	badStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8"))
)

// Hooks groups the optional persistence callbacks invoked by the TUI.
type Hooks struct {
	Save        func(launcher.LaunchConfig) error
	SaveProfile func(name string, launch launcher.LaunchConfig) error
}

// Model is the bubbletea model for the interactive launcher. All durable
// state lives in launch so the CLI and TUI share the same builder.
type Model struct {
	catalog         catalog.Catalog
	launch          launcher.LaunchConfig
	hooks           Hooks
	agents          []catalog.AgentStatus
	recentTop       string // command of the most-recently-used agent, if any
	permissionIDs   []string
	profiles        map[string]config.Profile
	profileNames    []string
	section         int
	cursor          int
	mountInput      string
	mountMode       string
	mountDir        string
	mountEntries    []string
	mountCursor     int
	mountFilter     string // basename prefix used to filter browser rows
	mountTyped      bool
	inputActive     bool
	textInputActive bool
	textInputKind   string
	textInputValue  string
	paramTarget     config.Param
	helpOpen        bool
	width           int
	height          int
	status          string
	result          []string
	cancelled       bool
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
	model := Model{
		catalog:       c,
		launch:        launch,
		agents:        visibleAgents(c.Agents(), global.RecentAgents, launch),
		recentTop:     recentTop,
		permissionIDs: make([]string, 0, len(global.Permissions)),
		profiles:      global.Profiles,
		profileNames:  config.ProfileNames(global),
		mountMode:     "rw",
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
	model.status = model.sectionHint()
	return model
}

// Run starts the interactive TUI and returns the confirmed launch
// configuration, or ErrCancelled when the user quits.
func Run(global config.Global, launch launcher.LaunchConfig) (launcher.LaunchConfig, error) {
	return RunWithHooks(global, launch, Hooks{})
}

// RunWithHooks is Run with optional save hooks invoked on Ctrl+S (local
// config) and Ctrl+P (named profile in the global config).
func RunWithHooks(global config.Global, launch launcher.LaunchConfig, hooks Hooks) (launcher.LaunchConfig, error) {
	model := NewModel(global, launch)
	model.hooks = hooks
	final, err := tea.NewProgram(model).Run()
	if err != nil {
		return launch, err
	}
	model, ok := final.(Model)
	if !ok || model.cancelled || len(model.result) == 0 {
		return launch, ErrCancelled
	}
	return model.launch, nil
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
	case "ctrl+c", "q", "esc":
		m.cancelled = true
		return m, tea.Quit
	case "tab", "ctrl+j":
		m.section = (m.section + 1) % m.sectionCount()
		m.cursor = 0
		m.status = m.sectionHint()
	case "shift+tab", "ctrl+k":
		m.section = (m.section + m.sectionCount() - 1) % m.sectionCount()
		m.cursor = 0
		m.status = m.sectionHint()
	case "1", "2", "3", "4", "5":
		if section := int(key.String()[0] - '1'); section < m.sectionCount() {
			m.section = section
			m.cursor = 0
			m.status = m.sectionHint()
		}
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "space", " ":
		if m.section == 0 {
			m.selectHighlightedAgent()
		} else {
			m.toggleCurrent()
		}
	case "/", "a", "+":
		// a / + are obvious "add" keys; / is the historical shortcut.
		if m.section == 2 {
			m.startMountBrowser()
		}
	case "backspace":
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
	case "enter":
		// Enter = confirm local action, never an accidental launch from Agent.
		//  - Agent: SELECT only (stay open)
		//  - Options param: edit
		//  - Profiles: load
		//  - elsewhere: no-op (use r to run)
		switch m.section {
		case 0:
			m.selectHighlightedAgent()
		case 3:
			if m.cursor >= len(m.optionRows()) {
				m.startParamInput()
			}
		case 4:
			if m.cursor < len(m.profileNames) {
				m.loadProfile(m.profileNames[m.cursor])
			}
		}
	}
	return m, nil
}

// sectionHint returns the always-visible footer for the active section so the
// user does not have to guess which keys apply.
func (m Model) sectionHint() string {
	base := "[r] RUN · [?] help · [q] quit"
	switch m.section {
	case 0:
		return "Agent · ↑/↓ · Space/Enter SELECT · " + base
	case 1:
		return "Permissions · Space on/off · Tab next · " + base
	case 2:
		return "Mounts · a/+// ADD folder · Space ro/rw · Backspace remove · " + base
	case 3:
		return "Options · Space toggle · Enter edit value · " + base
	case 4:
		return "Profiles · Enter/Space load · " + base
	default:
		return base
	}
}

// sectionCount reports how many sections the layout currently has; the
// profiles section only exists when the global config saves at least one.
func (m Model) sectionCount() int {
	if len(m.profileNames) > 0 {
		return 5
	}
	return 4
}

// saveLocal persists the current selection to .ai-launch.yaml via the hook.
func (m *Model) saveLocal() {
	if m.hooks.Save == nil {
		m.status = "Saving is not available in this mode"
	} else if err := m.hooks.Save(m.launch); err != nil {
		m.status = "Save failed: " + err.Error()
	} else {
		m.status = "Selection saved to .ai-launch.yaml"
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
		rows = append(rows, optionRow{name: "Jail / Sandbox", on: m.launch.UseJail, toggle: func(m *Model) { m.launch.UseJail = !m.launch.UseJail }})
	}
	rows = append(rows,
		optionRow{name: "ai-memory", on: m.launch.UseMemory, toggle: func(m *Model) { m.launch.UseMemory = !m.launch.UseMemory }},
		optionRow{name: "New workstream", on: m.launch.NewWorkstream != "", toggle: toggleWorkstreamOption},
		optionRow{name: "--yolo", on: m.launch.Yolo, toggle: func(m *Model) { m.launch.Yolo = !m.launch.Yolo }},
	)
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

// View implements tea.Model and renders the whole screen.
func (m Model) View() string {
	if m.helpOpen {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("ai-launcher"))
	m.agentsView(&b)
	m.permissionsView(&b)
	m.mountsView(&b)
	m.optionsView(&b)
	m.profilesView(&b)

	if m.textInputActive {
		label := "Profile"
		if m.textInputKind == "param" {
			label = m.paramTarget.Name + " (" + m.paramTarget.Flag + ")"
		}
		fmt.Fprintf(&b, "\n  %s: %s█\n", label, m.textInputValue)
	}
	b.WriteString("\n")
	preview, err := launcher.Build(m.launch)
	if err == nil {
		b.WriteString(mutedStyle.Render("Preview: " + strings.Join(preview, " ")))
		b.WriteString("\n")
	}
	if m.status != "" {
		b.WriteString("\n" + m.status + "\n")
	}
	return b.String()
}

func (m Model) agentsView(b *strings.Builder) {
	b.WriteString("\n\nAgent\n")
	b.WriteString(mutedStyle.Render("  (installed only · most recently used first · Space/Enter select · r RUN)") + "\n")
	// Selected line (what will launch) vs cursor (what ↑/↓ is on).
	selectedLabel := "none"
	if m.launch.ContinueSession {
		selectedLabel = "Continue last session"
	} else if cmd := strings.TrimSpace(m.launch.Agent.Command); cmd != "" {
		selectedLabel = m.launch.Agent.Name
		if selectedLabel == "" {
			selectedLabel = cmd
		}
		selectedLabel += " (" + cmd + ")"
	}
	b.WriteString(goodStyle.Render("  Selected: "+selectedLabel) + "\n")

	pointer := "  "
	if m.section == 0 && m.cursor == 0 {
		pointer = "❯ "
	}
	sel := "[ ]"
	if m.launch.ContinueSession {
		sel = goodStyle.Render("[●]")
	}
	fmt.Fprintf(b, "%s%s %-22s (%s)\n", pointer, sel, "Continue last session", "ai-memory run")
	if len(m.agents) == 0 {
		b.WriteString(badStyle.Render("  (no installed agents found — install one or check PATH)") + "\n")
	}
	for i, status := range m.agents {
		pointer := "  "
		if m.section == 0 && i+1 == m.cursor {
			pointer = "❯ "
		}
		command := status.Agent.Command
		if status.ResolvedCommand != "" && status.ResolvedCommand != status.Agent.Command {
			command += " via " + status.ResolvedCommand
		}
		sel := "[ ]"
		if !m.launch.ContinueSession && agentMatchesLaunch(status, m.launch) {
			sel = goodStyle.Render("[●]")
		}
		tag := mutedStyle.Render("· ready")
		if !status.Installed {
			tag = badStyle.Render("· not installed")
		} else if m.recentTop != "" && status.Agent.Command == m.recentTop {
			tag = goodStyle.Render("· last used")
		}
		fmt.Fprintf(b, "%s%s %-18s (%s) %s\n", pointer, sel, status.Agent.Name, command, tag)
	}
}

// visibleAgents returns the agents shown in the TUI: installed ones only
// (plus the currently selected agent if it is missing, so the selection is
// never invisible), ordered most-recently-used first, then catalog order.
func visibleAgents(all []catalog.AgentStatus, recent []string, launch launcher.LaunchConfig) []catalog.AgentStatus {
	selected := strings.TrimSpace(launch.Agent.Command)
	byCommand := make(map[string]catalog.AgentStatus, len(all))
	for _, status := range all {
		byCommand[status.Agent.Command] = status
	}
	seen := make(map[string]bool, len(all))
	out := make([]catalog.AgentStatus, 0, len(all))
	appendOne := func(status catalog.AgentStatus) {
		cmd := status.Agent.Command
		if seen[cmd] {
			return
		}
		if !status.Installed && cmd != selected {
			return
		}
		seen[cmd] = true
		out = append(out, status)
	}
	for _, cmd := range recent {
		if status, ok := byCommand[cmd]; ok {
			appendOne(status)
		}
	}
	for _, status := range all {
		appendOne(status)
	}
	// Ensure the active selection is present even if it was filtered out.
	if selected != "" && !seen[selected] {
		if status, ok := byCommand[selected]; ok {
			out = append([]catalog.AgentStatus{status}, out...)
		}
	}
	return out
}

func (m Model) permissionsView(b *strings.Builder) {
	b.WriteString("\nPermissions\n")
	for i, id := range m.permissionIDs {
		permission, _ := m.catalog.Permission(id)
		pointer := "  "
		if m.section == 1 && i == m.cursor {
			pointer = "❯ "
		}
		mark := "[ ]"
		if m.launch.Permissions[id] {
			mark = "[✓]"
		}
		if permission.Locked {
			mark = "[◆]"
		}
		fmt.Fprintf(b, "%s%s %-22s\n", pointer, mark, permission.Name)
	}
}

func (m Model) mountsView(b *strings.Builder) {
	b.WriteString("\nMounts\n")
	// Always visible (not only when the section is focused) so the user never
	// has to guess how to add folders.
	if !m.inputActive {
		b.WriteString(titleStyle.Render("  Tips") + "\n")
		b.WriteString(goodStyle.Render("    a  or  +  or  /     add a folder") + "\n")
		b.WriteString(mutedStyle.Render("    Space                toggle read-only ↔ read-write") + "\n")
		b.WriteString(mutedStyle.Render("    Backspace            remove highlighted mount") + "\n")
		b.WriteString(goodStyle.Render("    r                    RUN the selected agent") + "\n")
	}
	if len(m.launch.Mounts) == 0 && !m.inputActive {
		b.WriteString(goodStyle.Render("  (empty — press a to add a folder, or r to run without extra mounts)") + "\n")
	}
	for i, mount := range m.launch.Mounts {
		pointer := "  "
		if m.section == 2 && i == m.cursor && !m.inputActive {
			pointer = "❯ "
		}
		mode := mount.Mode
		if mode == "" {
			mode = "rw"
		}
		modeLabel := "read-write"
		if strings.EqualFold(mode, "ro") || strings.EqualFold(mode, "read-only") {
			modeLabel = "read-only"
		}
		fmt.Fprintf(b, "%s%s  (%s)\n", pointer, mount.Path, modeLabel)
	}
	if m.inputActive {
		m.mountBrowserView(b)
	}
}

// mountBrowserView renders the add-mount panel with a fixed key legend so the
// user never has to guess bindings (status lines are easy to miss / overwrite).
func (m Model) mountBrowserView(b *strings.Builder) {
	modeLabel := "read-write (agent can edit files)"
	if m.mountMode == "ro" {
		modeLabel = "read-only (agent can only read)"
	}
	b.WriteString("\n")
	b.WriteString(titleStyle.Render("  ── Add a folder to the jail ──") + "\n")
	fmt.Fprintf(b, "  Path: %s█\n", m.mountInput)
	fmt.Fprintf(b, "  Mode: %s\n", modeLabel)
	fmt.Fprintf(b, "  Looking in: %s\n", m.mountDir)
	willAdd := m.mountCommitPath()
	if willAdd != "" {
		b.WriteString(goodStyle.Render("  → Enter will add: "+willAdd) + "\n")
	} else {
		b.WriteString(badStyle.Render("  → Enter will add: (nothing selected — type a path or pick a folder)") + "\n")
	}
	b.WriteString(mutedStyle.Render("  Folders:") + "\n")
	if len(m.mountEntries) == 0 {
		b.WriteString("    (no folders match — type more of the path, or Tab to autocomplete)\n")
	} else {
		const maxVisible = 10
		start := 0
		if m.mountCursor >= maxVisible {
			start = m.mountCursor - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(m.mountEntries) {
			end = len(m.mountEntries)
		}
		if start > 0 {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("    … %d above\n", start)))
		}
		for i := start; i < end; i++ {
			pointer := "    "
			if i == m.mountCursor {
				pointer = "  ❯ "
			}
			label := m.mountEntries[i] + "/"
			if m.mountEntries[i] == ".." {
				label = "..  (parent folder)"
			}
			b.WriteString(pointer + label + "\n")
		}
		if end < len(m.mountEntries) {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("    … %d more\n", len(m.mountEntries)-end)))
		}
	}
	b.WriteString(mutedStyle.Render("  Keys:  ↑/↓ move   → open folder   ← parent   Tab autocomplete") + "\n")
	b.WriteString(mutedStyle.Render("         type a path   Ctrl+T mode   Enter ADD   Esc cancel") + "\n")
}

// mountCommitPath is the path Enter would add right now (for the on-screen hint).
func (m Model) mountCommitPath() string {
	input := strings.TrimSpace(m.mountInput)
	if m.shouldAddBrowserSelection(input) {
		return m.selectedMountPath()
	}
	if input == "" {
		return ""
	}
	return filepath.Clean(expandMountPath(input))
}

func (m Model) optionsView(b *strings.Builder) {
	b.WriteString("\nOptions\n")
	for i, option := range m.optionRows() {
		pointer := "  "
		if m.section == 3 && i == m.cursor {
			pointer = "❯ "
		}
		mark := "[ ]"
		if option.on {
			mark = "[✓]"
		}
		fmt.Fprintf(b, "%s%s %s\n", pointer, mark, option.name)
	}
	if m.launch.NewWorkstream != "" {
		b.WriteString("  workstream: " + m.launch.NewWorkstream + "\n")
	}
	for i, param := range m.launch.Agent.Params {
		pointer := "  "
		if m.section == 3 && m.cursor == len(m.optionRows())+i {
			pointer = "❯ "
		}
		value := m.launch.ParamValues[param.Name]
		if value == "" {
			value = mutedStyle.Render("(empty)")
		}
		fmt.Fprintf(b, "%s%s: %s (%s)\n", pointer, param.Name, value, param.Flag)
	}
}

// profilesView renders the saved profiles section, shown only when the
// global config has at least one profile.
func (m Model) profilesView(b *strings.Builder) {
	if len(m.profileNames) == 0 {
		return
	}
	b.WriteString("\nProfiles\n")
	for i, name := range m.profileNames {
		pointer := "  "
		if m.section == 4 && i == m.cursor {
			pointer = "❯ "
		}
		profile := m.profiles[name]
		line := fmt.Sprintf("%s%-18s (%s)", pointer, name, profile.Agent)
		if summary := config.ProfileSummary(profile); summary != "" {
			line += " " + mutedStyle.Render(summary)
		}
		b.WriteString(line + "\n")
	}
}

func (m *Model) moveCursor(delta int) {
	limit := len(m.agents) + 1 // +1 for the "continue last session" row
	switch m.section {
	case 1:
		limit = len(m.permissionIDs)
	case 2:
		limit = len(m.launch.Mounts)
	case 3:
		limit = len(m.optionRows()) + len(m.launch.Agent.Params)
	case 4:
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
	switch m.section {
	case 1:
		m.togglePermission()
	case 2:
		if m.cursor < len(m.launch.Mounts) {
			if strings.EqualFold(m.launch.Mounts[m.cursor].Mode, "ro") || strings.EqualFold(m.launch.Mounts[m.cursor].Mode, "read-only") {
				m.launch.Mounts[m.cursor].Mode = "rw"
			} else {
				m.launch.Mounts[m.cursor].Mode = "ro"
			}
			m.status = "Mount mode toggled"
		}
	case 3:
		m.toggleOption()
	case 4:
		if m.cursor < len(m.profileNames) {
			m.loadProfile(m.profileNames[m.cursor])
		}
	}
}

// toggleOption flips one of the fixed option toggles, or opens the text
// input when the cursor is on a catalog-declared param row.
func (m *Model) toggleOption() {
	rows := m.optionRows()
	if m.cursor >= len(rows) {
		m.startParamInput()
		return
	}
	rows[m.cursor].toggle(m)
	m.status = "Option toggled"
}

// startParamInput opens the text input for the param row under the cursor.
func (m *Model) startParamInput() {
	index := m.cursor - len(m.optionRows())
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

// loadProfile replaces the current selection with a saved profile, keeping
// the local values for every block the profile omits.
func (m *Model) loadProfile(name string) {
	profile, ok := m.profiles[name]
	if !ok {
		return
	}
	if strings.TrimSpace(profile.Agent) != "" {
		status, err := m.catalog.Resolve(profile.Agent)
		agent := status.Agent
		executable := status.Path
		if err != nil {
			agent = config.Agent{Name: profile.Agent, Command: profile.Agent}
			executable = ""
		} else if status.ResolvedCommand != "" {
			agent.Command = status.ResolvedCommand
		}
		m.launch.Agent = agent
		m.launch.Executable = executable
	}
	if profile.Permissions != nil {
		m.launch.Permissions = m.catalog.NormalizePermissions(profile.Permissions)
	}
	if profile.Mounts != nil {
		m.launch.Mounts = append([]config.Mount(nil), profile.Mounts...)
	}
	if profile.Options != nil {
		m.launch.UseJail = profile.Options.Jail && !isWindows()
		m.launch.UseMemory = profile.Options.Memory
		m.launch.Yolo = profile.Options.Yolo
		m.launch.Fresh = profile.Options.Fresh
		m.launch.NewWorkstream = profile.Options.NewWorkstream
		m.launch.Workstream = profile.Options.Workstream
		m.launch.Workspace = profile.Options.Workspace
		m.launch.Project = profile.Options.Project
		m.launch.JailFlags = profile.Options.JailFlags
		m.launch.ExtraArgs = append([]string(nil), profile.Options.ExtraArgs...)
		m.launch.ParamValues = copyParamValues(profile.Options.ParamValues)
	}
	m.status = "Profile loaded: " + name
}

func copyParamValues(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copied := make(map[string]string, len(values))
	for name, value := range values {
		copied[name] = value
	}
	return copied
}

// updateTextInput handles keys while a single-line text input (param value
// or profile name) is active.
func (m *Model) updateTextInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.textInputActive = false
		m.status = "Edit cancelled"
	case "enter":
		m.commitTextInput()
	case "backspace":
		if m.textInputValue != "" {
			runes := []rune(m.textInputValue)
			m.textInputValue = string(runes[:len(runes)-1])
		}
	default:
		if key.Type == tea.KeyRunes {
			m.textInputValue += string(key.Runes)
		}
	}
	return *m, nil
}

// commitTextInput applies the active text input: a param value is stored in
// param_values (empty clears it), a profile name saves the selection via the
// SaveProfile hook.
func (m *Model) commitTextInput() {
	value := strings.TrimSpace(m.textInputValue)
	m.textInputActive = false
	if m.textInputKind == "profile" {
		m.saveProfileAs(value)
		return
	}
	if value == "" {
		delete(m.launch.ParamValues, m.paramTarget.Name)
		m.status = "Parameter cleared: " + m.paramTarget.Name
		return
	}
	if m.launch.ParamValues == nil {
		m.launch.ParamValues = make(map[string]string)
	}
	m.launch.ParamValues[m.paramTarget.Name] = value
	m.status = "Parameter set: " + m.paramTarget.Name
}

// saveProfileAs persists the current selection as a named profile and adds
// it to the profiles section.
func (m *Model) saveProfileAs(name string) {
	if name == "" {
		m.status = "Profile name cannot be empty"
		return
	}
	if err := m.hooks.SaveProfile(name, m.launch); err != nil {
		m.status = "Profile save failed: " + err.Error()
		return
	}
	if m.profiles == nil {
		m.profiles = make(map[string]config.Profile)
	}
	m.profiles[name] = config.Profile{
		Agent:       m.launch.Agent.Command,
		Permissions: m.launch.Permissions,
		Mounts:      m.launch.Mounts,
		Options: &config.Options{
			Jail:          m.launch.UseJail,
			Memory:        m.launch.UseMemory,
			Yolo:          m.launch.Yolo,
			NewWorkstream: m.launch.NewWorkstream,
			Workstream:    m.launch.Workstream,
			Workspace:     m.launch.Workspace,
			Project:       m.launch.Project,
			JailFlags:     m.launch.JailFlags,
			ExtraArgs:     m.launch.ExtraArgs,
			ParamValues:   m.launch.ParamValues,
		},
	}
	if !containsString(m.profileNames, name) {
		m.profileNames = append(m.profileNames, name)
		sort.Strings(m.profileNames)
	}
	m.status = "Profile saved: " + name
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (m *Model) updateMountInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.inputActive = false
		m.status = "Mount addition cancelled"
	case "enter":
		// When the input is empty or only reflects the current browser
		// directory (after →/l navigation), add the highlighted entry.
		// Otherwise add the typed path as-is.
		path := strings.TrimSpace(m.mountInput)
		if m.shouldAddBrowserSelection(path) {
			path = m.selectedMountPath()
		}
		m.addMount(path)
	case "tab":
		// Tab completes directory names (shell-style). Mode toggle is Ctrl+T.
		m.completeMountPath()
	case "ctrl+t":
		if m.mountMode == "ro" {
			m.mountMode = "rw"
		} else {
			m.mountMode = "ro"
		}
		m.status = "Mount mode: " + m.mountMode
	case "right":
		// Arrows always drive the browser, even while a path is being typed.
		m.enterSelectedMountDir()
	case "left":
		m.mountBrowserUp()
	case "up":
		m.moveMountCursor(-1)
	case "down":
		m.moveMountCursor(1)
	case "l", "h", "k", "j":
		// While typing a path, letters (including j/k/l/h) go into the input.
		// When browsing with an empty input they navigate the directory list.
		if m.mountTyped || key.Type == tea.KeyRunes && m.mountInput != "" {
			if key.Type == tea.KeyRunes {
				m.appendMountInput(string(key.Runes))
			}
			break
		}
		switch key.String() {
		case "l":
			m.enterSelectedMountDir()
		case "h":
			m.mountBrowserUp()
		case "k":
			m.moveMountCursor(-1)
		case "j":
			m.moveMountCursor(1)
		}
	case "backspace":
		if m.mountInput != "" {
			runes := []rune(m.mountInput)
			m.mountInput = string(runes[:len(runes)-1])
		}
		if strings.TrimSpace(m.mountInput) == "" {
			m.mountTyped = false
			m.mountFilter = ""
			m.refreshMountEntries()
		} else {
			m.syncMountBrowserFromInput()
		}
	default:
		if key.Type == tea.KeyRunes {
			m.appendMountInput(string(key.Runes))
		}
	}
	return *m, nil
}

func (m *Model) appendMountInput(text string) {
	m.mountInput += text
	m.mountTyped = true
	m.syncMountBrowserFromInput()
}

// shouldAddBrowserSelection reports whether Enter should commit the
// highlighted browser row instead of the raw path input.
func (m Model) shouldAddBrowserSelection(input string) bool {
	if len(m.mountEntries) == 0 {
		return false
	}
	if strings.TrimSpace(input) == "" {
		return true
	}
	cleaned := filepath.Clean(expandMountPath(input))
	return cleaned == filepath.Clean(m.mountDir)
}

func (m *Model) moveMountCursor(delta int) {
	if len(m.mountEntries) == 0 {
		return
	}
	m.mountCursor = (m.mountCursor + delta + len(m.mountEntries)) % len(m.mountEntries)
}

// enterSelectedMountDir opens the highlighted browser row: ".." goes up,
// directories become the new browser root and are written into the path input
// with a trailing separator so further typing continues from there.
func (m *Model) enterSelectedMountDir() {
	if len(m.mountEntries) == 0 {
		return
	}
	name := m.mountEntries[m.mountCursor]
	if name == ".." {
		m.mountBrowserUp()
		return
	}
	path := filepath.Join(m.mountDir, name)
	if !isDirectory(path) {
		m.status = "Not a directory: " + path
		return
	}
	m.mountDir = path
	m.mountInput = pathWithTrailingSep(path)
	m.mountTyped = true
	m.mountFilter = ""
	m.refreshMountEntries()
	m.status = "Directory opened · Tab completes · Enter adds · Ctrl+T toggles ro/rw"
}

func (m *Model) mountBrowserUp() {
	parent := filepath.Dir(m.mountDir)
	if parent == m.mountDir {
		return
	}
	m.mountDir = parent
	if m.mountTyped || m.mountInput != "" {
		m.mountInput = pathWithTrailingSep(parent)
		m.mountTyped = true
	}
	m.mountFilter = ""
	m.refreshMountEntries()
	m.status = "Parent directory opened"
}

func (m *Model) startMountBrowser() {
	m.inputActive = true
	m.mountInput = ""
	m.mountTyped = false
	m.mountFilter = ""
	m.mountMode = "rw"
	m.mountDir = mustWorkingDirectory()
	m.refreshMountEntries()
	m.status = "Pick a folder below (or type a path). Enter adds it. Esc cancels."
}

// syncMountBrowserFromInput re-points the browser at the directory implied by
// the typed path and filters entries by the unfinished basename prefix.
func (m *Model) syncMountBrowserFromInput() {
	dir, prefix := m.inputDirAndPrefix()
	if dir != "" {
		m.mountDir = dir
	}
	m.mountFilter = prefix
	m.refreshMountEntries()
}

// completeMountPath is shell-style Tab completion against directories under the
// path currently being typed (or under the browser root when the input is empty).
func (m *Model) completeMountPath() {
	dir, prefix := m.inputDirAndPrefix()
	matches := directoryMatches(dir, prefix)
	if len(matches) == 0 {
		m.status = "No directory matches for " + quotePrefix(prefix)
		return
	}
	if len(matches) == 1 {
		m.applyMountCompletion(dir, matches[0])
		return
	}
	lcp := longestCommonPrefix(matches)
	// Extend when the shared prefix is longer, or when it only differs by case
	// from what the user typed (macOS/Windows case-insensitive volumes).
	if len(lcp) > len(prefix) || (lcp != prefix && strings.EqualFold(lcp, prefix)) {
		m.mountInput = joinMountInput(dir, lcp)
		m.mountTyped = true
		m.syncMountBrowserFromInput()
		m.status = fmt.Sprintf("%d matches — Tab again to cycle", len(matches))
		return
	}
	// Cycle through matches, putting each full path into the input.
	if len(m.mountEntries) == 0 {
		return
	}
	// Advance past ".." if present so cycling stays on real matches.
	for range len(m.mountEntries) {
		m.mountCursor = (m.mountCursor + 1) % len(m.mountEntries)
		if m.mountEntries[m.mountCursor] != ".." {
			break
		}
	}
	name := m.mountEntries[m.mountCursor]
	if name == ".." {
		return
	}
	full := filepath.Join(dir, name)
	if isDirectory(full) {
		m.mountInput = pathWithTrailingSep(full)
	} else {
		m.mountInput = full
	}
	m.mountTyped = true
	m.syncMountBrowserFromInput()
	m.status = fmt.Sprintf("Match %d/%d: %s", indexOfMatch(matches, name)+1, len(matches), name)
}

func (m *Model) applyMountCompletion(dir, name string) {
	full := filepath.Join(dir, name)
	if isDirectory(full) {
		m.mountDir = full
		m.mountInput = pathWithTrailingSep(full)
		m.mountFilter = ""
		m.mountTyped = true
		m.refreshMountEntries()
		m.status = "Completed: " + full
		return
	}
	m.mountInput = full
	m.mountTyped = true
	m.syncMountBrowserFromInput()
	m.status = "Completed: " + full
}

// inputDirAndPrefix splits the typed path into the directory to list and the
// unfinished basename used as a completion/filter prefix.
func (m Model) inputDirAndPrefix() (dir, prefix string) {
	raw := m.mountInput
	if strings.TrimSpace(raw) == "" {
		return m.mountDir, ""
	}
	expanded := expandMountPath(raw)
	trailing := strings.HasSuffix(raw, "/") || strings.HasSuffix(raw, string(filepath.Separator))
	if !filepath.IsAbs(expanded) {
		base := m.mountDir
		if base == "" {
			base = mustWorkingDirectory()
		}
		expanded = filepath.Join(base, expanded)
	}
	if trailing {
		return filepath.Clean(expanded), ""
	}
	return filepath.Dir(expanded), filepath.Base(expanded)
}

func (m *Model) refreshMountEntries() {
	previous := ""
	if m.mountCursor < len(m.mountEntries) {
		previous = m.mountEntries[m.mountCursor]
	}
	entries, err := os.ReadDir(m.mountDir)
	if err != nil {
		m.mountEntries = nil
		m.mountCursor = 0
		return
	}
	m.mountEntries = m.mountEntries[:0]
	if m.mountFilter == "" {
		if parent := filepath.Dir(m.mountDir); parent != m.mountDir {
			m.mountEntries = append(m.mountEntries, "..")
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if m.mountFilter != "" && !hasPathPrefixFold(name, m.mountFilter) {
			continue
		}
		if !entryIsDir(m.mountDir, entry) {
			continue
		}
		m.mountEntries = append(m.mountEntries, name)
	}
	sort.Strings(m.mountEntries)
	m.mountCursor = 0
	if previous != "" {
		for i, name := range m.mountEntries {
			if name == previous {
				m.mountCursor = i
				break
			}
		}
	}
}

func (m Model) selectedMountPath() string {
	if m.mountCursor >= len(m.mountEntries) {
		return ""
	}
	name := m.mountEntries[m.mountCursor]
	if name == ".." {
		return filepath.Dir(m.mountDir)
	}
	return filepath.Join(m.mountDir, name)
}

func (m *Model) addMount(path string) {
	path = expandMountPath(strings.TrimSpace(path))
	if path == "" {
		m.status = "Mount path cannot be empty"
		return
	}
	path = filepath.Clean(path)
	for _, mount := range m.launch.Mounts {
		if mount.Path == path {
			m.inputActive = false
			m.status = "Path is already in the mount list"
			return
		}
	}
	m.launch.Mounts = append(m.launch.Mounts, config.Mount{Path: path, Mode: m.mountMode})
	m.cursor = len(m.launch.Mounts) - 1
	m.inputActive = false
	m.mountTyped = false
	m.mountFilter = ""
	modeLabel := "read-write"
	if m.mountMode == "ro" {
		modeLabel = "read-only"
	}
	m.status = "Added " + path + " (" + modeLabel + "). r = RUN · a = add another · Space = ro/rw · Backspace = remove"
}

func mustWorkingDirectory() string {
	path, err := os.Getwd()
	if err != nil {
		return "."
	}
	return path
}

// expandMountPath expands a leading ~ to the user home directory.
func expandMountPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func pathWithTrailingSep(path string) string {
	if path == "" {
		return string(filepath.Separator)
	}
	if strings.HasSuffix(path, string(filepath.Separator)) {
		return path
	}
	return path + string(filepath.Separator)
}

func joinMountInput(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return filepath.Join(dir, name)
}

func directoryMatches(dir, prefix string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	matches := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if prefix != "" && !hasPathPrefixFold(name, prefix) {
			continue
		}
		if !entryIsDir(dir, entry) {
			continue
		}
		matches = append(matches, name)
	}
	sort.Strings(matches)
	return matches
}

func entryIsDir(parent string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	// Symlinks to directories report as non-dir via DirEntry.IsDir(); Stat.
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	return isDirectory(filepath.Join(parent, entry.Name()))
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func hasPathPrefixFold(name, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
}

func longestCommonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) {
			if prefix == "" {
				return ""
			}
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func indexOfMatch(matches []string, name string) int {
	for i, match := range matches {
		if match == name {
			return i
		}
	}
	return 0
}

func quotePrefix(prefix string) string {
	if prefix == "" {
		return "(any)"
	}
	return strconv.Quote(prefix)
}

func (m *Model) removeMount() {
	if m.section != 2 || len(m.launch.Mounts) == 0 || m.cursor >= len(m.launch.Mounts) {
		return
	}
	removed := m.launch.Mounts[m.cursor].Path
	m.launch.Mounts = append(m.launch.Mounts[:m.cursor], m.launch.Mounts[m.cursor+1:]...)
	if m.cursor >= len(m.launch.Mounts) {
		m.cursor = len(m.launch.Mounts) - 1
	}
	m.status = "Mount removed: " + removed
}

func (m Model) helpView() string {
	return titleStyle.Render("ai-launcher · help") + "\n\n" +
		"How to launch (5 steps)\n" +
		"  1. ↑/↓   highlight an agent (e.g. Pi)\n" +
		"  2. Space or Enter   SELECT it (interface stays open)\n" +
		"  3. (optional) Tab   Permissions / Mounts / Options\n" +
		"  4. r     RUN  (only then the UI closes and the agent starts)\n" +
		"  5. If RUN fails, errors stay on screen — fix and press r again\n\n" +
		"Everywhere\n" +
		"  Tab / Shift+Tab   next / previous section\n" +
		"  1-5               jump to a section\n" +
		"  r / Ctrl+Enter    RUN Selected (not the ↑/↓ highlight)\n" +
		"  d / Ctrl+D        dry-run (preview argv, stay open)\n" +
		"  ?                 this help\n" +
		"  q / Esc           quit without running\n\n" +
		"Agent\n" +
		"  Space / Enter     SELECT highlighted row into Selected\n" +
		"  r                 runs Selected only (↑/↓ alone does not switch)\n\n" +
		"Mounts\n" +
		"  a / + / /         open add-folder panel\n" +
		"  Space             toggle read-only ↔ read-write\n" +
		"  Backspace         remove mount\n\n" +
		"Add-folder panel\n" +
		"  ↑↓ → ← Tab type   browse / complete path\n" +
		"  Ctrl+T            read-write ↔ read-only\n" +
		"  Enter             ADD folder · Esc cancel\n\n" +
		"Other\n" +
		"  Space             toggle permission / option\n" +
		"  Ctrl+S            save .ai-launch.yaml\n" +
		"  Ctrl+P            save as a named profile\n\n" +
		mutedStyle.Render("Press any key to close")
}

// selectHighlightedAgent applies the Agent-section cursor to launch config
// and stays in the TUI. It does not build argv or quit.
func (m *Model) selectHighlightedAgent() {
	if err := m.applyAgentCursorSelection(); err != nil {
		m.status = err.Error()
		return
	}
	if m.launch.ContinueSession {
		m.status = "Selected: Continue last session — press r to RUN"
		return
	}
	name := m.launch.Agent.Name
	if name == "" {
		name = m.launch.Agent.Command
	}
	m.status = "Selected: " + name + " (" + m.launch.Agent.Command + ") — press r to RUN"
}

// confirmRun builds argv, validates pre-flight inside the TUI, and either
// keeps the UI open with errors or returns true so the caller can tea.Quit.
// dryRun only prints the command into the status line.
//
// r always runs the *Selected* agent (launch.Agent / ContinueSession), not the
// list cursor. ↑/↓ only moves the highlight; Space/Enter commit selection.
// Cursor is adopted only when nothing is selected yet (empty agent and not
// Continue), so a first open still has a sensible one-key path.
func (m *Model) confirmRun(dryRun bool) bool {
	if !m.launch.ContinueSession && strings.TrimSpace(m.launch.Agent.Command) == "" && m.section == 0 {
		if err := m.applyAgentCursorSelection(); err != nil {
			m.status = err.Error()
			return false
		}
	}
	argv, err := launcher.Build(m.launch)
	if err != nil {
		m.status = "Error: " + err.Error()
		return false
	}
	if dryRun {
		m.status = "dry-run: " + strings.Join(argv, " ")
		return false
	}
	issues := launcher.NewValidator().Validate(m.launch)
	fatal := make([]string, 0)
	warns := make([]string, 0)
	for _, issue := range issues {
		if issue.Warning {
			warns = append(warns, issue.Error())
		} else {
			fatal = append(fatal, issue.Error())
		}
	}
	if len(fatal) > 0 {
		// Multi-line so long pre-flight messages are not truncated mid-word
		// on a narrow terminal (the previous single-line form cut at "th…").
		m.status = badStyle.Render("Cannot run — fix these first:") + "\n  · " + strings.Join(fatal, "\n  · ")
		if len(warns) > 0 {
			m.status += "\n" + mutedStyle.Render(strings.Join(warns, " · "))
		}
		return false
	}
	if m.launch.ContinueSession {
		m.status = goodStyle.Render("Starting: Continue last session…")
	} else {
		m.status = goodStyle.Render("Starting: " + m.launch.Agent.Name + " (" + m.launch.Agent.Command + ")…")
	}
	if len(warns) > 0 {
		m.status += "\n" + mutedStyle.Render(strings.Join(warns, " · "))
	}
	m.result = argv
	return true
}

// applyAgentCursorSelection copies the highlighted Agent-section row into
// launch (Continue vs a concrete harness).
func (m *Model) applyAgentCursorSelection() error {
	if m.section != 0 {
		// Selection is only driven by the Agent list cursor.
		return nil
	}
	if m.cursor == 0 {
		m.launch.ContinueSession = true
		m.launch.Agent = config.Agent{}
		m.launch.Executable = ""
		return nil
	}
	index := m.cursor - 1
	if index < 0 || index >= len(m.agents) {
		return errors.New("no agent under the cursor")
	}
	selected := m.agents[index]
	selectedAgent := selected.Agent
	if selected.ResolvedCommand != "" {
		selectedAgent.Command = selected.ResolvedCommand
	}
	m.launch.ContinueSession = false
	m.launch.Agent = selectedAgent
	m.launch.Executable = selected.Path
	return nil
}

// agentMatchesLaunch reports whether a catalog row is the currently selected
// harness (by primary command, resolved command, or alias).
func agentMatchesLaunch(status catalog.AgentStatus, launch launcher.LaunchConfig) bool {
	cmd := strings.TrimSpace(launch.Agent.Command)
	if cmd == "" {
		return false
	}
	if status.Agent.Command == cmd || status.ResolvedCommand == cmd {
		return true
	}
	for _, alias := range status.Agent.Aliases {
		if alias == cmd {
			return true
		}
	}
	return false
}

// cursorForLaunch places the cursor on Continue or on the matching agent row
// so the highlighted line matches "Selected:" at startup.
func cursorForLaunch(agents []catalog.AgentStatus, launch launcher.LaunchConfig) int {
	if launch.ContinueSession {
		return 0
	}
	for i, status := range agents {
		if agentMatchesLaunch(status, launch) {
			return i + 1
		}
	}
	if len(agents) > 0 {
		return 1
	}
	return 0
}
