package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// readable and the flag-to-config mapping can be applied as a unit.
// cliOptions holds every command-line flag value in one place so run() stays
// readable and the flag-to-config mapping can be applied as a unit.
// readable and the flag-to-config mapping can be applied as a unit.
type cliOptions struct {
	mounts, rwMounts               stringList
	params, stacks, services       stringList
	agent, extraArgs               string
	globalPath, localPath          string
	addName, addPath               string
	addCommand, addDescription     string
	newWorkstream, workstream      string
	workspace, project             string
	profile, saveProfile           string
	deleteProfile                  string
	workstreamSearch               string
	workstreamID                   string
	searchLimit                    int
	searchJSON                     bool
	ssh, gh, docker, gpu           bool
	display, pictures              bool
	tailscale, systemdUser         bool
	mise, worktree                 bool
	noJail, sandbox                bool
	dockerBackend                  bool
	containerRuntime               string
	containerContext               string
	memory, noMemory               bool
	containerMemory, containerCPUs string
	containerPIDs                  int64
	containerPorts                 stringList
	containerServicePorts          stringList
	containerNetwork               string
	composeUpdate                  string
	yolo, noYolo                   bool
	fresh                          bool
	dryRun, save, install, upgrade bool
	listProfiles                   bool
	continueSession                bool
	showVersion, doctor            bool
	noLocalConfig                  bool
	noColor, highContrast          bool
}

