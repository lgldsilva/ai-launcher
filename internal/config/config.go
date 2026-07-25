// Package config owns the versioned global and workspace-local configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// CurrentVersion is the configuration schema version written by this build.
// It versions the config FILE format only — it is not the binary release
// version (that is the `version` variable in cmd/ai-launcher, injected via
// ldflags at build time and printed by --version).
const CurrentVersion = "2.0"

// DefaultMemoryServerURL is the ai-memory server used when the global config
// does not override it.
const DefaultMemoryServerURL = "https://aimemory.raspberrypi.lan"

// Agent describes a launchable AI agent CLI and its optional integrations.
type Agent struct {
	Name           string             `yaml:"name"`
	Command        string             `yaml:"command"`
	Aliases        []string           `yaml:"aliases,omitempty"`
	Path           string             `yaml:"path,omitempty"`
	SourceURL      string             `yaml:"source_url,omitempty"`
	SupportsMemory bool               `yaml:"supports_memory"`
	SupportsYolo   bool               `yaml:"supports_yolo"`
	Description    string             `yaml:"description,omitempty"`
	YoloFlag       string             `yaml:"yolo_flag,omitempty"`
	Params         []Param            `yaml:"params,omitempty"`
	Release        *GitHubRelease     `yaml:"release,omitempty"`
	Memory         *MemoryIntegration `yaml:"memory,omitempty"`
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
	Client          string `yaml:"client"`
	Agent           string `yaml:"agent"`
	InstallMCP      bool   `yaml:"install_mcp"`
	InstallHooks    bool   `yaml:"install_hooks"`
	MCPConfigFile   string `yaml:"mcp_config_file,omitempty"`
	HooksConfigFile string `yaml:"hooks_config_file,omitempty"`
}

// Tool describes an auxiliary CLI (for example ai-jail or ai-memory) that the
// launcher can install on demand.
type Tool struct {
	Name        string         `yaml:"name"`
	Command     string         `yaml:"command"`
	Aliases     []string       `yaml:"aliases,omitempty"`
	Path        string         `yaml:"path,omitempty"`
	SourceURL   string         `yaml:"source_url,omitempty"`
	Description string         `yaml:"description,omitempty"`
	Release     *GitHubRelease `yaml:"release,omitempty"`
}

// Permission is a toggleable capability with optional dependencies on other
// permissions (Requires) and a Locked flag for values the user cannot change.
type Permission struct {
	ID       string   `yaml:"id"`
	Name     string   `yaml:"name"`
	Default  bool     `yaml:"default"`
	Locked   bool     `yaml:"locked,omitempty"`
	Requires []string `yaml:"requires,omitempty"`
}

// Mount is a host directory exposed inside the sandbox. Mode is "rw" or
// "read-only"; an empty mode defaults to read-write.
type Mount struct {
	Path string `yaml:"path"`
	Mode string `yaml:"mode,omitempty"`
}

// JailFlags holds the ai-jail v1.15 capability toggles. Pointer booleans are
// tri-state: nil keeps the ai-jail default (no flag emitted), while true and
// false emit the positive or --no- form respectively. Default-on capabilities
// (GPU, Landlock, Seccomp, Rlimits, StatusBar) only emit the --no- form when
// explicitly disabled. Browser accepts "hard", "soft", or "off".
type JailFlags struct {
	Lockdown      *bool    `yaml:"lockdown,omitempty"`
	PrivateHome   *bool    `yaml:"private_home,omitempty"`
	Tailscale     *bool    `yaml:"tailscale,omitempty"`
	GPU           *bool    `yaml:"gpu,omitempty"`
	Landlock      *bool    `yaml:"landlock,omitempty"`
	Seccomp       *bool    `yaml:"seccomp,omitempty"`
	Rlimits       *bool    `yaml:"rlimits,omitempty"`
	StatusBar     *bool    `yaml:"status_bar,omitempty"`
	Browser       string   `yaml:"browser,omitempty"`
	ClaudeDir     string   `yaml:"claude_dir,omitempty"`
	OverlayMaps   []string `yaml:"overlay_maps,omitempty"`
	Mask          []string `yaml:"mask,omitempty"`
	DenyPaths     []string `yaml:"deny_paths,omitempty"`
	AllowTCPPorts []int    `yaml:"allow_tcp_ports,omitempty"`
}

