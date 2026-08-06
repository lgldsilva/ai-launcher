package container

import (
	"os"
	"path/filepath"
	"strings"
)

// AgentMount describes one credential/history directory shared with the
// container for an agent. HostPath is the path on the host; the container
// path is identical (same-path design decision): agents and ai-memory store
// absolute paths in their configs, so a different internal path would break
// session continuity and memory scoping.
type AgentMount struct {
	// Agent is the catalog command the mount belongs to ("claude", ...).
	Agent string
	// HostPath is the directory (or file) to mount, resolved from $HOME.
	HostPath string
	// ReadOnly is the default for credentials (design R9.2): the jail runs
	// $HOME as tmpfs, but docker shares the real host path, so write access
	// would let a compromised container mutate host credentials. Logs/history
	// that must persist both ways are mounted rw explicitly at the call site.
	ReadOnly bool
}

// agentConfigDirs maps each known agent command to the credential and history
// paths the launcher shares with the container. This is the authoritative
// table for R2 item 7, including the standalone ~/.claude.json file (R9.3:
// agentStateDirs in the launcher only covers ~/.claude, so the loose file
// needs its own entry here).
var agentConfigDirs = map[string][]string{
	"claude":   {"~/.claude", "~/.claude.json"},
	"codex":    {"~/.codex"},
	"opencode": {"~/.config/opencode", "~/.local/share/opencode"},
	"muse":     {"~/.muse"},
}

// AgentMounts returns the same-path mounts for the given agent commands,
// resolved against home. Unknown agents contribute nothing (their config
// locations are not modeled). Only paths that exist on the host are included
// — docker refuses a -v source that does not exist, and mounting a directory
// the user never created would silently persist nothing (R2 item 8).
//
// os.Stat is injectable for tests via the existingFiles parameter: nil means
// "assume every path exists" (used by the docker run builder, which mounts
// before any existence check), non-nil means "only mount paths in this set".
func AgentMounts(home string, commands []string, existingFiles func(string) bool) []AgentMount {
	var result []AgentMount
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if _, dup := seen[command]; dup {
			continue
		}
		seen[command] = struct{}{}
		for _, raw := range agentConfigDirs[command] {
			hostPath := ExpandHome(raw, home)
			if hostPath == "" {
				continue
			}
			if existingFiles != nil && !existingFiles(hostPath) {
				continue
			}
			result = append(result, AgentMount{
				Agent:    command,
				HostPath: hostPath,
				ReadOnly: true,
			})
		}
	}
	return result
}

// ExpandHome expands a leading ~/ (or bare ~) to home, and resolves relative
// paths under home so cache dirs like ".nvm" or ".cargo" land in the host
// home. Returns "" for empty input. Paths are cleaned so the generated -v
// flags are stable.
func ExpandHome(raw, home string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || home == "" {
		return ""
	}
	if raw == "~" {
		return filepath.Clean(home)
	}
	if strings.HasPrefix(raw, "~/") {
		return filepath.Clean(filepath.Join(home, raw[2:]))
	}
	if strings.HasPrefix(raw, "/") {
		return filepath.Clean(raw)
	}
	// Relative path (a cache dir under $HOME).
	return filepath.Clean(filepath.Join(home, raw))
}

// ExistsOnHost reports whether path exists; it is the default existence probe
// for callers that mount before docker runs. It is a variable so tests can
// stub the filesystem probe without touching the disk.
var ExistsOnHost = func(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// StackCacheMounts returns the host directories shared read-write with the
// container for the selected stacks: toolchain manager dirs (nvm, sdkman,
// cargo, rustup) and package caches (npm, go-build, m2, gradle, pip). Each is
// resolved under home and mounted at the identical path (same-path design), so
// downloads on either side are reused — the image does not re-download what
// the host has, and SDKs installed inside persist to the host. Only paths that
// exist on the host are included (docker refuses a missing -v source).
func StackCacheMounts(home string, stackIDs []string, exists func(string) bool) []string {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	if exists == nil {
		exists = ExistsOnHost
	}
	seen := make(map[string]struct{}, 4)
	var mounts []string
	for _, id := range stackIDs {
		stack, ok := StackByID(id)
		if !ok {
			continue
		}
		for _, raw := range stack.CacheDirs {
			hostPath := ExpandHome(raw, home)
			if hostPath == "" || !exists(hostPath) {
				continue
			}
			if _, dup := seen[hostPath]; dup {
				continue
			}
			seen[hostPath] = struct{}{}
			mounts = append(mounts, hostPath)
		}
	}
	return mounts
}
