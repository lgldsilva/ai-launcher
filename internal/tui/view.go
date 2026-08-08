package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// View implements tea.Model and renders the whole screen. When the terminal
// is shorter than the complete Docker layout, it switches to the active
// section instead of letting the alternate screen clip the top and footer.
func (m Model) View() string {
	if m.helpOpen {
		return m.helpView()
	}
	if m.composeReview != nil {
		return m.composeReviewView()
	}
	blocks := m.sectionBlocks()
	var b strings.Builder
	b.WriteString(titleStyle.Render(config.LauncherName))
	b.WriteString(lineBreak + m.sectionTabs() + lineBreak)
	for _, block := range blocks {
		b.WriteString(block)
	}

	if lines := m.textInputLines(); len(lines) > 0 {
		b.WriteString(lineBreak + strings.Join(lines, lineBreak) + lineBreak)
	}
	b.WriteString(lineBreak)
	m.previewView(&b)
	if m.status != "" {
		b.WriteString(lineBreak + m.status + lineBreak)
	}
	full := b.String()
	if m.height > 0 && viewLineCount(full) > m.height {
		return m.compactView(blocks)
	}
	return full
}

func (m Model) composeReviewView() string {
	const minimumVisibleDiffLines = 8
	lines := strings.Split(strings.TrimSuffix(m.composeReview.Diff, "\n"), "\n")
	visible := m.height - 8
	if visible < minimumVisibleDiffLines {
		visible = minimumVisibleDiffLines
	}
	if len(lines) > visible {
		maxOffset := len(lines) - visible
		if m.composeReviewOffset > maxOffset {
			m.composeReviewOffset = maxOffset
		}
	} else {
		m.composeReviewOffset = 0
	}
	start := m.composeReviewOffset
	end := start + visible
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("ai-launcher · Compose update review") + lineBreak + lineBreak)
	b.WriteString(badStyle.Render("docker-compose.yaml has manual changes") + lineBreak)
	b.WriteString(mutedStyle.Render("Keep preserves custom ports/services; Replace applies the Compose generated from the current selection.") + lineBreak + lineBreak)
	if start > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("↑ %d line(s) above", start)) + lineBreak)
	}
	for _, line := range lines[start:end] {
		style := mutedStyle
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			style = goodStyle
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			style = badStyle
		}
		b.WriteString(style.Render(line) + lineBreak)
	}
	if end < len(lines) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("↓ %d line(s) below", len(lines)-end)) + lineBreak)
	}
	b.WriteString(lineBreak + "[m] keep current   [s] replace with generated   [↑/↓] scroll   [Esc] cancel" + lineBreak)
	return b.String()
}

func (m Model) sectionBlocks() []string {
	blocks := []string{
		renderViewBlock(m.agentsView),
		renderViewBlock(m.permissionsView),
		renderViewBlock(m.mountsView),
		renderViewBlock(m.optionsView),
	}
	if m.launch.UseDocker {
		blocks = append(blocks, renderViewBlock(m.containerView), renderViewBlock(m.servicesView))
	}
	if len(m.profileNames) > 0 {
		blocks = append(blocks, renderViewBlock(m.profilesView))
	}
	return blocks
}

func renderViewBlock(render func(*strings.Builder)) string {
	var b strings.Builder
	render(&b)
	return b.String()
}

func viewLineCount(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(value, lineBreak) + 1
}

