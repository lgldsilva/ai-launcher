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
	"time"

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

// aiMemoryCommand is the catalog command of the ai-memory tool, which the
// installer treats specially: it also gets the managed native runner and is
// implicitly selected when a selected agent declares a memory integration.
const aiMemoryCommand = "ai-memory"

// configFileFlag is the ai-memory CLI flag that points install-mcp /
// install-hooks at the config file to write.
const configFileFlag = "--config-file"

// installNativeRunner installs the native ai-memory binary next to the
// wrapper; it is a variable so tests can observe the invocation without
// network access.
var installNativeRunner = installNativeMemoryRunner

// Deadlines for the two kinds of work an install step does. Both were
// context.Background() before, which is no deadline at all: a release host that
// accepts the connection without answering, or an ai-memory child that hangs
// waiting on something, froze `--install` until the operator killed it.
//
// They are variables rather than constants so tests can shorten them; nothing
// in production reassigns them.
var (
	// installStepTimeout bounds one target's download-and-install. It is
	// generous because it covers several requests plus a release archive on a
	// slow link; internal/installer bounds each individual request as well.
	installStepTimeout = 10 * time.Minute
	// memoryWireTimeout bounds one `ai-memory install-mcp` / `install-hooks`
	// child. That child edits a config file, so it should be quick; the limit
	// is a stuck-process guard, not a performance budget.
	memoryWireTimeout = 2 * time.Minute
	// memoryWireCleanupDelay is how long Wait may still block on the child's
	// pipes after the deadline killed it — see the WaitDelay comment in
	// runMemoryCommand. Short: by then the process is already gone.
	memoryWireCleanupDelay = 5 * time.Second
)

// withInstallTimeout derives a bounded context for one install step.
func withInstallTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, installStepTimeout)
}

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

// installStreams groups the output sinks shared by every install step: the
// user-facing stdout/stderr and the install trace log.
type installStreams struct {
	out    io.Writer
	errOut io.Writer
	trace  *installLog
}

// memoryWireConfig groups the ai-memory wiring settings shared by the
// MCP/hooks configuration steps.
type memoryWireConfig struct {
	memoryPath string
	home       string
	serverURL  string
	authToken  string
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
	streams := installStreams{out: out, errOut: errOut, trace: trace}
	installedPaths, failures := installAllTargets(client, targets, selected, force, home, streams)

	memoryPath := installedPaths[aiMemoryCommand]
	if memoryPath == "" {
		memoryPath = executableAvailable(aiMemoryCommand, nil, "")
	}
	trace.Printf("ai-memory path=%q", memoryPath)
	wireCfg := memoryWireConfig{memoryPath: memoryPath, home: home, serverURL: global.MemoryServerURL, authToken: global.MemoryAuthToken}
	failures = append(failures, wireMemoryTargets(targets, installedPaths, wireCfg, streams)...)
	if len(failures) > 0 {
		trace.Printf("install finished failures=%d", len(failures))
		return errors.Join(failures...)
	}
	trace.Printf("install finished successfully targets=%d", len(targets))
	return nil
}

