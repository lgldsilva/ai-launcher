// Package config owns the versioned global and workspace-local configuration.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// maxLocalConfigBytes caps workspace-local YAML before it is fully read or
// decoded. Repository-controlled config must not exhaust memory/CPU before the
// trust gate runs. 256 KiB is far above any legitimate launcher config.
const maxLocalConfigBytes = 256 * 1024

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
// accepts, verified against ai-memory 1.19.0. Anything else is rejected by
// clap with an opaque "invalid value for '[HARNESS]'" — raised inside the jail,
// where the user never sees it. This list is the single source of truth: a
// catalog agent either resolves to one of these (directly or through
// Memory.RunHarness) or declares SupportsMemory false.
var memoryRunHarnesses = []string{"claude", "codex", "opencode", "pi", "crush", "omp", "kimi", "grok"}

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
const (
	MinAIJailVersion   = "1.16.0"
	MinAIMemoryVersion = "1.19.0"
)

// Shared catalog names and platform identifiers. Keeping these values in the
// config package avoids slightly different spellings in the CLI, catalog and
// launcher packages that all consume the same persisted IDs.
const (
	LauncherName         = "ai-launcher"
	AIMemoryCommand      = "ai-memory"
	AIJailCommand        = "ai-jail"
	BinaryAssetMediaType = "application/octet-stream"
	MountReadOnlyLabel   = "read-only"
	PlatformWindows      = "windows"

	PermissionJail      = "jail"
	PermissionSSH       = "ssh"
	PermissionGitHub    = "gh"
	PermissionDocker    = "docker"
	PermissionGPU       = "gpu"
	PermissionDisplay   = "display"
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

// Agent describes a launchable AI agent CLI and its optional integrations.
type Agent struct {
	Name      string   `yaml:"name"`
	Command   string   `yaml:"command"`
	Aliases   []string `yaml:"aliases,omitempty"`
	Path      string   `yaml:"path,omitempty"`
	SourceURL string   `yaml:"source_url,omitempty"`
	// AllowUnverified permits the checksum-less source_url install path.
	// Without it a source_url recipe is refused (ARCHITECTURE invariant 4).
	AllowUnverified bool               `yaml:"allow_unverified,omitempty"`
	SupportsMemory  bool               `yaml:"supports_memory"`
	SupportsYolo    bool               `yaml:"supports_yolo"`
	Description     string             `yaml:"description,omitempty"`
	YoloFlag        string             `yaml:"yolo_flag,omitempty"`
	Params          []Param            `yaml:"params,omitempty"`
	Release         *GitHubRelease     `yaml:"release,omitempty"`
	Memory          *MemoryIntegration `yaml:"memory,omitempty"`
}

// Param declares a harness-specific CLI flag in the catalog so new agents or
// flags do not require a launcher rebuild. Name is the stable key used in
// param_values maps; Flag is the literal argument passed to the harness.
// TakesValue reports whether the flag consumes a separate value argument.
type Param struct {
	Name        string `yaml:"name"`
	Flag        string `yaml:"flag"`
	Description string `yaml:"description,omitempty"`
	TakesValue  bool   `yaml:"takes_value"`
}

// GitHubRelease describes how an executable is obtained from the latest
// GitHub release. Asset names are selected by runtime platform using keys
// such as linux-amd64 and darwin-arm64. Values may contain a single '*' glob.
// Binary is the file inside an archive, or the output filename for a raw
// executable asset.
type GitHubRelease struct {
	Repository      string            `yaml:"repository"`
	Assets          map[string]string `yaml:"assets"`
	Binary          string            `yaml:"binary"`
	ChecksumAsset   string            `yaml:"checksum_asset,omitempty"`
	AllowUnverified bool              `yaml:"allow_unverified,omitempty"`
}

// MemoryIntegration maps an ai-launcher harness to ai-memory's two host-side
// wiring commands. Unsupported harnesses leave this nil instead of receiving
// guessed configuration.
type MemoryIntegration struct {
	Client string `yaml:"client"`
	Agent  string `yaml:"agent"`
	// RunHarness is the name passed to `ai-memory run <harness>`. When empty,
	// the launcher uses Agent.Command. Set this when the catalog command is a
	// wrapper whose name is not in ai-memory's fixed harness list (for example
	// command "oc" → RunHarness "opencode").
	RunHarness      string `yaml:"run_harness,omitempty"`
	InstallMCP      bool   `yaml:"install_mcp"`
	InstallHooks    bool   `yaml:"install_hooks"`
	MCPConfigFile   string `yaml:"mcp_config_file,omitempty"`
	HooksConfigFile string `yaml:"hooks_config_file,omitempty"`
}

// Tool describes an auxiliary CLI (for example ai-jail or ai-memory) that the
// launcher can install on demand.
type Tool struct {
	Name      string   `yaml:"name"`
	Command   string   `yaml:"command"`
	Aliases   []string `yaml:"aliases,omitempty"`
	Path      string   `yaml:"path,omitempty"`
	SourceURL string   `yaml:"source_url,omitempty"`
	// AllowUnverified permits the checksum-less source_url install path.
	// Without it a source_url recipe is refused (ARCHITECTURE invariant 4).
	AllowUnverified bool           `yaml:"allow_unverified,omitempty"`
	Description     string         `yaml:"description,omitempty"`
	Release         *GitHubRelease `yaml:"release,omitempty"`
}

// Permission is a toggleable capability with optional dependencies on other
// permissions (Requires) and a Locked flag for values the user cannot change.
// Platforms restricts the permission to the listed GOOS values; an empty list
// means it is supported everywhere.
type Permission struct {
	ID        string   `yaml:"id"`
	Name      string   `yaml:"name"`
	Default   bool     `yaml:"default"`
	Locked    bool     `yaml:"locked,omitempty"`
	Requires  []string `yaml:"requires,omitempty"`
	Platforms []string `yaml:"platforms,omitempty"`
}

// PermissionSupportedOn reports whether a permission applies to goos.
// Permissions without a Platforms list are supported on every platform.
func PermissionSupportedOn(permission Permission, goos string) bool {
	if len(permission.Platforms) == 0 {
		return true
	}
	for _, platform := range permission.Platforms {
		if platform == goos {
			return true
		}
	}
	return false
}

// Mount is a host directory exposed inside the sandbox. Mode is "rw" or
// "read-only"; an empty mode defaults to read-write.
type Mount struct {
	Path string `yaml:"path"`
	Mode string `yaml:"mode,omitempty"`
}

// JailFlags holds the ai-jail v1.16 capability toggles. Pointer booleans are
// tri-state and mirror ai-jail's own model: nil leaves the capability in
// ai-jail's auto mode (no flag emitted, meaning "enabled when the resource
// exists on the host"), while true and false force it on or off with the
// positive or --no- form. Forcing a capability on is therefore a distinct
// state from leaving it unset, and both forms are always emitted. Browser
// accepts "hard", "soft", or "off".
type JailFlags struct {
	Lockdown    *bool `yaml:"lockdown,omitempty"`
	PrivateHome *bool `yaml:"private_home,omitempty"`
	// Docker is the one capability where auto mode is not a safe default.
	// Through ai-jail v1.15.x the Docker socket was bind-mounted read-write
	// whenever /var/run/docker.sock existed on the host, with no flag and no
	// warning — and write access to that socket is root on the host, which
	// walks straight past bubblewrap, Landlock, seccomp and every --mask.
	// v1.16.0 made it opt-in after ai-jail issue #88. The launcher does not
	// rely on that: see appendDockerDecision in internal/launcher.
	Docker     *bool `yaml:"docker,omitempty"`
	Tailscale  *bool `yaml:"tailscale,omitempty"`
	GPU        *bool `yaml:"gpu,omitempty"`
	Display    *bool `yaml:"display,omitempty"`
	Mise       *bool `yaml:"mise,omitempty"`
	Worktree   *bool `yaml:"worktree,omitempty"`
	Landlock   *bool `yaml:"landlock,omitempty"`
	Seccomp    *bool `yaml:"seccomp,omitempty"`
	Rlimits    *bool `yaml:"rlimits,omitempty"`
	StatusBar  *bool `yaml:"status_bar,omitempty"`
	HideConfig *bool `yaml:"hide_config,omitempty"`
	// SaveConfig toggles ai-jail's automatic .ai-jail writes. nil leaves the
	// ai-jail default in charge; true/false force --save-config/--no-save-config.
	SaveConfig    *bool    `yaml:"save_config,omitempty"`
	Browser       string   `yaml:"browser,omitempty"`
	ClaudeDir     string   `yaml:"claude_dir,omitempty"`
	OverlayMaps   []string `yaml:"overlay_maps,omitempty"`
	Mask          []string `yaml:"mask,omitempty"`
	DenyPaths     []string `yaml:"deny_paths,omitempty"`
	AllowTCPPorts []int    `yaml:"allow_tcp_ports,omitempty"`
	// v1.15 exception lists: each entry punches a hole in a mask / deny-path
	// rule or hides a dot-directory by name.
	MaskExceptions     []string `yaml:"mask_exceptions,omitempty"`
	DenyPathExceptions []string `yaml:"deny_path_exceptions,omitempty"`
	HideDotdirs        []string `yaml:"hide_dotdirs,omitempty"`
	// StatusBarStyle selects a status bar theme ("dark", "light", or
	// "pastel"); when set it wins over the StatusBar boolean toggle.
	StatusBarStyle string `yaml:"status_bar_style,omitempty"`
}

// IsZero reports whether no jail flag deviates from the ai-jail defaults.
func (f JailFlags) IsZero() bool {
	return f.Lockdown == nil && f.PrivateHome == nil && f.Docker == nil && f.Tailscale == nil &&
		f.GPU == nil && f.Display == nil && f.Mise == nil && f.Worktree == nil &&
		f.Landlock == nil && f.Seccomp == nil && f.Rlimits == nil &&
		f.StatusBar == nil && f.HideConfig == nil && f.SaveConfig == nil && f.Browser == "" && f.ClaudeDir == "" &&
		len(f.OverlayMaps) == 0 && len(f.Mask) == 0 && len(f.DenyPaths) == 0 &&
		len(f.AllowTCPPorts) == 0 && len(f.MaskExceptions) == 0 &&
		len(f.DenyPathExceptions) == 0 && len(f.HideDotdirs) == 0 && f.StatusBarStyle == ""
}

// Options holds the per-launch behavior toggles persisted in the local config.
type Options struct {
	Jail   bool `yaml:"jail"`
	Memory bool `yaml:"memory"`
	Yolo   bool `yaml:"yolo"`
	// Fresh maps to `ai-memory run --fresh`: start a new native session in the
	// current workstream instead of resuming or adopting one.
	Fresh         bool              `yaml:"fresh,omitempty"`
	NewWorkstream string            `yaml:"new_workstream,omitempty"`
	Workstream    string            `yaml:"workstream,omitempty"`
	Workspace     string            `yaml:"workspace,omitempty"`
	Project       string            `yaml:"project,omitempty"`
	JailFlags     JailFlags         `yaml:"jail_flags,omitempty"`
	ExtraArgs     []string          `yaml:"extra_args,omitempty"`
	ParamValues   map[string]string `yaml:"param_values,omitempty"`
}

// UnmarshalYAML accepts both the current lossless list form and the scalar
// form used by the original launcher documentation (for example,
// extra_args: "--model sonnet"). SaveLocal always emits the list form.
//
// Whichever form is used, an omitted jail or memory key keeps the safe default
// (ARCHITECTURE invariant 6). Applying it here rather than at each call site is
// what makes the rule hold for every Options in the schema: a profile's options
// block used to land on Go's zero value, so a profile naming only `yolo`
// silently launched with the sandbox off — invariant 6's defect, reintroduced
// through the global config instead of the workspace file.
func (o *Options) UnmarshalYAML(data []byte) error {
	type optionsWithoutMethods Options
	var list optionsWithoutMethods
	listErr := yaml.Unmarshal(data, &list)
	if listErr == nil {
		*o = Options(list)
		applyOptionDefaults(o, declaredKeys(data))
		return nil
	}

	var scalar struct {
		Jail          bool              `yaml:"jail"`
		Memory        bool              `yaml:"memory"`
		Yolo          bool              `yaml:"yolo"`
		NewWorkstream string            `yaml:"new_workstream,omitempty"`
		Workstream    string            `yaml:"workstream,omitempty"`
		Workspace     string            `yaml:"workspace,omitempty"`
		Project       string            `yaml:"project,omitempty"`
		JailFlags     JailFlags         `yaml:"jail_flags,omitempty"`
		ExtraArgs     string            `yaml:"extra_args,omitempty"`
		ParamValues   map[string]string `yaml:"param_values,omitempty"`
	}
	if err := yaml.Unmarshal(data, &scalar); err != nil {
		return listErr
	}
	*o = Options{
		Jail:          scalar.Jail,
		Memory:        scalar.Memory,
		Yolo:          scalar.Yolo,
		NewWorkstream: scalar.NewWorkstream,
		Workstream:    scalar.Workstream,
		Workspace:     scalar.Workspace,
		Project:       scalar.Project,
		JailFlags:     scalar.JailFlags,
		ExtraArgs:     strings.Fields(scalar.ExtraArgs),
		ParamValues:   scalar.ParamValues,
	}
	applyOptionDefaults(o, declaredKeys(data))
	return nil
}

// applyOptionDefaults turns the safe defaults back on for the toggles the
// document did not name. An explicit `jail: false` stays false — the point is
// that omission and denial are different answers, and only omission gets the
// default. declared == nil means the keys could not be read, in which case the
// safest reading is that nothing was declared.
func applyOptionDefaults(o *Options, declared map[string]bool) {
	if !declared["jail"] {
		o.Jail = true
	}
	if !declared["memory"] {
		o.Memory = true
	}
}

// declaredKeys returns the set of top-level keys of a YAML mapping. It decodes
// into a plain map on purpose: decoding into Options would run the custom
// unmarshaler above, which fills in defaults and would therefore erase the very
// distinction between "declared" and "absent" this is asked to report.
func declaredKeys(data []byte) map[string]bool {
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil || probe == nil {
		return nil
	}
	declared := make(map[string]bool, len(probe))
	for key := range probe {
		declared[key] = true
	}
	return declared
}

// Global is the machine-wide catalog of agents, tools, and permissions.
type Global struct {
	Version         string       `yaml:"version"`
	MemoryServerURL string       `yaml:"memory_server_url,omitempty"`
	MemoryAuthToken string       `yaml:"memory_auth_token,omitempty"`
	Agents          []Agent      `yaml:"agents"`
	Tools           []Tool       `yaml:"tools,omitempty"`
	Permissions     []Permission `yaml:"permissions"`
	DefaultMounts   []string     `yaml:"default_mounts,omitempty"`
	// RecentAgents is the most-recently-used agent commands (newest first).
	// The TUI lists installed agents in this order so the usual tools float
	// to the top. Updated on every successful launch.
	RecentAgents []string           `yaml:"recent_agents,omitempty"`
	Profiles     map[string]Profile `yaml:"profiles,omitempty"`
	// TrustedLocalConfigs records launcher-saved local configs bound to both
	// content hash and canonical path. A bare hash (legacy) is never enough:
	// identical bytes in another checkout must not inherit trust (ARCHITECTURE
	// invariant 2b).
	TrustedLocalConfigs []TrustedLocalEntry `yaml:"trusted_local_configs,omitempty"`
}

// TrustedLocalEntry is one provenance record: the canonical absolute path of a
// local config the launcher saved, plus the SHA-256 of its bytes at save time.
type TrustedLocalEntry struct {
	Path string `yaml:"path"`
	Hash string `yaml:"hash"`
}

// UnmarshalYAML accepts the current map form and the schema-2.0 form, which was
// a bare hash string with no path bound to it.
//
// The legacy form is read but deliberately not honored: it decodes to an entry
// with an empty Path, and LocalConfigTrusted compares against a canonical
// absolute path that is never empty, so it can never match. That is the point
// of the schema change — a hash alone proves the bytes, not the file, so
// identical bytes in a cloned checkout must not inherit the operator's trust.
//
// Reading it anyway is what keeps the rest of the global config alive. Rejecting
// the scalar here would fail the whole document, and LoadGlobal falls back to
// DefaultGlobal on a parse error: an operator upgrading from 2.0 would silently
// lose their --add agents, their profiles, their MRU list and their memory
// token, on top of the trust records they were always going to have to re-save.
// The stale rows are dropped from disk by the next RecordTrustedLocalConfig.
func (t *TrustedLocalEntry) UnmarshalYAML(data []byte) error {
	type trustedLocalEntryWithoutMethods TrustedLocalEntry
	var mapForm trustedLocalEntryWithoutMethods
	if err := yaml.Unmarshal(data, &mapForm); err == nil {
		*t = TrustedLocalEntry(mapForm)
		return nil
	}
	var legacyHash string
	if err := yaml.Unmarshal(data, &legacyHash); err != nil {
		return fmt.Errorf("parse trusted local config entry: %w", err)
	}
	*t = TrustedLocalEntry{Hash: strings.TrimSpace(legacyHash)}
	return nil
}

// recentAgentsMax is the cap on the MRU list stored in the global config.
const recentAgentsMax = 32

// TouchRecentAgent records command as the most recently used agent (newest
// first), deduplicating and capping the list. Empty commands are ignored.
func TouchRecentAgent(cfg *Global, command string) {
	if cfg == nil {
		return
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	out := make([]string, 0, len(cfg.RecentAgents)+1)
	out = append(out, command)
	for _, existing := range cfg.RecentAgents {
		if existing == command {
			continue
		}
		out = append(out, existing)
	}
	if len(out) > recentAgentsMax {
		out = out[:recentAgentsMax]
	}
	cfg.RecentAgents = out
}

// Profile is a named, reusable selection snapshot stored in the global
// config. It mirrors the workspace-local selection shape (agent, permissions,
// mounts, options). A nil Options pointer means the profile does not override
// the option toggles, so the loader's safe defaults remain in effect.
type Profile struct {
	Agent       string          `yaml:"agent"`
	Permissions map[string]bool `yaml:"permissions,omitempty"`
	Mounts      []Mount         `yaml:"mounts,omitempty"`
	Options     *Options        `yaml:"options,omitempty"`
}

// SetProfile validates and stores a named profile in the global config,
// creating the profiles map on first use.
func SetProfile(global *Global, name string, profile Profile) error {
	if global == nil {
		return errors.New("global config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("profile name is required")
	}
	profile.Agent = strings.TrimSpace(profile.Agent)
	if profile.Agent == "" {
		return errors.New("profile agent is required")
	}
	if global.Profiles == nil {
		global.Profiles = make(map[string]Profile)
	}
	global.Profiles[name] = profile
	return nil
}

// DeleteProfile removes a named profile, reporting whether it existed.
func DeleteProfile(global *Global, name string) bool {
	if global == nil {
		return false
	}
	if _, ok := global.Profiles[name]; !ok {
		return false
	}
	delete(global.Profiles, name)
	return true
}

// ProfileNames returns the saved profile names in sorted order.
func ProfileNames(global Global) []string {
	names := make([]string, 0, len(global.Profiles))
	for name := range global.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ProfileSummary returns a compact, human-readable description of a profile
// for --list-profiles and the TUI profiles section.
func ProfileSummary(profile Profile) string {
	var parts []string
	if profile.Options != nil {
		var toggles []string
		if profile.Options.Jail {
			toggles = append(toggles, "jail")
		}
		if profile.Options.Memory {
			toggles = append(toggles, "memory")
		}
		if profile.Options.Yolo {
			toggles = append(toggles, "yolo")
		}
		if len(toggles) > 0 {
			parts = append(parts, strings.Join(toggles, ","))
		}
		if len(profile.Options.ParamValues) > 0 {
			parts = append(parts, fmt.Sprintf("params=%d", len(profile.Options.ParamValues)))
		}
	}
	if len(profile.Mounts) > 0 {
		parts = append(parts, fmt.Sprintf("mounts=%d", len(profile.Mounts)))
	}
	return strings.Join(parts, " ")
}

// Local is the per-workspace launcher configuration.
type Local struct {
	Version     string          `yaml:"version"`
	Agent       string          `yaml:"agent"`
	Permissions map[string]bool `yaml:"permissions,omitempty"`
	Mounts      []Mount         `yaml:"mounts,omitempty"`
	Options     Options         `yaml:"options"`
}

// DefaultGlobal returns the built-in global catalog used when no global
// config file exists or when individual sections are missing from it.
func DefaultGlobal() Global {
	return Global{
		Version:         CurrentVersion,
		MemoryServerURL: DefaultMemoryServerURL,
		Agents: []Agent{
			{Name: "Claude Code", Command: "claude", SupportsMemory: true, SupportsYolo: true, Description: "Anthropic's Claude Code", YoloFlag: "--dangerously-skip-permissions", Params: []Param{modelParam("for example sonnet or opus")}, Memory: memoryFor("claude-code")},
			{Name: "Codex", Command: "codex", SupportsMemory: true, SupportsYolo: false, Description: "OpenAI Codex CLI", YoloFlag: "--dangerously-bypass-approvals-and-sandbox", Params: []Param{modelParam("for example gpt-5")}, Memory: memoryFor("codex")},
			{Name: "OpenCode", Command: "opencode", SupportsMemory: true, SupportsYolo: true, Description: "OpenCode CLI", YoloFlag: "--auto", Memory: memoryFor("opencode")},
			{Name: "Kimi Code", Command: "kimi", Aliases: []string{"kimi-cli", kimiCodeID}, SupportsMemory: true, SupportsYolo: false, Description: "Moonshot Kimi Code CLI", YoloFlag: "--yolo", Params: []Param{modelParam("for example k2"), {Name: "query", Flag: "--query", Description: "Initial query sent to Kimi", TakesValue: true}}, Memory: memoryFor(kimiCodeID)},
			{Name: "Kilo Code", Command: "kilo", Aliases: []string{"kilocode", "kilo-code"}, SupportsMemory: false, SupportsYolo: false, Description: "Kilo Code CLI", Release: &GitHubRelease{
				Repository: "Kilo-Org/kilocode",
				Assets: map[string]string{
					platformLinuxAMD64:  "kilo-linux-x64.tar.gz",
					"linux-arm64":       "kilo-linux-arm64.tar.gz",
					"darwin-amd64":      "kilo-darwin-x64.zip",
					platformDarwinARM64: "kilo-darwin-arm64.zip",
				},
				Binary: "kilo",
			}},
			{Name: "MiMo Code", Command: "mimo", Aliases: []string{"mimocode", "mimo-code"}, SupportsMemory: false, SupportsYolo: true, Description: "Xiaomi MiMo Code CLI"},
			{Name: "Antigravity", Command: "agy", Aliases: []string{"antigravity", antigravID}, SupportsMemory: false, SupportsYolo: false, Description: "Antigravity CLI", Memory: memoryFor(antigravID)},
			{Name: "Pi", Command: "pi", Aliases: []string{"pi-coding-agent"}, SupportsMemory: true, SupportsYolo: true, Description: "Pi coding agent", YoloFlag: "--approve", Memory: hooksOnlyMemoryIntegration("pi")},
			{Name: "Crush", Command: "crush", SupportsMemory: true, SupportsYolo: false, Description: "Charmbracelet Crush (ai-memory managed run only)", YoloFlag: "--yolo"},
			{Name: "Oh My Pi", Command: "omp", Aliases: []string{"oh-my-pi"}, SupportsMemory: true, SupportsYolo: true, Description: "Oh My Pi", Memory: memoryFor("omp")},
			{Name: "Cursor Agent", Command: "cursor-agent", Aliases: []string{"cursor"}, SupportsMemory: false, SupportsYolo: false, Description: "Cursor Agent CLI", Memory: memoryFor("cursor")},
			{Name: "Grok", Command: "grok", SupportsMemory: true, SupportsYolo: false, Description: "Grok CLI", Memory: memoryFor("grok")},
			{Name: "Zero", Command: "zero", SupportsMemory: false, SupportsYolo: false, Description: "Zero agent CLI", Memory: memoryFor("zero")},
			{Name: "Devin", Command: "devin", SupportsMemory: false, SupportsYolo: false, Description: "Devin CLI", Memory: memoryFor("devin")},
			// oc is a local preset TUI that ends up launching opencode; ai-memory
			// only knows the harness name "opencode", so RunHarness remaps it.
			{Name: "OpenCode Presets", Command: "oc", SupportsMemory: true, SupportsYolo: true, Description: "OpenCode preset selector", YoloFlag: "--auto", Memory: memoryForHarness("opencode")},
			{Name: "Gemini CLI", Command: "gemini", Aliases: []string{geminiCLIID}, SupportsMemory: false, SupportsYolo: false, Description: "Google Gemini CLI", Params: []Param{modelParam("for example gemini-2.5-pro")}, Memory: memoryFor(geminiCLIID)},
			{Name: "Qwen Code", Command: "qwen", Aliases: []string{"qwen-code"}, SupportsMemory: false, SupportsYolo: false, Description: "Alibaba Qwen Code CLI"},
			{Name: "Aider", Command: "aider", SupportsMemory: false, SupportsYolo: true, Description: "Aider CLI"},
			{Name: "Goose", Command: "goose", SupportsMemory: false, SupportsYolo: false, Description: "Block Goose CLI"},
			{Name: "Kiro CLI", Command: "kiro-cli", Aliases: []string{"kiro"}, SupportsMemory: false, SupportsYolo: false, Description: "Kiro CLI"},
			{Name: "OpenClaw", Command: "openclaw", SupportsMemory: false, SupportsYolo: true, Description: "OpenClaw CLI", Memory: mcpOnlyMemoryIntegration("openclaw")},
			{Name: "Hermes Agent", Command: "hermes", SupportsMemory: false, SupportsYolo: false, Description: "Hermes Agent CLI"},
			{Name: "Cline", Command: "cline", SupportsMemory: false, SupportsYolo: false, Description: "Cline CLI"},
		},
		Tools: []Tool{
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
						"linux-arm64":       "ai-memory-linux-aarch64.tar.gz",
						"darwin-amd64":      "ai-memory-macos-x86_64.tar.gz",
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

// LoadGlobal reads the global config, falling back to DefaultGlobal for a
// missing file and merging built-in defaults for omitted sections.
func LoadGlobal(path string) (Global, error) {
	defaults := DefaultGlobal()
	if path == "" {
		return defaults, nil
	}
	b, err := os.ReadFile(path) // #nosec G304 -- path is the user-supplied global config location by design
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, fmt.Errorf("read global config: %w", err)
	}
	var cfg Global
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return defaults, fmt.Errorf("parse global config %s: %w", path, err)
	}
	cfg = mergeGlobalDefaults(defaults, cfg)
	if err := ValidateVersion(cfg.Version); err != nil {
		return defaults, err
	}
	return cfg, nil
}

// SaveGlobal persists the global catalog atomically and with user-only
// permissions. It is used by the --add command and keeps the catalog
// extensible without requiring a new launcher build.
func SaveGlobal(path string, cfg Global) error {
	if path == "" {
		return errors.New("global config path is empty")
	}
	cfg.Version = CurrentVersion
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	return writeGlobalAtomically(path, b)
}

// SaveRecentAgents persists only the most-recently-used list, leaving every
// other key in the file untouched. The launch path used to call SaveGlobal on
// every run, which wrote the *merged* in-memory catalog back to disk — freezing
// that release's built-in agents and permissions into the user's config, so a
// later release's additions never appeared.
func SaveRecentAgents(path string, recent []string) error {
	if path == "" {
		return errors.New("global config path is empty")
	}
	document := make(map[string]any)
	b, err := os.ReadFile(path) // #nosec G304 -- path is the user's config location by design
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, &document); err != nil {
			return fmt.Errorf("parse global config %s: %w", path, err)
		}
		if document == nil {
			document = make(map[string]any)
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read global config: %w", err)
	}
	document["version"] = CurrentVersion
	document["recent_agents"] = recent
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	return writeGlobalAtomically(path, encoded)
}

// trustedLocalConfigsMax caps the provenance list so a long history of saves
// cannot grow the global config without bound.
const trustedLocalConfigsMax = 50

// localConfigHash returns the hex SHA-256 of a local config file's bytes.
func localConfigHash(path string) (string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- path is the user's config location by design
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalLocalPath returns a stable absolute path for trust binding.
// EvalSymlinks collapses aliases so two spellings of the same file share one
// record; on failure the cleaned absolute form is used.
func canonicalLocalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs), nil
	}
	return filepath.Clean(resolved), nil
}

// LocalConfigTrusted reports whether the local config at path is the same file
// (canonical path) and the same bytes the launcher saved. A repository-shipped
// .ai-launch.yaml has no record; editing after save changes the hash; copying
// identical bytes to another path does not inherit trust.
func LocalConfigTrusted(global Global, path string) bool {
	hash, err := localConfigHash(path)
	if err != nil {
		return false
	}
	canonical, err := canonicalLocalPath(path)
	if err != nil {
		return false
	}
	for _, trusted := range global.TrustedLocalConfigs {
		// An empty Path is a schema-2.0 record (bare hash, no file bound). It
		// never grants trust: the comparison below would already reject it,
		// but saying so explicitly keeps the rule from depending on the
		// coincidence that a canonical path is never empty.
		if trusted.Path == "" {
			continue
		}
		if trusted.Hash == hash && trusted.Path == canonical {
			return true
		}
	}
	return false
}

// RecordTrustedLocalConfig notes path+hash of a launcher-saved local config in
// the global config, leaving every other key untouched (same discipline as
// SaveRecentAgents). The next launch honors that exact path and content.
func RecordTrustedLocalConfig(globalPath, localPath string) error {
	if globalPath == "" {
		return errors.New("global config path is empty")
	}
	hash, err := localConfigHash(localPath)
	if err != nil {
		return fmt.Errorf("hash local config: %w", err)
	}
	canonical, err := canonicalLocalPath(localPath)
	if err != nil {
		return fmt.Errorf("canonical local path: %w", err)
	}
	document, err := readGlobalDocument(globalPath)
	if err != nil {
		return err
	}
	entry := TrustedLocalEntry{Path: canonical, Hash: hash}
	entries := append(trustedEntriesExcluding(document, entry), entry)
	if len(entries) > trustedLocalConfigsMax {
		entries = entries[len(entries)-trustedLocalConfigsMax:]
	}
	// Encode as plain maps so the on-disk shape stays readable and does not
	// depend on struct tags when the rest of the document is map[string]any.
	serialized := make([]map[string]string, 0, len(entries))
	for _, e := range entries {
		serialized = append(serialized, map[string]string{"path": e.Path, "hash": e.Hash})
	}
	document["version"] = CurrentVersion
	document["trusted_local_configs"] = serialized
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	return writeGlobalAtomically(globalPath, encoded)
}

// readGlobalDocument loads the global config as a plain YAML document so a
// single key can be updated without rewriting the merged catalog. A missing
// file yields an empty document.
func readGlobalDocument(globalPath string) (map[string]any, error) {
	document := make(map[string]any)
	b, err := os.ReadFile(globalPath) // #nosec G304 -- path is the user's config location by design
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, &document); err != nil {
			return nil, fmt.Errorf("parse global config %s: %w", globalPath, err)
		}
		if document == nil {
			document = make(map[string]any)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("read global config: %w", err)
	}
	return document, nil
}

// trustedEntriesExcluding returns the document's recorded trusted-config
// entries in order, dropping malformed rows and any entry equal to the one
// being re-recorded (caller re-appends it at the tail). Legacy bare-hash
// strings are dropped: they cannot bind a path and must not grant trust.
func trustedEntriesExcluding(document map[string]any, skip TrustedLocalEntry) []TrustedLocalEntry {
	existing, ok := document["trusted_local_configs"].([]any)
	if !ok {
		return nil
	}
	out := make([]TrustedLocalEntry, 0, len(existing))
	for _, raw := range existing {
		parsed, ok := parseTrustedLocalEntry(raw)
		if !ok {
			continue // legacy bare hash or malformed row
		}
		if parsed.Path == skip.Path && parsed.Hash == skip.Hash {
			continue
		}
		out = append(out, parsed)
	}
	return out
}

// parseTrustedLocalEntry accepts the map form written by RecordTrustedLocalConfig.
// Bare strings (pre-path-binding hashes) are rejected so they never grant trust.
func parseTrustedLocalEntry(raw any) (TrustedLocalEntry, bool) {
	switch value := raw.(type) {
	case map[string]any:
		path, _ := value["path"].(string)
		hash, _ := value["hash"].(string)
		path = strings.TrimSpace(path)
		hash = strings.TrimSpace(hash)
		if path == "" || hash == "" {
			return TrustedLocalEntry{}, false
		}
		return TrustedLocalEntry{Path: path, Hash: hash}, true
	case map[string]string:
		path := strings.TrimSpace(value["path"])
		hash := strings.TrimSpace(value["hash"])
		if path == "" || hash == "" {
			return TrustedLocalEntry{}, false
		}
		return TrustedLocalEntry{Path: path, Hash: hash}, true
	default:
		return TrustedLocalEntry{}, false
	}
}

type atomicWriteError struct {
	stage string
	err   error
}

func (e *atomicWriteError) Error() string { return e.stage + ": " + e.err.Error() }
func (e *atomicWriteError) Unwrap() error { return e.err }

// atomicWriteFile writes data through a temporary file and renames it into
// place. Callers create the destination directory and add their domain label
// to errors, while this helper owns the failure-prone write lifecycle.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	temporary, err := createTempFile(filepath.Dir(path), ".ai-launch-atomic-*.tmp")
	if err != nil {
		return &atomicWriteError{stage: "create temporary", err: err}
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return &atomicWriteError{stage: "write", err: err}
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return &atomicWriteError{stage: "protect", err: err}
	}
	if err := temporary.Close(); err != nil {
		return &atomicWriteError{stage: "close", err: err}
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return &atomicWriteError{stage: "replace", err: err}
	}
	return nil
}

