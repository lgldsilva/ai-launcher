// Package cmd hosts the non-launch command orchestration (install/upgrade of
// configured tools and agents, ai-memory wiring, and the --add catalog
// upsert) so the main package stays a thin flag-parsing and dispatch layer.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/installer"
)

// isWindows reports whether the host is Windows; it is a variable so tests
// can stub platform detection while running on Linux.
var isWindows = func() bool { return runtime.GOOS == "windows" }

// nativeRunnerManagedPath is the launcher's managed install location for the
// ai-memory native binary. The launcher exports it as AI_MEMORY_NATIVE_BIN so
// the ai-memory wrapper uses it directly instead of downloading a runner
// inside ai-jail. The installer expands ~/ and appends .exe on Windows.
const nativeRunnerManagedPath = "~/.local/share/ai-launcher/bin/ai-memory"

// installNativeRunner installs the native ai-memory binary next to the
// wrapper; it is a variable so tests can observe the invocation without
// network access.
var installNativeRunner = installNativeMemoryRunner

type installTarget struct {
	Name            string
	Command         string
	Aliases         []string
	Path            string
	SourceURL       string
	AllowUnverified bool
	Release         *config.GitHubRelease
	Memory          *config.MemoryIntegration
	NeedsMemory     bool
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

// InstallConfigured installs every catalog target matching selected (or all
// of them when selected is empty), then wires ai-memory MCP/hooks for the
// installed agents that declare an integration. The ai-memory auth token
// from the global config is passed to ai-memory through the environment and
// is never written to the install log.
func InstallConfigured(global config.Global, selected string, home string, force bool, out, errOut io.Writer) error {
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
		if path != "" && target.Command == "ai-memory" {
			native, nativeErr := installNativeRunner(client, target, force, trace)
			if nativeErr != nil {
				failures = append(failures, nativeErr)
				continue
			}
			if native.Path != "" {
				_, _ = fmt.Fprintf(out, "%s native runner: %s (%s)\n", target.Name, native.Status, native.Path)
			}
		}
	}

	memoryPath := installedPaths["ai-memory"]
	if memoryPath == "" {
		memoryPath = executableAvailable("ai-memory", nil, "")
	}
	trace.Printf("ai-memory path=%q", memoryPath)
	failures = append(failures, wireMemoryTargets(targets, installedPaths, memoryPath, home, global.MemoryServerURL, global.MemoryAuthToken, out, errOut, trace)...)
	if len(failures) > 0 {
		trace.Printf("install finished failures=%d", len(failures))
		return errors.Join(failures...)
	}
	trace.Printf("install finished successfully targets=%d", len(targets))
	return nil
}

// preferReleaseInstall reports whether a target installs from its GitHub
// release assets. Release assets are the only path with SHA-256 verification,
// so they win whenever the recipe publishes them. The source_url path validates
// nothing beyond an HTTPS scheme and a shebang, and it points at a mutable
// branch; it is a fallback for recipes that publish no assets at all.
func preferReleaseInstall(target installTarget) bool {
	return target.Release != nil
}