// compactView keeps the active section and navigation hint visible on short
// terminals. The full layout remains available automatically when the window
// is tall enough; no state is hidden, only the inactive sections are deferred
// until the operator moves to them with Tab or a numeric shortcut.
func (m Model) compactView(blocks []string) string {
	section := m.section
	if section < 0 || section >= len(blocks) {
		section = 0
	}
	lines := strings.Split(strings.Trim(blocks[section], "\n"), "\n")
	editorLines := m.textInputLines()
	statusLines := m.compactStatusLines()
	bodyLimit := m.height - 5 - len(editorLines) - len(statusLines) // title, tabs, editor, status, note, hint
	if bodyLimit < 2 {
		bodyLimit = 2
	}
	if len(lines) > bodyLimit {
		header := lines[0]
		body := lines[1:]
		windowSize := bodyLimit - 1 // keep the active section heading visible
		focus := m.cursor
		if focus >= len(body) {
			focus = len(body) - 1
		}
		start := focus - windowSize/2
		if start < 0 {
			start = 0
		}
		if start+windowSize > len(body) {
			start = len(body) - windowSize
		}
		end := start + windowSize
		window := append([]string{header}, body[start:end]...)
		if start > 0 {
			window[1] = fmt.Sprintf("  ↑ %d line(s) above", start)
		}
		if end < len(body) {
			window[len(window)-1] = fmt.Sprintf("  ↓ %d line(s) below", len(body)-end)
		}
		lines = window
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(config.LauncherName) + lineBreak)
	b.WriteString(m.sectionTabs() + lineBreak)
	b.WriteString(strings.Join(lines, lineBreak) + lineBreak)
	if len(editorLines) > 0 {
		b.WriteString(strings.Join(editorLines, lineBreak) + lineBreak)
	}
	if len(statusLines) > 0 {
		b.WriteString(strings.Join(statusLines, lineBreak) + lineBreak)
	}
	b.WriteString(mutedStyle.Render("  compact view · Tab/number keys switch sections") + lineBreak)
	b.WriteString(m.sectionHint() + lineBreak)
	return b.String()
}

func (m Model) compactStatusLines() []string {
	if m.textInputActive || strings.TrimSpace(m.status) == "" || m.status == m.sectionHint() {
		return nil
	}
	return strings.Split(m.status, lineBreak)
}

// sectionTabs makes the Tab navigation explicit. The TUI has sections rather
// than browser tabs, but the active section must be obvious when the compact
// layout hides the other blocks.
func (m Model) sectionTabs() string {
	titles := []string{"Agent", "Permissions", "Mounts", "Options"}
	if m.launch.UseDocker {
		titles = append(titles, "Container", "Services")
	}
	if len(m.profileNames) > 0 {
		titles = append(titles, "Profiles")
	}
	parts := make([]string, 0, len(titles))
	for index, title := range titles {
		label := fmt.Sprintf("%d %s", index+1, title)
		if index == m.section {
			parts = append(parts, titleStyle.Render("["+label+"]"))
		} else {
			parts = append(parts, mutedStyle.Render(label))
		}
	}
	return "  Sections: " + strings.Join(parts, "  ")
}

