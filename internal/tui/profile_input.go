package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// loadProfile replaces the current selection with a saved profile, keeping
// the local values for every block the profile omits.
func (m *Model) loadProfile(name string) {
	profile, ok := m.profiles[name]
	if !ok {
		return
	}
	var profileRuntime container.Runtime
	if profile.Options != nil && profile.Options.Docker {
		var err error
		profileRuntime, err = detectContainerRuntime(config.EffectiveContainerRuntime(profile.Options.ContainerRuntime))
		if err != nil {
			m.status = "Profile runtime unavailable: " + err.Error()
			return
		}
	}
	if strings.TrimSpace(profile.Agent) != "" {
		status, err := m.catalog.Resolve(profile.Agent)
		agent := status.Agent
		executable := status.Path
		if err != nil {
			agent = config.Agent{Name: profile.Agent, Command: profile.Agent}
			executable = ""
		} else if status.ResolvedCommand != "" {
			agent.CatalogCommand = agent.Command
			agent.Command = status.ResolvedCommand
		}
		m.launch.Agent = agent
		m.launch.Executable = executable
		m.syncMemoryForAgent()
		m.syncYoloForAgent()
	}
	if profile.Permissions != nil {
		m.launch.Permissions = m.catalog.NormalizePermissions(profile.Permissions)
	}
	if profile.Mounts != nil {
		m.launch.Mounts = append([]config.Mount(nil), profile.Mounts...)
	}
	if profile.Options != nil {
		m.launch.UseDocker = profile.Options.Docker
		m.launch.ContainerRuntime = config.EffectiveContainerRuntime(profile.Options.ContainerRuntime)
		m.launch.ContainerContext = strings.TrimSpace(profile.Options.ContainerContext)
		m.launch.Docker.Runtime = profileRuntime
		m.launch.Docker.Context = m.launch.ContainerContext
		m.launch.Docker.Selection.Stacks = append([]string(nil), profile.Options.Stacks...)
		m.launch.Docker.Tmux = profile.Options.ContainerTmux.Enabled && m.launch.Docker.Interactive
		m.launch.Services = append([]string(nil), profile.Options.Services...)
		m.launch.ContainerEnvironment = copyStringMap(profile.Options.ContainerEnvironment)
		m.launch.ContainerServicePorts = copyServicePortMappings(profile.Options.ContainerServicePorts)
		m.launch.ContainerTmux = profile.Options.ContainerTmux
		m.launch.Docker.MemoryLimit = profile.Options.ContainerMemory
		m.launch.Docker.CPULimit = profile.Options.ContainerCPUs
		m.launch.Docker.PIDsLimit = profile.Options.ContainerPIDs
		m.launch.Docker.ExposedPorts = append([]container.PortMapping(nil), profile.Options.ContainerPorts...)
		m.launch.Docker.NetworkName = profile.Options.ContainerNetwork
		m.launch.Docker.Selection.Agents = []container.AgentInstall{
			planDockerAgentInstall(m.launch.HomeDir, m.launch.Agent, m.launch.Executable),
		}
		m.launch.UseJail = profile.Options.Jail && !m.launch.UseDocker && !isWindows()
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
	// After the options block, because whether the auto-mounts are needed at all
	// depends on the UseJail the profile just set.
	m.reapplyAutoMounts()
	m.status = "Profile loaded: " + name
}

// reapplyAutoMounts re-merges the home dotfile symlink targets that escape
// $HOME (ARCHITECTURE invariant 9). The launcher merges them once before the
// TUI opens, but a profile replaces the mount list wholesale, so without this
// loading any profile with a mounts block left ~/.cache and friends dangling
// inside the jail — a silent regression of the very bug the invariant fixes.
//
// MergeAutoMounts skips targets an existing mount already covers, so a profile
// that maps a parent directory is not duplicated. With the jail off there is no
// tmpfs $HOME and nothing dangles, so nothing is added.
func (m *Model) reapplyAutoMounts() {
	if !m.launch.UseJail || len(m.autoMounts) == 0 {
		return
	}
	m.launch.Mounts = launcher.MergeAutoMounts(m.launch.Mounts, m.autoMounts)
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
	case keyEscape:
		m.textInputActive = false
		m.status = "Edit cancelled"
	case keyEnter:
		m.commitTextInput()
	case keyBackspace:
		if m.textInputValue != "" {
			runes := []rune(m.textInputValue)
			m.textInputValue = string(runes[:len(runes)-1])
		}
		if m.textInputKind == string(resourceContext) {
			m.contextCursor = -1
		}
		if m.textInputKind == string(resourceRuntime) {
			m.runtimeCursor = -1
		}
	case keyUp, keyDown:
		if m.textInputKind == string(resourceContext) {
			delta := -1
			if key.String() == keyDown {
				delta = 1
			}
			m.moveContextCursor(delta)
		}
		if m.textInputKind == string(resourceRuntime) {
			delta := -1
			if key.String() == keyDown {
				delta = 1
			}
			m.moveRuntimeCursor(delta)
		}
	default:
		if key.Type == tea.KeyRunes {
			m.textInputValue += string(key.Runes)
			if m.textInputKind == string(resourceContext) {
				m.contextCursor = -1
			}
			if m.textInputKind == string(resourceRuntime) {
				m.runtimeCursor = -1
			}
		}
	}
	return *m, nil
}

