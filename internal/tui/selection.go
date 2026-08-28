package tui

import (
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

func (m Model) helpView() string {
	return titleStyle.Render("ai-launcher · help") + "\n\n" +
		"How to launch (5 steps)\n" +
		"  1. ↑/↓   highlight an agent (e.g. Pi)\n" +
		"  2. Space or Enter   SELECT it (interface stays open)\n" +
		"  3. (optional) Tab   Permissions / Mounts / Options / Container / Services\n" +
		"  4. r     RUN  (only then the UI closes and the agent starts)\n" +
		"  5. If RUN fails, errors stay on screen — fix and press r again\n\n" +
		"Everywhere\n" +
		"  Tab / Shift+Tab   next / previous section\n" +
		"  1-7               jump to a section\n" +
		"  r / Ctrl+Enter    RUN Selected (not the ↑/↓ highlight)\n" +
		"  When editing      Enter apply · Esc cancel · press r after closing the editor\n" +
		"  d / Ctrl+D        dry-run (preview argv or Compose YAML, stay open)\n" +
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
		"Container\n" +
		"  Space             toggle stacks\n" +
		"  Enter             edit resources, ports, and network\n" +
		"  Memory            512m / 2g (minimum 6m)\n" +
		"  CPU               decimal cores, e.g. 1.0\n" +
		"  PIDs              positive integer, e.g. 256\n" +
		"  Ports             host:container[/tcp|udp], comma-separated\n" +
		"  Runtime           Enter then ↑/↓: auto, Docker, Podman, nerdctl (PATH status shown)\n" +
		"  Context           Docker context; empty uses current, list with docker context ls\n\n" +
		"  tmux              Space/Enter starts the agent in tmux; host ~/.tmux.conf,\n" +
		"                    ~/.tmux.conf.local and ~/.tmux are mounted read-only\n\n" +
		"Services\n" +
		"  Space / Enter     add or remove infrastructure services\n" +
		"  Preview           shows docker run argv or generated Compose YAML\n\n" +
		"  Compose review    m keep current · s replace generated · ↑/↓ scroll\n\n" +
		"Other\n" +
		"  Space             toggle permission / option\n" +
		"  Workstream        new/resume name; empty new name disables the toggle\n" +
		"  Workspace/Project forwarded to ai-memory scope\n" +
		"  Extra agent args  shell-style arguments; quotes supported\n" +
		"  Ctrl+S            save .ai-launcher/config.yaml\n" +
		"  Ctrl+P            save as a named profile\n" +
		"  Ctrl+L            reload global config from disk\n\n" +
		mutedStyle.Render("Press any key to close")
}

// selectHighlightedAgent applies the Agent-section cursor to launch config
// and stays in the TUI. It does not build argv or quit.
func (m *Model) selectHighlightedAgent() {
	if err := m.applyAgentCursorSelection(); err != nil {
		m.status = err.Error()
		return
	}
	memoryDisabled := m.syncMemoryForAgent()
	m.syncYoloForAgent()
	if m.launch.ContinueSession {
		m.status = "Selected: Continue last session — press r to RUN"
		return
	}
	if memoryDisabled {
		return
	}
	name := m.launch.Agent.Name
	if name == "" {
		name = m.launch.Agent.Command
	}
	m.status = "Selected: " + name + " (" + m.launch.Agent.Command + ") — press r to RUN"
}

// syncMemoryForAgent disables ai-memory automatically when the selected agent
// would build a command that ai-memory run cannot execute: either the agent
// declares supports_memory: false, or its run_harness is not in the accepted
// harness list. This prevents the TUI from building an unsupported
// "ai-memory run <harness>" command when the user just picked an agent while
// the memory toggle stayed on by default. It returns true when it changed the
// memory setting.
func (m *Model) syncMemoryForAgent() bool {
	if m.launch.ContinueSession {
		return false
	}
	if strings.TrimSpace(m.launch.Agent.Command) == "" {
		return false
	}
	if !m.launch.Agent.SupportsMemory && m.launch.UseMemory {
		m.launch.UseMemory = false
		m.status = "ai-memory disabled: " + m.launch.Agent.Command + " does not support it"
		return true
	}
	harness := m.launch.Agent.Command
	if m.launch.Agent.Memory != nil {
		if h := strings.TrimSpace(m.launch.Agent.Memory.RunHarness); h != "" {
			harness = h
		}
	}
	if m.launch.UseMemory && !config.SupportsMemoryRunHarness(harness) {
		m.launch.UseMemory = false
		m.status = "ai-memory disabled: harness " + harness + " is not accepted by ai-memory"
		return true
	}
	return false
}

// syncYoloForAgent disables the dangerous-mode flag automatically when the
// selected agent does not declare support for it, so a saved local config or
// profile cannot sneak a raw --yolo into an agent that does not understand it.
func (m *Model) syncYoloForAgent() {
	if m.launch.ContinueSession {
		return
	}
	if strings.TrimSpace(m.launch.Agent.Command) == "" {
		return
	}
	if !m.launch.Agent.SupportsYolo && m.launch.Yolo {
		m.launch.Yolo = false
		m.status = "--yolo disabled: " + m.launch.Agent.Command + " does not support it"
	}
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
	m.launch = launcher.EnsureDockerProjectDir(m.launch)
	m.launch = launcher.ResolveHostBinaries(m.launch)
	argv, err := launcher.Build(m.launch)
	if err != nil {
		m.status = "Error: " + err.Error()
		return false
	}
	if dryRun {
		m.status = "dry-run: " + strings.Join(argv, " ")
		return false
	}
	fatal, warns := validateLaunch(m.launch)
	if len(fatal) > 0 {
		m.status = blockedStatus(fatal, warns)
		return false
	}
	if !dryRun && !m.composeReviewAccepted && m.launch.UseDocker && len(m.launch.Services) > 0 && m.hooks.ReviewCompose != nil {
		review, err := m.hooks.ReviewCompose(m.launch)
		if err != nil {
			m.status = "Compose review unavailable: " + err.Error()
			return false
		}
		if review != nil && strings.TrimSpace(review.Diff) != "" {
			m.composeReview = review
			m.composeReviewOffset = 0
			m.status = "Compose file changed · review the diff before running"
			return false
		}
	}
	m.status = m.startingStatus()
	if len(warns) > 0 {
		m.status += lineBreak + mutedStyle.Render(strings.Join(warns, " · "))
	}
	m.result = argv
	return true
}

func (m Model) handleComposeReviewKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case keyUp, keyUpLetter:
		if m.composeReviewOffset > 0 {
			m.composeReviewOffset--
		}
	case keyDown, keyDownLetter:
		m.composeReviewOffset++
	case "m":
		if m.acceptComposeReview(KeepCompose) {
			return m, tea.Quit
		}
	case "s":
		if m.acceptComposeReview(ReplaceCompose) {
			return m, tea.Quit
		}
	case "q", keyEscape:
		m.composeReview = nil
		m.composeReviewOffset = 0
		m.status = "Compose update cancelled · press r to review again or q to quit"
	}
	return m, nil
}

