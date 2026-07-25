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
	model := Model{
		catalog:       c,
		launch:        launch,
		agents:        c.Agents(),
		permissionIDs: make([]string, 0, len(global.Permissions)),
		profiles:      global.Profiles,
		profileNames:  config.ProfileNames(global),
		mountMode:     "ro",
	}
	// ai-jail has no Windows build: the jail permission and everything that
	// requires it are not offered there.
	jailDependent := config.JailDependentIDs(global.Permissions)
	for _, permission := range global.Permissions {
		if isWindows() && jailDependent[permission.ID] {
			continue
		}
		model.permissionIDs = append(model.permissionIDs, permission.ID)
	}
	if isWindows() {
		model.launch.UseJail = false
	}
	model.status = "Tab: seção · Space: alterna · /: adiciona mount · Enter: executa · d: dry-run · q: sai"
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
	case "shift+tab", "ctrl+k":
		m.section = (m.section + m.sectionCount() - 1) % m.sectionCount()
		m.cursor = 0
	case "1", "2", "3", "4", "5":
		if section := int(key.String()[0] - '1'); section < m.sectionCount() {
			m.section = section
			m.cursor = 0
		}
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "space", " ":
		m.toggleCurrent()
	case "/":
		if m.section == 2 {
			m.startMountBrowser()
		}
	case "backspace":
		m.removeMount()
	case "d", "ctrl+d":
		m.preview(true)
	case "?":
		m.helpOpen = true
	case "ctrl+s":
		m.saveLocal()
	case "ctrl+p":
		m.startProfileInput()
	case "enter":
		if m.section == 2 && len(m.launch.Mounts) == 0 {
			m.startMountBrowser()
		} else if m.section == 3 && m.cursor >= len(m.optionRows()) {
			m.startParamInput()
		} else if m.section == 4 && m.cursor < len(m.profileNames) {
			m.loadProfile(m.profileNames[m.cursor])
		} else if m.preview(false) {
			return m, tea.Quit
		}
	}
	return m, nil
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
		m.status = "Salvar não está disponível neste modo"
	} else if err := m.hooks.Save(m.launch); err != nil {
		m.status = "Erro ao salvar: " + err.Error()
	} else {
		m.status = "Seleção salva em .ai-launch.yaml"
	}
}

// startProfileInput opens the text input that names a new saved profile.
func (m *Model) startProfileInput() {
	if m.hooks.SaveProfile == nil {
		m.status = "Salvar perfil não está disponível neste modo"
		return
	}
	m.textInputKind = "profile"
	m.textInputValue = ""
	m.textInputActive = true
	m.status = "Nome do perfil · Enter salva · Esc cancela"
}

// optionRow is one fixed toggle row in the options section; catalog-declared
// param rows follow them.
type optionRow struct {
	name   string
	on     bool
	toggle func(m *Model)
}

// optionRows returns the fixed option toggles in display order. The jail
// toggle is hidden on Windows, where ai-jail is unavailable.
func (m *Model) optionRows() []optionRow {
	rows := make([]optionRow, 0, 4)
	if !isWindows() {
		rows = append(rows, optionRow{name: "Jail / Sandbox", on: m.launch.UseJail, toggle: func(m *Model) { m.launch.UseJail = !m.launch.UseJail }})
	}
	return append(rows,
		optionRow{name: "ai-memory", on: m.launch.UseMemory, toggle: func(m *Model) { m.launch.UseMemory = !m.launch.UseMemory }},
		optionRow{name: "Novo workstream", on: m.launch.NewWorkstream != "", toggle: toggleWorkstreamOption},
		optionRow{name: "--yolo", on: m.launch.Yolo, toggle: func(m *Model) { m.launch.Yolo = !m.launch.Yolo }},
	)
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
		label := "Perfil"
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
	b.WriteString("\n\nAgente\n")
	pointer := "  "
	if m.section == 0 && m.cursor == 0 {
		pointer = "❯ "
	}
	mark := "  "
	if m.launch.ContinueSession {
		mark = "❯ "
	}
	fmt.Fprintf(b, "%s%s%-22s (%s)\n", pointer, mark, "Continuar última sessão", "ai-memory run")
	for i, status := range m.agents {
		pointer := "  "
		if m.section == 0 && i+1 == m.cursor {
			pointer = "❯ "
		}
		state := badStyle.Render("[not found]")
		if status.Installed {
			state = goodStyle.Render("[installed]")
		}
		command := status.Agent.Command
		if status.ResolvedCommand != "" && status.ResolvedCommand != status.Agent.Command {
			command += " via " + status.ResolvedCommand
		}
		fmt.Fprintf(b, "%s%-18s (%s) %s\n", pointer, status.Agent.Name, command, state)
	}
}