func wrapAtomicWriteError(label string, err error) error {
	var failure *atomicWriteError
	if errors.As(err, &failure) {
		if label == "local config" && failure.stage == "create temporary" {
			return fmt.Errorf("create temporary config: %w", failure.err)
		}
		return fmt.Errorf("%s %s: %w", failure.stage, label, failure.err)
	}
	return err
}

// writeGlobalAtomically writes the global config through a temporary file with
// user-only permissions, so a crash mid-write never leaves a partial catalog.
func writeGlobalAtomically(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create global config directory: %w", err)
	}
	if err := atomicWriteFile(path, b, 0o600); err != nil {
		return wrapAtomicWriteError("global config", err)
	}
	return nil
}

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

// LoadLocal reads the workspace-local config, falling back to DefaultLocal
// for a missing file and retaining safe defaults for omitted option keys.
// Files larger than maxLocalConfigBytes are rejected before full allocation.
func LoadLocal(path string) (Local, error) {
	cfg := DefaultLocal()
	if path == "" {
		return cfg, nil
	}
	// #nosec G304 -- path is the user-supplied local config location by design
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read local config: %w", err)
	}
	defer func() { _ = f.Close() }()
	// Read at most limit+1 so oversize is detected without slurping a multi-GB
	// hostile file into memory.
	limited := io.LimitReader(f, int64(maxLocalConfigBytes)+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return cfg, fmt.Errorf("read local config: %w", err)
	}
	if len(b) > maxLocalConfigBytes {
		return cfg, fmt.Errorf("local config %s exceeds %d byte limit", path, maxLocalConfigBytes)
	}
	var loaded Local
	if err := yaml.Unmarshal(b, &loaded); err != nil {
		return cfg, fmt.Errorf("parse local config %s: %w", path, err)
	}
	if loaded.Version == "" {
		loaded.Version = CurrentVersion
	}
	if loaded.Agent == "" {
		loaded.Agent = cfg.Agent
	}
	if loaded.Permissions == nil {
		loaded.Permissions = map[string]bool{}
	}
	// An omitted options block must retain safe defaults. When the block IS
	// present, Options.UnmarshalYAML has already restored the default for each
	// key the document left out, so explicit false values remain meaningful.
	if !hasOptionsBlock(b) {
		loaded.Options = cfg.Options
	}
	if err := ValidateVersion(loaded.Version); err != nil {
		return cfg, err
	}
	return loaded, nil
}

