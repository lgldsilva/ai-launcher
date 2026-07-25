package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/installer"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"github.com/lgldsilva/ai-launcher/internal/tui"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ai-launcher:", err)
		os.Exit(1)
	}
}

// cliOptions holds every command-line flag value in one place so run() stays
// readable and the flag-to-config mapping can be applied as a unit.
type cliOptions struct {
	mounts, rwMounts               stringList
	params                         stringList
	agent, extraArgs               string
	globalPath, localPath          string
	addName, addPath               string
	addCommand, addDescription     string
	newWorkstream, workstream      string
	profile, saveProfile           string
	deleteProfile                  string
	ssh, gh, docker, gpu           bool
	noJail, sandbox                bool
	memory, noMemory               bool
	yolo, noYolo                   bool
	dryRun, save, install, upgrade bool
	listProfiles                   bool
}

func (o *cliOptions) register(flags *flag.FlagSet) {
	flags.StringVar(&o.agent, "agent", "", "agent command (claude, codex, opencode, ...)")
	flags.BoolVar(&o.ssh, "ssh", false, "enable SSH permission")
	flags.BoolVar(&o.gh, "gh", false, "enable GitHub CLI permission")
	flags.BoolVar(&o.docker, "docker", false, "enable Docker permission")
	flags.BoolVar(&o.gpu, "gpu", false, "enable GPU permission")
	flags.BoolVar(&o.noJail, "no-jail", false, "run without ai-jail")
	flags.BoolVar(&o.sandbox, "sandbox", false, "enable ai-jail (alias for the default sandbox)")
	flags.BoolVar(&o.memory, "memory", false, "enable ai-memory")
	flags.BoolVar(&o.noMemory, "no-memory", false, "disable ai-memory")
	flags.BoolVar(&o.yolo, "yolo", false, "pass the dangerous-mode flag to the agent")
	flags.BoolVar(&o.noYolo, "no-yolo", false, "do not pass the dangerous-mode flag to the agent")
	flags.BoolVar(&o.dryRun, "dry-run", false, "print the generated command")
	flags.BoolVar(&o.save, "save", false, "save local configuration and exit")
	flags.BoolVar(&o.save, "save-only", false, "alias for --save")
	flags.BoolVar(&o.install, "install", false, "install missing configured tools from GitHub releases and update stale ones")
	flags.BoolVar(&o.upgrade, "upgrade", false, "force reinstall of configured tools from the latest GitHub release")
	flags.StringVar(&o.newWorkstream, "new", "", "start a new ai-memory workstream with this name")
	flags.StringVar(&o.workstream, "workstream", "", "new workstream name (used with --new)")
	flags.Var(&o.mounts, "mount", "read-only mount, optionally with :ro or :rw")
	flags.Var(&o.mounts, "map", "alias for --mount")
	flags.Var(&o.rwMounts, "rw-map", "read-write mount")
	flags.Var(&o.params, "param", "set a harness parameter declared in the agent catalog (name=value, repeatable)")
	flags.StringVar(&o.extraArgs, "extra-args", "", "additional agent arguments")
	flags.StringVar(&o.extraArgs, "args", "", "alias for --extra-args")
	flags.StringVar(&o.globalPath, "config", "", "global config path")
	flags.StringVar(&o.localPath, "local-config", "", "workspace config path")
	flags.StringVar(&o.addName, "add", "", "add or update an agent in the global catalog")
	flags.StringVar(&o.addPath, "path", "", "executable path used with --add")
	flags.StringVar(&o.addCommand, "command", "", "command name used with --add; defaults to the executable basename")
	flags.StringVar(&o.addDescription, "description", "", "description used with --add")
	flags.StringVar(&o.profile, "profile", "", "load the named profile from the global config as the base selection (precedence: built-in defaults < local .ai-launch.yaml < profile < explicit flags)")
	flags.StringVar(&o.saveProfile, "save-profile", "", "save the fully merged selection as the named profile in the global config and exit without launching")
	flags.BoolVar(&o.listProfiles, "list-profiles", false, "list the profiles saved in the global config and exit")
	flags.StringVar(&o.deleteProfile, "delete-profile", "", "delete the named profile from the global config and exit")
}