// textInputLines keeps the active editor visible in both the full and compact
// layouts. The compact layout used to drop this entirely, so a short terminal
// showed neither the value being typed nor the syntax needed to enter it.
func (m Model) textInputLines() []string {
	if !m.textInputActive {
		return nil
	}
	label := "Profile"
	hint := ""
	switch {
	case m.textInputKind == "param":
		label = m.paramTarget.Name + " (" + m.paramTarget.Flag + ")"
	case m.advancedOptionKindActive():
		if row, ok := m.advancedOption(advancedOptionKind(m.textInputKind)); ok {
			label = row.name
			hint = row.hint
		}
	case strings.HasPrefix(m.textInputKind, "container-"):
		kind := containerResourceKind(m.textInputKind)
		label = containerResourceLabel(kind)
		hint = containerResourceEditHint(kind)
	case strings.HasPrefix(m.textInputKind, "service-ports:"):
		serviceID := strings.TrimPrefix(m.textInputKind, "service-ports:")
		if service, ok := container.ServiceByID(serviceID); ok {
			label = service.Name + " ports"
		}
		hint = servicePortEditHint()
	}
	lines := []string{fmt.Sprintf("  %s: %s█", label, m.textInputValue)}
	if hint != "" {
		lines = append(lines, mutedStyle.Render("  Format: "+hint))
	}
	if m.textInputKind == string(resourceContext) {
		choices := m.contextChoices()
		labels := make([]string, 0, len(choices))
		for index, choice := range choices {
			label := contextChoiceLabel(choice)
			if index == m.contextCursor {
				label = "[" + label + "]"
			}
			labels = append(labels, label)
		}
		lines = append(lines, mutedStyle.Render("  Contexts (↑/↓): "+strings.Join(labels, "  ")))
	}
	if m.textInputKind == string(resourceRuntime) {
		choices := m.runtimeChoices()
		labels := make([]string, 0, len(choices))
		for index, choice := range choices {
			label := runtimeChoiceLabel(choice, m.runtimeAvailable(choice))
			if index == m.runtimeCursor {
				label = "[" + label + "]"
			}
			labels = append(labels, label)
		}
		lines = append(lines, mutedStyle.Render("  Runtimes (↑/↓): "+strings.Join(labels, "  ")))
	}
	lines = append(lines, mutedStyle.Render("  Enter apply · Esc cancel · r RUN after closing this editor"))
	if strings.HasPrefix(m.status, "Invalid container resource:") {
		lines = append(lines, badStyle.Render("  "+m.status))
	}
	if strings.HasPrefix(m.status, "Invalid service port:") {
		lines = append(lines, badStyle.Render("  "+m.status))
	}
	if strings.HasPrefix(m.status, "Invalid extra args:") {
		lines = append(lines, badStyle.Render("  "+m.status))
	}
	return lines
}

func (m Model) previewView(b *strings.Builder) {
	if len(m.launch.Services) > 0 && m.launch.UseDocker {
		compose, err := launcher.BuildCompose(m.launch)
		if err != nil {
			b.WriteString(badStyle.Render("Compose preview unavailable: "+launcher.SanitizeDisplay(err.Error())) + lineBreak)
			return
		}
		rendered, err := container.RenderCompose(compose)
		if err != nil {
			b.WriteString(badStyle.Render("Compose preview unavailable: "+launcher.SanitizeDisplay(err.Error())) + lineBreak)
			return
		}
		b.WriteString(mutedStyle.Render("Compose preview:\n" + rendered))
		b.WriteString(lineBreak)
		return
	}
	preview, err := launcher.Build(m.launch)
	if err != nil {
		return
	}
	safe := make([]string, len(preview))
	for i, part := range preview {
		safe[i] = launcher.SanitizeDisplay(part)
	}
	b.WriteString(mutedStyle.Render("Preview: " + strings.Join(safe, " ")))
	b.WriteString(lineBreak)
}

func (m Model) agentsView(b *strings.Builder) {
	b.WriteString("\n\nAgent\n")
	b.WriteString(mutedStyle.Render("  (installed only · most recently used first · Space/Enter select · r RUN)") + lineBreak)
	// Selected line (what will launch) vs cursor (what ↑/↓ is on).
	b.WriteString(goodStyle.Render("  Selected: "+m.selectedAgentLabel()) + lineBreak)

	m.continueRowView(b)
	if len(m.agents) == 0 {
		b.WriteString(badStyle.Render("  (no installed agents found — install one or check PATH)") + lineBreak)
	}
	for i, status := range m.agents {
		m.agentRowView(b, i, status)
	}
}

// selectedAgentLabel describes what will launch on the "Selected:" line.
func (m Model) selectedAgentLabel() string {
	if m.launch.ContinueSession {
		return "Continue last session"
	}
	cmd := strings.TrimSpace(m.launch.Agent.Command)
	if cmd == "" {
		return "none"
	}
	label := m.launch.Agent.Name
	if label == "" {
		label = cmd
	}
	return label + " (" + cmd + ")"
}

