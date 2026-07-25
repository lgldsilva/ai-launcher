// Package launcher builds and validates the argv used to start an AI agent.
package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// Environment returns the inherited environment with the configured ai-memory
// server URL applied to child processes. ai-memory reads this variable for its
// runtime wrapper and generated integrations.
func Environment(cfg LaunchConfig) []string {
	env := append([]string(nil), os.Environ()...)
	if cfg.UseMemory && strings.TrimSpace(cfg.MemoryServerURL) != "" {
		const key = "AI_MEMORY_SERVER_URL="
		configured := key + strings.TrimSpace(cfg.MemoryServerURL)
		filtered := env[:0]
		for _, entry := range env {
			if strings.HasPrefix(entry, key) {
				continue
			}
			filtered = append(filtered, entry)
		}
		env = append(filtered, configured)
	}
	return env
}

type LaunchConfig struct {
	Agent           config.Agent
	Executable      string
	HomeDir         string
	MemoryServerURL string
	UseJail         bool
	UseMemory       bool
	NewWorkstream   string
	Permissions     map[string]bool
	Mounts          []config.Mount
	Yolo            bool
	ExtraArgs       []string
}

// Build returns argv without executing anything. This is deliberately pure so
// dry-run, table tests, and future frontends all share the same behavior.
func Build(cfg LaunchConfig) ([]string, error) {
	command := make([]string, 0, 12+len(cfg.Mounts)+len(cfg.ExtraArgs))
	if strings.TrimSpace(cfg.Agent.Command) == "" {
		return nil, errors.New("agent command is required")
	}
	if cfg.UseJail {
		command = append(command, "ai-jail")
		if cfg.Permissions["ssh"] {
			command = append(command, "--ssh")
		}
		if cfg.Permissions["gh"] {
			home := cfg.HomeDir
			if home == "" {
				home = os.Getenv("HOME")
			}
			command = append(command, "--rw-map", filepathOrEmpty(home, ".config/gh"))
		}
		if cfg.Permissions["docker"] {
			command = append(command, "--docker")
		}
		if cfg.Permissions["gpu"] {
			command = append(command, "--gpu")
		}
		for _, mount := range cfg.Mounts {
			path := strings.TrimSpace(mount.Path)
			if path == "" {
				continue
			}
			if strings.EqualFold(mount.Mode, "ro") || strings.EqualFold(mount.Mode, "read-only") {
				command = append(command, "--map", path)
			} else {
				command = append(command, "--rw-map", path)
			}
		}
	}
	if cfg.UseMemory {
		command = append(command, "ai-memory", "run")
		if cfg.NewWorkstream != "" {
			command = append(command, "--new", cfg.NewWorkstream)
		}
		command = append(command, cfg.Agent.Command)
		if cfg.Executable != "" {
			command = append(command, "--executable", cfg.Executable)
		}
	} else {
		executable := cfg.Agent.Command
		if cfg.Executable != "" {
			executable = cfg.Executable
		}
		command = append(command, executable)
	}
	if cfg.Yolo {
		command = append(command, "--yolo")
	}
	command = append(command, cfg.ExtraArgs...)
	return command, nil
}

func filepathOrEmpty(home, suffix string) string {
	if home == "" {
		return suffix
	}
	return strings.TrimRight(home, "/") + "/" + suffix
}

type Issue struct {
	Code    string
	Message string
}

func (i Issue) Error() string { return fmt.Sprintf("%s: %s", i.Code, i.Message) }

type Validator struct {
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
}

func NewValidator() Validator {
	return Validator{LookPath: exec.LookPath, Stat: os.Stat}
}

func (v Validator) Validate(cfg LaunchConfig) []Issue {
	issues := make([]Issue, 0)
	lookPath := v.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	stat := v.Stat
	if stat == nil {
		stat = os.Stat
	}
	agentPath := cfg.Agent.Command
	if cfg.Executable != "" {
		agentPath = cfg.Executable
	}
	if _, err := lookPath(agentPath); err != nil {
		issues = append(issues, Issue{Code: "agent-not-found", Message: fmt.Sprintf("%q is not available in PATH", cfg.Agent.Command)})
	}
	if cfg.UseJail {
		if _, err := lookPath("ai-jail"); err != nil {
			issues = append(issues, Issue{Code: "jail-not-found", Message: "ai-jail is required when sandboxing is enabled"})
		}
	}
	if cfg.UseMemory {
		if _, err := lookPath("ai-memory"); err != nil {
			issues = append(issues, Issue{Code: "memory-not-found", Message: "ai-memory is required when memory integration is enabled"})
		}
	}
	for _, mount := range cfg.Mounts {
		if strings.TrimSpace(mount.Path) == "" {
			continue
		}
		if _, err := stat(mount.Path); err != nil {
			issues = append(issues, Issue{Code: "mount-not-found", Message: fmt.Sprintf("mount path %q does not exist", mount.Path)})
		}
	}
	if cfg.Permissions["ssh"] || cfg.Permissions["gh"] || cfg.Permissions["docker"] || cfg.Permissions["gpu"] {
		if !cfg.UseJail {
			issues = append(issues, Issue{Code: "permission-without-jail", Message: "ssh, gh, docker, and gpu permissions require ai-jail"})
		}
	}
	if cfg.Permissions["gpu"] && !cfg.Permissions["docker"] {
		issues = append(issues, Issue{Code: "gpu-without-docker", Message: "gpu permission requires docker"})
	}
	return issues
}
