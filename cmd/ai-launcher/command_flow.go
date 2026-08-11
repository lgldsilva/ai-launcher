package main

import (
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/catalog"
	launchcmd "github.com/lgldsilva/ai-launcher/internal/cmd"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

type resolvedLaunchInputs struct {
	handled        bool
	global         config.Global
	local          config.Local
	status         catalog.AgentStatus
	resolved       bool
	permissions    map[string]bool
	mounts         []config.Mount
	positionalArgs []string
}

func run(args []string, in io.Reader, out, errOut io.Writer) error {
	// A positional `upgrade` is intercepted before normal flag parsing because
	// the remaining arguments belong to the self-update command.
	if len(args) > 0 && args[0] == "upgrade" {
		return runUpgrade(args[1:], out, errOut)
	}
	interactiveTUI := len(args) == 0
	generateCommand := len(args) > 0 && args[0] == "generate"
	if generateCommand {
		args = args[1:]
	}
	flags, opts, handled, err := parseCommandLine(args, out, errOut)
	if handled || err != nil {
		return err
	}
	composeUpdate, err := parseComposeUpdateChoice(opts.composeUpdate)
	if err != nil {
		return err
	}
	home := userHome()
	resolveConfigPaths(&opts, home)
	global, handled, err := dispatchSideCommands(flags, &opts, home, out, errOut)
	if handled || err != nil {
		return err
	}
	inputs, err := resolveLaunchInputs(flags, &opts, global, interactiveTUI)
	if err != nil {
		return err
	}
	if inputs.handled {
		return nil
	}
	composeCommand, composeArgs := parseComposeCommand(inputs.positionalArgs, &opts)
	if isGenerate, err := validateGenerateCommand(generateCommand, inputs.positionalArgs); isGenerate || err != nil {
		if err != nil {
			return err
		}
		generateLocal := cloneLocal(inputs.local)
		generateLocal.Permissions = copyPermissions(inputs.permissions)
		return generateContainerArtifacts(generateLocal, inputs.status.Agent, inputs.global, home, in, out, composeUpdate, interactiveLaunch(opts))
	}
	disableMemoryIfUnsupported(inputs.status.Agent, inputs.resolved, &inputs.local.Options.Memory, errOut)
	disableYoloIfUnsupported(inputs.status.Agent, inputs.resolved, &inputs.local.Options.Yolo, errOut)
	if !composeCommand && len(inputs.positionalArgs) > 0 {
		inputs.local.Options.ExtraArgs = append(inputs.local.Options.ExtraArgs, inputs.positionalArgs...)
	}
	dockerConfig, runtimeErr := dockerRunConfigFromOptions(dockerRunOptions{
		agent:             inputs.status.Agent,
		stacks:            inputs.local.Options.Stacks,
		home:              home,
		memoryServerURL:   inputs.global.MemoryServerURL,
		memoryAuthToken:   inputs.global.MemoryAuthToken,
		runtimePreference: inputs.local.Options.ContainerRuntime,
		interactive:       interactiveLaunch(opts),
		memoryEnabled:     inputs.local.Options.Memory,
	})
	// Keep the auxiliary-tool selection ready even when the TUI starts in
	// native mode: the operator may enable Container after opening it. A
	// malformed tool recipe is actionable only for a Docker launch; native
	// mode must not be blocked by an unused container catalog entry.
	dockerConfig.Selection, err = selectionWithContainerTools(
		dockerConfig.Selection,
		inputs.global,
		home,
		inputs.local.Options.Docker,
		runtimeErr,
	)
	if err != nil {
		runtimeErr = err
	}
	dockerConfig.MemoryLimit = inputs.local.Options.ContainerMemory
	dockerConfig.CPULimit = inputs.local.Options.ContainerCPUs
	dockerConfig.PIDsLimit = inputs.local.Options.ContainerPIDs
	dockerConfig.ExposedPorts = append([]container.PortMapping(nil), inputs.local.Options.ContainerPorts...)
	dockerConfig.NetworkName = inputs.local.Options.ContainerNetwork
	dockerConfig.NetworkInternal = config.EffectiveContainerNetworkInternal(inputs.local.Options.ContainerNetworkInternal)
	dockerConfig.Tmux = inputs.local.Options.ContainerTmux.Enabled && dockerConfig.Interactive
	dockerConfig.Context = strings.TrimSpace(inputs.local.Options.ContainerContext)
	dockerConfig.AddHostGateway = effectiveContainerHostGateway(inputs.local.Options.ContainerHostGateway)
	if !inputs.local.Options.Memory {
		dockerConfig.Env = nil
		dockerConfig.MemoryNativeBin = ""
	}
	if runtimeErr != nil && inputs.local.Options.Docker && !opts.save && !opts.dryRun {
		return runtimeErr
	}
	launchConfig := launcher.LaunchConfig{
		Agent:                          inputs.status.Agent,
		Executable:                     inputs.status.Path,
		HomeDir:                        home,
		MemoryServerURL:                inputs.global.MemoryServerURL,
		MemoryAuthToken:                inputs.global.MemoryAuthToken,
		UseJail:                        inputs.local.Options.Jail && !inputs.local.Options.Docker,
		UseDocker:                      inputs.local.Options.Docker,
		ContainerRuntime:               config.EffectiveContainerRuntime(inputs.local.Options.ContainerRuntime),
		ContainerContext:               strings.TrimSpace(inputs.local.Options.ContainerContext),
		ContainerNetworkInternal:       inputs.local.Options.ContainerNetworkInternal,
		ContainerHostGateway:           inputs.local.Options.ContainerHostGateway,
		Services:                       append([]string(nil), inputs.local.Options.Services...),
		ContainerNetworkAllowedDomains: append([]string(nil), inputs.local.Options.ContainerNetworkAllowedDomains...),
		ContainerEnvironment:           cloneStringMap(inputs.local.Options.ContainerEnvironment),
		ContainerServicePorts:          cloneServicePortMappings(inputs.local.Options.ContainerServicePorts),
		ContainerDependencies:          config.MergeDependencySettings(inputs.global.ContainerDependencies, inputs.local.Options.ContainerDependencies),
		ContainerTmux:                  inputs.local.Options.ContainerTmux,
		Docker:                         dockerConfig,
		UseMemory:                      inputs.local.Options.Memory,
		ContinueSession:                opts.continueSession,
		JailExec:                       len(args) > 0,
		NewWorkstream:                  inputs.local.Options.NewWorkstream,
		Workstream:                     inputs.local.Options.Workstream,
		Workspace:                      inputs.local.Options.Workspace,
		Project:                        inputs.local.Options.Project,
		JailFlags:                      inputs.local.Options.JailFlags,
		Permissions:                    inputs.permissions,
		Mounts:                         inputs.mounts,
		Yolo:                           inputs.local.Options.Yolo,
		Fresh:                          inputs.local.Options.Fresh,
		ExtraArgs:                      inputs.local.Options.ExtraArgs,
		ParamValues:                    inputs.local.Options.ParamValues,
	}
	// The docker backend mounts the current project; default the workspace to
	// the working directory when neither workspace nor project were configured
	// (the jail gets the cwd implicitly, docker needs it explicitly — M6).
	launchConfig = ensureDockerWorkspace(launchConfig)
	launchConfig = finalizeLaunchConfig(launchConfig, global, home, errOut)
	if composeCommand {
		return runComposeCommand(composeArgs, launchConfig, global, opts, in, out, errOut)
	}
	if opts.save && launchConfig.UseDocker {
		if err := saveContainerArtifacts(launchConfig, global, in, out, errOut, composeUpdate, interactiveLaunch(opts)); err != nil {
			return err
		}
	}
	if opts.saveProfile != "" {
		return saveProfileCommand(opts.globalPath, global, opts.saveProfile, launchConfig, out)
	}
	return launch(launchRequest{args: args, opts: opts, global: global, local: inputs.local,
		launchConfig: launchConfig, in: in, out: out, errOut: errOut, composeUpdate: composeUpdate})
}

func saveContainerArtifacts(launchConfig launcher.LaunchConfig, global config.Global, in io.Reader, out, errOut io.Writer, choice composeUpdateChoice, interactive bool) error {
	choice, err := resolveComposeUpdate(launchConfig, choice, in, out, interactive)
	if err != nil {
		return err
	}
	return materializeContainerArtifactsWithChoice(launchConfig, global, out, errOut, choice)
}

// selectionWithContainerTools keeps the Docker image selection ready for a
// native-mode TUI that may later enable Container. A malformed auxiliary tool
// recipe is reported only when Docker is the active backend and no earlier
// runtime error already explains the failed launch.
func selectionWithContainerTools(selection container.Selection, global config.Global, home string, dockerMode bool, runtimeErr error) (container.Selection, error) {
	selected, err := addContainerTools(selection, global, home)
	if err == nil {
		return selected, nil
	}
	if dockerMode && runtimeErr == nil {
		return selection, err
	}
	return selection, nil
}

func parseComposeCommand(positional []string, opts *cliOptions) (bool, []string) {
	if len(positional) == 0 || positional[0] != "compose" {
		return false, nil
	}
	args := append([]string(nil), positional[1:]...)
	for _, arg := range args {
		if arg == "--dry-run" {
			opts.dryRun = true
		}
	}
	return true, args
}

func validateGenerateCommand(explicit bool, positional []string) (bool, error) {
	if explicit {
		if len(positional) > 0 {
			return true, errors.New("generate does not accept positional agent arguments")
		}
		return true, nil
	}
	if len(positional) == 0 || positional[0] != "generate" {
		return false, nil
	}
	if len(positional) > 1 {
		return true, errors.New("generate does not accept positional agent arguments")
	}
	return true, nil
}

// dispatchSideCommands handles commands that do not launch an agent. It also
// loads the global catalog once, so every later path uses the same defaults.
func dispatchSideCommands(flags *flag.FlagSet, opts *cliOptions, home string, out, errOut io.Writer) (config.Global, bool, error) {
	if flagsWasSet(flags, "add") {
		return config.Global{}, true, launchcmd.AddAgent(opts.globalPath, opts.addName, opts.addPath, opts.addCommand, opts.addDescription, out)
	}
	global, loadErr := config.LoadGlobal(opts.globalPath)
	if loadErr != nil {
		warnf(errOut, valueFormat, loadErr)
	}
	if handled, err := runGlobalCommands(opts, global, home, out, errOut); handled {
		return global, true, err
	}
	return global, false, nil
}

func resolveLaunchInputs(flags *flag.FlagSet, opts *cliOptions, global config.Global, interactiveTUI bool) (resolvedLaunchInputs, error) {
	local, rawLocal, appliedProfile, err := loadLocalSelection(opts, global, flags.Output())
	if err != nil {
		return resolvedLaunchInputs{}, err
	}
	catalogue := catalog.New(global)
	if flags.NArg() > 0 && flags.Arg(0) == "help" {
		flags.Usage()
		return resolvedLaunchInputs{handled: true}, nil
	}
	status, resolved := resolveAgentSelection(catalogue, opts.agent, local)
	trust := localTrustFrom(catalogue, opts.agent, rawLocal, appliedProfile, opts.noLocalConfig)
	if err := enforceLocalConfigTrust(flags, global, trust, config.LocalConfigTrusted(global, opts.localPath), interactiveTUI); err != nil {
		return resolvedLaunchInputs{}, err
	}
	permissions := resolvePermissions(flags, opts, local, catalogue)
	mounts, err := opts.applyToLocal(flags, &local, global.DefaultMounts)
	if err != nil {
		return resolvedLaunchInputs{}, err
	}
	if err := enforceDockerfileSymlink(local.Options.Docker); err != nil {
		return resolvedLaunchInputs{}, err
	}
	services, err := container.ValidServiceIDs(local.Options.Services)
	if err != nil {
		return resolvedLaunchInputs{}, err
	}
	local.Options.Services = services
	if len(services) > 0 {
		// Infrastructure services require a shared container network. Selecting
		// one is therefore an implicit opt-in to container mode, including the
		// direct CLI form `--service ... --dry-run`.
		local.Options.Docker = true
		local.Options.Jail = false
	}
	return resolvedLaunchInputs{global: global, local: local, status: status, resolved: resolved,
		permissions: permissions, mounts: mounts, positionalArgs: append([]string(nil), flags.Args()...)}, nil
}

// parseCommandLine builds the flag set, parses args, and runs the flags that
// only print information and exit. handled is true when one of them ran.
func parseCommandLine(args []string, out, errOut io.Writer) (*flag.FlagSet, cliOptions, bool, error) {
	flags := flag.NewFlagSet(config.LauncherName, flag.ContinueOnError)
	flags.SetOutput(errOut)
	var opts cliOptions
	opts.register(flags)
	if err := flags.Parse(normalizeMemoryResourceFlag(args)); err != nil {
		return nil, opts, false, err
	}
	handled, err := reportInfoFlags(opts, out)
	return flags, opts, handled, err
}

// normalizeMemoryResourceFlag keeps the historical boolean --memory flag
// compatible while allowing the natural Docker spelling `--memory 4g`. The
// latter is rewritten to the unambiguous --container-memory flag before the
// standard library parser sees it. Boolean spellings remain ai-memory flags.
func normalizeMemoryResourceFlag(args []string) []string {
	normalized := append([]string(nil), args...)
	for index, arg := range normalized {
		if strings.HasPrefix(arg, "--memory=") {
			value := strings.TrimPrefix(arg, "--memory=")
			if !isBoolFlagValue(value) {
				normalized[index] = "--container-memory=" + value
			}
			continue
		}
		if arg != "--memory" || index+1 >= len(normalized) || strings.HasPrefix(normalized[index+1], "-") || isBoolFlagValue(normalized[index+1]) {
			continue
		}
		normalized[index] = "--container-memory"
	}
	return normalized
}

func isBoolFlagValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "0", "t", "f", "true", "false":
		return true
	default:
		return false
	}
}