// continueRowView renders the always-present "Continue last session" row.
func (m Model) continueRowView(b *strings.Builder) {
	pointer := pointerEmpty
	if m.section == 0 && m.cursor == 0 {
		pointer = pointerSelected
	}
	sel := "[ ]"
	if m.launch.ContinueSession {
		sel = goodStyle.Render("[●]")
	}
	fmt.Fprintf(b, "%s%s %-22s (%s)\n", pointer, sel, "Continue last session", "ai-memory run")
}

// agentRowView renders one catalog agent row with cursor, selection mark and
// install/recency tag.
func (m Model) agentRowView(b *strings.Builder, index int, status catalog.AgentStatus) {
	pointer := pointerEmpty
	if m.section == 0 && index+1 == m.cursor {
		pointer = pointerSelected
	}
	command := status.Agent.Command
	if status.ResolvedCommand != "" && status.ResolvedCommand != status.Agent.Command {
		command += " via " + status.ResolvedCommand
	}
	sel := "[ ]"
	if !m.launch.ContinueSession && agentMatchesLaunch(status, m.launch) {
		sel = goodStyle.Render("[●]")
	}
	fmt.Fprintf(b, "%s%s %-18s (%s) %s\n", pointer, sel, status.Agent.Name, command, m.agentRowTag(status))
}

// agentRowTag renders the trailing status tag of an agent row.
func (m Model) agentRowTag(status catalog.AgentStatus) string {
	if !status.Installed {
		return badStyle.Render("· not installed")
	}
	if m.recentTop != "" && status.Agent.Command == m.recentTop {
		return goodStyle.Render("· last used")
	}
	return mutedStyle.Render("· ready")
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
		pointer := pointerEmpty
		if m.section == 1 && i == m.cursor {
			pointer = pointerSelected
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

// containerStackIDs returns the selectable stack ids in catalog order.
func containerStackIDs() []string {
	ids := make([]string, 0, len(container.Stacks))
	for _, stack := range container.Stacks {
		ids = append(ids, stack.ID)
	}
	return ids
}

// containerServiceIDs returns selectable service IDs in catalog order. The
// section uses the same flat order for cursor movement while rendering adds
// category headings around the rows.
func containerServiceIDs() []string {
	return container.ServiceIDs()
}

// selectedStacks returns the set of stacks currently in the docker selection,
// as a map for O(1) lookup during rendering.
func (m Model) selectedStacks() map[string]bool {
	selected := make(map[string]bool, len(m.launch.Docker.Selection.Stacks))
	for _, id := range m.launch.Docker.Selection.Stacks {
		selected[id] = true
	}
	return selected
}

func (m Model) containerResourceRows() []containerResourceRow {
	ports := make([]string, 0, len(m.launch.Docker.ExposedPorts))
	for _, mapping := range m.launch.Docker.ExposedPorts {
		ports = append(ports, mapping.DockerFlag())
	}
	runtime := container.RuntimeOrDefault(m.launch.Docker.Runtime)
	source := "auto-detected"
	if config.EffectiveContainerRuntime(m.launch.ContainerRuntime) != config.ContainerRuntimeAuto {
		source = "--container-runtime"
	}
	return []containerResourceRow{
		{name: "Memory", kind: resourceMemory, value: displayResourceValue(m.launch.Docker.MemoryLimit), hint: "512m / 2g"},
		{name: "CPU", kind: resourceCPU, value: displayResourceValue(m.launch.Docker.CPULimit), hint: "1.0 cores"},
		{name: "PIDs", kind: resourcePIDs, value: displayPIDs(m.launch.Docker.PIDsLimit), hint: "integer, e.g. 256"},
		{name: "Ports", kind: resourcePorts, value: displayResourceValue(strings.Join(ports, ",")), hint: "3000:3000[,udp]"},
		{name: "Network", kind: resourceNetwork, value: displayResourceValue(m.launch.Docker.NetworkName), hint: "bridge / host"},
		{name: "Runtime", kind: resourceRuntime, value: runtime.Name() + " (" + source + ")", hint: "auto / docker / podman / nerdctl"},
		{name: "Docker context", kind: resourceContext, value: displayResourceValue(m.launch.ContainerContext), hint: "empty = current; list with docker context ls"},
	}
}

func displayResourceValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(default)"
	}
	return value
}