func (m *Model) acceptComposeReview(choice ComposeUpdateChoice) bool {
	if m.composeReview == nil || m.composeReview.Choose == nil {
		m.status = "Compose review is incomplete"
		return false
	}
	if err := m.composeReview.Choose(choice); err != nil {
		m.status = "Compose review failed: " + err.Error()
		return false
	}
	m.composeReview = nil
	m.composeReviewOffset = 0
	m.composeReviewAccepted = true
	if choice == KeepCompose {
		m.status = "Keeping custom docker-compose.yaml · starting…"
	} else {
		m.status = "Replacing docker-compose.yaml with generated version · starting…"
	}
	return m.confirmRun(false)
}

// validateLaunch runs the pre-flight validator and splits the issues into
// fatal errors and warnings.
func validateLaunch(launch launcher.LaunchConfig) (fatal, warns []string) {
	fatal = make([]string, 0)
	warns = make([]string, 0)
	for _, issue := range launcher.NewValidator().Validate(launch) {
		if issue.Warning {
			warns = append(warns, issue.Error())
		} else {
			fatal = append(fatal, issue.Error())
		}
	}
	return fatal, warns
}

// blockedStatus renders the multi-line pre-flight failure status so long
// messages are not truncated mid-word on a narrow terminal (the previous
// single-line form cut at "th…").
func blockedStatus(fatal, warns []string) string {
	status := badStyle.Render("Cannot run — fix these first:") + "\n  · " + strings.Join(fatal, "\n  · ")
	if len(warns) > 0 {
		status += lineBreak + mutedStyle.Render(strings.Join(warns, " · "))
	}
	return status
}

// startingStatus renders the "Starting:" line shown just before the TUI
// closes and the agent runs.
func (m Model) startingStatus() string {
	if m.launch.ContinueSession {
		return goodStyle.Render("Starting: Continue last session…")
	}
	return goodStyle.Render("Starting: " + m.launch.Agent.Name + " (" + m.launch.Agent.Command + ")…")
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
		selectedAgent.CatalogCommand = selectedAgent.Command
		selectedAgent.Command = selected.ResolvedCommand
	}
	m.launch.ContinueSession = false
	m.launch.Agent = selectedAgent
	m.launch.Executable = selected.Path
	// In docker mode the image selection must follow the newly selected agent:
	// re-plan the install kind (release/script/npm/host) and pin the version
	// for it so the image tag and the in-container executable match (H2). The
	// home/env wiring stays with the CLI launch path; only the recipe is
	// re-derived.
	if m.launch.UseDocker {
		m.launch.Docker.Selection.Agents = []container.AgentInstall{
			planDockerAgentInstall(m.launch.HomeDir, selectedAgent, selected.Path),
		}
	}
	return nil
}

// planDockerAgentInstall re-derives the container install recipe for an agent
// the same way the CLI does before opening the TUI
// (cmd/ai-launcher/container_flow.go dockerSelectionFromAgent): plan from the
// catalog command and pin the version from the host install-state, falling
// back to a stable placeholder so a release install never carries an empty
// version — Selection.Validate rejects that and the launch would become
// unrecoverable in-process.
func planDockerAgentInstall(home string, agent config.Agent, hostPath string) container.AgentInstall {
	if agent.CatalogCommand != "" {
		agent.Command = agent.CatalogCommand
	}
	version := container.AgentVersion(home, agent.Command, "")
	if version == "" {
		version = "0.0.0-recipe"
	}
	return container.PlanInstall(agent, version, hostPath)
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

// reloadConfig re-reads the global config from disk and rebuilds the model.
// The current selection is preserved so the operator does not lose in-flight
// edits; changes to the workspace config require a restart.
func (m Model) reloadConfig() (tea.Model, tea.Cmd) {
	global, err := config.LoadGlobal(m.opts.GlobalPath)
	if err != nil {
		m.status = "Reload failed: " + err.Error()
		return m, nil
	}

	fresh := NewModel(global, m.launch)
	fresh.opts = m.opts
	fresh.hooks = m.hooks
	fresh.width = m.width
	fresh.height = m.height
	fresh.status = "Global config reloaded"
	return fresh, nil
}
