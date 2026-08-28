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
// "kiro-cli" and Antigravity "agy". Listing only the canonical "kiro" or
// "antigravity" would make pre-flight refuse a harness the installed ai-memory
// accepts. A catalog entry that sets Memory.RunHarness papers over the gap for
// itself, but nothing forces it to, and an operator naming the agent directly
// gets the refusal with no override in sight. All aliases here were verified
// against ai-memory 1.32.2.
var memoryRunHarnesses = []string{
	"claude", "codex", "opencode", "pi", "crush", "omp", "kimi",
	"command-code", "commandcode", "cmdc", "cmd",
	"kiro", "kiro-cli",
	"grok",
	"antigravity", "antigravity-cli", "agy",
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
// ai-jail 1.20.1 is a security floor, and on macOS it is the first release in
// the 1.19/1.20 line that works at all. Four bugs, all seatbelt-only — Linux
// is unaffected by every one of them:
//
//   - The sandbox could exec a binary it never vetted. 1.19.3 resolved the
//     command through its symlink chain but canonicalized the chain away
//     before it reached the profile, so the PATH entry execvp actually walks
//     was never named. An unnamed entry does not make execvp fail; it
//     continues down PATH and runs the first match inside an already-readable
//     prefix. The same release made /opt readable, so a Homebrew copy would
//     win: ai-jail resolved and granted one build of an agent and then ran a
//     different one. A sandbox that vets one binary and executes another has
//     given up the property it exists for.
//   - DNS and TLS both failed under --network, because /etc, /var and /tmp
//     are symlinks into /private and /etc/ssl/cert.pem was unreadable.
//   - No Node script could run: anything calling realpath(3) on its entry
//     point died with EPERM, which takes out every #!/usr/bin/env node CLI.
//   - Claude Code could not read its own scratch dir; the second launch in a
//     project failed with EEXIST on /tmp/claude-<uid>.
//
// The flag surface did not move across any of this. 1.18.2 through 1.20.1
// accept byte-for-byte the same argv this launcher builds — the floor is
// about what the sandbox does with that argv, not about what it parses.
//
// The previous floor (1.18.2) was the argv floor: the launcher emits
// --network, which 1.17.x and older reject outright with "unknown option",
// and 1.18.0 turned network access, the process environment and half a dozen
// other capabilities into explicit opt-ins. 1.18.2 rather than 1.18.0 because
// linked git worktrees had their git dir mounted read-only until the patch,
// which breaks `git commit` inside the sandbox. The one before that (1.16.0)
// was a security floor for the Docker socket: through 1.15.x it was
// bind-mounted read-write on sight, so "docker off by default" was not true
// of the resulting sandbox no matter what the launcher emitted.
// appendDockerDecision still states it explicitly.
//
// ai-memory 1.25.0 is a feature floor, one release per harness the catalog
// offers: `ai-memory run kiro` arrived in 1.24.0, `run antigravity` (aliases
// antigravity-cli, agy) also in 1.24.0, and `run command-code` in 1.25.0.
// memoryRunHarnesses promises pre-flight will let all three through — against
// an older ai-memory that promise turns into the opaque clap rejection raised
// inside the jail that invariant 6b exists to prevent.
const (
	MinAIJailVersion   = "1.20.1"
	MinAIMemoryVersion = "1.25.0"
)

// UntestedAIJailVersion and UntestedAIMemoryVersion are the first upstream
// versions this launcher's argv has NOT been validated against. They are
// exclusive bounds — an install at or above one of them is reported by
// `ai-launcher --doctor`, and the launch proceeds.
//
// The floor alone only ever looked down, which leaves the launcher unable to
// notice the failure mode that actually costs an afternoon: upstream keeping
// every flag this launcher emits while changing what they *mean*. ai-jail
// 1.18.0 was exactly that — it accepted the same argv and made network
// access, the process environment, GPU, display, X11, host shared memory and
// agent credential state explicit opt-ins, so a launcher that stayed quiet
// produced a sandbox with no network and a nearly empty environment. The
// Gherkin contract cannot catch that: it locks the shape of the argv, not its
// meaning. These bounds are the part that can.
//
// Both bounds record how far the upstream surface was actually read, not a
// guess: ai-jail through 1.20.1 (no flag added, renamed or removed since
// 1.18.0; the argv verified against the installed binary) and ai-memory
// through 1.34.0 (the `run` grammar and its harness list unchanged since
// 1.25.0).
const (
	UntestedAIJailVersion   = "1.21.0"
	UntestedAIMemoryVersion = "1.35.0"
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
	PermissionNetwork = "network"
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
