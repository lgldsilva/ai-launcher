package config

import (
	"os"
	"runtime"
	"strings"
)

const (
	flagYolo                 = "--yolo"
	flagDangerouslySkipPerms = "--dangerously-skip-permissions"
	platformLinuxARM64       = "linux-arm64"
	platformDarwinAMD64      = "darwin-amd64"
)

// DefaultGlobal returns the built-in global catalog used when no global
// config file exists or when individual sections are missing from it.
func DefaultGlobal() Global {
	return Global{
		Version:         CurrentVersion,
		MemoryServerURL: DefaultMemoryServerURL,
		Agents: []Agent{
			{Name: "Claude Code", Command: "claude", SupportsMemory: true, SupportsYolo: true, Description: "Anthropic's Claude Code", YoloFlag: flagDangerouslySkipPerms, SourceURL: officialClaudeInstall, AllowUnverified: true, Params: []Param{modelParam("for example sonnet or opus")}, Memory: memoryFor("claude-code")},
			{Name: "Codex", Command: "codex", SupportsMemory: true, SupportsYolo: true, Description: "OpenAI Codex CLI", YoloFlag: "--dangerously-bypass-approvals-and-sandbox", SourceURL: officialCodexInstall, AllowUnverified: true, Params: []Param{modelParam("for example gpt-5")}, Memory: memoryFor("codex")},
			{Name: "OpenCode", Command: "opencode", SupportsMemory: true, SupportsYolo: true, Description: "OpenCode CLI", YoloFlag: "--auto", SourceURL: officialOpenCodeInstall, AllowUnverified: true, Memory: memoryFor("opencode")},
			{Name: "Kimi Code", Command: "kimi", Aliases: []string{"kimi-cli", kimiCodeID}, SupportsMemory: true, SupportsYolo: true, Description: "Moonshot Kimi Code CLI", YoloFlag: flagYolo, SourceURL: officialKimiInstall, AllowUnverified: true, Params: []Param{modelParam("for example k2"), {Name: "query", Flag: "--prompt", Description: "Initial prompt sent to Kimi", TakesValue: true}}, Memory: memoryFor(kimiCodeID)},
			{Name: "Kilo Code", Command: "kilo", Aliases: []string{"kilocode", "kilo-code"}, SupportsMemory: false, SupportsYolo: false, Description: "Kilo Code CLI", Release: &GitHubRelease{
				Repository: "Kilo-Org/kilocode",
				Assets: map[string]string{
					platformLinuxAMD64:  "kilo-linux-x64.tar.gz",
					platformLinuxARM64:  "kilo-linux-arm64.tar.gz",
					platformDarwinAMD64: "kilo-darwin-x64.zip",
					platformDarwinARM64: "kilo-darwin-arm64.zip",
				},
				Binary: "kilo",
			}},
			{Name: "MiMo Code", Command: "mimo", Aliases: []string{"mimocode", "mimo-code"}, SupportsMemory: false, SupportsYolo: true, Description: "Xiaomi MiMo Code CLI", YoloFlag: flagDangerouslySkipPerms},
			{Name: "Antigravity", Command: "agy", Aliases: []string{"antigravity", antigravID}, SupportsMemory: true, SupportsYolo: true, Description: "Antigravity CLI", YoloFlag: flagDangerouslySkipPerms, SourceURL: officialAntigravityInstall, AllowUnverified: true, Memory: memoryForHarness("antigravity")},
			{Name: "Pi", Command: "pi", Aliases: []string{"pi-coding-agent"}, SupportsMemory: true, SupportsYolo: true, Description: "Pi coding agent", YoloFlag: "--approve", SourceURL: officialPiInstall, AllowUnverified: true, NeedsNode: true, Memory: hooksOnlyMemoryIntegration("pi")},
			{Name: "Crush", Command: "crush", SupportsMemory: true, SupportsYolo: true, Description: "Charmbracelet Crush (ai-memory managed run only)", YoloFlag: flagYolo, NpmPackage: "@charmland/crush"},
			{Name: "Oh My Pi", Command: "omp", Aliases: []string{"oh-my-pi"}, SupportsMemory: true, SupportsYolo: true, Description: "Oh My Pi", YoloFlag: "--auto-approve", SourceURL: officialOmpInstall, AllowUnverified: true, Memory: memoryFor("omp")},
			{Name: "Cursor Agent", Command: "cursor-agent", Aliases: []string{"cursor"}, SupportsMemory: false, SupportsYolo: true, Description: "Cursor Agent CLI", YoloFlag: flagYolo, SourceURL: officialCursorInstall, AllowUnverified: true, Memory: memoryFor("cursor")},
			{Name: "Grok", Command: "grok", SupportsMemory: true, SupportsYolo: true, Description: "Grok CLI", YoloFlag: "--always-approve", SourceURL: officialGrokInstall, AllowUnverified: true, Memory: memoryFor("grok")},
			{Name: "Zero", Command: "zero", SupportsMemory: false, SupportsYolo: false, Description: "Zero agent CLI", Memory: memoryFor("zero")},
			{Name: "Devin", Command: "devin", SupportsMemory: false, SupportsYolo: true, Description: "Devin CLI", YoloFlag: "--permission-mode dangerous", SourceURL: officialDevinInstall, AllowUnverified: true, SetupInteractive: true, Memory: memoryFor("devin")},
			// oc is a local preset TUI that ends up launching opencode; ai-memory
			// only knows the harness name "opencode", so RunHarness remaps it.
			{Name: "OpenCode Presets", Command: "oc", SupportsMemory: true, SupportsYolo: true, Description: "OpenCode preset selector", YoloFlag: "--auto", Memory: memoryForHarness("opencode")},
			{Name: "Gemini CLI", Command: "gemini", Aliases: []string{geminiCLIID}, SupportsMemory: false, SupportsYolo: true, Description: "Google Gemini CLI", YoloFlag: flagYolo, NpmPackage: "@google/gemini-cli", Params: []Param{modelParam("for example gemini-2.5-pro")}, Memory: memoryFor(geminiCLIID)},
			{Name: "Qwen Code", Command: "qwen", Aliases: []string{"qwen-code"}, SupportsMemory: false, SupportsYolo: true, Description: "Alibaba Qwen Code CLI", YoloFlag: flagYolo, NpmPackage: "@qwen-code/qwen-code"},
			{Name: "Aider", Command: "aider", SupportsMemory: false, SupportsYolo: true, Description: "Aider CLI", YoloFlag: "--yes-always"},
			{Name: "Goose", Command: "goose", SupportsMemory: false, SupportsYolo: false, Description: "Block Goose CLI"},
			{Name: "Kiro CLI", Command: "kiro-cli", Aliases: []string{"kiro"}, SupportsMemory: false, SupportsYolo: false, Description: "Kiro CLI"},
			{Name: "OpenClaw", Command: "openclaw", SupportsMemory: false, SupportsYolo: false, Description: "OpenClaw CLI", NpmPackage: "openclaw", Memory: mcpOnlyMemoryIntegration("openclaw")},
			{Name: "Hermes Agent", Command: "hermes", SupportsMemory: false, SupportsYolo: true, Description: "Hermes Agent CLI", YoloFlag: flagYolo},
			{Name: "Cline", Command: "cline", SupportsMemory: false, SupportsYolo: true, Description: "Cline CLI", YoloFlag: flagYolo, NpmPackage: "cline"},
		},
		Tools: []Tool{
			{
				Name: SemidxCommand, Command: SemidxCommand,
				Description: "Semantic code search CLI and MCP server",
				Release: &GitHubRelease{
					Repository: "lgldsilva/semidx",
					Assets: map[string]string{
						"linux-amd64":       "semidx_*_linux_amd64.tar.gz",
						platformLinuxARM64:  "semidx_*_linux_arm64.tar.gz",
						platformDarwinAMD64: "semidx_*_darwin_amd64.tar.gz",
						"darwin-arm64":      "semidx_*_darwin_arm64.tar.gz",
						"windows-amd64":     "semidx_*_windows_amd64.zip",
						"windows-arm64":     "semidx_*_windows_arm64.zip",
					},
					Binary:        SemidxCommand,
					ChecksumAsset: "checksums.txt",
				},
			},
			{
				Name: aiJailTool, Command: aiJailTool, Description: "Sandbox wrapper used by ai-launcher (Linux and macOS only)",
				Release: &GitHubRelease{
					Repository: "akitaonrails/ai-jail",
					// ai-jail v1.15 publishes only these two assets; there are
					// no linux-arm64, darwin-amd64, or Windows builds.
					Assets: map[string]string{
						platformLinuxAMD64:  "ai-jail-linux-x86_64.tar.gz",
						platformDarwinARM64: "ai-jail-macos-aarch64.tar.gz",
					},
					Binary:        aiJailTool,
					ChecksumAsset: "checksums.txt",
				},
			},
			{
				Name: aiMemoryTool, Command: aiMemoryTool,
				Description: "Memory wrapper; installed from the checksum-verified release assets and the native runner self-updates on first use",
				Release: &GitHubRelease{
					Repository: "akitaonrails/ai-memory",
					// Native per-platform runners (used on Windows, where the
					// shell wrapper does not apply). Each asset has a .sha256
					// sidecar in the release.
					Assets: map[string]string{
						platformLinuxAMD64:  "ai-memory-linux-x86_64.tar.gz",
						platformLinuxARM64:  "ai-memory-linux-aarch64.tar.gz",
						platformDarwinAMD64: "ai-memory-macos-x86_64.tar.gz",
						platformDarwinARM64: "ai-memory-macos-aarch64.tar.gz",
						"windows-amd64":     "ai-memory-windows-x86_64.zip",
					},
					Binary: aiMemoryTool,
				},
			},
		},
		Permissions: []Permission{
			{ID: PermissionJail, Name: "Jail / Sandbox", Default: true, Locked: true},
			{ID: PermissionSSH, Name: "SSH access", Requires: []string{PermissionJail}},
			{ID: PermissionGitHub, Name: "GitHub CLI", Requires: []string{PermissionJail}},
			{ID: PermissionDocker, Name: "Docker socket", Requires: []string{PermissionJail}},
			{ID: PermissionGPU, Name: "GPU passthrough", Requires: []string{PermissionJail}},
			// The passthroughs below default to off because ai-jail already
			// auto-enables display, mise and worktree when the resource exists.
			// Turning one on here forces it on; forcing one off is a jail_flags
			// decision, which is tri-state and can express it.
			{ID: PermissionDisplay, Name: "Display passthrough", Default: false, Requires: []string{PermissionJail}, Platforms: []string{"linux"}},
			{ID: PermissionPictures, Name: "Pictures folder", Default: false, Requires: []string{PermissionJail}, Platforms: []string{"linux", "darwin"}},
			{ID: PermissionTailscale, Name: "Tailscale socket", Default: false, Requires: []string{PermissionJail}, Platforms: []string{"linux", "darwin"}},
			{ID: PermissionSystemd, Name: "systemd user bus", Default: false, Requires: []string{PermissionJail}, Platforms: []string{"linux"}},
			{ID: PermissionMise, Name: "mise integration", Default: false, Requires: []string{PermissionJail}},
			{ID: PermissionWorktree, Name: "Git worktree passthrough", Default: false, Requires: []string{PermissionJail}},
		},
		DefaultMounts: DefaultMountCandidates(runtime.GOOS),
	}
}

