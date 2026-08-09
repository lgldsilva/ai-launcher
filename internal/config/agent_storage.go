package config

import (
	"errors"
	"strings"
)

// UpsertAgent adds an agent or updates an existing entry with the same name
// or command. This makes repeated --add calls idempotent.
func UpsertAgent(global *Global, agent Agent) error {
	if global == nil {
		return errors.New("global config is nil")
	}
	agent.Name = strings.TrimSpace(agent.Name)
	agent.Command = strings.TrimSpace(agent.Command)
	agent.Path = strings.TrimSpace(agent.Path)
	if agent.Name == "" {
		return errors.New("agent name is required")
	}
	if agent.Command == "" {
		return errors.New("agent command is required")
	}
	for i, existing := range global.Agents {
		if existing.Name == agent.Name || existing.Command == agent.Command {
			global.Agents[i] = agent
			return nil
		}
	}
	global.Agents = append(global.Agents, agent)
	return nil
}
