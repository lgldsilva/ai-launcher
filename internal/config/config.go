// Package config owns the versioned global and workspace-local configuration.
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// maxLocalConfigBytes caps workspace-local YAML before it is fully read or
// decoded. Repository-controlled config must not exhaust memory/CPU before the
// trust gate runs. 256 KiB is far above any legitimate launcher config.
const maxLocalConfigBytes = 256 * 1024

const (
	localConfigDirectoryName = ".ai-launcher"
	localConfigFileName      = "config.yaml"
	legacyLocalConfigName    = ".ai-launch.yaml"
)

// LocalConfigDir returns the workspace-local launcher directory. An optional
// project directory makes the helper useful to callers that already resolved
// the workspace while keeping the common current-directory form concise.
func LocalConfigDir(projectDir ...string) string {
	base := ""
	if len(projectDir) > 0 {
		base = projectDir[0]
	} else if cwd, err := os.Getwd(); err == nil {
		base = cwd
	}
	if base == "" {
		base = "."
	}
	return filepath.Join(base, localConfigDirectoryName)
}

// LocalConfigPath returns the new workspace-local selection path.
func LocalConfigPath(projectDir ...string) string {
	return filepath.Join(LocalConfigDir(projectDir...), localConfigFileName)
}

// LegacyLocalConfigPath returns the pre-directory workspace-local selection
// path kept for backwards-compatible reads and automatic migration.
func LegacyLocalConfigPath(projectDir ...string) string {
	base := ""
	if len(projectDir) > 0 {
		base = projectDir[0]
	} else if cwd, err := os.Getwd(); err == nil {
		base = cwd
	}
	if base == "" {
		base = "."
	}
	return filepath.Join(base, legacyLocalConfigName)
}

// ResolveLocalConfigPath chooses the new path first, then the legacy path.
// When neither exists it returns the new path so the first save creates the
// directory-based layout.
func ResolveLocalConfigPath(projectDir ...string) string {
	newPath := LocalConfigPath(projectDir...)
	if localPathExists(newPath) {
		return newPath
	}
	legacyPath := LegacyLocalConfigPath(projectDir...)
	if localPathExists(legacyPath) {
		return legacyPath
	}
	return newPath
}

// CurrentVersion is the configuration schema version written by this build.
// It versions the config FILE format only — it is not the binary release
// version (that is the `version` variable in cmd/ai-launcher, injected via
// ldflags at build time and printed by --version).
//
// 2.1 changed `trusted_local_configs` from a list of bare SHA-256 strings to a
// list of {path, hash} records. Reading 2.0 still works (see
// TrustedLocalEntry.UnmarshalYAML), but the old records no longer grant trust,
// so a local config saved under 2.0 needs one `--save` to be honored again.
// Writing is one-way: a 2.1 file is not loadable by a pre-2.1 binary.
const CurrentVersion = "2.1"

// LoadableVersions are the schema versions this build accepts on read, oldest
// first. Every entry must survive LoadGlobal/LoadLocal without losing data —
// add one only together with the code that migrates it.
var LoadableVersions = []string{"1", "1.0", "2.0", CurrentVersion}

// DefaultMemoryServerURL is intentionally empty. Deployments configure the
// ai-memory endpoint in the global config or through AI_MEMORY_SERVER_URL.
const DefaultMemoryServerURL = ""

// memoryRunHarnesses is the fixed set of names `ai-memory run <HARNESS>`
// accepts, verified against ai-memory 1.25.0. Anything else is rejected by
// clap with an opaque "invalid value for '[HARNESS]'" — raised inside the jail,
// where the user never sees it. This list is the single source of truth: a
// catalog agent either resolves to one of these (directly or through
// Memory.RunHarness) or declares SupportsMemory false.
//
// Aliases count: clap accepts them exactly like the canonical name, and the
// catalog reaches ai-memory through Agent.Command, which spells Kiro
// "kiro-cli". Listing only the canonical "kiro" would make pre-flight refuse a
// harness the installed ai-memory accepts.
var memoryRunHarnesses = []string{
	"claude", "codex", "opencode", "pi", "crush", "omp", "kimi",
	"command-code", "commandcode", "cmdc", "cmd",
	"kiro", "kiro-cli",
	"grok", "antigravity",
}

// createTempFile is an indirection over os.CreateTemp so tests can force the
// temporary-file creation step to fail deterministically — a chmod-based
// read-only directory does not fail under root or on Windows.
var createTempFile = os.CreateTemp

