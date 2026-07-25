// Package tui provides the interactive frontend. It deliberately keeps
// state in launcher.LaunchConfig so the CLI and TUI share the same builder.
package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

var ErrCancelled = errors.New("launcher cancelled")

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#b4befe"))
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))
	goodStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
	badStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8"))
)

type Model struct {
	catalog       catalog.Catalog
	launch        launcher.LaunchConfig
	save          func(launcher.LaunchConfig) error
	agents        []catalog.AgentStatus
	permissionIDs []string
	section       int
	cursor        int
	mountInput    string
	mountMode     string
	mountDir      string
	mountEntries  []string
	mountCursor   int
	mountTyped    bool
	inputActive   bool
	helpOpen      bool
	width         int
	height        int
	status        string
	result        []string
	cancelled     bool
}

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
		mountMode:     "ro",
	}
	for _, permission := range global.Permissions {
		model.permissionIDs = append(model.permissionIDs, permission.ID)
	}
	model.status = "Tab: seção · Space: alterna · /: adiciona mount · Enter: executa · d: dry-run · q: sai"
	return model
}

func Run(global config.Global, launch launcher.LaunchConfig) (launcher.LaunchConfig, error) {
	return RunWithSave(global, launch, nil)
}

func RunWithSave(global config.Global, launch launcher.LaunchConfig, save func(launcher.LaunchConfig) error) (launcher.LaunchConfig, error) {
	model := NewModel(global, launch)
	model.save = save
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

func (m Model) Init() tea.Cmd { return nil }

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
		switch value.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "tab", "ctrl+j":
			m.section = (m.section + 1) % 4
			m.cursor = 0
		case "shift+tab", "ctrl+k":
			m.section = (m.section + 3) % 4
			m.cursor = 0
		case "1", "2", "3", "4":
			m.section = int(value.String()[0] - '1')
			m.cursor = 0
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
			if m.save == nil {
				m.status = "Salvar não está disponível neste modo"
			} else if err := m.save(m.launch); err != nil {
				m.status = "Erro ao salvar: " + err.Error()
			} else {
				m.status = "Seleção salva em .ai-launch.yaml"
			}
		case "enter":
			if m.section == 2 && len(m.launch.Mounts) == 0 {
				m.startMountBrowser()
			} else if m.preview(false) {
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.helpOpen {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("ai-launcher"))
	b.WriteString("\n\nAgente\n")
	for i, status := range m.agents {
		pointer := "  "
		if m.section == 0 && i == m.cursor {
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
		b.WriteString(fmt.Sprintf("%s%-18s (%s) %s\n", pointer, status.Agent.Name, command, state))
	}

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
		b.WriteString(fmt.Sprintf("%s%s %-22s\n", pointer, mark, permission.Name))
	}

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
		b.WriteString(fmt.Sprintf("%s%s [%s]\n", pointer, mount.Path, mode))
	}
	if m.inputActive {
		b.WriteString(fmt.Sprintf("  Adicionar mount (%s): %s█\n", m.mountMode, m.mountInput))
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

	b.WriteString("\nOpções\n")
	options := []struct {
		name string
		on   bool
	}{
		{name: "Jail / Sandbox", on: m.launch.UseJail},
		{name: "ai-memory", on: m.launch.UseMemory},
		{name: "Novo workstream", on: m.launch.NewWorkstream != ""},
		{name: "--yolo", on: m.launch.Yolo},
	}
	for i, option := range options {
		pointer := "  "
		if m.section == 3 && i == m.cursor {
			pointer = "❯ "
		}
		mark := "[ ]"
		if option.on {
			mark = "[✓]"
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", pointer, mark, option.name))
	}
	if m.launch.NewWorkstream != "" {
		b.WriteString("  workstream: " + m.launch.NewWorkstream + "\n")
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

func (m *Model) moveCursor(delta int) {
	limit := len(m.agents)
	switch m.section {
	case 1:
		limit = len(m.permissionIDs)
	case 2:
		limit = len(m.launch.Mounts)
	case 3:
		limit = 4
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
		switch m.cursor {
		case 0:
			m.launch.UseJail = !m.launch.UseJail
		case 1:
			m.launch.UseMemory = !m.launch.UseMemory
		case 2:
			if m.launch.NewWorkstream == "" {
				m.launch.NewWorkstream = "new-workstream"
			} else {
				m.launch.NewWorkstream = ""
			}
		case 3:
			m.launch.Yolo = !m.launch.Yolo
		}
		m.status = "Opção alternada"
	}
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
	case "right", "l":
		if !m.mountTyped && len(m.mountEntries) > 0 {
			path := m.selectedMountPath()
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				m.mountDir = path
				m.refreshMountEntries()
				m.status = "Diretório aberto; Enter adiciona, ←/h sobe"
			}
		}
	case "left", "h":
		if !m.mountTyped {
			parent := filepath.Dir(m.mountDir)
			if parent != m.mountDir {
				m.mountDir = parent
				m.refreshMountEntries()
				m.status = "Diretório pai aberto"
			}
		}
	case "up", "k":
		if !m.mountTyped && len(m.mountEntries) > 0 {
			m.mountCursor = (m.mountCursor + len(m.mountEntries) - 1) % len(m.mountEntries)
		}
	case "down", "j":
		if !m.mountTyped && len(m.mountEntries) > 0 {
			m.mountCursor = (m.mountCursor + 1) % len(m.mountEntries)
		}
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
		"1-4               saltar para uma seção\n" +
		"↑↓ / j k          navegar\n" +
		"Space             alternar seleção\n" +
		"/                 abrir navegador de mounts\n" +
		"→ / l             entrar no diretório\n" +
		"← / h             subir para o diretório pai\n" +
		"Enter             adicionar diretório / executar\n" +
		"Backspace         remover mount\n" +
		"Ctrl+D / d        dry-run\n" +
		"Ctrl+S            salvar configuração\n" +
		"q / Esc           sair\n\n" +
		mutedStyle.Render("Pressione qualquer tecla para fechar")
}

func (m *Model) preview(dryRun bool) bool {
	if m.section == 0 && m.cursor < len(m.agents) {
		selected := m.agents[m.cursor]
		selectedAgent := selected.Agent
		if selected.ResolvedCommand != "" {
			selectedAgent.Command = selected.ResolvedCommand
		}
		if m.launch.Agent.Command != selectedAgent.Command {
			m.launch.Agent = selectedAgent
			m.launch.Executable = selected.Path
			m.status = "Agente selecionado; pressione Enter novamente para executar"
			return false
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