// applyToLocal folds every explicitly-set flag into the local configuration
// and returns the effective mount list (flag mounts replace configured ones).
func (o *cliOptions) applyToLocal(flags *flag.FlagSet, local *config.Local) ([]config.Mount, error) {
	if flagsWasSet(flags, "no-jail") {
		local.Options.Jail = !o.noJail
	}
	if flagsWasSet(flags, "sandbox") {
		local.Options.Jail = o.sandbox
	}
	if flagsWasSet(flags, "memory") {
		local.Options.Memory = o.memory
	}
	if flagsWasSet(flags, "no-memory") && o.noMemory {
		local.Options.Memory = false
	}
	if flagsWasSet(flags, "yolo") {
		local.Options.Yolo = o.yolo
	}
	if flagsWasSet(flags, "no-yolo") {
		local.Options.Yolo = !o.noYolo
	}
	if flagsWasSet(flags, "new") || flagsWasSet(flags, "workstream") {
		local.Options.NewWorkstream = o.newWorkstream
		if local.Options.NewWorkstream == "" {
			local.Options.NewWorkstream = o.workstream
		}
	}
	mountConfig := append([]config.Mount(nil), local.Mounts...)
	if flagsWasSet(flags, "mount") || flagsWasSet(flags, "map") || flagsWasSet(flags, "rw-map") {
		mountConfig = nil
		for _, value := range o.mounts {
			mountConfig = append(mountConfig, parseMount(value, "ro"))
		}
		for _, value := range o.rwMounts {
			mountConfig = append(mountConfig, parseMount(value, "rw"))
		}
	}
	if flagsWasSet(flags, "extra-args") || flagsWasSet(flags, "args") {
		parsed, err := splitArgs(o.extraArgs)
		if err != nil {
			return nil, fmt.Errorf("parse extra arguments: %w", err)
		}
		local.Options.ExtraArgs = parsed
	}
	if flagsWasSet(flags, "param") {
		if err := o.applyParamFlags(local); err != nil {
			return nil, err
		}
	}
	return mountConfig, nil
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

func run(args []string, in io.Reader, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("ai-launcher", flag.ContinueOnError)
	flags.SetOutput(errOut)
	var opts cliOptions
	opts.register(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	if opts.globalPath == "" && home != "" {
		opts.globalPath = filepath.Join(home, ".config", "ai-launch", "config.yaml")
	}
	if opts.localPath == "" {
		opts.localPath = filepath.Join(mustGetwd(), ".ai-launch.yaml")
	}
	if flagsWasSet(flags, "add") {
		return addAgent(opts.globalPath, opts.addName, opts.addPath, opts.addCommand, opts.addDescription, out)
	}
	global, globalErr := config.LoadGlobal(opts.globalPath)
	if globalErr != nil {
		_, _ = fmt.Fprintln(errOut, "warning:", globalErr)
	}
	if opts.install || opts.upgrade {
		return installConfigured(global, opts.agent, home, opts.upgrade, out, errOut)
	}
	if opts.listProfiles {
		listProfiles(global, out)
		return nil
	}
	if opts.deleteProfile != "" {
		return deleteProfile(opts.globalPath, global, opts.deleteProfile, out)
	}
	local, localErr := config.LoadLocal(opts.localPath)
	if localErr != nil {
		return localErr
	}
	if runtime.GOOS == "windows" && local.Options.Jail {
		_, _ = fmt.Fprintln(errOut, "warning: ai-jail is not supported on Windows; continuing without sandbox")
		local.Options.Jail = false
	}
	if opts.profile != "" {
		profile, ok := global.Profiles[opts.profile]
		if !ok {
			return fmt.Errorf("profile %q not found in %s", opts.profile, opts.globalPath)
		}
		applyProfile(&local, profile)
	}
	catalogue := catalog.New(global)
	if flags.NArg() > 0 && flags.Arg(0) == "help" {
		flags.Usage()
		return nil
	}
	positionalArgs := append([]string(nil), flags.Args()...)

	selectedAgent := opts.agent
	if selectedAgent == "" {
		selectedAgent = local.Agent
	}
	status, resolveErr := catalogue.Resolve(selectedAgent)
	if resolveErr != nil {
		status = catalog.AgentStatus{Agent: config.Agent{Name: selectedAgent, Command: selectedAgent}}
	} else if status.ResolvedCommand != "" {
		// Keep the configured catalog name for display, but invoke the alias
		// that was actually found on this machine (for example kilocode).
		status.Agent.Command = status.ResolvedCommand
	}
	permissions := catalogue.NormalizePermissions(local.Permissions)
	applyBoolFlag(flags, "ssh", permissions, "ssh", opts.ssh)
	applyBoolFlag(flags, "gh", permissions, "gh", opts.gh)
	applyBoolFlag(flags, "docker", permissions, "docker", opts.docker)
	applyBoolFlag(flags, "gpu", permissions, "gpu", opts.gpu)
	mountConfig, err := opts.applyToLocal(flags, &local)
	if err != nil {
		return err
	}
	if len(positionalArgs) > 0 {
		local.Options.ExtraArgs = append(local.Options.ExtraArgs, positionalArgs...)
	}

	launchConfig := launcher.LaunchConfig{
		Agent:           status.Agent,
		Executable:      status.Path,
		HomeDir:         home,
		MemoryServerURL: global.MemoryServerURL,
		UseJail:         local.Options.Jail,
		UseMemory:       local.Options.Memory,
		NewWorkstream:   local.Options.NewWorkstream,
		Permissions:     permissions,
		Mounts:          mountConfig,
		Yolo:            local.Options.Yolo,
		ExtraArgs:       local.Options.ExtraArgs,
		ParamValues:     local.Options.ParamValues,
	}
	if opts.saveProfile != "" {
		return saveProfileCommand(opts.globalPath, global, opts.saveProfile, launchConfig, out)
	}
	return launch(args, opts, global, local, launchConfig, in, out, errOut)
}

// applyProfile layers a named profile over the local configuration. Only the
// fields the profile defines are replaced, so omitted blocks keep the local
// (or built-in default) values and explicit CLI flags still win afterwards.
func applyProfile(local *config.Local, profile config.Profile) {
	if strings.TrimSpace(profile.Agent) != "" {
		local.Agent = profile.Agent
	}
	if profile.Permissions != nil {
		local.Permissions = profile.Permissions
	}
	if profile.Mounts != nil {
		local.Mounts = profile.Mounts
	}
	if profile.Options != nil {
		local.Options = *profile.Options
	}
}

// listProfiles prints the saved profiles with agent and a compact summary.
func listProfiles(global config.Global, out io.Writer) {
	names := config.ProfileNames(global)
	if len(names) == 0 {
		_, _ = fmt.Fprintln(out, "no profiles saved")
		return
	}
	for _, name := range names {
		profile := global.Profiles[name]
		summary := config.ProfileSummary(profile)
		if summary != "" {
			summary = "\n  " + summary
		}
		_, _ = fmt.Fprintf(out, "%s\n  agent: %s%s\n", name, profile.Agent, summary)
	}
}

// deleteProfile removes a named profile and persists the global config.
func deleteProfile(globalPath string, global config.Global, name string, out io.Writer) error {
	if !config.DeleteProfile(&global, name) {
		return fmt.Errorf("profile %q not found in %s", name, globalPath)
	}
	if err := config.SaveGlobal(globalPath, global); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "profile %q deleted from %s\n", name, globalPath)
	return err
}

// saveProfileCommand persists the fully merged selection as a named profile
// in the global config without launching anything.
func saveProfileCommand(globalPath string, global config.Global, name string, launch launcher.LaunchConfig, out io.Writer) error {
	if err := config.SetProfile(&global, name, profileFromLaunch(launch)); err != nil {
		return err
	}
	if err := config.SaveGlobal(globalPath, global); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "profile %q saved to %s\n", name, globalPath)
	return err
}

