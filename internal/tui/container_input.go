package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
)

// toggleOrEditContainerResource routes the row under the cursor by its role:
// a toggle row flips in place via its closure, a text row opens the editor.
func (m *Model) toggleOrEditContainerResource() {
	index := m.cursor - len(m.stackIDs)
	rows := m.containerResourceRows()
	if index < 0 || index >= len(rows) {
		return
	}
	if row := rows[index]; row.role == containerRowToggle {
		if row.toggle != nil {
			row.toggle(m)
		}
		return
	}
	m.startContainerResourceInput()
}

func (m *Model) startContainerResourceInput() {
	index := m.cursor - len(m.stackIDs)
	rows := m.containerResourceRows()
	if index < 0 || index >= len(rows) {
		return
	}
	row := rows[index]
	m.textInputKind = newContainerResourceTextInput(row.kind)
	switch row.kind {
	case resourceRuntime:
		m.textInputValue = config.EffectiveContainerRuntime(m.launch.ContainerRuntime)
	default:
		m.textInputValue = containerResourceInputValue(row.kind, m.launch.Docker)
	}
	m.textInputActive = true
	status := "Editing " + row.name + " · " + containerResourceEditHint(row.kind) + " · Enter confirms · Esc cancels"
	switch row.kind {
	case resourceRuntime:
		m.loadContainerRuntimes()
		m.runtimeCursor = m.runtimeChoiceIndex(m.textInputValue)
		status += " · ↑/↓ select a runtime; unavailable entries cannot be applied"
	case resourceContext:
		if err := m.loadDockerContexts(); err != nil {
			status += " · contexts unavailable: " + err.Error()
		} else {
			m.contextCursor = m.contextChoiceIndex(m.textInputValue)
			status += " · ↑/↓ select a listed context"
		}
	}
	m.status = status
}

// loadContainerRuntimes loads the PATH availability snapshot once per TUI
// session. It is intentionally cheap and synchronous: it only checks the
// executable names, never starts a daemon.
func (m *Model) loadContainerRuntimes() {
	if m.containerRuntimesLoaded {
		return
	}
	m.containerRuntimesLoaded = true
	m.containerRuntimes = listContainerRuntimeStatuses()
}

// runtimeChoices keeps auto first and then presents every supported runtime so
// an unavailable saved choice remains visible and diagnosable instead of
// disappearing from the editor.
func (m Model) runtimeChoices() []string {
	choices := make([]string, 0, len(m.containerRuntimes)+1)
	choices = append(choices, config.ContainerRuntimeAuto)
	for _, status := range m.containerRuntimes {
		choices = append(choices, status.Name)
	}
	return choices
}

func (m Model) runtimeAvailable(name string) bool {
	if name == config.ContainerRuntimeAuto {
		return true
	}
	for _, status := range m.containerRuntimes {
		if status.Name == name {
			return status.Available
		}
	}
	return false
}

func runtimeChoiceLabel(name string, available bool) string {
	if name == config.ContainerRuntimeAuto {
		return "auto"
	}
	if !available {
		return name + " (unavailable)"
	}
	return name + " ✓"
}

func (m Model) runtimeChoiceIndex(value string) int {
	value = config.EffectiveContainerRuntime(value)
	for index, choice := range m.runtimeChoices() {
		if choice == value {
			return index
		}
	}
	return -1
}

func (m *Model) moveRuntimeCursor(delta int) {
	choices := m.runtimeChoices()
	if len(choices) == 0 {
		return
	}
	if m.runtimeCursor < 0 || m.runtimeCursor >= len(choices) {
		m.runtimeCursor = 0
	}
	m.runtimeCursor = (m.runtimeCursor + delta + len(choices)) % len(choices)
	m.textInputValue = choices[m.runtimeCursor]
}

// loadDockerContexts loads the read-only context list once per TUI session.
// A failed lookup does not block manual entry: Docker context names can still
// be typed and are validated when the editor is committed.
func (m *Model) loadDockerContexts() error {
	if m.containerContextsLoaded {
		return nil
	}
	m.containerContextsLoaded = true
	if container.RuntimeOrDefault(m.launch.Docker.Runtime).Name() != (container.DockerRuntime{}).Name() {
		return fmt.Errorf("context picker is only available for docker")
	}
	contexts, err := listDockerContexts()
	if err != nil {
		return err
	}
	m.containerContexts = contexts
	return nil
}

// contextChoices keeps the empty entry first because it means "Docker's
// current context" and avoids making the launcher change global Docker state.
func (m Model) contextChoices() []string {
	choices := make([]string, 0, len(m.containerContexts)+1)
	choices = append(choices, "")
	choices = append(choices, m.containerContexts...)
	return choices
}

func (m Model) contextChoiceIndex(value string) int {
	for index, choice := range m.contextChoices() {
		if choice == strings.TrimSpace(value) {
			return index
		}
	}
	return -1
}

func contextChoiceLabel(value string) string {
	if value == "" {
		return "(current)"
	}
	return value
}

func (m *Model) moveContextCursor(delta int) {
	choices := m.contextChoices()
	if len(choices) == 0 {
		return
	}
	if m.contextCursor < 0 || m.contextCursor >= len(choices) {
		m.contextCursor = 0
	}
	m.contextCursor = (m.contextCursor + delta + len(choices)) % len(choices)
	m.textInputValue = choices[m.contextCursor]
}

