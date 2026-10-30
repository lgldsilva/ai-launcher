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
// "kiro-cli" and Antigravity "agy", and which becomes "kimi-cli" when that
// binary is the one on PATH. Listing only the canonical "kiro", "antigravity"
// or "kimi" would make pre-flight refuse a harness the installed ai-memory
// accepts. A catalog entry that sets Memory.RunHarness papers over the gap for
// itself, but nothing forces it to, and an operator naming the agent directly
// gets the refusal with no override in sight. The names below were verified
// against ai-memory 3.0.0 (RunHarnessChoice). kimi-cli is the same Kimi choice
// as "kimi" (present since the 1.25 floor); kimi-code stays with the names
// gated at 2.3. A claude* wildcard that upstream also accepts is intentionally
// absent: pre-flight stays a closed list, and a per-account wrapper uses
// run_harness: claude.
var memoryRunHarnesses = []string{
	"claude", "claude-code", "codex", "opencode", "open-code", "opencode2", "opencode-v2", "open-code2",
	"pi", "crush", "omp", "oh-my-pi", "kimi", "kimi-cli", kimiCodeID,
	"command-code", "commandcode", "cmdc", "cmd",
	"kiro", "kiro-cli",
	"grok", "grok-build",
	"antigravity", "antigravity-cli", "agy",
	"copilot", "copilot-cli",
}

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

// memoryHarnessAddedIn23 are names `ai-memory run` did not accept at the
// 1.25 floor. They were verified on 2.4.0 and are refused below 2.3 when the
// installed version is known, so clap does not reject them inside the jail.
var memoryHarnessAddedIn23 = map[string]struct{}{
	"opencode2": {}, "opencode-v2": {}, "open-code2": {},
	"claude-code": {}, "open-code": {}, "oh-my-pi": {},
	kimiCodeID: {}, "grok-build": {},
}

// memoryHarnessAddedIn30 are the harnesses ai-memory 3.0.0 added. Refused
// below 3.0.0 when the installed version is known.
var memoryHarnessAddedIn30 = map[string]struct{}{
	"copilot": {}, "copilot-cli": {},
}

