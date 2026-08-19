// Package launcher builds and validates the argv used to start an AI agent.
package launcher

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
)

// LaunchConfig carries every input needed to build and validate the argv used
// to start an agent, without performing any I/O.
type LaunchConfig struct {
	Agent      config.Agent
	Executable string
	// MemoryExecutable is the host path of the ai-memory binary. Build stays
	// I/O-free: ResolveHostBinaries (CLI finalize and the TUI before Build)
	// fills it so the jail can --map that directory (ai-memory runs inside
	// the sandbox). Without a jail, Build still emits the bare PATH name.
	MemoryExecutable string
	HomeDir          string
	MemoryServerURL  string
	MemoryAuthToken  string
	UseJail          bool
	// UseDocker selects the docker container backend: when true, Build emits
	// `docker run ...` instead of `ai-jail ...`. UseDocker and UseJail are
	// mutually exclusive — the container replaces the jail (design decision 25).
	// The docker backend needs no host-side ai-jail binary, so a container
	// launch works on every platform that has a docker daemon, including
	// Windows.
	UseDocker bool
	// JailVersion is the ai-jail version detected on this host, or "" when the
	// jail is off or the probe failed. It is filled by ResolveHostBinaries —
	// which already does I/O and already runs before Build in both the CLI and
	// the TUI — so Build stays pure and Validator.Validate stays hermetic: the
	// validator reads a string, it never execs anything.
	JailVersion string
	// ProjectDir is the host directory the container backend bind-mounts at the
	// identical path and sets as WORKDIR. It is deliberately separate from
	// Workspace and Project, which are ai-memory *scope names*: the container
	// backend used to borrow whichever of those was set, so a legitimate
	// `--workspace acme` reached Docker as a mount source and a docker launch
	// wrote the current directory back into options.workspace, where the next
	// jail launch passed it to `ai-memory run --workspace` as a path-shaped
	// scope name. Defaults to the working directory; never persisted, because
	// it is derived from where the launcher was invoked.
	ProjectDir string
	// Services is the validated infrastructure-service selection. The current
	// argv builder carries it for config/TUI/profile propagation; Compose
	// materialization consumes it when the container backend is selected.
	Services []string
	// ContainerNetworkAllowedDomains activates the v2 egress-allowlist proxy
	// when ContainerNetworkInternal is also true — see the field's doc
	// comment there. Compose-only, like ContainerNetworkInternal: never
	// consumed by container.RunConfig/BuildRunCommand (the plain `docker
	// run` path has no proxy to inject).
	ContainerNetworkAllowedDomains []string
	// ContainerEnvironment overrides the generated service connection
	// variables in compose. Values are persisted in local/profile config, but
	// are not used by the single-container backend.
	ContainerEnvironment map[string]string
	// ContainerServicePorts overrides the host-published mappings of selected
	// Compose services. Service-to-service traffic continues to use the
	// catalog's internal ports on the Compose network.
	ContainerServicePorts map[string][]config.PortMapping
	// ContainerDependencies is the effective trusted-global plus local
	// dependency-directory policy for this launch.
	ContainerDependencies config.DependencySettings
	// ContainerRuntime is the configured preference (auto, docker, podman, or
	// nerdctl). Docker.Runtime carries the concrete runtime selected for this
	// launch; keeping the preference here lets save/profile preserve whether
	// detection or an explicit flag produced it.
	ContainerRuntime string
	// ContainerContext is the optional Docker context applied to run, build,
	// image-inspect and Compose commands. Empty uses Docker's current context.
	ContainerContext string
	// ContainerNetworkInternal is the raw tri-state operator choice, carried
	// separately from Docker.NetworkInternal (the already-resolved bool
	// BuildCompose/BuildRunCommand consume) so --save and --save-profile do
	// not silently drop this safety-relevant toggle — see
	// ContainerHostGateway below for the same reasoning.
	ContainerNetworkInternal *bool
	// ContainerHostGateway is the raw tri-state operator choice for the host
	// gateway bridge, carried separately from the already-resolved
	// Docker.AddHostGateway bool so --save/--save-profile round-trip it
	// instead of silently dropping it.
	ContainerHostGateway *bool
	// Docker holds the inputs the docker argv builder needs: the image
	// selection (stacks + pinned agents), the mounts to share, and the launch
	// environment. Zero value means the docker backend was enabled without a
	// selection; Validate reports it.
	Docker container.RunConfig
	// ContainerTmux enables the optional interactive tmux session and controls
	// which host tmux configuration files are mounted read-only.
	ContainerTmux   config.TmuxSettings
	UseMemory       bool
	ContinueSession bool
	JailExec        bool
	NewWorkstream   string
	Workstream      string
	Workspace       string
	Project         string
	JailFlags       config.JailFlags
	Permissions     map[string]bool
	Mounts          []config.Mount
	Yolo            bool
	// Fresh maps to `ai-memory run --fresh`: start a new native session in the
	// current workstream instead of resuming or adopting one.
	Fresh       bool
	ExtraArgs   []string
	ParamValues map[string]string
}

