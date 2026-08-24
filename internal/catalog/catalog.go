// Package catalog resolves configured agents and normalizes permission dependencies.
package catalog

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// LookPath resolves a command name to an executable path; it is a field on
// Catalog so tests can substitute discovery.
type LookPath func(string) (string, error)

// AgentStatus is the resolution result for one catalog agent: whether it is
// installed, where, and which candidate command matched.
type AgentStatus struct {
	Agent           config.Agent
	Path            string
	ResolvedCommand string
	Installed       bool
}

// Catalog resolves the configured agents against the host PATH.
type Catalog struct {
	Global   config.Global
	LookPath LookPath
}

// New builds a Catalog backed by exec.LookPath.
func New(global config.Global) Catalog {
	return Catalog{Global: global, LookPath: exec.LookPath}
}

// Agents returns the resolution status of every configured agent, probing
// the configured path first and then command and alias candidates.
func (c Catalog) Agents() []AgentStatus {
	result := make([]AgentStatus, 0, len(c.Global.Agents))
	for _, agent := range c.Global.Agents {
		status := AgentStatus{Agent: agent, ResolvedCommand: agent.Command}
		for _, candidate := range candidates(agent.Command, agent.Aliases, agent.Path) {
			path, err := c.lookPath(candidate)
			if err != nil {
				continue
			}
			status.Path = path
			status.ResolvedCommand = candidate
			status.Installed = true
			break
		}
		result = append(result, status)
	}
	return result
}

// Resolve finds an agent by command, name, or alias. Unknown commands are
// returned as a bare AgentStatus together with an error.
//
// Only the matching agent's candidates are probed on PATH. Probing the whole
// catalog (as Agents does for the TUI listing) made every CLI launch pay
// exec.LookPath for every agent × alias just to pick one — and run() calls
// Resolve twice per launch (selection + trust check).
func (c Catalog) Resolve(command string) (AgentStatus, error) {
	for _, agent := range c.Global.Agents {
		if !agentMatches(agent, command) {
			continue
		}
		status := AgentStatus{Agent: agent, ResolvedCommand: agent.Command}
		for _, candidate := range candidates(agent.Command, agent.Aliases, agent.Path) {
			path, err := c.lookPath(candidate)
			if err != nil {
				continue
			}
			status.Path = path
			status.ResolvedCommand = candidate
			status.Installed = true
			break
		}
		return status, nil
	}
	return AgentStatus{Agent: config.Agent{Name: command, Command: command}}, fmt.Errorf("agent %q is not in the catalog", command)
}

// agentMatches reports whether an agent answers to the given command,
// catalog name, or one of its aliases.
func agentMatches(agent config.Agent, command string) bool {
	return agent.Command == command || agent.Name == command || contains(agent.Aliases, command)
}

func candidates(command string, aliases []string, configuredPath string) []string {
	if configuredPath != "" {
		return []string{configuredPath}
	}
	if len(aliases) == 0 {
		trimmed := strings.TrimSpace(command)
		if trimmed == "" {
			return nil
		}
		return []string{trimmed}
	}
	result := make([]string, 0, len(aliases)+1)
	seen := make(map[string]struct{}, len(aliases)+1)
	for _, value := range append([]string{command}, aliases...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// Permission returns the catalog permission with the given ID.
func (c Catalog) Permission(id string) (config.Permission, bool) {
	for _, permission := range c.Global.Permissions {
		if permission.ID == id {
			return permission, true
		}
	}
	return config.Permission{}, false
}

// NormalizePermissions enables dependencies for enabled permissions and removes
// dependents when a parent is disabled. Locked defaults are always retained.
func (c Catalog) NormalizePermissions(selected map[string]bool) map[string]bool {
	result := c.applyDefaults()
	c.applySelection(result, selected)
	c.propagateRequires(result)
	c.stripOrphans(result)
	return result
}

func (c Catalog) applyDefaults() map[string]bool {
	result := make(map[string]bool, len(c.Global.Permissions))
	for _, permission := range c.Global.Permissions {
		result[permission.ID] = permission.Default
	}
	return result
}

func (c Catalog) applySelection(result map[string]bool, selected map[string]bool) {
	for id, enabled := range selected {
		if _, ok := c.Permission(id); ok {
			result[id] = enabled
		}
	}
	for _, permission := range c.Global.Permissions {
		if permission.Locked {
			result[permission.ID] = true
		}
	}
}

// propagateRequires enables every transitive dependency of an enabled
// permission until a fixed point is reached.
func (c Catalog) propagateRequires(result map[string]bool) {
	changed := true
	for changed {
		changed = false
		for _, permission := range c.Global.Permissions {
			if !result[permission.ID] {
				continue
			}
			for _, required := range permission.Requires {
				if !result[required] {
					result[required] = true
					changed = true
				}
			}
		}
	}
}

// stripOrphans turns off permissions that require a disabled one.
func (c Catalog) stripOrphans(result map[string]bool) {
	for _, permission := range c.Global.Permissions {
		if result[permission.ID] {
			continue
		}
		for _, other := range c.Global.Permissions {
			if !result[other.ID] && contains(other.Requires, permission.ID) {
				result[other.ID] = false
			}
		}
	}
}

func (c Catalog) lookPath(command string) (string, error) {
	if c.LookPath == nil {
		return exec.LookPath(command)
	}
	return c.LookPath(command)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