// DefaultMountCandidates returns the built-in mount suggestions for goos.
//
// It returns nothing, on every platform, and the signature is kept so the
// callers and the goos-shaped tests stay honest about that.
//
// It used to return this author's own volumes — /storage and /storage/Projetos
// on Linux, /Volumes/MSD512 on macOS — compiled into a tool other people
// install. ExistingPaths kept that harmless (a path that is not there is
// dropped), which is exactly why it survived: on the machine it was written
// for it worked, and everywhere else the feature silently did nothing.
//
// A default mount is a read-write hole in the sandbox. Guessing one is the
// wrong shape of guess: too specific to be right for a stranger, and if it
// ever did match, it would grant an agent write access to a directory nobody
// asked about. The launcher now mounts what it is told to mount — `--mount` /
// `--rw-map`, the workspace file, a profile, or `default_mounts` in the global
// config, which is the supported way to get this behaviour back:
//
//	# ~/.config/ai-launch/config.yaml
//	default_mounts:
//	  - /storage/Projetos
//
// ai-jail already mounts the current project read-write on its own, so the
// common case needs no mounts at all.
func DefaultMountCandidates(goos string) []string {
	_ = goos // no platform has a defensible built-in default; see above
	return nil
}

// ExistingPaths returns the subset of paths that currently exist on the host.
// Empty and whitespace-only entries are skipped. The order of paths is
// preserved.
func ExistingPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		out = append(out, path)
	}
	return out
}