func (m Model) permissionsView(b *strings.Builder) {
	b.WriteString("\nPermissões\n")
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
	if len(m.launch.Mounts) == 0 {
		b.WriteString("  (nenhum; pressione / para adicionar)\n")
	}
	for i, mount := range m.launch.Mounts {
		pointer := "  "
		if m.section == 2 && i == m.cursor {
			pointer = "❯ "
		}
		mode := mount.Mode
		if mode == "" {
			mode = "rw"
		}
		fmt.Fprintf(b, "%s%s [%s]\n", pointer, mount.Path, mode)
	}
	if m.inputActive {
		fmt.Fprintf(b, "  Adicionar mount (%s): %s█\n", m.mountMode, m.mountInput)
		b.WriteString("  Navegador: " + m.mountDir + "\n")
		if len(m.mountEntries) == 0 {
			b.WriteString("    (sem subdiretórios legíveis; digite um caminho)\n")
		} else {
			for i, entry := range m.mountEntries {
				pointer := "    "
				if i == m.mountCursor {
					pointer = "  ❯ "
				}
				b.WriteString(pointer + entry + "/\n")
			}
		}
	}
}

func (m Model) optionsView(b *strings.Builder) {
	b.WriteString("\nOpções\n")
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
			value = mutedStyle.Render("(vazio)")
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
	b.WriteString("\nPerfis\n")
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
	m.status = "Dependências normalizadas automaticamente"
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
			m.status = "Modo do mount alternado"
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
	m.status = "Opção alternada"
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
	m.status = "Editando " + m.paramTarget.Name + " (" + m.paramTarget.Flag + ") · Enter confirma · Esc cancela"
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
		m.launch.NewWorkstream = profile.Options.NewWorkstream
		m.launch.Workstream = profile.Options.Workstream
		m.launch.Workspace = profile.Options.Workspace
		m.launch.Project = profile.Options.Project
		m.launch.JailFlags = profile.Options.JailFlags
		m.launch.ExtraArgs = append([]string(nil), profile.Options.ExtraArgs...)
		m.launch.ParamValues = copyParamValues(profile.Options.ParamValues)
	}
	m.status = "Perfil carregado: " + name
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
		m.status = "Edição cancelada"
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
		m.status = "Parâmetro limpo: " + m.paramTarget.Name
		return
	}
	if m.launch.ParamValues == nil {
		m.launch.ParamValues = make(map[string]string)
	}
	m.launch.ParamValues[m.paramTarget.Name] = value
	m.status = "Parâmetro definido: " + m.paramTarget.Name
}

// saveProfileAs persists the current selection as a named profile and adds
// it to the profiles section.
func (m *Model) saveProfileAs(name string) {
	if name == "" {
		m.status = "O nome do perfil não pode ser vazio"
		return
	}
	if err := m.hooks.SaveProfile(name, m.launch); err != nil {
		m.status = "Erro ao salvar perfil: " + err.Error()
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
	m.status = "Perfil salvo: " + name
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
		m.status = "Adição de mount cancelada"
	case "enter":
		path := strings.TrimSpace(m.mountInput)
		if path == "" && len(m.mountEntries) > 0 {
			path = m.selectedMountPath()
		}
		m.addMount(path)
	case "right", "l", "left", "h", "up", "k", "down", "j":
		m.navigateMountBrowser(key.String())
	case "tab":
		if m.mountMode == "ro" {
			m.mountMode = "rw"
		} else {
			m.mountMode = "ro"
		}
		m.status = "Modo do mount: " + m.mountMode
	case "backspace":
		if m.mountInput != "" {
			runes := []rune(m.mountInput)
			m.mountInput = string(runes[:len(runes)-1])
		}
		if m.mountInput == "" {
			m.mountTyped = false
		}
	default:
		if key.Type == tea.KeyRunes {
			m.mountInput += string(key.Runes)
			m.mountTyped = true
		}
	}
	return *m, nil
}