// profileFromLaunch converts a fully merged launch selection into a profile.
func profileFromLaunch(launch launcher.LaunchConfig) config.Profile {
	return config.Profile{
		Agent:       launch.Agent.Command,
		Permissions: launch.Permissions,
		Mounts:      launch.Mounts,
		Options: &config.Options{
			Jail:          launch.UseJail,
			Memory:        launch.UseMemory,
			Yolo:          launch.Yolo,
			NewWorkstream: launch.NewWorkstream,
			ExtraArgs:     launch.ExtraArgs,
			ParamValues:   launch.ParamValues,
		},
	}
}

// launch confirms the configuration (TUI when interactive), optionally saves
// it, and builds, validates, and executes the resulting argv.
func launch(args []string, opts cliOptions, global config.Global, local config.Local, launchConfig launcher.LaunchConfig, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		confirmed, err := tui.RunWithHooks(global, launchConfig, tui.Hooks{
			Save: func(updated launcher.LaunchConfig) error {
				return saveIfRequested(true, opts.localPath, local, updated)
			},
			SaveProfile: func(name string, updated launcher.LaunchConfig) error {
				if err := config.SetProfile(&global, name, profileFromLaunch(updated)); err != nil {
					return err
				}
				return config.SaveGlobal(opts.globalPath, global)
			},
		})
		if err != nil {
			return nil
		}
		launchConfig = confirmed
	} else if err := saveIfRequested(opts.save, opts.localPath, local, launchConfig); err != nil {
		return err
	} else if opts.save {
		return nil
	}
	argv, err := launcher.Build(launchConfig)
	if err != nil {
		return err
	}
	if opts.dryRun || len(args) == 0 {
		_, _ = fmt.Fprintln(out, shellJoin(argv))
		return nil
	}
	issues := launcher.NewValidator().Validate(launchConfig)
	if len(issues) > 0 {
		for _, issue := range issues {
			_, _ = fmt.Fprintln(errOut, "warning:", issue)
		}
		return errors.New("pre-flight validation failed")
	}
	if in == os.Stdin && out == os.Stdout && errOut == os.Stderr {
		return launcher.ReplaceWithEnv(argv, launcher.Environment(launchConfig))
	}
	return launcher.PTYExecutor{}.RunWithEnv(context.Background(), argv, launcher.Environment(launchConfig), in, out, errOut)
}

