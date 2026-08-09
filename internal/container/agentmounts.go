package container

import (
	"os"
	"path/filepath"
	"runtime"
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
	// HostPath is mounted read-write: each agent mounts ONLY its own config
	// dirs, so the container reads/writes its own login and sessions. This is
	// the shared-login model: a login made inside a container persists to the
	// host and to every other container sharing the same host paths (and the
	// host sees sessions the container wrote). The container does not see
	// other agents' config dirs.
}

// ConfigDir describes one agent config location. Path is relative to $HOME;
// Platforms restricts where it applies (empty = every platform). Every dir is
// mounted read-write: the shared-login model — the agent owns these paths,
// reads and writes its own login/sessions, and they persist to the host and
// to other containers sharing the same host paths.
type ConfigDir struct {
	Path      string
	Platforms []string // empty = all platforms
}

// Platform keys mirror config.Platform* where they exist.
const (
	platformLinux   = "linux"
	platformDarwin  = "darwin"
	platformWindows = "windows"
)

// agentConfigDirs maps each known agent command to the credential and history
// paths the launcher shares with the container, per platform. This is the
// authoritative table for R2 item 7, including the standalone ~/.claude.json
// file (R9.3: agentStateDirs in the launcher only covers ~/.claude, so the
// loose file needs its own entry here).
//
// Platform coverage: the docker backend runs linux containers, but the host
// may be macOS, Linux, or Windows (Docker Desktop). The same-path mount uses
// the HOST path verbatim — a macOS host mounts /Users/me/.claude, a Windows
// host mounts C:\Users\me\.claude (mounted via the container's path mapping).
// Paths listed without Platforms apply everywhere; platform-specific entries
// (e.g. Windows %APPDATA%) let the resolver pick the right location per host.
var agentConfigDirs = map[string][]ConfigDir{
	"claude": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.claude.
		{Path: "~/.claude"},
		// empirically verified: Linux container run on 2026-08-06 writes ~/.claude.json.
		{Path: "~/.claude.json"},
		// empirically verified: Linux container run on 2026-08-06 writes ~/.claude/projects.
		{Path: "~/.claude/projects", Platforms: []string{platformLinux, platformDarwin}},
	},
	"codex": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.codex.
		{Path: "~/.codex"},
	},
	"opencode": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.config/opencode.
		{Path: "~/.config/opencode"},
		// empirically verified: Linux container run on 2026-08-06 writes ~/.local/share/opencode.
		{Path: "~/.local/share/opencode"},
		// empirically verified: Linux container run on 2026-08-06 writes ~/.local/state/opencode.
		{Path: "~/.local/state/opencode"},
	},
	"kimi": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.kimi.
		{Path: "~/.kimi"},
	},
	"kilo": {
		// guessed: needs verification for the host-binary fallback.
		{Path: "~/.kilo"},
	},
	"mimo": {
		// guessed: needs verification for the host-binary fallback.
		{Path: "~/.mimo"},
	},
	"agy": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.gemini/antigravity-cli.
		{Path: "~/.gemini/antigravity-cli"},
		// empirically verified: Linux container run on 2026-08-06 writes ~/.gemini/config.
		{Path: "~/.gemini/config"},
	},
	"pi": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.pi.
		{Path: "~/.pi"},
	},
	"crush": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.local/share/crush.
		{Path: "~/.local/share/crush"},
	},
	"omp": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.omp.
		{Path: "~/.omp"},
	},
	"cursor-agent": {
		// empirically verified: Linux container run on 2026-08-07 writes ~/.cursor.
		{Path: "~/.cursor"},
	},
	"grok": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.grok.
		{Path: "~/.grok"},
	},
	"zero": {
		// guessed: needs verification for the host-binary fallback.
		{Path: "~/.config/zero"},
	},
	"devin": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.config/devin.
		{Path: "~/.config/devin"},
		// empirically verified: Linux container run on 2026-08-06 writes ~/.local/share/devin.
		{Path: "~/.local/share/devin"},
	},
	"oc": {
		// guessed: needs verification for the host-binary preset selector.
		{Path: "~/.config/opencode"},
	},
	"gemini": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.gemini.
		{Path: "~/.gemini"},
	},
	"qwen": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.qwen.
		{Path: "~/.qwen"},
	},
	"aider": {
		// guessed: needs verification for the host-binary fallback.
		{Path: "~/.aider.conf.yml"},
		// guessed: needs verification for the host-binary fallback.
		{Path: "~/.aider"},
	},
	"goose": {
		// guessed: needs verification for the host-binary fallback.
		{Path: "~/.config/goose"},
	},
	"kiro-cli": {
		// guessed: needs verification for the host-binary fallback.
		{Path: "~/.kiro"},
	},
	"openclaw": {
		// empirically verified: Linux onboard run on 2026-08-06 writes ~/.openclaw.
		{Path: "~/.openclaw"},
	},
	"hermes": {
		// guessed: needs verification for the host-binary fallback.
		{Path: "~/.hermes"},
	},
	"cline": {
		// empirically verified: Linux container run on 2026-08-06 writes ~/.cline.
		{Path: "~/.cline"},
	},
	"semidx": {
		// semidx stores its endpoint/token configuration here. Mount only this
		// tool-owned directory, never the host's complete ~/.config tree.
		{Path: "~/.config/semidx"},
		// The optional local index/cache is persisted separately from config.
		{Path: "~/.cache/semidx"},
	},
}

// AgentMounts returns the same-path mounts for the given agent commands,
// resolved against home for the host platform. Unknown agents contribute
// nothing (their config locations are not modeled). Only paths that exist on
// the host are included — docker refuses a -v source that does not exist, and
// mounting a directory the user never created would silently persist nothing
// (R2 item 8).
//
// goos selects the platform-specific entries ("" = the calling host's GOOS).
// os.Stat is injectable for tests via the existingFiles parameter: nil means
// "assume every path exists" (used by the docker run builder, which mounts
// before any existence check), non-nil means "only mount paths in this set".
func AgentMounts(home string, commands []string, existingFiles func(string) bool) []AgentMount {
	return AgentMountsFor(home, commands, existingFiles, "")
}

// AgentMountsFor is AgentMounts with an explicit host platform (goos); the
// production path passes "" and resolves the running GOOS. Tests pass a fixed
// platform to cover the Windows/macOS/Linux variants.
func AgentMountsFor(home string, commands []string, existingFiles func(string) bool, goos string) []AgentMount {
	if goos == "" {
		goos = hostGOOS()
	}
	var result []AgentMount
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if _, dup := seen[command]; dup {
			continue
		}
		seen[command] = struct{}{}
		for _, cfg := range agentConfigDirs[command] {
			if !platformApplies(cfg.Platforms, goos) {
				continue
			}
			hostPath := ExpandHome(cfg.Path, home)
			if hostPath == "" {
				continue
			}
			if existingFiles != nil && !existingFiles(hostPath) {
				continue
			}
			result = append(result, AgentMount{
				Agent:    command,
				HostPath: hostPath,
			})
		}
	}
	return result
}

// platformApplies reports whether a ConfigDir applies to goos (empty Platforms
// means every platform).
func platformApplies(platforms []string, goos string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, p := range platforms {
		if p == goos {
			return true
		}
	}
	return false
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

// hostGOOS mirrors runtime.GOOS so tests can stub the platform.
var hostGOOS = func() string { return runtime.GOOS }

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