// installAllTargets installs every target, collecting per-target failures and
// the paths of the executables that were installed or found.
func installAllTargets(client *installer.Installer, targets []installTarget, selected string, force bool, home string, streams installStreams) (map[string]string, []error) {
	var failures []error
	installedPaths := make(map[string]string, len(targets))
	for _, target := range targets {
		path, err := installOne(client, target, selected, force, home, streams)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if path == "" {
			continue
		}
		installedPaths[target.Command] = path
		if target.Command == aiMemoryCommand {
			if err := installNativeRunnerForTarget(client, target, force, streams); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return installedPaths, failures
}

// installNativeRunnerForTarget installs the managed ai-memory native runner
// for a freshly installed ai-memory target, reporting the result on out.
func installNativeRunnerForTarget(client *installer.Installer, target installTarget, force bool, streams installStreams) error {
	native, err := installNativeRunner(client, target, force, streams.trace)
	if err != nil {
		return err
	}
	if native.Path != "" {
		_, _ = fmt.Fprintf(streams.out, "%s native runner: %s (%s)\n", target.Name, native.Status, native.Path)
	}
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
func installOne(client *installer.Installer, target installTarget, selected string, force bool, home string, streams installStreams) (string, error) {
	trace := streams.trace
	trace.Printf("target name=%q command=%q aliases=%v source=%t release=%t", target.Name, target.Command, target.Aliases, target.SourceURL != "", target.Release != nil)
	if !preferReleaseInstall(target) {
		return installWithoutRecipe(client, target, selected, force, streams.out, streams.errOut, trace)
	}
	installPath := target.Path
	if installPath == "" {
		// If a variant is already installed (for example kilocode), update
		// that actual executable instead of creating a second canonical one.
		installPath = executableAvailable(target.Command, target.Aliases, "")
	}
	ctx, cancel := withInstallTimeout(context.Background())
	defer cancel()
	result, err := client.Install(ctx, target.Name, target.Command, installPath, target.Release, force)
	if err != nil && target.Path == "" && installPath != "" && errors.Is(err, os.ErrPermission) {
		// A discovered system-wide binary may be readable but not writable by
		// the current user. Retry in ~/.local/bin instead of requiring sudo.
		trace.Printf("release install retry name=%q blocked_path=%q fallback=%q error=%v", target.Name, installPath, filepath.Join(home, ".local", "bin", target.Command), err)
		installPath = ""
		retryCtx, retryCancel := withInstallTimeout(context.Background())
		defer retryCancel()
		result, err = client.Install(retryCtx, target.Name, target.Command, installPath, target.Release, force)
	}
	if err != nil {
		trace.Printf("release install failed name=%q path=%q error=%v", target.Name, installPath, err)
		return "", err
	}
	trace.Printf("release install result name=%q status=%q version=%q path=%q", result.Name, result.Status, result.Version, result.Path)
	if result.Version != "" {
		_, _ = fmt.Fprintf(streams.out, "%s: %s %s (%s)\n", result.Name, result.Status, result.Version, result.Path)
	} else {
		_, _ = fmt.Fprintf(streams.out, "%s: %s\n", result.Name, result.Status)
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
	ctx, cancel := withInstallTimeout(context.Background())
	defer cancel()
	result, err := client.Install(ctx, target.Name, target.Command, nativeRunnerManagedPath, target.Release, force)
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
			return "", fmt.Errorf("%s: source_url installs carry no checksum; set allow_unverified: true in the recipe to accept that, or add a release recipe (note: an agent overridden in the global config re-declares scalar fields — allow_unverified must be set on your entry, it is not inherited)", target.Name)
		}
		ctx, cancel := withInstallTimeout(context.Background())
		defer cancel()
		result, err := client.InstallSource(ctx, target.Name, target.Command, target.Path, target.SourceURL, force)
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
func wireMemoryTargets(targets []installTarget, installedPaths map[string]string, cfg memoryWireConfig, streams installStreams) []error {
	var failures []error
	for _, target := range targets {
		if target.Memory == nil || installedPaths[target.Command] == "" {
			if target.NeedsMemory && installedPaths[target.Command] != "" && target.Memory == nil {
				_, _ = fmt.Fprintf(streams.out, "%s: ai-memory runtime ready (no hooks/MCP adapter configured)\n", target.Name)
			}
			continue
		}
		if cfg.memoryPath == "" {
			streams.trace.Printf("memory wiring skipped name=%q: ai-memory unavailable", target.Name)
			failures = append(failures, fmt.Errorf("%s: ai-memory is required to install MCP/hooks", target.Name))
			continue
		}
		if err := wireMemory(context.Background(), cfg, target.Memory, streams); err != nil {
			streams.trace.Printf("memory wiring failed name=%q error=%v", target.Name, err)
			failures = append(failures, fmt.Errorf("%s: configure ai-memory: %w", target.Name, err))
			continue
		}
		streams.trace.Printf("memory wiring complete name=%q client=%q agent=%q", target.Name, target.Memory.Client, target.Memory.Agent)
		_, _ = fmt.Fprintf(streams.out, "%s: ai-memory MCP/hooks configured\n", target.Name)
	}
	return failures
}

func configuredInstallTargets(global config.Global, selected string) []installTarget {
	var result []installTarget
	for _, agent := range global.Agents {
		appendSelectedTarget(&result, agentInstallTarget(agent), selected)
	}
	for _, tool := range global.Tools {
		appendSelectedTarget(&result, toolInstallTarget(tool), selected)
	}
	return withImplicitMemoryTool(result, global, selected)
}

// agentInstallTarget maps a catalog agent to its install target, carrying the
// memory integration metadata.
func agentInstallTarget(agent config.Agent) installTarget {
	return installTarget{Name: agent.Name, Command: agent.Command, Aliases: agent.Aliases, Path: agent.Path, SourceURL: agent.SourceURL, AllowUnverified: agent.AllowUnverified, Release: agent.Release, Memory: agent.Memory, NeedsMemory: agent.SupportsMemory}
}

// toolInstallTarget maps a catalog tool to its install target.
func toolInstallTarget(tool config.Tool) installTarget {
	return installTarget{Name: tool.Name, Command: tool.Command, Aliases: tool.Aliases, Path: tool.Path, SourceURL: tool.SourceURL, AllowUnverified: tool.AllowUnverified, Release: tool.Release}
}

// appendSelectedTarget appends target to result when it matches the selected
// filter; ai-jail is skipped on Windows because the sandbox is Unix-only.
func appendSelectedTarget(result *[]installTarget, target installTarget, selected string) {
	if isWindows() && target.Command == "ai-jail" {
		return
	}
	if selected == "" || target.Command == selected || target.Name == selected || containsString(target.Aliases, selected) {
		*result = append(*result, target)
	}
}

// withImplicitMemoryTool appends the ai-memory tool to an explicit selection
// when a selected agent needs it and it is not already part of the result.
func withImplicitMemoryTool(result []installTarget, global config.Global, selected string) []installTarget {
	if selected == "" || containsInstallTarget(result, aiMemoryCommand) {
		return result
	}
	for _, target := range result {
		if target.Memory == nil && !target.NeedsMemory {
			continue
		}
		for _, tool := range global.Tools {
			if tool.Command == aiMemoryCommand {
				result = append(result, toolInstallTarget(tool))
			}
		}
		break
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

func wireMemory(ctx context.Context, cfg memoryWireConfig, integration *config.MemoryIntegration, streams installStreams) error {
	if integration == nil {
		return nil
	}
	if integration.InstallMCP {
		if err := wireMemoryMCP(ctx, cfg, integration, streams); err != nil {
			return err
		}
	}
	if integration.InstallHooks {
		if err := wireMemoryHooks(ctx, cfg, integration, streams); err != nil {
			return err
		}
	}
	return nil
}

// wireMemoryMCP runs ai-memory install-mcp against a staged MCP config file
// and publishes the result.
func wireMemoryMCP(ctx context.Context, cfg memoryWireConfig, integration *config.MemoryIntegration, streams installStreams) error {
	if strings.TrimSpace(integration.Client) == "" {
		return errors.New("ai-memory MCP client is empty")
	}
	configFile, finalize, err := prepareMemoryConfigFile(cfg.home, integration.MCPConfigFile, integration.Client, false)
	if err != nil {
		return fmt.Errorf("prepare MCP config: %w", err)
	}
	args := memoryInstallArgs("install-mcp", integration.Client, cfg.serverURL, configFile)
	streams.trace.Printf("memory MCP config client=%q file=%q", integration.Client, lastArgValue(args, configFileFlag))
	if err := runMemoryCommand(ctx, cfg.memoryPath, args, cfg.authToken, streams.out, streams.errOut, streams.trace); err != nil {
		return fmt.Errorf("install-mcp: %w", err)
	}
	if err := finalize(); err != nil {
		return fmt.Errorf("publish MCP config: %w", err)
	}
	return nil
}

// wireMemoryHooks runs ai-memory install-hooks against a staged hooks config
// file and publishes the result.
func wireMemoryHooks(ctx context.Context, cfg memoryWireConfig, integration *config.MemoryIntegration, streams installStreams) error {
	if strings.TrimSpace(integration.Agent) == "" {
		return errors.New("ai-memory hook agent is empty")
	}
	configFile, finalize, err := prepareMemoryConfigFile(cfg.home, integration.HooksConfigFile, integration.Agent, true)
	if err != nil {
		return fmt.Errorf("prepare hooks config: %w", err)
	}
	args := memoryInstallArgs("install-hooks", integration.Agent, cfg.serverURL, configFile)
	streams.trace.Printf("memory hooks config agent=%q file=%q", integration.Agent, lastArgValue(args, configFileFlag))
	if err := runMemoryCommand(ctx, cfg.memoryPath, args, cfg.authToken, streams.out, streams.errOut, streams.trace); err != nil {
		return fmt.Errorf("install-hooks: %w", err)
	}
	if err := finalize(); err != nil {
		return fmt.Errorf("publish hooks config: %w", err)
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
		args = append(args, configFileFlag, configFile)
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
		return expandHomePath(home, configured), noopFinalize, nil
	}
	logical := logicalMemoryConfigFile(home, target, hooks)
	if logical == "" {
		return "", noopFinalize, nil
	}
	resolved := resolveSymlinkParent(logical)
	if resolved == logical {
		return resolved, noopFinalize, nil
	}
	return stageMemoryConfigFile(home, target, hooks, logical, resolved)
}

// noopFinalize is the publish step for config files written directly in
// place, where there is no staged copy to publish.
func noopFinalize() error { return nil }

// stageMemoryConfigFile copies the logical agent config into a private
// staging file so ai-memory edits the copy; the returned finalize publishes
// the staged result over the symlink-resolved path and removes the staging
// file.
func stageMemoryConfigFile(home, target string, hooks bool, logical, resolved string) (string, func() error, error) {
	staging := filepath.Join(home, ".config", "ai-launch", "memory-staging", target)
	if hooks {
		staging += "-hooks.json"
	} else {
		staging += "-mcp.json"
	}
	if err := os.MkdirAll(filepath.Dir(staging), 0o700); err != nil {
		return "", nil, err
	}
	if err := copyConfigToStaging(logical, staging); err != nil {
		return "", nil, err
	}
	finalize := func() error { return publishStagedConfig(staging, resolved) }
	return staging, finalize, nil
}

// copyConfigToStaging seeds the staging file with the current config content;
// a config that does not exist yet starts empty.
func copyConfigToStaging(logical, staging string) error {
	data, err := os.ReadFile(logical) // #nosec G304 -- logical is a well-known agent config path under the user home
	if err == nil {
		return os.WriteFile(staging, data, 0o600) // #nosec G703 -- staging is derived from the user home and the fixed target name, not user input
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// publishStagedConfig writes the staged config over the resolved target path,
// preserving its permission bits when it already exists, then removes the
// staging file.
func publishStagedConfig(staging, resolved string) error {
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
	// The child is the launcher's own subprocess, so bounding it is the
	// launcher's job: an ai-memory stuck on a prompt or a dead server would
	// otherwise hold --install open indefinitely with no output.
	ctx, cancel := context.WithTimeout(ctx, memoryWireTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, memoryPath, args...) // #nosec G204 -- memoryPath is the ai-memory executable resolved from the launcher's own install or PATH; running it is the integration's purpose
	// Killing the child is not enough on its own: Wait also blocks until the
	// stdout/stderr pipes close, and a grandchild that outlives its parent
	// keeps the write end open. WaitDelay bounds that second wait, so the
	// timeout above cannot be defeated by a process ai-memory forked.
	command.WaitDelay = memoryWireCleanupDelay
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
	// SupportsMemory follows what `ai-memory run` actually accepts. Hardcoding
	// true registered agents that then failed pre-flight with
	// memory-harness-unsupported on their first launch — an error about a
	// decision --add had made, not one the operator did. A wrapper whose real
	// harness has another name declares memory.run_harness in the catalog.
	agent := config.Agent{
		Name:           name,
		Command:        command,
		Path:           path,
		SupportsMemory: config.SupportsMemoryRunHarness(command),
		SupportsYolo:   true,
		Description:    description,
	}
	if err := config.UpsertAgent(&global, agent); err != nil {
		return err
	}
	if err := config.SaveGlobal(globalPath, global); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "agent %q added to %s\n", name, globalPath)
	return err
}