// Build returns argv without executing anything. This is deliberately pure so
// dry-run, table tests, and future frontends all share the same behavior.
// The canonical composition is ai-jail outermost, then ai-memory run, then
// the harness with its native arguments.
func Build(cfg LaunchConfig) ([]string, error) {
	if cfg.ContinueSession {
		return buildContinue(cfg)
	}
	command := make([]string, 0, 12+len(cfg.Mounts)+len(cfg.ExtraArgs))
	if strings.TrimSpace(cfg.Agent.Command) == "" {
		return nil, errors.New("agent command is required")
	}
	if cfg.UseDocker {
		return buildDockerRun(cfg)
	}
	resolve := newPathResolver()
	if cfg.UseJail {
		command = appendJailArgs(command, cfg, resolve)
	}
	if cfg.UseMemory {
		command = append(command, memoryCommand(cfg, resolve), "run")
		command = appendMemoryScope(command, cfg)
		command = appendWorkstreamSelection(command, cfg)
		if cfg.Fresh {
			command = append(command, "--fresh")
		}
		// Harness name must be one ai-memory accepts (claude, opencode, …).
		// Wrappers like "oc" keep Agent.Command for PATH/display but remap here.
		command = append(command, memoryRunHarness(cfg.Agent))
		if resolved := resolve.hostPath(cfg.Executable); resolved != "" {
			command = append(command, "--executable", resolved)
		}
	} else {
		executable := cfg.Agent.Command
		if resolved := resolve.hostPath(cfg.Executable); resolved != "" {
			executable = resolved
		}
		command = append(command, executable)
	}
	command = appendDeclaredParams(command, cfg)
	command = appendYoloFlag(command, cfg)
	command = append(command, cfg.ExtraArgs...)
	return command, nil
}

// memoryRunHarness is the token after `ai-memory run`: an explicit
// Memory.RunHarness when the catalog declares one, otherwise the command
// itself. The wrapper remap used to be hardcoded here because a user config
// listing any agent replaced the whole built-in list and lost run_harness;
// config.mergeAgents now merges per entry, so the catalog is the only source.
func memoryRunHarness(agent config.Agent) string {
	if agent.Memory != nil {
		if harness := strings.TrimSpace(agent.Memory.RunHarness); harness != "" {
			return harness
		}
	}
	return agent.Command
}

// buildDockerRun translates a docker-mode LaunchConfig into the docker run
// argv. The container replaces the jail (design decision 25): the composed
// command is `docker run ... <image> <harness> [native args]`, where the
// image is built from the pinned stack/agent selection. When memory is
// enabled, the in-container command is `ai-memory run <harness>` and uses
// the Linux wrapper/runner installed in that image.
//
// Memory scope and workstream flags do not apply in container mode: ai-memory
// run's --scope/--workstream are wrapper flags consumed by the host-side
// launcher, not by the in-container binary.
func buildDockerRun(cfg LaunchConfig) ([]string, error) {
	run, err := prepareDockerRunConfig(cfg)
	if err != nil {
		return nil, err
	}
	return container.BuildRunCommand(run)
}