// SaveLocal persists the local config atomically and with user-only
// permissions.
func SaveLocal(path string, cfg Local) error {
	if path == "" {
		return errors.New("local config path is empty")
	}
	cfg.Version = CurrentVersion
	if cfg.Permissions == nil {
		cfg.Permissions = map[string]bool{}
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode local config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := atomicWriteFile(path, b, 0o600); err != nil {
		return wrapAtomicWriteError("local config", err)
	}
	return nil
}

// ValidateVersion reports whether a config schema version is compatible with
// this build: empty (pre-versioning) or any entry in LoadableVersions.
func ValidateVersion(version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil
	}
	for _, loadable := range LoadableVersions {
		if version == loadable {
			return nil
		}
	}
	return fmt.Errorf("unsupported config version %q (supported: %s)",
		version, strings.Join(LoadableVersions, ", "))
}

func mergeGlobalDefaults(defaults, cfg Global) Global {
	if cfg.Version == "" {
		cfg.Version = defaults.Version
	}
	if strings.TrimSpace(cfg.MemoryServerURL) == "" {
		cfg.MemoryServerURL = defaults.MemoryServerURL
	}
	cfg.Agents = mergeAgents(defaults.Agents, cfg.Agents)
	cfg.Permissions = mergePermissions(defaults.Permissions, cfg.Permissions)
	if len(cfg.Tools) == 0 {
		cfg.Tools = defaults.Tools
	}
	if len(cfg.DefaultMounts) == 0 {
		cfg.DefaultMounts = defaults.DefaultMounts
	}
	return cfg
}