func displayPIDs(value int64) string {
	if value <= 0 {
		return "(default)"
	}
	return strconv.FormatInt(value, 10)
}

// containerView renders the docker backend section: the stack checkboxes and
// a status line about the image. It is only rendered when the docker backend
// is active; otherwise the section does not exist in the layout.
func (m Model) containerView(b *strings.Builder) {
	b.WriteString("\nContainer\n")
	selected := m.selectedStacks()
	for i, id := range m.stackIDs {
		stack, _ := container.StackByID(id)
		pointer := pointerEmpty
		if m.section == m.containerIndex() && i == m.cursor {
			pointer = pointerSelected
		}
		mark := "[ ]"
		if selected[id] {
			mark = "[✓]"
		}
		fmt.Fprintf(b, "%s%s %s\n", pointer, mark, stack.Name)
	}
	m.sharedConfigView(b)
	b.WriteString(lineBreak + titleStyle.Render("Resources") + lineBreak)
	for index, row := range m.containerResourceRows() {
		pointer := pointerEmpty
		if m.section == m.containerIndex() && index+len(m.stackIDs) == m.cursor {
			pointer = pointerSelected
		}
		if row.kind == resourceRuntime {
			fmt.Fprintf(b, "%sRuntime: %s  %s\n", pointer, row.value, mutedStyle.Render("["+row.hint+"]"))
			continue
		}
		fmt.Fprintf(b, "%s%-10s: %s  %s\n", pointer, row.name, row.value, mutedStyle.Render("["+row.hint+"]"))
	}
}

func (m Model) selectedServices() map[string]bool {
	selected := make(map[string]bool, len(m.launch.Services))
	for _, id := range m.launch.Services {
		selected[id] = true
	}
	return selected
}

// servicesView renders the Docker-only infrastructure selector grouped by
// catalog category. Categories are headings, not cursor rows; every service
// row shows its ports and catalog persistence mappings so selection
// consequences are visible.
func (m Model) servicesView(b *strings.Builder) {
	b.WriteString("\nServices\n")
	grouped := container.ServicesByCategory()
	selected := m.selectedServices()
	start, end := m.serviceWindow()
	row := 0
	for _, category := range container.ServiceCategories() {
		row = m.renderServiceCategory(b, category, grouped[category], selected, row, start, end)
	}
	if start > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ↑ %d service(s) above", start)) + lineBreak)
	}
	if end < len(m.serviceIDs) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ↓ %d service(s) below", len(m.serviceIDs)-end)) + lineBreak)
	}
}

func (m Model) renderServiceCategory(b *strings.Builder, category container.ServiceCategory, services []container.Service, selected map[string]bool, row, start, end int) int {
	categoryRendered := false
	for _, service := range services {
		if row >= start && row < end {
			if !categoryRendered {
				b.WriteString("  " + string(category) + lineBreak)
				categoryRendered = true
			}
			m.renderServiceRow(b, service, selected[service.ID], row)
		}
		row++
	}
	return row
}

func (m Model) renderServiceRow(b *strings.Builder, service container.Service, selected bool, row int) {
	pointer := pointerEmpty
	if m.section == m.servicesIndex() && row == m.cursor {
		pointer = pointerSelected
	}
	mark := "[ ]"
	if selected {
		mark = "[✓]"
	}
	mappings := service.Ports
	if configured, ok := m.launch.ContainerServicePorts[service.ID]; ok {
		mappings = configured
	}
	ports := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		ports = append(ports, mapping.DockerFlag())
	}
	portText := "-"
	if len(ports) > 0 {
		portText = strings.Join(ports, ",")
	}
	details := []string{portText}
	if len(service.Volumes) > 0 {
		details = append(details, "volumes="+strings.Join(service.Volumes, ","))
		details = append(details, "data=.ai-launcher/data/"+service.ID)
	}
	fmt.Fprintf(b, "%s%s %-18s %s\n", pointer, mark, service.Name, strings.Join(details, "  "))
}