// EffectiveProjectDir is the directory the container backend mounts and uses as
// WORKDIR. Pre-flight, Build and the Compose artifact writers must all agree on
// it, so the resolution lives in one exported place instead of being repeated
// by each caller.
func EffectiveProjectDir(cfg LaunchConfig) string {
	return firstNonEmpty(cfg.ProjectDir, cfg.Docker.ProjectDir)
}

// prepareDockerRunConfig resolves the shared container inputs once so the
// single-container and compose backends mount the same project, credentials,
// toolchain caches and host binaries. It performs no filesystem or runtime
// calls beyond the existing mount-existence seam used by BuildRunCommand.
func prepareDockerRunConfig(cfg LaunchConfig) (container.RunConfig, error) {
	run := cfg.Docker
	// The image tag hashes the Dockerfile options; derive the run-side toggle
	// from the same permission the build side uses so both reference one tag.
	run.DockerCLI = cfg.Permissions[config.PermissionDocker]
	if contextName := strings.TrimSpace(cfg.ContainerContext); contextName != "" {
		run.Context = contextName
	}
	run.HomeDir = cfg.HomeDir
	run.ProjectDir = EffectiveProjectDir(cfg)
	if err := container.ValidateProjectDir(run.ProjectDir); err != nil {
		return container.RunConfig{}, err
	}
	run.AgentCommands = agentDockerCommands(cfg)
	run.UseMemory = cfg.UseMemory
	run.Tmux = cfg.ContainerTmux.Enabled && run.Interactive
	run.TmuxMounts = container.ResolveTmuxMounts(cfg.HomeDir, cfg.ContainerTmux, container.ExistsOnHost)
	if cfg.UseMemory {
		run.MemoryHarness = memoryRunHarness(cfg.Agent)
	}
	run.GHConfig = permissionHomeMount(cfg, config.PermissionGitHub, ".config/gh")
	run.SSHConfig = permissionHomeMount(cfg, config.PermissionSSH, ".ssh")
	run.MountDockerSocket = cfg.Permissions[config.PermissionDocker]
	if run.MountDockerSocket && !run.DockerSocketGroupSet {
		socket := strings.TrimSpace(run.DockerSocketPath)
		if socket == "" {
			socket = container.RuntimeOrDefault(run.Runtime).SocketPath()
		}
		if groupID, groupErr := container.SocketGroupID(socket); groupErr == nil {
			run.DockerSocketGroupID = groupID
			run.DockerSocketGroupSet = true
		}
	}
	dependencyResolution, err := container.ResolveDependencyMounts(
		cfg.HomeDir,
		firstNonEmpty(run.HostGOOS, container.HostGOOS()),
		run.Selection.Stacks,
		cfg.ContainerDependencies,
		container.ExistsOnHost,
	)
	if err != nil {
		return container.RunConfig{}, err
	}
	run.DependencyMounts = dependencyResolution.Mounts
	run.DependencyEnv = dependencyResolution.Env
	// Host-mounted agents (InstallHostBinary, no recipe) need their binary
	// directory bind-mounted read-only so the executable resolves inside the
	// container (R3 item 13). Derived from the selection, not the host path
	// discovery, so dry-run and the contract agree with a real launch.
	run.HostBinaryMounts = hostBinaryDirs(run.Selection)
	// MemoryNativeBin is deliberately NOT resolved here: deciding whether the
	// managed binary exists is I/O, which Build must stay free of. The caller
	// (CLI/TUI) sets it on cfg.Docker when the binary exists, mirroring how
	// Environment() stats it before exporting AI_MEMORY_NATIVE_BIN.
	// The in-container executable depends on the install kind: agents installed
	// in the image (release/script) run by their command name (the installer
	// puts the binary on PATH); host-mounted agents (InstallHostBinary) run by
	// their resolved host path, which is bind-mounted at the same location.
	// Using the host path for an image-installed agent would point at a macOS
	// binary that does not exist inside the linux container.
	if selectionHasHostBinary(run.Selection) {
		run.AgentExecutable = cfg.Executable
		if cfg.UseMemory {
			run.MemoryExecutable = cfg.Executable
		}
	}
	if run.AgentExecutable == "" {
		run.AgentExecutable = cfg.Agent.Command
	}
	if cfg.Yolo && cfg.Agent.SupportsYolo && strings.TrimSpace(cfg.Agent.YoloFlag) == "" {
		return container.RunConfig{}, fmt.Errorf("agent %q declares yolo support but has no yolo_flag for the docker backend", cfg.Agent.Command)
	}
	// Native args compose exactly like the jail path: declared params first,
	// then the yolo flag, then extra args. Without params/yolo the docker
	// backend silently dropped --param model=... and --yolo (C3).
	run.AgentArgs = appendDeclaredParams(run.AgentArgs, cfg)
	run.AgentArgs = appendYoloFlag(run.AgentArgs, cfg)
	run.AgentArgs = append(run.AgentArgs, cfg.ExtraArgs...)
	if err := run.Selection.Validate(); err != nil {
		return container.RunConfig{}, err
	}
	return run, nil
}