// MemoryHarnessMinVersion is the first ai-memory that accepts name, or ""
// when the 1.25 floor already did. Unknown names return "".
func MemoryHarnessMinVersion(name string) string {
	name = strings.TrimSpace(name)
	if _, ok := memoryHarnessAddedIn23[name]; ok {
		return MinNoAutowireAIMemoryVersion
	}
	if _, ok := memoryHarnessAddedIn30[name]; ok {
		return MinCopilotAIMemoryVersion
	}
	return ""
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
// guess. ai-jail 1.21.0 through 2.9.0 kept every flag this launcher already
// emitted; 2.0.0 added opt-in flags (--allow-host, --audit-log,
// --env-from-file), 2.2.0 added --secret and 2.9.0 added --forward-port, none
// of which the default argv sends. What moved inside the range is meaning,
// not shape: 2.6.0 turns agent_state on by default (agent credential dirs
// read-write, provider API keys copied from the host), so the same argv
// builds a more permissive sandbox on 2.6.0 than on 2.5.x; 2.5.0 (Linux) and
// 2.8.0 (per-harness API hosts) add default filtered egress whenever network
// is left unstated, which this launcher never does, and from 2.8.0 the
// harness's own API host is unioned into --allow-host lists, so allow_hosts
// stops being an exact egress description there; 2.6.2 moves --env values
// off the argv and makes --lockdown force --unshare-net even with --network,
// and 2.5.0 refuses a flag spelled like ai-jail's own after the
// command word, which is why ai-memory's pre-harness --env KEY=VALUE cannot
// ride through the jail while --env-file can. ai-memory 1.35.0 through 3.2.1
// kept the `run` grammar this launcher emits (--workspace, --project,
// --workstream, --new, --executable, harness, --fresh); the additions are
// wrapper flags re-extracted from native args (--yolo's own ai-jail offer in
// 2.5.0, --true-yolo, --env/--env-file, --profile, --require-server and
// --force-unlock in 2.6.0, --jail/--no-jail in 2.5.2 -- ignored inside
// ai-jail). 2.6.0 also downgrades an unreachable server to a degraded launch
// instead of failing, and 3.0.0 adds the copilot/copilot-cli harnesses while
// removing the 1.x token commands and OPENCODE_ZEN_BASE_URL, none of which
// this launcher uses. ai-jail 2.10 through 2.11.2 and ai-memory 3.0.1 through
// 3.2.1 added no flag this launcher emits (2.11.0's --allow-private-host is
// opt-in through jail_flags); ai-memory 3.1.0 only changes what `run` hands the
// harness on a switch (AI_MEMORY_PREVIOUS_SESSION_HISTORY). ai-jail 2.12.0
// adds --macos-chromium (new opt-in, added in #101); ai-memory
// 3.2.2 through 3.2.6 keep the same `run` grammar. ai-memory 3.3.0 was read
// and executed, not guessed: `run --help` still accepts the same harness
// names (including agy, copilot, and copilot-cli), and the flags this
// launcher emits (--no-autowire, --workspace, --project, --workstream, --new,
// --executable, --fresh, --profile, --env-file, --jail / --no-jail) kept
// their meaning. `--fresh claude` also sets AI_MEMORY_PREVIOUS_SESSION_HISTORY
// (upstream behavior, not a launcher flag). `--project-id` is new and was
// left to upstream's own selector. `--no-exit-prompt` is opt-in through
// memory_exit_prompt: false. ai-jail 2.12.0 through 2.13.1 were read the
// same way: --macos-chromium and --publish-port are new opt-ins this
// launcher does not emit, and 2.13.1's command-precedence, last-wins env,
// and nested-mount fixes do not reject or redefine a flag this launcher
// sends. Live dry-run of the composed chain was re-run against ai-jail
// 2.13.1 and ai-memory 3.3.0.
const (
	UntestedAIJailVersion   = "3.0.0"
	UntestedAIMemoryVersion = "3.4.0"
)

// MinAllowHostAIJailVersion is the first ai-jail that accepts --allow-host.
// Older releases reject the flag outright, so the launcher refuses a configured
// allow list below this instead of emitting it.
const MinAllowHostAIJailVersion = "2.0.0"

// MinAllowPrivateHostAIJailVersion is the first ai-jail with
// --allow-private-host: --allow-host whose named host may also resolve to
// RFC1918, ULA or carrier-grade NAT addresses. Older releases reject the flag.
const MinAllowPrivateHostAIJailVersion = "2.11.0"

// MinAgentStateAIJailVersion is the first ai-jail with --agent-state, and the
// release that turned agent state on by default (harness credential dirs
// read-write, provider API keys copied from the host). Below it the flag is an
// unknown option, so a configured agent_state is refused instead of emitted;
// at or above it the decision is always stated explicitly, like --docker and
// --network.
const MinAgentStateAIJailVersion = "2.6.0"

// MinTogglesAIJailVersion is the floor for the ai-jail 2.5.0 toggle batch:
// --toolchains (the jail-owned dev cache store, on by default upstream),
// --kvm, and the opt-in credential mounts --github, --aws, --kube, --gcloud
// and --docker-config. Below it every one of them is an unknown option, so
// configuring any is refused instead of emitted.
const MinTogglesAIJailVersion = "2.5.0"

// MinForwardPortAIJailVersion is the first ai-jail with --forward-port, which
// relays a host loopback port into the sandbox's own loopback.
const MinForwardPortAIJailVersion = "2.9.0"

// MinLockdownUnshareAIJailVersion is the first ai-jail where --lockdown
// always forces --unshare-net even when --network is passed. Before it a
// lockdown with network kept the network; from it the same argv is offline.
const MinLockdownUnshareAIJailVersion = "2.6.2"

// MinEgressUnionAIJailVersion is the first ai-jail that unions the harness's
// own default API host into --allow-host filtered egress, so an allow list is
// no longer an exact egress description from it on.
const MinEgressUnionAIJailVersion = "2.8.0"

// MinAuditLogAIJailVersion and MinEnvFromFileAIJailVersion are the 2.0.0
// additions this launcher now mirrors: --audit-log (opt-in launch audit at
// ~/.local/share/ai-jail/history.jsonl) and --env-from-file (KEY=VALUE lines
// read with ownership/mode checks upstream applies itself).
const (
	MinAuditLogAIJailVersion    = "2.0.0"
	MinEnvFromFileAIJailVersion = "2.0.0"
)

// MinSecretAIJailVersion is the first ai-jail with --secret KEY=host, which
// keeps the real value out of the sandbox entirely (the child sees a
// placeholder; the filtered-egress proxy substitutes it only for requests to
// host).
const MinSecretAIJailVersion = "2.2.0"

// MinModernTogglesAIJailVersion is the floor for the remaining 2.9 toggles
// this launcher mirrors (--audio, --terminal-passthrough, --macos-host-ipc,
// --update-check). Their arrival releases were not pinned in the changelog
// digest, so the floor is the version they were verified against — older
// jails keep working exactly as before, and a configured toggle below it is
// refused with the floor named. --x11 and --host-shm carry no floor: the
// repository's own 1.18.0 notes name both as opt-ins of that release.
const MinModernTogglesAIJailVersion = "2.9.0"

// MinMacOSChromiumAIJailVersion is the first ai-jail with --macos-chromium:
// the narrow Seatbelt opt-in that lets headless Chromium (and Playwright)
// register its MachPortRendezvousServer. Older releases reject the flag.
const MinMacOSChromiumAIJailVersion = "2.12.0"

// MinEnvFileAIMemoryVersion is the first ai-memory whose `run` accepts
// --env-file (KEY=VALUE lines for the spawned harness, read before the
// harness name).
const MinEnvFileAIMemoryVersion = "2.5.0"

// MinProfileAIMemoryVersion is the first ai-memory whose `run` accepts
// --profile (named env-only launch profiles from config.toml).
const MinProfileAIMemoryVersion = "2.6.0"

// MinMemoryJailAIMemoryVersion is the first ai-memory whose `run` accepts
// --jail[=TOGGLES] / --no-jail — ai-memory jailing the session itself.
const MinMemoryJailAIMemoryVersion = "2.5.2"

// MinCopilotAIMemoryVersion is the first ai-memory with the copilot and
// copilot-cli harnesses.
const MinCopilotAIMemoryVersion = "3.0.0"

// MinNoExitPromptAIMemoryVersion is the first ai-memory whose `run` accepts
// --no-exit-prompt. Below it the flag is an unknown option, so pre-flight
// refuses memory_exit_prompt: false instead of emitting it inside the jail.
const MinNoExitPromptAIMemoryVersion = "3.3.0"

// MinPreHarnessAIJailVersion is the first ai-jail verified to forward
// ai-memory's newer pre-harness wrapper flags (--profile, --env-file)
// through the managed-harness window instead of bailing on the unknown dash
// token. The 1.x parser accepts exactly five value options there and falls
// back to the outer command on anything else, silently dropping the
// agent-state mounts (the build.go comment documents that failure); the
// forwarding was verified against 2.9.0 with --dry-run.
const MinPreHarnessAIJailVersion = "2.9.0"

// MinNoAutowireAIMemoryVersion is the first ai-memory whose `run` accepts
// --no-autowire. Below it the flag is an unknown option, so it is omitted.
const MinNoAutowireAIMemoryVersion = "2.3.0"

// MinRequireServerAIMemoryVersion is the first ai-memory that knows
// AI_MEMORY_RUN_REQUIRE_SERVER, and the release that downgraded an
// unreachable server to a degraded launch. Below it the CLI already fails
// closed, so the launcher exports nothing there.
const MinRequireServerAIMemoryVersion = "2.6.0"

// MinForceUnlockAIMemoryVersion is the first ai-memory whose `run` accepts
// --force-unlock (expire the selected workstream's active lease). Same
// release as require-server; kept as its own constant so the floor stays
// named in refusals.
const MinForceUnlockAIMemoryVersion = "2.6.0"

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