func (o *cliOptions) register(flags *flag.FlagSet) {
	flags.StringVar(&o.agent, "agent", "", "agent command (claude, codex, opencode, ...)")
	flags.BoolVar(&o.ssh, config.PermissionSSH, false, "enable SSH permission")
	flags.BoolVar(&o.gh, config.PermissionGitHub, false, "optional: map ~/.config/gh into the jail (for host gh auth; not required)")
	flags.BoolVar(&o.docker, config.PermissionDocker, false, "enable Docker permission")
	flags.BoolVar(&o.gpu, config.PermissionGPU, false, "enable GPU permission")
	flags.BoolVar(&o.display, "display", false, "force X11/Wayland display passthrough in the jail (Linux only)")
	flags.BoolVar(&o.pictures, "pictures", false, "map the Pictures folder into the jail (Linux/macOS)")
	flags.BoolVar(&o.tailscale, "tailscale", false, "expose the Tailscale socket in the jail (Linux/macOS)")
	flags.BoolVar(&o.systemdUser, permissionSystemdUser, false, "expose the systemd user bus in the jail (Linux only)")
	flags.BoolVar(&o.mise, "mise", false, "enable the ai-jail mise integration")
	flags.BoolVar(&o.worktree, "worktree", false, "enable Git worktree passthrough in the jail")
	flags.BoolVar(&o.noJail, flagNoJail, false, "run without ai-jail")
	flags.BoolVar(&o.sandbox, "sandbox", false, "enable ai-jail (alias for the default sandbox)")
	flags.BoolVar(&o.dockerBackend, "docker-backend", false, "run the agent inside a docker container instead of ai-jail")
	flags.StringVar(&o.containerRuntime, "container-runtime", "", "container runtime: auto, docker, podman, or nerdctl (enables container mode)")
	flags.StringVar(&o.containerContext, "container-context", "", "Docker context name (empty uses the current context; see docker context ls)")
	flags.Var(&o.stacks, "stack", "toolchain stack for the docker image (go, python, rust, java, maven, gradle, node, cpp, neovim; repeatable)")
	flags.Var(&o.services, "service", "infrastructure service for the docker environment (mongo, redis, postgres, wiremock, code-server, ...; repeatable)")
	flags.BoolVar(&o.memory, "memory", false, "enable ai-memory")
	flags.StringVar(&o.containerMemory, "container-memory", "", "docker memory limit (for example, 4g); --memory 4g is also accepted")
	flags.StringVar(&o.containerCPUs, "cpus", "", "docker CPU limit (for example, 2.0)")
	flags.Int64Var(&o.containerPIDs, "pids", 0, "docker process limit")
	flags.Var(&o.containerPorts, "publish", "publish a docker port (host:internal[/protocol], repeatable)")
	flags.Var(&o.containerServicePorts, "service-port", "override a Compose service port (service=host:internal[/protocol], repeatable)")
	flags.StringVar(&o.containerNetwork, "network", "", "docker network name (for example, host)")
	flags.StringVar(&o.composeUpdate, "compose-update", "prompt", "Compose artifact conflict policy: prompt, keep, or replace")
	flags.BoolVar(&o.noMemory, "no-memory", false, "disable ai-memory")
	flags.BoolVar(&o.yolo, "yolo", false, "pass the dangerous-mode flag to the agent")
	flags.BoolVar(&o.fresh, "fresh", false, "start a new ai-memory session in the current workstream instead of resuming one")
	flags.BoolVar(&o.noYolo, "no-yolo", false, "do not pass the dangerous-mode flag to the agent")
	flags.BoolVar(&o.dryRun, "dry-run", false, "print the generated command")
	flags.BoolVar(&o.save, "save", false, "save local configuration and exit")
	flags.BoolVar(&o.save, "save-only", false, "alias for --save")
	flags.BoolVar(&o.install, "install", false, "install missing configured tools from GitHub releases and update stale ones")
	flags.BoolVar(&o.upgrade, "upgrade", false, "force reinstall of configured tools from the latest GitHub release")
	flags.StringVar(&o.newWorkstream, "new", "", "start a new ai-memory workstream with this name")
	flags.StringVar(&o.workstream, "workstream", "", "resume an existing ai-memory workstream by name")
	flags.StringVar(&o.workspace, "workspace", "", "ai-memory workspace name (forwarded to ai-memory run)")
	flags.StringVar(&o.project, "project", "", "ai-memory project name (forwarded to ai-memory run)")
	flags.BoolVar(&o.continueSession, "continue", false, "continue the most recent ai-memory session of this checkout (ai-memory run without a harness)")
	flags.Var(&o.mounts, "mount", "read-only mount, optionally with :ro or :rw")
	flags.Var(&o.mounts, "map", "alias for --mount")
	flags.Var(&o.rwMounts, "rw-map", "read-write mount")
	flags.Var(&o.params, "param", "set a harness parameter declared in the agent catalog (name=value, repeatable)")
	flags.StringVar(&o.extraArgs, "extra-args", "", "additional agent arguments")
	flags.StringVar(&o.extraArgs, "args", "", "alias for --extra-args")
	flags.StringVar(&o.globalPath, "config", "", "global config path")
	flags.StringVar(&o.localPath, "local-config", "", "workspace config path")
	flags.BoolVar(&o.noLocalConfig, "no-local-config", false, "ignore workspace config and start from defaults")
	flags.BoolVar(&o.noColor, "no-color", false, "disable color output")
	flags.BoolVar(&o.highContrast, "high-contrast", false, "use high-contrast colors")
	flags.StringVar(&o.addName, "add", "", "add or update an agent in the global catalog")
	flags.StringVar(&o.addPath, "path", "", "executable path used with --add")
	flags.StringVar(&o.addCommand, "command", "", "command name used with --add; defaults to the executable basename")
	flags.StringVar(&o.addDescription, "description", "", "description used with --add")
	flags.StringVar(&o.profile, "profile", "", "load the named profile from the global config as the base selection (precedence: built-in defaults < local .ai-launcher/config.yaml < profile < explicit flags)")
	flags.StringVar(&o.saveProfile, "save-profile", "", "save the fully merged selection as the named profile in the global config and exit without launching")
	flags.BoolVar(&o.listProfiles, "list-profiles", false, "list the profiles saved in the global config and exit")
	flags.StringVar(&o.deleteProfile, "delete-profile", "", "delete the named profile from the global config and exit")
	flags.StringVar(&o.workstreamSearch, "workstream-search", "", "search the ai-memory workstream ledger for this query and exit")
	flags.StringVar(&o.workstreamID, "workstream-id", "", "workstream to search with --workstream-search (defaults to $AI_MEMORY_WORKSTREAM_ID)")
	flags.IntVar(&o.searchLimit, "limit", 0, "maximum results for --workstream-search (ai-memory's own default when unset)")
	flags.BoolVar(&o.searchJSON, "json", false, "emit --workstream-search results as JSON")
	flags.BoolVar(&o.showVersion, "version", false, "print the binary version and exit")
	flags.BoolVar(&o.doctor, "doctor", false, "report the installed upstream tool versions against the supported floor and exit")
}