type installTarget struct {
	Name        string
	Command     string
	Aliases     []string
	Path        string
	SourceURL   string
	Release     *config.GitHubRelease
	Memory      *config.MemoryIntegration
	NeedsMemory bool
}

type installLog struct {
	logger *log.Logger
	file   *os.File
	path   string
}

func newInstallLog(home string) (*installLog, error) {
	if strings.TrimSpace(home) == "" {
		return &installLog{logger: log.New(io.Discard, "", log.LstdFlags)}, nil
	}
	path := filepath.Join(home, ".config", "ai-launch", "install.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create install log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- path is the installer's own log under the user home
	if err != nil {
		return nil, fmt.Errorf("open install log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("protect install log: %w", err)
	}
	return &installLog{logger: log.New(file, "", log.LstdFlags|log.Lmicroseconds), file: file, path: path}, nil
}

func (l *installLog) Printf(format string, args ...any) {
	if l != nil && l.logger != nil {
		l.logger.Printf(format, args...)
	}
}

func (l *installLog) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func installConfigured(global config.Global, selected string, home string, force bool, out, errOut io.Writer) error {
	trace, logErr := newInstallLog(home)
	if logErr != nil {
		_, _ = fmt.Fprintln(errOut, "warning:", logErr)
		trace = &installLog{logger: log.New(io.Discard, "", log.LstdFlags)}
	}
	defer func() { _ = trace.Close() }()
	trace.Printf("install start selected=%q force=%t", selected, force)
	targets := configuredInstallTargets(global, selected)
	if len(targets) == 0 {
		trace.Printf("install aborted: no target for selected=%q", selected)
		if selected == "" {
			return errors.New("no GitHub release recipes are configured")
		}
		return fmt.Errorf("agent or tool %q is not in the catalog", selected)
	}
	client := installer.New(home)
	trace.Printf("ai-memory server URL=%q", global.MemoryServerURL)
	var failures []error
	installedPaths := make(map[string]string, len(targets))
	for _, target := range targets {
		path, err := installOne(client, target, selected, force, home, out, errOut, trace)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if path != "" {
			installedPaths[target.Command] = path
		}
	}

	memoryPath := installedPaths["ai-memory"]
	if memoryPath == "" {
		memoryPath = executableAvailable("ai-memory", nil, "")
	}
	trace.Printf("ai-memory path=%q", memoryPath)
	failures = append(failures, wireMemoryTargets(targets, installedPaths, memoryPath, home, global.MemoryServerURL, out, errOut, trace)...)
	if len(failures) > 0 {
		trace.Printf("install finished failures=%d", len(failures))
		return errors.Join(failures...)
	}
	trace.Printf("install finished successfully targets=%d", len(targets))
	return nil
}

// installOne installs a single target and returns the resulting executable
// path. A target without a release recipe is only a failure when it was
// explicitly selected; otherwise it is reported as a warning.
func installOne(client *installer.Installer, target installTarget, selected string, force bool, home string, out, errOut io.Writer, trace *installLog) (string, error) {
	trace.Printf("target name=%q command=%q aliases=%v source=%t release=%t", target.Name, target.Command, target.Aliases, target.SourceURL != "", target.Release != nil)
	if target.Release == nil {
		return installWithoutRecipe(client, target, selected, force, out, errOut, trace)
	}
	installPath := target.Path
	if installPath == "" {
		// If a variant is already installed (for example kilocode), update
		// that actual executable instead of creating a second canonical one.
		installPath = executableAvailable(target.Command, target.Aliases, "")
	}
	result, err := client.Install(context.Background(), target.Name, target.Command, installPath, target.Release, force)
	if err != nil && target.Path == "" && installPath != "" && errors.Is(err, os.ErrPermission) {
		// A discovered system-wide binary may be readable but not writable by
		// the current user. Retry in ~/.local/bin instead of requiring sudo.
		trace.Printf("release install retry name=%q blocked_path=%q fallback=%q error=%v", target.Name, installPath, filepath.Join(home, ".local", "bin", target.Command), err)
		installPath = ""
		result, err = client.Install(context.Background(), target.Name, target.Command, installPath, target.Release, force)
	}
	if err != nil {
		trace.Printf("release install failed name=%q path=%q error=%v", target.Name, installPath, err)
		return "", err
	}
	trace.Printf("release install result name=%q status=%q version=%q path=%q", result.Name, result.Status, result.Version, result.Path)
	if result.Version != "" {
		_, _ = fmt.Fprintf(out, "%s: %s %s (%s)\n", result.Name, result.Status, result.Version, result.Path)
	} else {
		_, _ = fmt.Fprintf(out, "%s: %s\n", result.Name, result.Status)
	}
	return result.Path, nil
}

// installWithoutRecipe handles targets with no GitHub release recipe: either
// a trusted source URL download, or an already-available executable.
func installWithoutRecipe(client *installer.Installer, target installTarget, selected string, force bool, out, errOut io.Writer, trace *installLog) (string, error) {
	if target.SourceURL != "" {
		result, err := client.InstallSource(context.Background(), target.Name, target.Command, target.Path, target.SourceURL, force)
		if err != nil {
			trace.Printf("source install failed name=%q error=%v", target.Name, err)
			return "", err
		}
		trace.Printf("source install result name=%q status=%q path=%q", result.Name, result.Status, result.Path)
		_, _ = fmt.Fprintf(out, "%s: %s (%s)\n", result.Name, result.Status, result.Path)
		return result.Path, nil
	}
	if found := executableAvailable(target.Command, target.Aliases, target.Path); found != "" {
		trace.Printf("existing executable name=%q path=%q", target.Name, found)
		_, _ = fmt.Fprintf(out, "%s: available as %s (no GitHub release recipe)\n", target.Name, found)
		return found, nil
	}
	err := fmt.Errorf("%s: not installed and no GitHub release recipe is configured", target.Name)
	if selected == "" {
		_, _ = fmt.Fprintln(errOut, "warning:", err)
		return "", nil
	}
	trace.Printf("target missing without recipe name=%q", target.Name)
	return "", err
}

// wireMemoryTargets configures ai-memory MCP/hooks for every installed target
// that declares an integration, collecting all failures.
func wireMemoryTargets(targets []installTarget, installedPaths map[string]string, memoryPath, home, serverURL string, out, errOut io.Writer, trace *installLog) []error {
	var failures []error
	for _, target := range targets {
		if target.Memory == nil || installedPaths[target.Command] == "" {
			if target.NeedsMemory && installedPaths[target.Command] != "" && target.Memory == nil {
				_, _ = fmt.Fprintf(out, "%s: ai-memory runtime ready (no hooks/MCP adapter configured)\n", target.Name)
			}
			continue
		}
		if memoryPath == "" {
			trace.Printf("memory wiring skipped name=%q: ai-memory unavailable", target.Name)
			failures = append(failures, fmt.Errorf("%s: ai-memory is required to install MCP/hooks", target.Name))
			continue
		}
		if err := wireMemory(context.Background(), memoryPath, home, serverURL, target.Memory, out, errOut, trace); err != nil {
			trace.Printf("memory wiring failed name=%q error=%v", target.Name, err)
			failures = append(failures, fmt.Errorf("%s: configure ai-memory: %w", target.Name, err))
			continue
		}
		trace.Printf("memory wiring complete name=%q client=%q agent=%q", target.Name, target.Memory.Client, target.Memory.Agent)
		_, _ = fmt.Fprintf(out, "%s: ai-memory MCP/hooks configured\n", target.Name)
	}
	return failures
}

func configuredInstallTargets(global config.Global, selected string) []installTarget {
	var result []installTarget
	appendTarget := func(target installTarget) {
		if runtime.GOOS == "windows" && target.Command == "ai-jail" {
			return
		}
		if selected == "" || target.Command == selected || target.Name == selected || containsString(target.Aliases, selected) {
			result = append(result, target)
		}
	}
	for _, agent := range global.Agents {
		appendTarget(installTarget{Name: agent.Name, Command: agent.Command, Aliases: agent.Aliases, Path: agent.Path, SourceURL: agent.SourceURL, Release: agent.Release, Memory: agent.Memory, NeedsMemory: agent.SupportsMemory})
	}
	for _, tool := range global.Tools {
		appendTarget(installTarget{Name: tool.Name, Command: tool.Command, Aliases: tool.Aliases, Path: tool.Path, SourceURL: tool.SourceURL, Release: tool.Release})
	}
	if selected != "" && !containsInstallTarget(result, "ai-memory") {
		for _, target := range result {
			if target.Memory != nil || target.NeedsMemory {
				for _, tool := range global.Tools {
					if tool.Command == "ai-memory" {
						result = append(result, installTarget{Name: tool.Name, Command: tool.Command, Aliases: tool.Aliases, Path: tool.Path, SourceURL: tool.SourceURL, Release: tool.Release})
					}
				}
				break
			}
		}
	}
	return result
}

func containsInstallTarget(targets []installTarget, command string) bool {
	for _, target := range targets {
		if target.Command == command {
			return true
		}
	}
	return false
}

func wireMemory(ctx context.Context, memoryPath, home, serverURL string, integration *config.MemoryIntegration, out, errOut io.Writer, trace *installLog) error {
	if integration == nil {
		return nil
	}
	if integration.InstallMCP {
		if strings.TrimSpace(integration.Client) == "" {
			return errors.New("ai-memory MCP client is empty")
		}
		configFile, finalize, err := prepareMemoryConfigFile(home, integration.MCPConfigFile, integration.Client, false)
		if err != nil {
			return fmt.Errorf("prepare MCP config: %w", err)
		}
		args := memoryInstallArgs("install-mcp", integration.Client, serverURL, configFile)
		trace.Printf("memory MCP config client=%q file=%q", integration.Client, lastArgValue(args, "--config-file"))
		if err := runMemoryCommand(ctx, memoryPath, args, out, errOut, trace); err != nil {
			return fmt.Errorf("install-mcp: %w", err)
		}
		if err := finalize(); err != nil {
			return fmt.Errorf("publish MCP config: %w", err)
		}
	}
	if integration.InstallHooks {
		if strings.TrimSpace(integration.Agent) == "" {
			return errors.New("ai-memory hook agent is empty")
		}
		configFile, finalize, err := prepareMemoryConfigFile(home, integration.HooksConfigFile, integration.Agent, true)
		if err != nil {
			return fmt.Errorf("prepare hooks config: %w", err)
		}
		args := memoryInstallArgs("install-hooks", integration.Agent, serverURL, configFile)
		trace.Printf("memory hooks config agent=%q file=%q", integration.Agent, lastArgValue(args, "--config-file"))
		if err := runMemoryCommand(ctx, memoryPath, args, out, errOut, trace); err != nil {
			return fmt.Errorf("install-hooks: %w", err)
		}
		if err := finalize(); err != nil {
			return fmt.Errorf("publish hooks config: %w", err)
		}
	}
	return nil
}

func memoryInstallArgs(command, target, serverURL, configFile string) []string {
	args := []string{command}
	if command == "install-mcp" {
		args = append(args, "--client", target)
	} else {
		args = append(args, "--agent", target)
	}
	if strings.TrimSpace(serverURL) != "" {
		args = append(args, "--server-url", strings.TrimSpace(serverURL))
	}
	if strings.TrimSpace(configFile) != "" {
		args = append(args, "--config-file", configFile)
	}
	return append(args, "--apply")
}

func lastArgValue(args []string, flagName string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flagName {
			return args[i+1]
		}
	}
	return ""
}

func prepareMemoryConfigFile(home, configured, target string, hooks bool) (string, func() error, error) {
	if strings.TrimSpace(configured) != "" {
		return expandHomePath(home, configured), func() error { return nil }, nil
	}
	logical := logicalMemoryConfigFile(home, target, hooks)
	if logical == "" {
		return "", func() error { return nil }, nil
	}
	resolved := resolveSymlinkParent(logical)
	if resolved == logical {
		return resolved, func() error { return nil }, nil
	}
	staging := filepath.Join(home, ".config", "ai-launch", "memory-staging", target)
	if hooks {
		staging += "-hooks.json"
	} else {
		staging += "-mcp.json"
	}
	if err := os.MkdirAll(filepath.Dir(staging), 0o700); err != nil {
		return "", nil, err
	}
	if data, err := os.ReadFile(logical); err == nil { // #nosec G304 -- logical is a well-known agent config path under the user home
		if err := os.WriteFile(staging, data, 0o600); err != nil { // #nosec G703 -- staging is derived from the user home and the fixed target name, not user input
			return "", nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	finalize := func() error {
		data, err := os.ReadFile(staging) // #nosec G304 -- staging is derived from the user home and the fixed target name, not user input
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info, statErr := os.Stat(resolved); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(resolved, data, mode); err != nil { // #nosec G703 -- resolved is the symlink-resolved agent config path under the user home
			return err
		}
		return os.Remove(staging)
	}
	return staging, finalize, nil
}

func logicalMemoryConfigFile(home, target string, hooks bool) string {
	switch {
	case hooks && target == "claude-code":
		return filepath.Join(home, ".claude", "settings.json")
	case target == "antigravity-cli":
		if hooks {
			return filepath.Join(home, ".gemini", "config", "hooks.json")
		}
		return filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json")
	default:
		return ""
	}
}

func expandHomePath(home, path string) string {
	path = strings.TrimSpace(path)
	if home != "" && strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func resolveSymlinkParent(path string) string {
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return path
	}
	return filepath.Join(resolved, filepath.Base(path))
}

func runMemoryCommand(ctx context.Context, memoryPath string, args []string, out, errOut io.Writer, trace *installLog) error {
	trace.Printf("run memory command path=%q args=%q", memoryPath, args)
	command := exec.CommandContext(ctx, memoryPath, args...) // #nosec G204 -- memoryPath is the ai-memory executable resolved from the launcher's own install or PATH; running it is the integration's purpose
	command.Stdin = os.Stdin
	command.Stdout = out
	command.Stderr = errOut
	err := command.Run()
	if err != nil {
		trace.Printf("memory command failed path=%q args=%q error=%v", memoryPath, args, err)
	} else {
		trace.Printf("memory command succeeded path=%q args=%q", memoryPath, args)
	}
	return err
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func executableAvailable(command string, aliases []string, configuredPath string) string {
	if configuredPath != "" {
		info, err := os.Stat(configuredPath)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return configuredPath
		}
		return ""
	}
	for _, candidate := range append([]string{command}, aliases...) {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func addAgent(globalPath, name, path, command, description string, out io.Writer) error {
	name = strings.TrimSpace(name)
	path = strings.TrimSpace(path)
	if name == "" {
		return errors.New("--add requires an agent name")
	}
	if path == "" {
		return errors.New("--add requires --path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect agent path: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("agent path %q is not an executable file", path)
	}
	if strings.TrimSpace(command) == "" {
		command = filepath.Base(path)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		return err
	}
	agent := config.Agent{Name: name, Command: command, Path: path, SupportsMemory: true, SupportsYolo: true, Description: description}
	if err := config.UpsertAgent(&global, agent); err != nil {
		return err
	}
	if err := config.SaveGlobal(globalPath, global); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "agent %q added to %s\n", name, globalPath)
	return err
}

func saveIfRequested(save bool, path string, local config.Local, launch launcher.LaunchConfig) error {
	if !save {
		return nil
	}
	local.Agent = launch.Agent.Command
	local.Permissions = launch.Permissions
	local.Mounts = launch.Mounts
	local.Options.Jail = launch.UseJail
	local.Options.Memory = launch.UseMemory
	local.Options.Yolo = launch.Yolo
	local.Options.NewWorkstream = launch.NewWorkstream
	local.Options.ExtraArgs = launch.ExtraArgs
	local.Options.ParamValues = launch.ParamValues
	return config.SaveLocal(path, local)
}

func parseMount(value, defaultMode string) config.Mount {
	parts := strings.Split(value, ":")
	if len(parts) >= 2 && (parts[len(parts)-1] == "ro" || parts[len(parts)-1] == "rw" || parts[len(parts)-1] == "read-only") {
		return config.Mount{Path: strings.Join(parts[:len(parts)-1], ":"), Mode: parts[len(parts)-1]}
	}
	return config.Mount{Path: value, Mode: defaultMode}
}

func applyBoolFlag(flags *flag.FlagSet, name string, permissions map[string]bool, id string, value bool) {
	if flagsWasSet(flags, name) {
		permissions[id] = value
	}
}

func flagsWasSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, value := range argv {
		parts[i] = shellQuote(value)
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-", r)
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func splitArgs(input string) ([]string, error) {
	var result []string
	var current strings.Builder
	var quote rune
	escaped := false
	haveValue := false
	flush := func() {
		if haveValue {
			result = append(result, current.String())
			current.Reset()
			haveValue = false
		}
	}
	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			escaped = false
			haveValue = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else if quote == '\'' {
				current.WriteRune(r)
				haveValue = true
			} else if r == '\\' {
				escaped = true
			} else {
				current.WriteRune(r)
				haveValue = true
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			haveValue = true
		case '\\':
			escaped = true
			haveValue = true
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
			haveValue = true
		}
	}
	if escaped {
		return nil, errors.New("trailing escape")
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	flush()
	return result, nil
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