// hostBinaryDirs returns the unique directories of host-mounted agents
// (InstallHostBinary) in the selection, so the docker run bind-mounts them
// read-only. Paths are deduplicated and the executable path is included via
// its parent directory.
func hostBinaryDirs(selection container.Selection) []string {
	seen := make(map[string]struct{}, len(selection.Agents))
	var dirs []string
	for _, agent := range selection.Agents {
		if agent.Kind != container.InstallHostBinary {
			continue
		}
		dir := filepath.Dir(strings.TrimSpace(agent.HostPath))
		if dir == "." || dir == string(filepath.Separator) {
			continue
		}
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	return dirs
}

// selectionHasHostBinary reports whether the selection's single agent is a
// host-mounted binary (InstallHostBinary), in which case the docker run
// executes the resolved host path (bind-mounted at the same location).
func selectionHasHostBinary(selection container.Selection) bool {
	if len(selection.Agents) != 1 {
		return false
	}
	return selection.Agents[0].Kind == container.InstallHostBinary
}

// firstNonEmpty returns the first non-empty string; used to pick the docker
// workspace from the launch inputs.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// agentDockerCommands returns the agent and auxiliary CLI commands whose
// credential/history/config directories are shared with the container,
// deduplicated in catalog order. semidx is included only when the selected
// image actually installs it; the host's full home/config tree is never
// mounted as a shortcut.
func agentDockerCommands(cfg LaunchConfig) []string {
	result := make([]string, 0, 1+len(cfg.Docker.Selection.Tools))
	seen := make(map[string]struct{}, 1+len(cfg.Docker.Selection.Tools))
	add := func(command string) {
		command = strings.TrimSpace(command)
		if command == "" {
			return
		}
		if _, dup := seen[command]; dup {
			return
		}
		seen[command] = struct{}{}
		result = append(result, command)
	}
	add(cfg.Agent.Command)
	for _, tool := range cfg.Docker.Selection.Tools {
		if tool.Command == config.SemidxCommand {
			add(tool.Command)
		}
	}
	return result
}

// permissionHomeMount resolves the home-directory mount for a permission
// (mirroring jailPermissions' mountHome), returning "" when the permission is
// off or no home is known. It is the docker equivalent of the jail's
// permission→mount chain, extracted so both backends share the mapping (C3).
func permissionHomeMount(cfg LaunchConfig, permissionID, subPath string) string {
	if !cfg.Permissions[permissionID] {
		return ""
	}
	if strings.TrimSpace(cfg.HomeDir) == "" {
		return ""
	}
	return filepath.Join(cfg.HomeDir, subPath)
}