// MemoryRunHarnesses returns the harnesses `ai-memory run` accepts.
func MemoryRunHarnesses() []string {
	return append([]string(nil), memoryRunHarnesses...)
}

// SupportsMemoryRunHarness reports whether `ai-memory run` accepts name.
func SupportsMemoryRunHarness(name string) bool {
	name = strings.TrimSpace(name)
	for _, harness := range memoryRunHarnesses {
		if harness == name {
			return true
		}
	}
	return false
}

// MinAIJailVersion and MinAIMemoryVersion pin the minimum upstream CLI
// versions this launcher composes against. Older installs still launch;
// `ai-launcher --doctor` reports them (see launcher.UpstreamReport).
//
// ai-jail 1.16.0 is a security floor, not a feature floor: up to 1.15.x the
// Docker socket was bind-mounted read-write on sight, so "docker off by
// default" was not true of the resulting sandbox no matter what the launcher
// emitted. The argv fix (an explicit --no-docker, see appendDockerDecision)
// covers 1.15.x hosts too; the floor is what tells an operator to stop
// relying on a build that had no way to say no.
// ai-memory 1.25.0 is a feature floor: `ai-memory run kiro` arrived in 1.24.0
// and `ai-memory run command-code` in 1.25.0. The catalog offers both as
// memory-capable agents, and memoryRunHarnesses promises pre-flight will let
// them through — against an older ai-memory that promise turns into the opaque
// clap rejection raised inside the jail that invariant 6b exists to prevent.
const (
	MinAIJailVersion   = "1.16.0"
	MinAIMemoryVersion = "1.25.0"
)

// Shared catalog names and platform identifiers. Keeping these values in the
// config package avoids slightly different spellings in the CLI, catalog and
// launcher packages that all consume the same persisted IDs.
const (
	LauncherName         = "ai-launcher"
	AIMemoryCommand      = "ai-memory"
	SemidxCommand        = "semidx"
	AIJailCommand        = "ai-jail"
	BinaryAssetMediaType = "application/octet-stream"
	MountReadOnlyLabel   = "read-only"
	PlatformWindows      = "windows"

	PermissionJail    = "jail"
	PermissionSSH     = "ssh"
	PermissionGitHub  = "gh"
	PermissionDocker  = "docker"
	PermissionGPU     = "gpu"
	PermissionDisplay = "display"

	// Official installer scripts for the mainstream coding agents. Each CLI's
	// vendor ships a curl|bash installer as its canonical installation method
	// (there is no checksum-verifiable release alternative for these), so the
	// built-in catalog records it with allow_unverified: true — the operator
	// already accepts this exact trust model when installing on the host. The
	// URLs are pinned to the vendor domains (never a third-party mirror).
	officialClaudeInstall      = "https://claude.ai/install.sh"
	officialCodexInstall       = "https://github.com/openai/codex/raw/main/scripts/install/install.sh"
	officialOpenCodeInstall    = "https://opencode.ai/install"
	officialKimiInstall        = "https://code.kimi.com/install.sh"
	officialPiInstall          = "https://pi.dev/install.sh"
	officialAntigravityInstall = "https://antigravity.google/cli/install.sh"
	officialGrokInstall        = "https://x.ai/cli/install.sh"
	officialCursorInstall      = "https://cursor.com/install"
	officialDevinInstall       = "https://cli.devin.ai/install.sh"
	officialOmpInstall         = "https://raw.githubusercontent.com/can1357/oh-my-pi/main/scripts/install.sh"

	PermissionPictures  = "pictures"
	PermissionTailscale = "tailscale"
	PermissionSystemd   = "systemd-user"
	PermissionMise      = "mise"
	PermissionWorktree  = "worktree"
	MountReadOnly       = "ro"
	MountReadWrite      = "rw"
)

// Platform keys shared by the GitHubRelease.Assets maps of the built-in
// catalog entries (see GitHubRelease).
const (
	platformLinuxAMD64  = "linux-amd64"
	platformDarwinARM64 = "darwin-arm64"
)

// Catalog identifiers repeated within a single built-in entry (name, command,
// binary) or across an entry and its memory integration.
const (
	aiJailTool   = AIJailCommand
	aiMemoryTool = AIMemoryCommand
	kimiCodeID   = "kimi-code"
	antigravID   = "antigravity-cli"
	geminiCLIID  = "gemini-cli"
)
