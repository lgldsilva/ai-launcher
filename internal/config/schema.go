package config

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// Agent describes a launchable AI agent CLI and its optional integrations.
type Agent struct {
	Name      string   `yaml:"name"`
	Command   string   `yaml:"command"`
	Aliases   []string `yaml:"aliases,omitempty"`
	Path      string   `yaml:"path,omitempty"`
	SourceURL string   `yaml:"source_url,omitempty"`
	// NpmPackage names a global npm package that installs this agent's CLI
	// (e.g. "@google/gemini-cli"). The docker backend installs it with
	// `npm install -g` inside the image build (the base carries Node LTS via
	// nvm). Mutually exclusive with SourceURL.
	NpmPackage string `yaml:"npm_package,omitempty"`
	// NeedsNode marks a non-npm installer that still requires Node/npm at build
	// time (for example Pi's official installer). The Docker backend uses it to
	// include the Node stack only for images that need it.
	NeedsNode bool `yaml:"needs_node,omitempty"`
	// AllowUnverified permits the checksum-less source_url install path.
	// Without it a source_url recipe is refused (ARCHITECTURE invariant 4).
	AllowUnverified bool `yaml:"allow_unverified,omitempty"`
	// SetupInteractive marks installers whose post-install step opens an
	// interactive login (devin's `setup`). The binary is installed before that
	// step, so the Dockerfile accepts that non-interactive status only when the
	// executable is present after the installer finishes.
	SetupInteractive bool               `yaml:"setup_interactive,omitempty"`
	SupportsMemory   bool               `yaml:"supports_memory"`
	SupportsYolo     bool               `yaml:"supports_yolo"`
	Description      string             `yaml:"description,omitempty"`
	YoloFlag         string             `yaml:"yolo_flag,omitempty"`
	Params           []Param            `yaml:"params,omitempty"`
	Release          *GitHubRelease     `yaml:"release,omitempty"`
	Memory           *MemoryIntegration `yaml:"memory,omitempty"`
	// CatalogCommand preserves the canonical command from the catalog before
	// resolution overwrites Command with the alias actually found on PATH.
	// It is used by launcher internals that need the original catalog identity
	// (for example agent-specific state-directory mounts).
	CatalogCommand string `yaml:"catalog_command,omitempty"`
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

// PortMapping maps a container port to a host port. Host defaults to Internal
// when omitted; Protocol defaults to tcp when omitted.
type PortMapping struct {
	Host     int    `yaml:"host"`
	Internal int    `yaml:"internal"`
	Protocol string `yaml:"protocol,omitempty"`
}

// TmuxSettings controls the optional interactive tmux session used by the
// Docker backend. The config files are mounted read-only at their original
// host paths, so HOME-based includes and host-specific key bindings keep
// working without exposing the host home directory wholesale.
type TmuxSettings struct {
	Enabled     bool   `yaml:"enabled,omitempty"`
	Config      string `yaml:"config,omitempty"`
	LocalConfig string `yaml:"local_config,omitempty"`
	OhMyTmuxDir string `yaml:"oh_my_tmux_dir,omitempty"`
	// AdditionalPaths contains explicitly selected files or directories used
	// by custom tmux configs (plugins, scripts and sourced fragments).
	AdditionalPaths []string `yaml:"additional_paths,omitempty"`
}

// HostPort returns the effective host port for the mapping.
func (p PortMapping) HostPort() int {
	if p.Host == 0 {
		return p.Internal
	}
	return p.Host
}

// DockerFlag renders the host:container port syntax without the -p option.
// TCP is the default and is intentionally omitted.
func (p PortMapping) DockerFlag() string {
	flag := fmt.Sprintf("%d:%d", p.HostPort(), p.Internal)
	protocol := strings.ToLower(strings.TrimSpace(p.Protocol))
	if protocol != "" && protocol != "tcp" {
		flag += "/" + protocol
	}
	return flag
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
	// Docker selects the container backend: when true, the launch runs inside
	// a docker image built from the stack/agent selection instead of ai-jail.
	// Docker replaces the jail (design decision 25), so an options block with
	// docker: true implies the sandbox is the container. The field is additive:
	// documents that never mention it keep the jail default (invariant 6).
	Docker bool `yaml:"docker,omitempty"`
	// ContainerRuntime selects the container CLI. Empty is the backward-
	// compatible spelling of auto; the effective value is docker → podman →
	// nerdctl at runtime detection time.
	ContainerRuntime string `yaml:"container_runtime,omitempty"`
	// ContainerContext selects a Docker context for container commands. Empty
	// keeps Docker's current context; it is explicit and does not mutate the
	// process-wide DOCKER_CONTEXT environment.
	ContainerContext string `yaml:"container_context,omitempty"`
	// ContainerHostGateway controls the narrow host bridge used for loopback
	// ai-memory/MCP endpoints. nil keeps the compatibility default (enabled);
	// false disables both the host-gateway entry and loopback URL rewriting.
	// It never mounts the host filesystem or Docker socket.
	ContainerHostGateway *bool `yaml:"container_host_gateway,omitempty"`
	// ContainerNetworkInternal marks the generated Compose network internal
	// (no gateway to the outside world), blocking ALL outbound traffic from
	// every Compose service, including the agent's own LLM API calls. nil
	// keeps today's default (open egress); true is an explicit, all-or-
	// nothing v1 opt-in — see docs/design-decisions.md. It only takes effect
	// on the Compose path (at least one service selected); a plain `docker
	// run` launch ignores it, so preflight rejects that combination as a
	// false sense of security (internal-network-requires-compose).
	//
	// If you add/change this field, you MUST also update the scalar struct
	// and literal below in UnmarshalYAML — see
	// TestOptionsScalarFormPreservesContainerNetworkInternal.
	ContainerNetworkInternal *bool `yaml:"container_network_internal,omitempty"`
	// ContainerNetworkAllowedDomains activates the v2 egress-allowlist proxy
	// when ContainerNetworkInternal is also true: BuildCompose injects a
	// dual-homed squid service instead of leaving the agent with zero
	// egress, restricting outbound traffic to these domains. nil and an
	// empty slice are treated identically (no domains configured — only
	// len() > 0 is ever checked, matching the Services convention below).
	// Configuring this without ContainerNetworkInternal is a silent no-op,
	// so preflight rejects it as a false sense of security
	// (container-network-allowed-domains-without-internal-network).
	// See docs/design-decisions.md.
	//
	// If you add/change this field, you MUST also update the scalar struct
	// and literal below in UnmarshalYAML.
	ContainerNetworkAllowedDomains []string `yaml:"container_network_allowed_domains,omitempty"`
	// Stacks names the toolchains installed in the docker image (go, python,
	// rust, ...). Only meaningful with docker: true; ignored by the jail path.
	Stacks []string `yaml:"stacks,omitempty"`
	// Services names optional infrastructure dependencies selected for the
	// Docker environment. They are modeled now and consumed by the Compose
	// generator in a later phase.
	Services []string `yaml:"services,omitempty"`
	// ContainerMemory, ContainerCPUs, ContainerPIDs, ContainerPorts, and
	// ContainerNetwork are opt-in Docker runtime resources.
	ContainerMemory  string        `yaml:"container_memory,omitempty"`
	ContainerCPUs    string        `yaml:"container_cpus,omitempty"`
	ContainerPIDs    int64         `yaml:"container_pids,omitempty"`
	ContainerPorts   []PortMapping `yaml:"container_ports,omitempty"`
	ContainerNetwork string        `yaml:"container_network,omitempty"`
	// ContainerEnvironment overrides the generated compose connection
	// variables. It is intentionally a map so a project can point a service at
	// a custom database name, credentials provider or compatible endpoint.
	ContainerEnvironment map[string]string `yaml:"container_environment,omitempty"`
	// ContainerServicePorts overrides the host-published ports of selected
	// Compose services. The map key is the catalog service ID and the value
	// replaces that service's published mappings; an empty list intentionally
	// disables host publication while keeping the service reachable by its
	// Compose DNS name.
	ContainerServicePorts map[string][]PortMapping `yaml:"container_service_ports,omitempty"`
	// ContainerTmux starts the selected agent inside a tmux session when the
	// Docker launch has a real terminal. Host tmux configuration is optional
	// and is mounted read-only; no tmux socket or host home root is shared.
	ContainerTmux TmuxSettings `yaml:"container_tmux,omitempty"`
	// ContainerDependencies controls the host toolchain and package-cache
	// directories shared with the Docker backend. The effective value layers
	// this project setting over Global.ContainerDependencies.
	ContainerDependencies DependencySettings `yaml:"container_dependencies,omitempty"`
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

// ContainerRuntimeAuto is the default runtime preference for container mode.
const ContainerRuntimeAuto = "auto"

// EffectiveContainerRuntime normalizes the persisted preference while keeping
// old config files that predate the field equivalent to `auto`.
func EffectiveContainerRuntime(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ContainerRuntimeAuto
	}
	return value
}

// EffectiveContainerNetworkInternal resolves the tri-state
// ContainerNetworkInternal preference: nil or explicit false both mean open
// egress (today's default); only an explicit true blocks it.
func EffectiveContainerNetworkInternal(value *bool) bool {
	return value != nil && *value
}

// EffectiveContainerHostGateway resolves the tri-state ContainerHostGateway
// preference: the host bridge is on by default, so nil or explicit true both
// mean on; only an explicit false turns it off. Lives here (not in
// cmd/ai-launcher, where an equivalent used to be package-local) because
// internal/tui also needs it and cannot import cmd/ai-launcher.
func EffectiveContainerHostGateway(value *bool) bool {
	return value == nil || *value
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
		Jail                           bool                     `yaml:"jail"`
		Memory                         bool                     `yaml:"memory"`
		Yolo                           bool                     `yaml:"yolo"`
		Docker                         bool                     `yaml:"docker,omitempty"`
		ContainerRuntime               string                   `yaml:"container_runtime,omitempty"`
		ContainerContext               string                   `yaml:"container_context,omitempty"`
		ContainerHostGateway           *bool                    `yaml:"container_host_gateway,omitempty"`
		ContainerNetworkInternal       *bool                    `yaml:"container_network_internal,omitempty"`
		ContainerNetworkAllowedDomains []string                 `yaml:"container_network_allowed_domains,omitempty"`
		Stacks                         []string                 `yaml:"stacks,omitempty"`
		Services                       []string                 `yaml:"services,omitempty"`
		ContainerMemory                string                   `yaml:"container_memory,omitempty"`
		ContainerCPUs                  string                   `yaml:"container_cpus,omitempty"`
		ContainerPIDs                  int64                    `yaml:"container_pids,omitempty"`
		ContainerPorts                 []PortMapping            `yaml:"container_ports,omitempty"`
		ContainerNetwork               string                   `yaml:"container_network,omitempty"`
		ContainerEnvironment           map[string]string        `yaml:"container_environment,omitempty"`
		ContainerServicePorts          map[string][]PortMapping `yaml:"container_service_ports,omitempty"`
		ContainerDependencies          DependencySettings       `yaml:"container_dependencies,omitempty"`
		ContainerTmux                  TmuxSettings             `yaml:"container_tmux,omitempty"`
		NewWorkstream                  string                   `yaml:"new_workstream,omitempty"`
		Workstream                     string                   `yaml:"workstream,omitempty"`
		Workspace                      string                   `yaml:"workspace,omitempty"`
		Project                        string                   `yaml:"project,omitempty"`
		JailFlags                      JailFlags                `yaml:"jail_flags,omitempty"`
		ExtraArgs                      string                   `yaml:"extra_args,omitempty"`
		ParamValues                    map[string]string        `yaml:"param_values,omitempty"`
	}
	if err := yaml.Unmarshal(data, &scalar); err != nil {
		return listErr
	}
	extraArgs, err := splitScalarExtraArgs(scalar.ExtraArgs)
	if err != nil {
		return err
	}
	*o = Options{
		Jail:                           scalar.Jail,
		Memory:                         scalar.Memory,
		Yolo:                           scalar.Yolo,
		Docker:                         scalar.Docker,
		ContainerRuntime:               scalar.ContainerRuntime,
		ContainerContext:               strings.TrimSpace(scalar.ContainerContext),
		ContainerHostGateway:           scalar.ContainerHostGateway,
		ContainerNetworkInternal:       scalar.ContainerNetworkInternal,
		ContainerNetworkAllowedDomains: scalar.ContainerNetworkAllowedDomains,
		Stacks:                         scalar.Stacks,
		Services:                       scalar.Services,
		ContainerMemory:                scalar.ContainerMemory,
		ContainerCPUs:                  scalar.ContainerCPUs,
		ContainerPIDs:                  scalar.ContainerPIDs,
		ContainerPorts:                 scalar.ContainerPorts,
		ContainerNetwork:               scalar.ContainerNetwork,
		ContainerEnvironment:           cloneStringMap(scalar.ContainerEnvironment),
		ContainerServicePorts:          cloneServicePortMappings(scalar.ContainerServicePorts),
		ContainerDependencies:          scalar.ContainerDependencies.Clone(),
		ContainerTmux:                  scalar.ContainerTmux,
		NewWorkstream:                  scalar.NewWorkstream,
		Workstream:                     scalar.Workstream,
		Workspace:                      scalar.Workspace,
		Project:                        scalar.Project,
		JailFlags:                      scalar.JailFlags,
		ExtraArgs:                      extraArgs,
		ParamValues:                    scalar.ParamValues,
	}
	applyOptionDefaults(o, declaredKeys(data))
	return nil
}

// splitScalarExtraArgs parses a scalar extra_args string using the same
// quote-aware rules as the --extra-args CLI flag, so values containing spaces
// survive when quoted.
func splitScalarExtraArgs(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	args, err := SplitArgs(value)
	if err != nil {
		return nil, fmt.Errorf("extra_args: %w", err)
	}
	return args, nil
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

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func cloneServicePortMappings(values map[string][]PortMapping) map[string][]PortMapping {
	if values == nil {
		return nil
	}
	clone := make(map[string][]PortMapping, len(values))
	for serviceID, mappings := range values {
		clone[serviceID] = append([]PortMapping(nil), mappings...)
	}
	return clone
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