// serviceWindow returns the visible slice of the flat service cursor list.
// Tests without a terminal size render the complete catalog; a real short
// terminal gets a bounded window that follows the highlighted service.
func (m Model) serviceWindow() (int, int) {
	total := len(m.serviceIDs)
	if total == 0 || m.height <= 0 {
		return 0, total
	}
	visible := m.height / 2
	if visible < 6 {
		visible = 6
	}
	if visible >= total {
		return 0, total
	}
	start := m.cursor - visible/2
	if start < 0 {
		start = 0
	}
	if start+visible > total {
		start = total - visible
	}
	return start, start + visible
}

// sharedConfigView lists the host config directories the selected agent's
// login/sessions share with the container (read-write, same-path). It makes
// the mount map visible: which dirs are shared, and that the agent only sees
// its own — never another agent's credentials.
func (m Model) sharedConfigView(b *strings.Builder) {
	if m.launch.ContinueSession || strings.TrimSpace(m.launch.Agent.Command) == "" {
		return
	}
	mounts := container.AgentMounts(m.launch.HomeDir, []string{m.launch.Agent.Command}, container.ExistsOnHost)
	if len(mounts) == 0 {
		b.WriteString(mutedStyle.Render("  no shared config dirs for this agent") + lineBreak)
		return
	}
	b.WriteString(lineBreak + titleStyle.Render("Shared config (rw)") + lineBreak)
	for _, mount := range mounts {
		b.WriteString("  " + mount.HostPath + lineBreak)
	}
	b.WriteString(mutedStyle.Render("  login/sessions shared host ↔ container") + lineBreak)
	if m.launch.ContainerTmux.Enabled {
		b.WriteString(lineBreak + titleStyle.Render("Shared tmux config (ro)") + lineBreak)
		paths := container.ResolveTmuxMounts(m.launch.HomeDir, m.launch.ContainerTmux, container.ExistsOnHost)
		if len(paths) == 0 {
			b.WriteString(badStyle.Render("  enabled, but no tmux config was found") + lineBreak)
		} else {
			for _, path := range paths {
				b.WriteString("  " + path + lineBreak)
			}
			b.WriteString(mutedStyle.Render("  shortcuts/plugins are read-only; tmux socket stays inside the container") + lineBreak)
		}
	}
}