// IsZero reports whether no jail flag deviates from the ai-jail defaults.
func (f JailFlags) IsZero() bool {
	return f.Lockdown == nil && f.PrivateHome == nil && f.Tailscale == nil &&
		f.GPU == nil && f.Landlock == nil && f.Seccomp == nil && f.Rlimits == nil &&
		f.StatusBar == nil && f.Browser == "" && f.ClaudeDir == "" &&
		len(f.OverlayMaps) == 0 && len(f.Mask) == 0 && len(f.DenyPaths) == 0 &&
		len(f.AllowTCPPorts) == 0
}

// Options holds the per-launch behavior toggles persisted in the local config.
type Options struct {
	Jail          bool              `yaml:"jail"`
	Memory        bool              `yaml:"memory"`
	Yolo          bool              `yaml:"yolo"`
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
func (o *Options) UnmarshalYAML(data []byte) error {
	type optionsWithoutMethods Options
	var list optionsWithoutMethods
	listErr := yaml.Unmarshal(data, &list)
	if listErr == nil {
		*o = Options(list)
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
	return nil
}

// Global is the machine-wide catalog of agents, tools, and permissions.
type Global struct {
	Version         string             `yaml:"version"`
	MemoryServerURL string             `yaml:"memory_server_url,omitempty"`
	MemoryAuthToken string             `yaml:"memory_auth_token,omitempty"`
	Agents          []Agent            `yaml:"agents"`
	Tools           []Tool             `yaml:"tools,omitempty"`
	Permissions     []Permission       `yaml:"permissions"`
	DefaultMounts   []string           `yaml:"default_mounts,omitempty"`
	Profiles        map[string]Profile `yaml:"profiles,omitempty"`
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
			{Name: "Claude Code", Command: "claude", SupportsMemory: true, SupportsYolo: true, Description: "Anthropic's Claude Code", YoloFlag: "--dangerously-skip-permissions", Params: []Param{modelParam("for example sonnet or opus")}, Memory: defaultMemoryIntegration("claude-code", "claude-code")},
			{Name: "Codex", Command: "codex", SupportsMemory: true, SupportsYolo: false, Description: "OpenAI Codex CLI", YoloFlag: "--dangerously-bypass-approvals-and-sandbox", Params: []Param{modelParam("for example gpt-5")}, Memory: defaultMemoryIntegration("codex", "codex")},
			{Name: "OpenCode", Command: "opencode", SupportsMemory: true, SupportsYolo: true, Description: "OpenCode CLI", YoloFlag: "--auto", Memory: defaultMemoryIntegration("opencode", "opencode")},
			{Name: "Kimi Code", Command: "kimi", Aliases: []string{"kimi-cli", "kimi-code"}, SupportsMemory: true, SupportsYolo: false, Description: "Moonshot Kimi Code CLI", YoloFlag: "--yolo", Params: []Param{modelParam("for example k2"), {Name: "query", Flag: "--query", Description: "Initial query sent to Kimi", TakesValue: true}}, Memory: defaultMemoryIntegration("kimi-code", "kimi-code")},
			{Name: "Kilo Code", Command: "kilo", Aliases: []string{"kilocode", "kilo-code"}, SupportsMemory: true, SupportsYolo: false, Description: "Kilo Code CLI", Release: &GitHubRelease{
				Repository: "Kilo-Org/kilocode",
				Assets: map[string]string{
					"linux-amd64":  "kilo-linux-x64.tar.gz",
					"linux-arm64":  "kilo-linux-arm64.tar.gz",
					"darwin-amd64": "kilo-darwin-x64.zip",
					"darwin-arm64": "kilo-darwin-arm64.zip",
				},
				Binary: "kilo",
			}},
			{Name: "MiMo Code", Command: "mimo", Aliases: []string{"mimocode", "mimo-code"}, SupportsMemory: true, SupportsYolo: true, Description: "Xiaomi MiMo Code CLI"},
			{Name: "Antigravity", Command: "agy", Aliases: []string{"antigravity", "antigravity-cli"}, SupportsMemory: true, SupportsYolo: false, Description: "Antigravity CLI", Memory: defaultMemoryIntegration("antigravity-cli", "antigravity-cli")},
			{Name: "Pi", Command: "pi", Aliases: []string{"pi-coding-agent"}, SupportsMemory: true, SupportsYolo: true, Description: "Pi coding agent", YoloFlag: "--approve", Memory: defaultMemoryIntegration("pi", "pi")},
			{Name: "Crush", Command: "crush", SupportsMemory: true, SupportsYolo: false, Description: "Charmbracelet Crush (ai-memory managed run only)", YoloFlag: "--yolo"},
			{Name: "Oh My Pi", Command: "omp", Aliases: []string{"oh-my-pi"}, SupportsMemory: true, SupportsYolo: true, Description: "Oh My Pi", Memory: defaultMemoryIntegration("omp", "omp")},
			{Name: "Cursor Agent", Command: "cursor-agent", Aliases: []string{"cursor"}, SupportsMemory: true, SupportsYolo: false, Description: "Cursor Agent CLI", Memory: defaultMemoryIntegration("cursor", "cursor")},
			{Name: "Grok", Command: "grok", SupportsMemory: true, SupportsYolo: false, Description: "Grok CLI", Memory: defaultMemoryIntegration("grok", "grok")},
			{Name: "Zero", Command: "zero", SupportsMemory: true, SupportsYolo: false, Description: "Zero agent CLI", Memory: defaultMemoryIntegration("zero", "zero")},
			{Name: "Devin", Command: "devin", SupportsMemory: true, SupportsYolo: false, Description: "Devin CLI", Memory: defaultMemoryIntegration("devin", "devin")},
			{Name: "OpenCode Presets", Command: "oc", SupportsMemory: true, SupportsYolo: true, Description: "OpenCode preset selector"},
			{Name: "Gemini CLI", Command: "gemini", Aliases: []string{"gemini-cli"}, SupportsMemory: true, SupportsYolo: false, Description: "Google Gemini CLI", Params: []Param{modelParam("for example gemini-2.5-pro")}, Memory: defaultMemoryIntegration("gemini-cli", "gemini-cli")},
			{Name: "Qwen Code", Command: "qwen", Aliases: []string{"qwen-code"}, SupportsMemory: true, SupportsYolo: false, Description: "Alibaba Qwen Code CLI"},
			{Name: "Aider", Command: "aider", SupportsMemory: true, SupportsYolo: true, Description: "Aider CLI"},
			{Name: "Goose", Command: "goose", SupportsMemory: true, SupportsYolo: false, Description: "Block Goose CLI"},
			{Name: "Kiro CLI", Command: "kiro-cli", Aliases: []string{"kiro"}, SupportsMemory: true, SupportsYolo: false, Description: "Kiro CLI"},
			{Name: "OpenClaw", Command: "openclaw", SupportsMemory: true, SupportsYolo: true, Description: "OpenClaw CLI", Memory: mcpOnlyMemoryIntegration("openclaw")},
			{Name: "Hermes Agent", Command: "hermes", SupportsMemory: true, SupportsYolo: false, Description: "Hermes Agent CLI"},
			{Name: "Cline", Command: "cline", SupportsMemory: true, SupportsYolo: false, Description: "Cline CLI"},
		},
		Tools: []Tool{
			{
				Name: "ai-jail", Command: "ai-jail", Description: "Sandbox wrapper used by ai-launcher (Linux and macOS only)",
				Release: &GitHubRelease{
					Repository: "akitaonrails/ai-jail",
					// ai-jail v1.15 publishes only these two assets; there are
					// no linux-arm64, darwin-amd64, or Windows builds.
					Assets: map[string]string{
						"linux-amd64":  "ai-jail-linux-x86_64.tar.gz",
						"darwin-arm64": "ai-jail-macos-aarch64.tar.gz",
					},
					Binary:        "ai-jail",
					ChecksumAsset: "checksums.txt",
				},
			},
			{
				Name: "ai-memory", Command: "ai-memory", SourceURL: "https://raw.githubusercontent.com/akitaonrails/ai-memory/main/bin/ai-memory",
				Description: "Memory wrapper; the source_url wrapper stays the primary install path and the native runner self-updates on first use",
				Release: &GitHubRelease{
					Repository: "akitaonrails/ai-memory",
					// Native per-platform runners (used on Windows, where the
					// shell wrapper does not apply). Each asset has a .sha256
					// sidecar in the release.
					Assets: map[string]string{
						"linux-amd64":   "ai-memory-linux-x86_64.tar.gz",
						"linux-arm64":   "ai-memory-linux-aarch64.tar.gz",
						"darwin-amd64":  "ai-memory-macos-x86_64.tar.gz",
						"darwin-arm64":  "ai-memory-macos-aarch64.tar.gz",
						"windows-amd64": "ai-memory-windows-x86_64.zip",
					},
					Binary: "ai-memory",
				},
			},
		},
		Permissions: []Permission{
			{ID: "jail", Name: "Jail / Sandbox", Default: true, Locked: true},
			{ID: "ssh", Name: "SSH access", Requires: []string{"jail"}},
			{ID: "gh", Name: "GitHub CLI", Requires: []string{"jail"}},
			{ID: "docker", Name: "Docker socket", Requires: []string{"jail"}},
			{ID: "gpu", Name: "GPU passthrough", Requires: []string{"docker"}},
		},
		DefaultMounts: []string{"/storage", "/storage/Projetos", "/storage/cache"},
	}
}