// applyToLocal folds every explicitly-set flag into the local configuration
// and returns the effective mount list. Flag mounts replace configured ones;
// when neither flags nor the local/profile config define any mount, the
// global default_mounts are suggested (read-write by default, with the same
// optional :ro/:rw suffix as --mount).
func (o *cliOptions) applyToLocal(flags *flag.FlagSet, local *config.Local, defaultMounts []string) ([]config.Mount, error) {
	o.applyOptionFlags(flags, local)
	if err := o.applyResourceFlags(flags, local); err != nil {
		return nil, err
	}
	if err := o.applyServicePortFlags(flags, local); err != nil {
		return nil, err
	}
	if err := validateContainerOptions(local.Options); err != nil {
		return nil, err
	}
	mountConfig, err := o.buildMountConfig(flags, local.Mounts, defaultMounts)
	if err != nil {
		return nil, err
	}
	if err := o.applyExtraArgsFlag(flags, local); err != nil {
		return nil, err
	}
	if flagsWasSet(flags, "param") {
		if err := o.applyParamFlags(local); err != nil {
			return nil, err
		}
	}
	return mountConfig, nil
}

func validateContainerOptions(options config.Options) error {
	return container.ValidateRunResources(container.RunConfig{
		MemoryLimit:  options.ContainerMemory,
		CPULimit:     options.ContainerCPUs,
		PIDsLimit:    options.ContainerPIDs,
		ExposedPorts: options.ContainerPorts,
		NetworkName:  options.ContainerNetwork,
	})
}

// applyResourceFlags folds explicit container resource flags into options.
// Resource flags imply container mode because their native meaning is a
// container-runtime constraint, while an omitted flag preserves the config.
func (o *cliOptions) applyResourceFlags(flags *flag.FlagSet, local *config.Local) error {
	resourceSet := false
	if flagsWasSet(flags, "container-memory") {
		local.Options.ContainerMemory = o.containerMemory
		resourceSet = true
	}
	if flagsWasSet(flags, "cpus") {
		local.Options.ContainerCPUs = o.containerCPUs
		resourceSet = true
	}
	if flagsWasSet(flags, "pids") {
		local.Options.ContainerPIDs = o.containerPIDs
		resourceSet = true
	}
	if flagsWasSet(flags, "publish") {
		ports, err := container.ParsePortMappings(o.containerPorts)
		if err != nil {
			return err
		}
		local.Options.ContainerPorts = ports
		resourceSet = true
	}
	if flagsWasSet(flags, "network") {
		local.Options.ContainerNetwork = o.containerNetwork
		resourceSet = true
	}
	if resourceSet {
		local.Options.Docker = true
	}
	if hasContainerResources(local.Options) {
		local.Options.Docker = true
	}
	return nil
}

// hasContainerResources reports whether options ask for the container backend
// through something other than the `docker` toggle itself. Services belong in
// this list: resolveLaunchInputs turns a non-empty selection into
// `Docker = true, Jail = false`, so a file naming one swaps the sandbox exactly
// like `docker: true` does. Leaving it out let a workspace file replace ai-jail
// with a container without ever reaching enforceDockerBackendConsent.
func hasContainerResources(options config.Options) bool {
	return strings.TrimSpace(options.ContainerMemory) != "" ||
		strings.TrimSpace(options.ContainerCPUs) != "" ||
		options.ContainerPIDs != 0 || len(options.ContainerPorts) > 0 ||
		strings.TrimSpace(options.ContainerNetwork) != "" ||
		len(options.ContainerServicePorts) > 0 || len(options.Services) > 0
}