// installOne installs a single target and returns the resulting executable
// path. A target without a usable recipe is only a failure when it was
// explicitly selected; otherwise it is reported as a warning.
func installOne(client *installer.Installer, target installTarget, selected string, force bool, home string, out, errOut io.Writer, trace *installLog) (string, error) {
	trace.Printf("target name=%q command=%q aliases=%v source=%t release=%t", target.Name, target.Command, target.Aliases, target.SourceURL != "", target.Release != nil)
	if !preferReleaseInstall(target) {
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

// installNativeMemoryRunner installs the native ai-memory binary from the
// target's release assets into the launcher's managed path, reusing the
// checksum-verified installer machinery. Platforms without a release asset
// are skipped gracefully: the ai-memory wrapper keeps its own fallback.
func installNativeMemoryRunner(client *installer.Installer, target installTarget, force bool, trace *installLog) (installer.Result, error) {
	if target.Release == nil {
		return installer.Result{}, nil
	}
	goos := client.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := client.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	platform := goos + "-" + goarch
	if strings.TrimSpace(target.Release.Assets[platform]) == "" {
		trace.Printf("native runner skipped name=%q platform=%q: no release asset", target.Name, platform)
		return installer.Result{}, nil
	}
	result, err := client.Install(context.Background(), target.Name, target.Command, nativeRunnerManagedPath, target.Release, force)
	if err != nil {
		trace.Printf("native runner install failed name=%q error=%v", target.Name, err)
		return installer.Result{}, fmt.Errorf("%s native runner: %w", target.Name, err)
	}
	trace.Printf("native runner install result name=%q status=%q version=%q path=%q", result.Name, result.Status, result.Version, result.Path)
	return result, nil
}

// installWithoutRecipe handles targets with no usable GitHub release recipe:
// either a trusted source URL download, or an already-available executable.
// The source path validates nothing beyond an HTTPS scheme and a shebang, so
// invariant 4 requires an explicit allow_unverified: true in the recipe —
// anything else refuses instead of fetching from a mutable URL.
func installWithoutRecipe(client *installer.Installer, target installTarget, selected string, force bool, out, errOut io.Writer, trace *installLog) (string, error) {
	if target.SourceURL != "" {
		if !target.AllowUnverified {
			return "", fmt.Errorf("%s: source_url installs carry no checksum; set allow_unverified: true in the recipe to accept that, or add a release recipe", target.Name)
		}
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
func wireMemoryTargets(targets []installTarget, installedPaths map[string]string, memoryPath, home, serverURL, authToken string, out, errOut io.Writer, trace *installLog) []error {
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
		if err := wireMemory(context.Background(), memoryPath, home, serverURL, authToken, target.Memory, out, errOut, trace); err != nil {
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
		if isWindows() && target.Command == "ai-jail" {
			return
		}
		if selected == "" || target.Command == selected || target.Name == selected || containsString(target.Aliases, selected) {
			result = append(result, target)
		}
	}
	for _, agent := range global.Agents {
		appendTarget(installTarget{Name: agent.Name, Command: agent.Command, Aliases: agent.Aliases, Path: agent.Path, SourceURL: agent.SourceURL, AllowUnverified: agent.AllowUnverified, Release: agent.Release, Memory: agent.Memory, NeedsMemory: agent.SupportsMemory})
	}
	for _, tool := range global.Tools {
		appendTarget(installTarget{Name: tool.Name, Command: tool.Command, Aliases: tool.Aliases, Path: tool.Path, SourceURL: tool.SourceURL, AllowUnverified: tool.AllowUnverified, Release: tool.Release})
	}
	if selected != "" && !containsInstallTarget(result, "ai-memory") {
		for _, target := range result {
			if target.Memory != nil || target.NeedsMemory {
				for _, tool := range global.Tools {
					if tool.Command == "ai-memory" {
						result = append(result, installTarget{Name: tool.Name, Command: tool.Command, Aliases: tool.Aliases, Path: tool.Path, SourceURL: tool.SourceURL, AllowUnverified: tool.AllowUnverified, Release: tool.Release})
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

func wireMemory(ctx context.Context, memoryPath, home, serverURL, authToken string, integration *config.MemoryIntegration, out, errOut io.Writer, trace *installLog) error {
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
		if err := runMemoryCommand(ctx, memoryPath, args, authToken, out, errOut, trace); err != nil {
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
		if err := runMemoryCommand(ctx, memoryPath, args, authToken, out, errOut, trace); err != nil {
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

// runMemoryCommand executes ai-memory with the given args. The auth token is
// exported to the child environment only; it is never included in the logged
// argv.
func runMemoryCommand(ctx context.Context, memoryPath string, args []string, authToken string, out, errOut io.Writer, trace *installLog) error {
	trace.Printf("run memory command path=%q args=%q", memoryPath, args)
	command := exec.CommandContext(ctx, memoryPath, args...) // #nosec G204 -- memoryPath is the ai-memory executable resolved from the launcher's own install or PATH; running it is the integration's purpose
	command.Stdin = os.Stdin
	command.Stdout = out
	command.Stderr = errOut
	if token := strings.TrimSpace(authToken); token != "" {
		command.Env = append(os.Environ(), "AI_MEMORY_AUTH_TOKEN="+token)
	}
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

// AddAgent adds or updates an agent in the global catalog, deriving the
// command from the executable basename when it is not given.
func AddAgent(globalPath, name, path, command, description string, out io.Writer) error {
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