func containerResourceLabel(kind containerResourceKind) string {
	switch kind {
	case resourceMemory:
		return "Memory limit"
	case resourceCPU:
		return "CPU limit"
	case resourcePIDs:
		return "PID limit"
	case resourcePorts:
		return "Published ports"
	case resourceNetwork:
		return "Network"
	case resourceRuntime:
		return "Container runtime"
	case resourceContext:
		return "Docker context"
	default:
		return string(kind)
	}
}

func containerResourceEditHint(kind containerResourceKind) string {
	switch kind {
	case resourceMemory:
		return "positive integer + b/k/m/g, e.g. 512m or 2g; minimum 6m; empty = default"
	case resourceCPU:
		return "positive decimal cores, e.g. 1.0 or 2; empty = default"
	case resourcePIDs:
		return "positive integer, e.g. 256; empty = default"
	case resourcePorts:
		return "host:container[/tcp|udp], comma-separated, e.g. 3000:3000, 5353:53/udp; empty = none"
	case resourceNetwork:
		return "network name, e.g. bridge or host; empty = default"
	case resourceRuntime:
		return "auto, docker, podman, or nerdctl; ↑/↓ lists PATH availability"
	case resourceContext:
		return "Docker context name; empty = current; list with docker context ls"
	default:
		return ""
	}
}

func containerResourceInputValue(kind containerResourceKind, run container.RunConfig) string {
	switch kind {
	case resourceMemory:
		return run.MemoryLimit
	case resourceCPU:
		return run.CPULimit
	case resourcePIDs:
		if run.PIDsLimit > 0 {
			return strconv.FormatInt(run.PIDsLimit, 10)
		}
	case resourcePorts:
		values := make([]string, 0, len(run.ExposedPorts))
		for _, mapping := range run.ExposedPorts {
			values = append(values, mapping.DockerFlag())
		}
		return strings.Join(values, ",")
	case resourceNetwork:
		return run.NetworkName
	case resourceContext:
		return run.Context
	}
	return ""
}

func (m *Model) commitContainerResource(kind containerResourceKind, value string) error {
	updated := m.launch.Docker
	switch kind {
	case resourceMemory:
		updated.MemoryLimit = value
	case resourceCPU:
		updated.CPULimit = value
	case resourcePIDs:
		if value == "" {
			updated.PIDsLimit = 0
		} else {
			pids, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("PIDs must be an integer: %w", err)
			}
			updated.PIDsLimit = pids
		}
	case resourcePorts:
		if value == "" {
			updated.ExposedPorts = nil
		} else {
			parts := strings.Split(value, ",")
			ports, err := container.ParsePortMappings(parts)
			if err != nil {
				return err
			}
			updated.ExposedPorts = ports
		}
	case resourceNetwork:
		updated.NetworkName = value
	case resourceRuntime:
		preference := config.EffectiveContainerRuntime(value)
		runtime, err := detectContainerRuntime(preference)
		if err != nil {
			return err
		}
		updated.Runtime = runtime
		m.launch.ContainerRuntime = preference
		// A Docker context has no meaning for Podman or nerdctl. Clear it as
		// part of the same edit so the TUI cannot leave an invalid combination
		// that only fails later during launch.
		if runtime.Name() != (container.DockerRuntime{}).Name() {
			m.launch.ContainerContext = ""
			updated.Context = ""
		}
	case resourceContext:
		value = strings.TrimSpace(value)
		if err := container.ValidateContext(m.launch.Docker.Runtime, value); err != nil {
			return err
		}
		m.launch.ContainerContext = value
		updated.Context = value
	default:
		return fmt.Errorf("unknown container resource %q", kind)
	}
	if err := container.ValidateRunResources(updated); err != nil {
		return err
	}
	m.launch.Docker = updated
	m.launch.UseDocker = true
	m.launch.UseJail = false
	return nil
}

func servicePortEditHint() string {
	return "host:container[/tcp|udp], comma-separated; empty = no host publication"
}

func servicePortInputValue(service container.Service, overrides map[string][]config.PortMapping) string {
	mappings := service.Ports
	if configured, ok := overrides[service.ID]; ok {
		mappings = configured
	}
	values := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		values = append(values, mapping.DockerFlag())
	}
	return strings.Join(values, ",")
}

func (m *Model) commitServicePortOverride(serviceID, value string) error {
	service, ok := container.ServiceByID(serviceID)
	if !ok {
		return fmt.Errorf("unknown compose service %q", serviceID)
	}
	var mappings []config.PortMapping
	if value != "" {
		parts := strings.Split(value, ",")
		parsed, err := container.ParsePortMappings(parts)
		if err != nil {
			return err
		}
		mappings = parsed
	}
	overrides := copyServicePortMappings(m.launch.ContainerServicePorts)
	if overrides == nil {
		overrides = make(map[string][]config.PortMapping)
	}
	overrides[serviceID] = mappings
	if _, err := container.ServiceWithPortOverride(service, overrides); err != nil {
		return err
	}
	m.launch.ContainerServicePorts = overrides
	m.launch.UseDocker = true
	m.launch.UseJail = false
	return nil
}