func (o *cliOptions) applyServicePortFlags(flags *flag.FlagSet, local *config.Local) error {
	if !flagsWasSet(flags, "service-port") {
		return nil
	}
	overrides, err := parseServicePortOverrides(o.containerServicePorts)
	if err != nil {
		return err
	}
	local.Options.ContainerServicePorts = overrides
	local.Options.Docker = true
	return nil
}

func parseServicePortOverrides(values []string) (map[string][]config.PortMapping, error) {
	overrides := make(map[string][]config.PortMapping)
	for _, value := range values {
		serviceID, mappingText, ok := strings.Cut(strings.TrimSpace(value), "=")
		serviceID = strings.TrimSpace(serviceID)
		mappingText = strings.TrimSpace(mappingText)
		if !ok || serviceID == "" || mappingText == "" {
			return nil, fmt.Errorf("invalid --service-port %q; expected service=host:internal[/protocol]", value)
		}
		if _, exists := container.ServiceByID(serviceID); !exists {
			return nil, fmt.Errorf("unknown compose service %q", serviceID)
		}
		mapping, err := container.ParsePortMapping(mappingText)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", serviceID, err)
		}
		overrides[serviceID] = append(overrides[serviceID], mapping)
	}
	for serviceID := range overrides {
		service, _ := container.ServiceByID(serviceID)
		if _, err := container.ServiceWithPortOverride(service, overrides); err != nil {
			return nil, err
		}
	}
	return overrides, nil
}

// applyOptionFlags folds every explicitly-set option flag into the local
// configuration. --no-jail, --no-memory, and --no-yolo invert the value they
// store. Special-casing --no-memory to "only ever disable" made
// --no-memory=false silently inert.
func (o *cliOptions) applyOptionFlags(flags *flag.FlagSet, local *config.Local) {
	options := []struct {
		name  string
		apply func()
	}{
		{flagNoJail, func() { local.Options.Jail = !o.noJail }},
		{"sandbox", func() { local.Options.Jail = o.sandbox }},
		{"docker-backend", func() { local.Options.Docker = o.dockerBackend }},
		{"container-runtime", func() {
			local.Options.ContainerRuntime = config.EffectiveContainerRuntime(o.containerRuntime)
			local.Options.Docker = true
		}},
		{"container-context", func() {
			local.Options.ContainerContext = strings.TrimSpace(o.containerContext)
			local.Options.Docker = true
		}},
		{"stack", func() { local.Options.Stacks = o.stacks }},
		{"service", func() { local.Options.Services = o.services }},
		{"memory", func() { local.Options.Memory = o.memory }},
		{"no-memory", func() { local.Options.Memory = !o.noMemory }},
		{"fresh", func() { local.Options.Fresh = o.fresh }},
		{"yolo", func() { local.Options.Yolo = o.yolo }},
		{"no-yolo", func() { local.Options.Yolo = !o.noYolo }},
		{"new", func() { local.Options.NewWorkstream = o.newWorkstream }},
		{"workstream", func() { local.Options.Workstream = o.workstream }},
		{"workspace", func() { local.Options.Workspace = o.workspace }},
		{"project", func() { local.Options.Project = o.project }},
	}
	for _, option := range options {
		if flagsWasSet(flags, option.name) {
			option.apply()
		}
	}
}

// applyExtraArgsFlag parses --extra-args/--args into the local configuration.
func (o *cliOptions) applyExtraArgsFlag(flags *flag.FlagSet, local *config.Local) error {
	if !flagsWasSet(flags, "extra-args") && !flagsWasSet(flags, "args") {
		return nil
	}
	parsed, err := splitArgs(o.extraArgs)
	if err != nil {
		return fmt.Errorf("parse extra arguments: %w", err)
	}
	local.Options.ExtraArgs = parsed
	return nil
}

// applyParamFlags folds repeatable --param name=value flags into the option
// param_values map, creating it on first use.
func (o *cliOptions) applyParamFlags(local *config.Local) error {
	for _, entry := range o.params {
		name, value, found := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return fmt.Errorf("invalid --param %q (expected name=value)", entry)
		}
		if local.Options.ParamValues == nil {
			local.Options.ParamValues = make(map[string]string)
		}
		local.Options.ParamValues[name] = value
	}
	return nil
}