// DefaultLocal returns the safe-by-default local configuration.
func DefaultLocal() Local {
	return Local{Version: CurrentVersion, Agent: "claude", Permissions: map[string]bool{}, Options: Options{Jail: true, Memory: true}}
}

func defaultMemoryIntegration(client, agent string) *MemoryIntegration {
	return &MemoryIntegration{Client: client, Agent: agent, InstallMCP: true, InstallHooks: true}
}

func modelParam(example string) Param {
	return Param{Name: "model", Flag: "--model", Description: "Model to run (" + example + ")", TakesValue: true}
}

func mcpOnlyMemoryIntegration(client string) *MemoryIntegration {
	return &MemoryIntegration{Client: client, InstallMCP: true}
}

// JailDependentIDs returns the IDs of permissions that require "jail"
// directly or transitively (including "jail" itself). Platforms without
// ai-jail support use it to filter the offered permissions.
func JailDependentIDs(permissions []Permission) map[string]bool {
	dependent := map[string]bool{"jail": true}
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
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create global config directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ai-launch-global-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary global config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write global config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect global config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close global config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace global config: %w", err)
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
func LoadLocal(path string) (Local, error) {
	cfg := DefaultLocal()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path) // #nosec G304 -- path is the user-supplied local config location by design
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read local config: %w", err)
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
	// An omitted options block must retain safe defaults. Once the block is
	// present, inspect each key so explicit false values remain meaningful.
	if !hasSectionKey(b, "options") {
		loaded.Options = cfg.Options
	} else {
		if !hasOptionKey(b, "jail") {
			loaded.Options.Jail = true
		}
		if !hasOptionKey(b, "memory") {
			loaded.Options.Memory = true
		}
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ai-launch-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write local config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect local config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close local config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace local config: %w", err)
	}
	return nil
}