func (m Model) mountsView(b *strings.Builder) {
	b.WriteString("\nMounts\n")
	// Always visible (not only when the section is focused) so the user never
	// has to guess how to add folders.
	if !m.inputActive {
		b.WriteString(titleStyle.Render("  Tips") + lineBreak)
		b.WriteString(goodStyle.Render("    a  or  +  or  /     add a folder") + lineBreak)
		b.WriteString(mutedStyle.Render("    Space                toggle read-only ↔ read-write") + lineBreak)
		b.WriteString(mutedStyle.Render("    Backspace            remove highlighted mount") + lineBreak)
		b.WriteString(goodStyle.Render("    r                    RUN the selected agent") + lineBreak)
	}
	if len(m.launch.Mounts) == 0 && !m.inputActive {
		b.WriteString(goodStyle.Render("  (empty — press a to add a folder, or r to run without extra mounts)") + lineBreak)
	}
	for i, mount := range m.launch.Mounts {
		pointer := pointerEmpty
		if m.section == 2 && i == m.cursor && !m.inputActive {
			pointer = pointerSelected
		}
		mode := mount.Mode
		if mode == "" {
			mode = modeReadWriteID
		}
		modeLabel := "read-write"
		if strings.EqualFold(mode, modeReadOnlyID) || strings.EqualFold(mode, modeReadOnly) {
			modeLabel = modeReadOnly
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
	if m.mountMode == modeReadOnlyID {
		modeLabel = "read-only (agent can only read)"
	}
	b.WriteString(lineBreak)
	b.WriteString(titleStyle.Render("  ── Add a folder to the jail ──") + lineBreak)
	fmt.Fprintf(b, "  Path: %s█\n", m.mountInput)
	fmt.Fprintf(b, "  Mode: %s\n", modeLabel)
	fmt.Fprintf(b, "  Looking in: %s\n", m.mountDir)
	m.mountCommitHintView(b)
	b.WriteString(mutedStyle.Render("  Folders:") + lineBreak)
	m.mountEntriesView(b)
	b.WriteString(mutedStyle.Render("  Keys:  ↑/↓ move   → open folder   ← parent   Tab autocomplete") + lineBreak)
	b.WriteString(mutedStyle.Render("         type a path   Ctrl+T mode   Enter ADD   Esc cancel") + lineBreak)
}

// mountCommitHintView renders the "Enter will add" preview line.
func (m Model) mountCommitHintView(b *strings.Builder) {
	if willAdd := m.mountCommitPath(); willAdd != "" {
		b.WriteString(goodStyle.Render("  → Enter will add: "+willAdd) + lineBreak)
	} else {
		b.WriteString(badStyle.Render("  → Enter will add: (nothing selected — type a path or pick a folder)") + lineBreak)
	}
}

// mountEntriesView renders the visible window of the folder list.
func (m Model) mountEntriesView(b *strings.Builder) {
	if len(m.mountEntries) == 0 {
		b.WriteString("    (no folders match — type more of the path, or Tab to autocomplete)\n")
		return
	}
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
		b.WriteString(mountEntryRow(i, m.mountCursor, m.mountEntries[i]) + lineBreak)
	}
	if end < len(m.mountEntries) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("    … %d more\n", len(m.mountEntries)-end)))
	}
}

// mountEntryRow renders one browser row (cursor pointer + label).
// Filenames come from the checkout filesystem and are sanitized so a hostile
// name cannot inject terminal control sequences into the Bubble Tea frame.
func mountEntryRow(index, cursor int, entry string) string {
	pointer := "    "
	if index == cursor {
		pointer = "  ❯ "
	}
	safe := launcher.SanitizeDisplay(entry)
	label := safe + "/"
	if entry == ".." {
		label = "..  (parent folder)"
	}
	return pointer + label
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
	fixed := m.optionRows()
	for i, option := range fixed {
		pointer := pointerEmpty
		if m.section == 3 && i == m.cursor {
			pointer = pointerSelected
		}
		mark := "[ ]"
		if option.on {
			mark = "[✓]"
		}
		fmt.Fprintf(b, "%s%s %s\n", pointer, mark, option.name)
	}
	advanced := m.advancedOptionRows()
	for i, row := range advanced {
		pointer := pointerEmpty
		if m.section == 3 && m.cursor == len(fixed)+i {
			pointer = pointerSelected
		}
		value := launcher.SanitizeDisplay(row.value)
		if value == "" {
			value = mutedStyle.Render("(empty)")
		}
		fmt.Fprintf(b, "%s%s: %s %s\n", pointer, row.name, value, mutedStyle.Render("["+row.hint+"]"))
	}
	for i, param := range m.launch.Agent.Params {
		pointer := pointerEmpty
		if m.section == 3 && m.cursor == len(fixed)+len(advanced)+i {
			pointer = pointerSelected
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
		pointer := pointerEmpty
		if m.section == m.profilesIndex() && i == m.cursor {
			pointer = pointerSelected
		}
		profile := m.profiles[name]
		line := fmt.Sprintf("%s%-18s (%s)", pointer, name, profile.Agent)
		if summary := config.ProfileSummary(profile); summary != "" {
			line += " " + mutedStyle.Render(summary)
		}
		b.WriteString(line + lineBreak)
	}
}