// commitTextInput applies the active text input: a param value is stored in
// param_values (empty clears it), a profile name saves the selection via the
// SaveProfile hook.
func (m *Model) commitTextInput() {
	value := strings.TrimSpace(m.textInputValue)
	if m.textInputKind == "profile" {
		m.textInputActive = false
		m.saveProfileAs(value)
		return
	}
	if strings.HasPrefix(m.textInputKind, "service-ports:") {
		serviceID := strings.TrimPrefix(m.textInputKind, "service-ports:")
		if err := m.commitServicePortOverride(serviceID, value); err != nil {
			m.textInputActive = true
			m.status = "Invalid service port: " + err.Error()
			return
		}
		m.textInputActive = false
		m.status = "Service ports updated"
		return
	}
	if strings.HasPrefix(m.textInputKind, "container-") {
		if err := m.commitContainerResource(containerResourceKind(m.textInputKind), value); err != nil {
			m.textInputActive = true
			m.status = "Invalid container resource: " + err.Error()
			return
		}
		m.textInputActive = false
		m.status = "Container resource updated"
		return
	}
	if m.advancedOptionKindActive() {
		kind := advancedOptionKind(m.textInputKind)
		if kind == advancedExtraArgs {
			args, err := launcher.SplitArgs(value)
			if err != nil {
				m.textInputActive = true
				m.status = "Invalid extra args: " + err.Error()
				return
			}
			m.launch.ExtraArgs = args
			m.textInputActive = false
			m.status = "Extra agent args updated"
			return
		}
		switch kind {
		case advancedNewWorkstream:
			m.launch.NewWorkstream = value
		case advancedWorkstream:
			m.launch.Workstream = value
		case advancedWorkspace:
			m.launch.Workspace = value
		case advancedProject:
			m.launch.Project = value
		default:
			break
		}
		m.textInputActive = false
		row, _ := m.advancedOption(kind)
		m.status = row.name + " updated"
		return
	}
	m.textInputActive = false
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
			Jail:                  m.launch.UseJail,
			Docker:                m.launch.UseDocker,
			ContainerRuntime:      config.EffectiveContainerRuntime(m.launch.ContainerRuntime),
			ContainerContext:      strings.TrimSpace(m.launch.ContainerContext),
			Stacks:                append([]string(nil), m.launch.Docker.Selection.Stacks...),
			Services:              append([]string(nil), m.launch.Services...),
			ContainerEnvironment:  copyStringMap(m.launch.ContainerEnvironment),
			ContainerServicePorts: copyServicePortMappings(m.launch.ContainerServicePorts),
			ContainerTmux:         m.launch.ContainerTmux,
			ContainerMemory:       m.launch.Docker.MemoryLimit,
			ContainerCPUs:         m.launch.Docker.CPULimit,
			ContainerPIDs:         m.launch.Docker.PIDsLimit,
			ContainerPorts:        append([]config.PortMapping(nil), m.launch.Docker.ExposedPorts...),
			ContainerNetwork:      m.launch.Docker.NetworkName,
			Memory:                m.launch.UseMemory,
			Yolo:                  m.launch.Yolo,
			Fresh:                 m.launch.Fresh,
			NewWorkstream:         m.launch.NewWorkstream,
			Workstream:            m.launch.Workstream,
			Workspace:             m.launch.Workspace,
			Project:               m.launch.Project,
			JailFlags:             m.launch.JailFlags,
			ExtraArgs:             m.launch.ExtraArgs,
			ParamValues:           m.launch.ParamValues,
		},
	}
	if !containsString(m.profileNames, name) {
		m.profileNames = append(m.profileNames, name)
		sort.Strings(m.profileNames)
	}
	m.status = "Profile saved: " + name
}
