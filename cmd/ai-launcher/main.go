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

func run(args []string, in io.Reader, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("ai-launcher", flag.ContinueOnError)
	flags.SetOutput(errOut)
	var mounts, rwMounts stringList
	var agent, extraArgs, globalPath, localPath string
	var addName, addPath, addCommand, addDescription string
	var ssh, gh, docker, gpu, noJail, sandbox, memory, noMemory, yolo, noYolo, dryRun, save, install, upgrade bool
	var newWorkstream, workstream string
	flags.StringVar(&agent, "agent", "", "agent command (claude, codex, opencode, ...)")
	flags.BoolVar(&ssh, "ssh", false, "enable SSH permission")
	flags.BoolVar(&gh, "gh", false, "enable GitHub CLI permission")
	flags.BoolVar(&docker, "docker", false, "enable Docker permission")
	flags.BoolVar(&gpu, "gpu", false, "enable GPU permission")
	flags.BoolVar(&noJail, "no-jail", false, "run without ai-jail")
	flags.BoolVar(&sandbox, "sandbox", false, "enable ai-jail (alias for the default sandbox)")
	flags.BoolVar(&memory, "memory", false, "enable ai-memory")
	flags.BoolVar(&noMemory, "no-memory", false, "disable ai-memory")
	flags.BoolVar(&yolo, "yolo", false, "pass --yolo to the agent")
	flags.BoolVar(&noYolo, "no-yolo", false, "do not pass --yolo to the agent")
	flags.BoolVar(&dryRun, "dry-run", false, "print the generated command")
	flags.BoolVar(&save, "save", false, "save local configuration and exit")
	flags.BoolVar(&save, "save-only", false, "alias for --save")
	flags.BoolVar(&install, "install", false, "install missing configured tools from GitHub releases and update stale ones")
	flags.BoolVar(&upgrade, "upgrade", false, "force reinstall of configured tools from the latest GitHub release")
	flags.StringVar(&newWorkstream, "new", "", "start a new ai-memory workstream with this name")
	flags.StringVar(&workstream, "workstream", "", "new workstream name (used with --new)")
	flags.Var(&mounts, "mount", "read-only mount, optionally with :ro or :rw")
	flags.Var(&mounts, "map", "alias for --mount")
	flags.Var(&rwMounts, "rw-map", "read-write mount")
	flags.StringVar(&extraArgs, "extra-args", "", "additional agent arguments")
	flags.StringVar(&extraArgs, "args", "", "alias for --extra-args")
	flags.StringVar(&globalPath, "config", "", "global config path")
	flags.StringVar(&localPath, "local-config", "", "workspace config path")
	flags.StringVar(&addName, "add", "", "add or update an agent in the global catalog")
	flags.StringVar(&addPath, "path", "", "executable path used with --add")
	flags.StringVar(&addCommand, "command", "", "command name used with --add; defaults to the executable basename")
	flags.StringVar(&addDescription, "description", "", "description used with --add")
	if err := flags.Parse(args); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	if globalPath == "" && home != "" {
		globalPath = filepath.Join(home, ".config", "ai-launch", "config.yaml")
	}
	if localPath == "" {
		localPath = filepath.Join(mustGetwd(), ".ai-launch.yaml")
	}
	if flagsWasSet(flags, "add") {
		return addAgent(globalPath, addName, addPath, addCommand, addDescription, out)
	}
	global, globalErr := config.LoadGlobal(globalPath)
	if globalErr != nil {
		fmt.Fprintln(errOut, "warning:", globalErr)
	}
	if install || upgrade {
		return installConfigured(global, agent, home, upgrade, out, errOut)
	}
	local, localErr := config.LoadLocal(localPath)
	if localErr != nil {
		return localErr
	}
	if runtime.GOOS == "windows" && local.Options.Jail {
		fmt.Fprintln(errOut, "warning: ai-jail is not supported on Windows; continuing without sandbox")
		local.Options.Jail = false
	}
	catalogue := catalog.New(global)
	if flags.NArg() > 0 && flags.Arg(0) == "help" {
		flags.Usage()
		return nil
	}
	positionalArgs := append([]string(nil), flags.Args()...)

	selectedAgent := agent
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
	applyBoolFlag(flags, "ssh", permissions, "ssh", ssh)
	applyBoolFlag(flags, "gh", permissions, "gh", gh)
	applyBoolFlag(flags, "docker", permissions, "docker", docker)
	applyBoolFlag(flags, "gpu", permissions, "gpu", gpu)
	if flagsWasSet(flags, "no-jail") {
		local.Options.Jail = !noJail
	}
	if flagsWasSet(flags, "sandbox") {
		local.Options.Jail = sandbox
	}
	if flagsWasSet(flags, "memory") {
		local.Options.Memory = memory
	}
	if flagsWasSet(flags, "no-memory") && noMemory {
		local.Options.Memory = false
	}
	if flagsWasSet(flags, "yolo") {
		local.Options.Yolo = yolo
	}
	if flagsWasSet(flags, "no-yolo") {
		local.Options.Yolo = !noYolo
	}
	if flagsWasSet(flags, "new") || flagsWasSet(flags, "workstream") {
		local.Options.NewWorkstream = newWorkstream
		if local.Options.NewWorkstream == "" {
			local.Options.NewWorkstream = workstream
		}
	}
	mountConfig := append([]config.Mount(nil), local.Mounts...)
	if flagsWasSet(flags, "mount") || flagsWasSet(flags, "map") || flagsWasSet(flags, "rw-map") {
		mountConfig = nil
		for _, value := range mounts {
			mountConfig = append(mountConfig, parseMount(value, "ro"))
		}
		for _, value := range rwMounts {
			mountConfig = append(mountConfig, parseMount(value, "rw"))
		}
	}
	if flagsWasSet(flags, "extra-args") || flagsWasSet(flags, "args") {
		parsed, parseErr := splitArgs(extraArgs)
		if parseErr != nil {
			return fmt.Errorf("parse extra arguments: %w", parseErr)
		}
		local.Options.ExtraArgs = parsed
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
	}
	if len(args) == 0 {
		launchConfig, err = tui.RunWithSave(global, launchConfig, func(updated launcher.LaunchConfig) error {
			return saveIfRequested(true, localPath, local, updated)
		})
		if err != nil {
			return nil
		}
	} else if err := saveIfRequested(save, localPath, local, launchConfig); err != nil {
		return err
	} else if save {
		return nil
	}
	argv, err := launcher.Build(launchConfig)
	if err != nil {
		return err
	}
	if dryRun || len(args) == 0 {
		fmt.Fprintln(out, shellJoin(argv))
		return nil
	}
	issues := launcher.NewValidator().Validate(launchConfig)
	if len(issues) > 0 {
		for _, issue := range issues {
			fmt.Fprintln(errOut, "warning:", issue)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create install log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open install log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
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
		fmt.Fprintln(errOut, "warning:", logErr)
		trace = &installLog{logger: log.New(io.Discard, "", log.LstdFlags)}
	}
	defer trace.Close()
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
		trace.Printf("target name=%q command=%q aliases=%v source=%t release=%t", target.Name, target.Command, target.Aliases, target.SourceURL != "", target.Release != nil)
		if target.Release == nil {
			if target.SourceURL != "" {
				result, err := client.InstallSource(context.Background(), target.Name, target.Command, target.Path, target.SourceURL, force)
				if err != nil {
					trace.Printf("source install failed name=%q error=%v", target.Name, err)
					failures = append(failures, err)
					continue
				}
				installedPaths[target.Command] = result.Path
				trace.Printf("source install result name=%q status=%q path=%q", result.Name, result.Status, result.Path)
				fmt.Fprintf(out, "%s: %s (%s)\n", result.Name, result.Status, result.Path)
				continue
			}
			if found := executableAvailable(target.Command, target.Aliases, target.Path); found != "" {
				trace.Printf("existing executable name=%q path=%q", target.Name, found)
				installedPaths[target.Command] = found
				fmt.Fprintf(out, "%s: available as %s (no GitHub release recipe)\n", target.Name, found)
				continue
			}
			err := fmt.Errorf("%s: not installed and no GitHub release recipe is configured", target.Name)
			if selected != "" {
				trace.Printf("target missing without recipe name=%q", target.Name)
				failures = append(failures, err)
			} else {
				fmt.Fprintln(errOut, "warning:", err)
			}
			continue
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
			failures = append(failures, err)
			continue
		}
		installedPaths[target.Command] = result.Path
		trace.Printf("release install result name=%q status=%q version=%q path=%q", result.Name, result.Status, result.Version, result.Path)
		if result.Version != "" {
			fmt.Fprintf(out, "%s: %s %s (%s)\n", result.Name, result.Status, result.Version, result.Path)
		} else {
			fmt.Fprintf(out, "%s: %s\n", result.Name, result.Status)
		}
	}

	memoryPath := installedPaths["ai-memory"]
	if memoryPath == "" {
		memoryPath = executableAvailable("ai-memory", nil, "")
	}
	trace.Printf("ai-memory path=%q", memoryPath)
	for _, target := range targets {
		if target.Memory == nil || installedPaths[target.Command] == "" {
			if target.NeedsMemory && installedPaths[target.Command] != "" && target.Memory == nil {
				fmt.Fprintf(out, "%s: ai-memory runtime ready (no hooks/MCP adapter configured)\n", target.Name)
			}
			continue
		}
		if memoryPath == "" {
			trace.Printf("memory wiring skipped name=%q: ai-memory unavailable", target.Name)
			failures = append(failures, fmt.Errorf("%s: ai-memory is required to install MCP/hooks", target.Name))
			continue
		}
		if err := wireMemory(context.Background(), memoryPath, home, global.MemoryServerURL, target.Memory, out, errOut, trace); err != nil {
			trace.Printf("memory wiring failed name=%q error=%v", target.Name, err)
			failures = append(failures, fmt.Errorf("%s: configure ai-memory: %w", target.Name, err))
			continue
		}
		trace.Printf("memory wiring complete name=%q client=%q agent=%q", target.Name, target.Memory.Client, target.Memory.Agent)
		fmt.Fprintf(out, "%s: ai-memory MCP/hooks configured\n", target.Name)
	}
	if len(failures) > 0 {
		trace.Printf("install finished failures=%d", len(failures))
		return errors.Join(failures...)
	}
	trace.Printf("install finished successfully targets=%d", len(targets))
	return nil
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

func configFileFor(home, configured, target string, hooks bool) string {
	if strings.TrimSpace(configured) != "" {
		return expandHomePath(home, configured)
	}
	var path string
	switch {
	case hooks && target == "claude-code":
		path = filepath.Join(home, ".claude", "settings.json")
	case target == "antigravity-cli":
		if hooks {
			path = filepath.Join(home, ".gemini", "config", "hooks.json")
		} else {
			path = filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json")
		}
	default:
		return ""
	}
	return resolveSymlinkParent(path)
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
	if data, err := os.ReadFile(logical); err == nil {
		if err := os.WriteFile(staging, data, 0o600); err != nil {
			return "", nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	finalize := func() error {
		data, err := os.ReadFile(staging)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info, statErr := os.Stat(resolved); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(resolved, data, mode); err != nil {
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
	command := exec.CommandContext(ctx, memoryPath, args...)
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
