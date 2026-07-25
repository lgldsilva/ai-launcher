// Package catalog resolves configured agents and normalizes permission dependencies.
package catalog

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

type LookPath func(string) (string, error)

type AgentStatus struct {
	Agent           config.Agent
	Path            string
	ResolvedCommand string
	Installed       bool
}

type Catalog struct {
	Global   config.Global
	LookPath LookPath
}

func New(global config.Global) Catalog {
	return Catalog{Global: global, LookPath: exec.LookPath}
}

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

func (c Catalog) Resolve(command string) (AgentStatus, error) {
	for _, status := range c.Agents() {
		if status.Agent.Command == command || status.Agent.Name == command || contains(status.Agent.Aliases, command) {
			return status, nil
		}
	}
	return AgentStatus{Agent: config.Agent{Name: command, Command: command}}, fmt.Errorf("agent %q is not in the catalog", command)
}

func candidates(command string, aliases []string, configuredPath string) []string {
	if configuredPath != "" {
		return []string{configuredPath}
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
	result := make(map[string]bool, len(c.Global.Permissions))
	for _, permission := range c.Global.Permissions {
		result[permission.ID] = permission.Default
	}
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
	return result
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