// mergeAgents merges the built-in agent catalog with the user's, per entry,
// keyed by Command. A user field that is set wins; a field the user left empty
// falls back to the built-in default, so an entry written by hand — or
// materialized by an older release — no longer loses Memory.RunHarness,
// YoloFlag or Params. Replacing the list wholesale is what made the builder
// need a hardcoded "oc" remap.
func mergeAgents(defaults, user []Agent) []Agent {
	if len(user) == 0 {
		return defaults
	}
	byCommand := make(map[string]Agent, len(user))
	for _, agent := range user {
		byCommand[agent.Command] = agent
	}
	merged := make([]Agent, 0, len(defaults)+len(user))
	seen := make(map[string]bool, len(defaults))
	for _, base := range defaults {
		seen[base.Command] = true
		if override, ok := byCommand[base.Command]; ok {
			merged = append(merged, mergeAgent(base, override))
			continue
		}
		merged = append(merged, base)
	}
	for _, agent := range user {
		if !seen[agent.Command] {
			merged = append(merged, agent)
		}
	}
	return merged
}

// mergeAgent overlays one user entry on its built-in default. Booleans are
// taken from the user entry as-is: false is a meaningful value there (it is how
// an operator turns off memory support for an agent).
func mergeAgent(base, override Agent) Agent {
	merged := override
	if merged.Name == "" {
		merged.Name = base.Name
	}
	if len(merged.Aliases) == 0 {
		merged.Aliases = base.Aliases
	}
	if merged.SourceURL == "" {
		merged.SourceURL = base.SourceURL
	}
	if merged.Description == "" {
		merged.Description = base.Description
	}
	if merged.YoloFlag == "" {
		merged.YoloFlag = base.YoloFlag
	}
	if len(merged.Params) == 0 {
		merged.Params = base.Params
	}
	if merged.Release == nil {
		merged.Release = base.Release
	}
	if merged.Memory == nil {
		merged.Memory = base.Memory
	}
	return merged
}