// ValidateVersion reports whether a config schema version is compatible with
// this build (empty, "1", "1.0", or CurrentVersion).
func ValidateVersion(version string) error {
	version = strings.TrimSpace(version)
	if version == "" || version == "1" || version == "1.0" || version == CurrentVersion {
		return nil
	}
	return fmt.Errorf("unsupported config version %q (supported: 1, 1.0, %s)", version, CurrentVersion)
}

func mergeGlobalDefaults(defaults, cfg Global) Global {
	if cfg.Version == "" {
		cfg.Version = defaults.Version
	}
	if strings.TrimSpace(cfg.MemoryServerURL) == "" {
		cfg.MemoryServerURL = defaults.MemoryServerURL
	}
	if len(cfg.Agents) == 0 {
		cfg.Agents = defaults.Agents
	}
	if len(cfg.Permissions) == 0 {
		cfg.Permissions = defaults.Permissions
	}
	if len(cfg.Tools) == 0 {
		cfg.Tools = defaults.Tools
	}
	if len(cfg.DefaultMounts) == 0 {
		cfg.DefaultMounts = defaults.DefaultMounts
	}
	return cfg
}

func hasOptionKey(data []byte, key string) bool {
	return strings.Contains(string(data), "\n"+key+":") || strings.HasPrefix(strings.TrimSpace(string(data)), key+":") || strings.Contains(string(data), "\n  "+key+":")
}

func hasSectionKey(data []byte, key string) bool {
	text := strings.TrimSpace(string(data))
	return strings.HasPrefix(text, key+":") || strings.Contains(text, "\n"+key+":")
}