// navigateMountBrowser handles the browser-only movement keys. It is a no-op
// while the user is typing a path manually (mountTyped).
func (m *Model) navigateMountBrowser(key string) {
	if m.mountTyped {
		return
	}
	switch key {
	case "right", "l":
		if len(m.mountEntries) == 0 {
			return
		}
		path := m.selectedMountPath()
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			m.mountDir = path
			m.refreshMountEntries()
			m.status = "Diretório aberto; Enter adiciona, ←/h sobe"
		}
	case "left", "h":
		parent := filepath.Dir(m.mountDir)
		if parent != m.mountDir {
			m.mountDir = parent
			m.refreshMountEntries()
			m.status = "Diretório pai aberto"
		}
	case "up", "k":
		if len(m.mountEntries) > 0 {
			m.mountCursor = (m.mountCursor + len(m.mountEntries) - 1) % len(m.mountEntries)
		}
	case "down", "j":
		if len(m.mountEntries) > 0 {
			m.mountCursor = (m.mountCursor + 1) % len(m.mountEntries)
		}
	}
}

func (m *Model) startMountBrowser() {
	m.inputActive = true
	m.mountInput = ""
	m.mountTyped = false
	m.mountMode = "ro"
	m.mountDir = mustWorkingDirectory()
	m.refreshMountEntries()
	m.status = "↑/↓ seleciona · →/l entra · ←/h sobe · Enter adiciona · Tab alterna ro/rw · Esc cancela"
}

func (m *Model) refreshMountEntries() {
	entries, err := os.ReadDir(m.mountDir)
	if err != nil {
		m.mountEntries = nil
		m.mountCursor = 0
		return
	}
	m.mountEntries = m.mountEntries[:0]
	if parent := filepath.Dir(m.mountDir); parent != m.mountDir {
		m.mountEntries = append(m.mountEntries, "..")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			m.mountEntries = append(m.mountEntries, entry.Name())
		}
	}
	sort.Strings(m.mountEntries)
	m.mountCursor = 0
}

func (m Model) selectedMountPath() string {
	if m.mountCursor >= len(m.mountEntries) {
		return ""
	}
	return filepath.Join(m.mountDir, m.mountEntries[m.mountCursor])
}

func (m *Model) addMount(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		m.status = "O caminho do mount não pode ser vazio"
		return
	}
	for _, mount := range m.launch.Mounts {
		if mount.Path == path {
			m.inputActive = false
			m.status = "Path já está na lista de mounts"
			return
		}
	}
	m.launch.Mounts = append(m.launch.Mounts, config.Mount{Path: path, Mode: m.mountMode})
	m.cursor = len(m.launch.Mounts) - 1
	m.inputActive = false
	m.status = "Mount adicionado"
}

func mustWorkingDirectory() string {
	path, err := os.Getwd()
	if err != nil {
		return "."
	}
	return path
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
	m.status = "Mount removido: " + removed
}

func (m Model) helpView() string {
	return titleStyle.Render("ai-launcher · ajuda") + "\n\n" +
		"Tab / Shift+Tab   próxima seção\n" +
		"1-5               saltar para uma seção\n" +
		"↑↓ / j k          navegar\n" +
		"Space             alternar seleção / carregar perfil\n" +
		"/                 abrir navegador de mounts\n" +
		"→ / l             entrar no diretório\n" +
		"← / h             subir para o diretório pai\n" +
		"Enter             adicionar diretório, editar parâmetro ou executar\n" +
		"Backspace         remover mount\n" +
		"Ctrl+D / d        dry-run\n" +
		"Ctrl+S            salvar .ai-launch.yaml\n" +
		"Ctrl+P            salvar como perfil nomeado\n" +
		"q / Esc           sair\n\n" +
		mutedStyle.Render("Pressione qualquer tecla para fechar")
}

func (m *Model) preview(dryRun bool) bool {
	if m.section == 0 {
		if m.cursor == 0 {
			// The first row continues the most recent ai-memory session of the
			// checkout ("ai-memory run" without a harness).
			if !m.launch.ContinueSession {
				m.launch.ContinueSession = true
				m.launch.Agent = config.Agent{}
				m.launch.Executable = ""
				m.status = "Continuar última sessão selecionado; pressione Enter novamente para executar"
				return false
			}
		} else if m.cursor-1 < len(m.agents) {
			selected := m.agents[m.cursor-1]
			selectedAgent := selected.Agent
			if selected.ResolvedCommand != "" {
				selectedAgent.Command = selected.ResolvedCommand
			}
			if m.launch.ContinueSession || m.launch.Agent.Command != selectedAgent.Command {
				m.launch.ContinueSession = false
				m.launch.Agent = selectedAgent
				m.launch.Executable = selected.Path
				m.status = "Agente selecionado; pressione Enter novamente para executar"
				return false
			}
		}
	}
	argv, err := launcher.Build(m.launch)
	if err != nil {
		m.status = "Erro: " + err.Error()
		return false
	}
	if dryRun {
		m.status = "dry-run: " + strings.Join(argv, " ")
		return false
	}
	m.result = argv
	return true
}