// mergePermissions merges the built-in permission defaults with the user's
// configured list by ID. User entries win on conflict; defaults with IDs the
// user does not have are appended so new launcher releases surface their new
// permissions without clobbering the user's customizations.
func mergePermissions(defaults, user []Permission) []Permission {
	if len(user) == 0 {
		return defaults
	}
	byID := make(map[string]Permission, len(user))
	for _, permission := range user {
		byID[permission.ID] = permission
	}
	merged := make([]Permission, 0, len(defaults)+len(user))
	for _, permission := range defaults {
		if existing, ok := byID[permission.ID]; ok {
			merged = append(merged, existing)
		} else {
			merged = append(merged, permission)
		}
	}
	// Preserve any user-defined permissions that are not in the built-in
	// defaults (custom permissions the operator added by hand).
	for _, permission := range user {
		found := false
		for _, def := range defaults {
			if def.ID == permission.ID {
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, permission)
		}
	}
	return merged
}

// hasOptionsBlock reports whether the document declares a top-level options
// block at all. Presence has to come from the parsed document: a substring
// search over the raw bytes matches "options:" or "jail:" anywhere in the file
// — nested in a permissions block, in a comment, in a mount path — and a false
// positive there silently skips the safety default, leaving Options.Jail at
// Go's zero value. Writing the config that looks like it enables the sandbox
// was exactly what turned it off.
//
// The probe deliberately decodes into a plain map instead of Options: Options
// has a custom unmarshaler that fills in defaults, which would erase the
// distinction between "declared" and "absent".
func hasOptionsBlock(data []byte) bool {
	var probe struct {
		Options map[string]any `yaml:"options"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Options != nil
}