// DefaultLocal returns the safe-by-default local configuration.
func DefaultLocal() Local {
	return Local{Version: CurrentVersion, Agent: "claude", Permissions: map[string]bool{}, Options: Options{Jail: true, Memory: true}}
}

func memoryFor(command string) *MemoryIntegration {
	return &MemoryIntegration{Client: command, Agent: command, InstallMCP: true, InstallHooks: true}
}

func memoryForHarness(command string) *MemoryIntegration {
	integration := memoryFor(command)
	integration.RunHarness = command
	return integration
}

func modelParam(example string) Param {
	return Param{Name: "model", Flag: "--model", Description: "Model to run (" + example + ")", TakesValue: true}
}

func mcpOnlyMemoryIntegration(client string) *MemoryIntegration {
	return &MemoryIntegration{Client: client, InstallMCP: true}
}

// hooksOnlyMemoryIntegration is for harnesses whose ai-memory support is a
// generated extension installed by `install-hooks` (e.g. Pi); upstream
// rejects `install-mcp` for them.
func hooksOnlyMemoryIntegration(agent string) *MemoryIntegration {
	return &MemoryIntegration{Agent: agent, InstallHooks: true}
}

// JailDependentIDs returns the IDs of permissions that require "jail"
// directly or transitively (including "jail" itself). Platforms without
// ai-jail support use it to filter the offered permissions.
func JailDependentIDs(permissions []Permission) map[string]bool {
	dependent := map[string]bool{PermissionJail: true}
	changed := true
	for changed {
		changed = false
		for _, permission := range permissions {
			if dependent[permission.ID] {
				continue
			}
			for _, required := range permission.Requires {
				if dependent[required] {
					dependent[permission.ID] = true
					changed = true
				}
			}
		}
	}
	return dependent
}
