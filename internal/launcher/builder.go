// Package launcher builds and validates the argv used to start an AI agent.
package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// aiMemoryCommand is the upstream memory CLI invoked as the wrapper layer of
// the composed argv (`ai-memory run <harness>`) and probed on PATH by the
// validator. The literal is locked byte-for-byte by the Gherkin contract
// suite, so every emission shares this single constant.
const aiMemoryCommand = "ai-memory"

// userHomeDir and isWindows abstract the platform lookups so tests can
// exercise the managed ai-memory native runner path for any target platform.
var userHomeDir = os.UserHomeDir
var isWindows = func() bool { return runtime.GOOS == "windows" }

// ownedMemoryEnvKeys are the AI_MEMORY_* variables the launcher owns. They are
// always stripped from the inherited environment first; only an enabled memory
// wrapper re-adds the exact values it needs. Leaving them in when memory is
// off would hand the bare harness a bearer token and server URL.
var ownedMemoryEnvKeys = []string{
	"AI_MEMORY_SERVER_URL",
	"AI_MEMORY_AUTH_TOKEN",
	"AI_MEMORY_NATIVE_BIN",
}

// Environment returns the inherited environment with the configured ai-memory
// server URL and auth token applied to child processes. ai-memory reads these
// variables for its runtime wrapper and generated integrations. When the
// launcher-managed native binary exists, AI_MEMORY_NATIVE_BIN is exported too
// so the wrapper skips its own download. The token is a bearer secret: it is
// passed through the environment only and must never be written to logs.
//
// Owned AI_MEMORY_* keys are always removed first. When UseMemory is false the
// child sees none of them (even if the parent shell exported a stale token).
// When UseMemory is true, only the values this launch configures are re-added.
func Environment(cfg LaunchConfig) []string {
	env := append([]string(nil), os.Environ()...)
	for _, key := range ownedMemoryEnvKeys {
		env = upsertEnv(env, key, "")
	}
	if !cfg.UseMemory {
		return env
	}
	// An empty config value means the operator chose environment-based
	// configuration for the server URL only — restore the stripped inherited
	// value by reading it from the original process environment after a
	// non-empty config override is absent.
	if serverURL := strings.TrimSpace(cfg.MemoryServerURL); serverURL != "" {
		env = upsertEnv(env, "AI_MEMORY_SERVER_URL", serverURL)
	} else if inherited := strings.TrimSpace(os.Getenv("AI_MEMORY_SERVER_URL")); inherited != "" {
		env = upsertEnv(env, "AI_MEMORY_SERVER_URL", inherited)
	}
	env = upsertEnv(env, "AI_MEMORY_AUTH_TOKEN", strings.TrimSpace(cfg.MemoryAuthToken))
	// The ai-memory wrapper skips its own download/refresh logic (fragile
	// inside ai-jail) when AI_MEMORY_NATIVE_BIN points at an executable
	// native binary managed by the launcher's installer. The executable
	// bit is required: a regular file without it would make the wrapper
	// exec a non-runnable path. Windows has no exec bit, so the regular
	// file check is enough there. The check is best-effort — the managed
	// directory is user-writable, so the file can change before the child
	// execs it (accepted TOCTOU; the wrapper fails visibly, not silently).
	if native := managedNativeRunnerPath(cfg.HomeDir); native != "" {
		if info, err := os.Stat(native); err == nil && info.Mode().IsRegular() &&
			(isWindows() || info.Mode().Perm()&0o111 != 0) {
			env = upsertEnv(env, "AI_MEMORY_NATIVE_BIN", native)
		}
	}
	return env
}

// managedNativeRunnerPath resolves the launcher's managed install location of
// the ai-memory native binary, honoring the same home-dir convention as the
// installer (~/.local/share/ai-launcher/bin, with .exe on Windows). It returns
// "" when no home directory is available.
func managedNativeRunnerPath(home string) string {
	home = strings.TrimSpace(home)
	if home == "" {
		resolved, err := userHomeDir()
		if err != nil {
			return ""
		}
		home = resolved
	}
	path := filepath.Join(home, ".local", "share", "ai-launcher", "bin", aiMemoryCommand)
	if isWindows() {
		path += ".exe"
	}
	return path
}

// upsertEnv replaces or appends a KEY=value entry, and removes the key when
// value is empty. Removal matters: the child inherits the parent environment,
// so returning early on an empty value forwarded whatever the parent happened
// to have — a stale AI_MEMORY_AUTH_TOKEN or an AI_MEMORY_SERVER_URL from
// direnv would silently point the sandboxed agent at another server.
// "Not configured" has to mean "not set".
func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	filtered := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	if value == "" {
		return filtered
	}
	return append(filtered, prefix+value)
}

// LaunchConfig carries every input needed to build and validate the argv used
// to start an agent, without performing any I/O.
type LaunchConfig struct {
	Agent           config.Agent
	Executable      string
	HomeDir         string
	MemoryServerURL string
	MemoryAuthToken string
	UseJail         bool
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
	if cfg.UseJail {
		command = appendJailArgs(command, cfg)
	}
	if cfg.UseMemory {
		command = append(command, aiMemoryCommand, "run")
		command = appendMemoryScope(command, cfg)
		command = appendWorkstreamSelection(command, cfg)
		if cfg.Fresh {
			command = append(command, "--fresh")
		}
		// Harness name must be one ai-memory accepts (claude, opencode, …).
		// Wrappers like "oc" keep Agent.Command for PATH/display but remap here.
		command = append(command, memoryRunHarness(cfg.Agent))
		if cfg.Executable != "" {
			command = append(command, "--executable", cfg.Executable)
		}
	} else {
		executable := cfg.Agent.Command
		if cfg.Executable != "" {
			executable = cfg.Executable
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

// buildContinue composes "ai-memory run" without a harness, which auto-resumes
// the most recent session of the checkout, jail-wrapped when the jail is on.
// Every wrapper flag `ai-memory run` accepts without a harness is emitted:
// only the harness token and its native arguments are dropped, and the
// validator reports that so nothing disappears silently.
func buildContinue(cfg LaunchConfig) ([]string, error) {
	if !cfg.UseMemory {
		return nil, errors.New("continuing a session requires ai-memory")
	}
	command := make([]string, 0, 10)
	if cfg.UseJail {
		command = appendJailArgs(command, cfg)
	}
	command = append(command, aiMemoryCommand, "run")
	command = appendMemoryScope(command, cfg)
	command = appendWorkstreamSelection(command, cfg)
	return appendYoloFlag(command, cfg), nil
}

// appendWorkstreamSelection emits the workstream flags in ai-memory's own
// precedence: creating a new one wins over resuming a named one.
func appendWorkstreamSelection(command []string, cfg LaunchConfig) []string {
	if cfg.NewWorkstream != "" {
		return append(command, "--new", cfg.NewWorkstream)
	}
	if cfg.Workstream != "" {
		return append(command, "--workstream", cfg.Workstream)
	}
	return command
}

// appendMemoryScope appends the ai-memory run scoping wrapper flags.
func appendMemoryScope(command []string, cfg LaunchConfig) []string {
	if workspace := strings.TrimSpace(cfg.Workspace); workspace != "" {
		command = append(command, "--workspace", workspace)
	}
	if project := strings.TrimSpace(cfg.Project); project != "" {
		command = append(command, "--project", project)
	}
	return command
}

// appendDeclaredParams appends the catalog-declared harness flags whose names
// have a value in cfg.ParamValues, in declaration order. Undeclared names are
// ignored here and reported by the validator instead.
func appendDeclaredParams(command []string, cfg LaunchConfig) []string {
	for _, param := range cfg.Agent.Params {
		value, ok := cfg.ParamValues[param.Name]
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if !param.TakesValue {
			if flagEnabled(value) {
				command = append(command, param.Flag)
			}
			continue
		}
		command = append(command, param.Flag, value)
	}
	return command
}

// flagEnabled reports whether a param value enables a boolean-style flag
// (any non-empty value except the usual negative spellings).
func flagEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "false", "0", "no", "off":
		return false
	}
	return true
}

// appendYoloFlag appends the resolved dangerous-mode flag. When ai-memory
// wraps the harness, --yolo is passed to the ai-memory run invocation, which
// does its own per-harness translation; otherwise the agent's declared
// yolo_flag is used, falling back to --yolo. A flag already present in
// extra_args is never duplicated.
func appendYoloFlag(command []string, cfg LaunchConfig) []string {
	if !cfg.Yolo {
		return command
	}
	flag := cfg.Agent.YoloFlag
	if cfg.UseMemory || strings.TrimSpace(flag) == "" {
		flag = "--yolo"
	}
	for _, arg := range cfg.ExtraArgs {
		if arg == flag {
			return command
		}
	}
	return append(command, flag)
}

// appendJailArgs prepends the ai-jail wrapper with the programmatic-mode
// flag, the declarative jail capability flags, and the flags derived from the
// enabled permissions and configured mounts.
func appendJailArgs(command []string, cfg LaunchConfig) []string {
	command = append(command, "ai-jail")
	if cfg.JailExec {
		// --exec is ai-jail's programmatic mode: direct exec without the PTY
		// proxy or status bar. Interactive TUI launches leave the ai-jail
		// defaults in charge of the terminal instead.
		command = append(command, "--exec")
	}
	command = appendDockerDecision(command, cfg)
	command = appendJailFlags(command, cfg.JailFlags)
	command = appendPermissionArgs(command, cfg)
	command = appendExecutableMount(command, cfg)
	for _, mount := range cfg.Mounts {
		path := strings.TrimSpace(mount.Path)
		if path == "" {
			continue
		}
		if strings.EqualFold(mount.Mode, "ro") || strings.EqualFold(mount.Mode, "read-only") {
			command = append(command, "--map", path)
		} else {
			command = append(command, "--rw-map", path)
		}
	}
	return command
}

// appendExecutableMount maps the directory holding the resolved harness binary
// read-only, unless a configured mount already covers it. `--executable` names
// a host path that has to exist *inside* the sandbox; nothing mounted it, so
// the argv pointed at a binary the agent could not reach and operators worked
// around it by hand (the .ai-jail in this repo carries /opt/homebrew for
// exactly that reason). Read-only is enough: the agent runs it, never writes it.
func appendExecutableMount(command []string, cfg LaunchConfig) []string {
	executable := strings.TrimSpace(cfg.Executable)
	if executable == "" || !filepath.IsAbs(executable) {
		return command
	}
	dir := filepath.Dir(executable)
	if dir == "" || dir == string(filepath.Separator) {
		return command
	}
	if coveredByMounts(dir, cfg.Mounts) {
		return command
	}
	return append(command, "--map", dir)
}

// appendDockerDecision emits exactly one of --docker / --no-docker, always.
//
// Every other capability the launcher touches may be left unset, which puts
// ai-jail in auto mode: enabled when the resource exists on the host. For the
// Docker socket that default was a sandbox escape. Through ai-jail v1.15.x an
// existing /var/run/docker.sock was bind-mounted read-write with no flag and
// no warning, and write access to that socket is root on the host — the agent
// asks the daemon to mount / into a container and walks past bubblewrap,
// Landlock, seccomp, the tmpfs $HOME and every --mask in one command
// (ai-jail issue #88). v1.16.0 flipped the default to opt-in.
//
// So the launcher stops leaving it to the default. Being explicit costs one
// argv token, says the same thing to 1.15.x and 1.16.x, and makes the README's
// "docker off by default" a property of the command this launcher builds
// rather than a property of whichever ai-jail happens to be installed.
//
// Precedence matches every other capability: an explicit jail_flags.docker is
// the operator's declarative choice and wins, then the permission toggle, then
// the safe default.
func appendDockerDecision(command []string, cfg LaunchConfig) []string {
	if declared := cfg.JailFlags.Docker; declared != nil {
		if *declared {
			return append(command, "--docker")
		}
		return append(command, "--no-docker")
	}
	if cfg.Permissions["docker"] {
		return append(command, "--docker")
	}
	return append(command, "--no-docker")
}

// jailPermission maps a catalog permission id to the ai-jail capability it
// enables. override names the config.JailFlags field that declares the same
// capability: when that field is set, the declarative value wins and the
// permission stays silent, so the argv never contradicts itself with
// "--no-tailscale --tailscale". mountHome replaces the flag with a read-write
// map of a path under $HOME (ai-jail has no dedicated flag for it).
//
// docker is deliberately not in this list: appendDockerDecision owns it,
// because it is the one capability that must be stated even when it is off.
type jailPermission struct {
	id        string
	flag      string
	mountHome string
	override  func(config.JailFlags) *bool
}

// jailPermissions lists the permission-driven jail arguments in their stable
// emission order.
func jailPermissions() []jailPermission {
	return []jailPermission{
		{id: "ssh", flag: "--ssh"},
		{id: "gh", mountHome: ".config/gh"},
		{id: "gpu", flag: "--gpu", override: func(f config.JailFlags) *bool { return f.GPU }},
		{id: "display", flag: "--display", override: func(f config.JailFlags) *bool { return f.Display }},
		{id: "pictures", flag: "--pictures"},
		{id: "tailscale", flag: "--tailscale", override: func(f config.JailFlags) *bool { return f.Tailscale }},
		{id: "systemd-user", flag: "--systemd-user"},
		{id: "mise", flag: "--mise", override: func(f config.JailFlags) *bool { return f.Mise }},
		{id: "worktree", flag: "--worktree", override: func(f config.JailFlags) *bool { return f.Worktree }},
	}
}

// appendPermissionArgs emits the jail arguments enabled by the permission map,
// skipping every capability the declarative jail_flags block already decided.
func appendPermissionArgs(command []string, cfg LaunchConfig) []string {
	for _, permission := range jailPermissions() {
		if !cfg.Permissions[permission.id] {
			continue
		}
		if permission.override != nil && permission.override(cfg.JailFlags) != nil {
			continue
		}
		if permission.mountHome != "" {
			mount, ok := homeMountPath(cfg.HomeDir, permission.mountHome)
			if !ok {
				// Fail closed: without a home directory the mount would be a
				// relative path whose meaning changes with the cwd.
				continue
			}
			if coveredByMounts(mount, cfg.Mounts) {
				continue
			}
			command = append(command, "--rw-map", mount)
			continue
		}
		command = append(command, permission.flag)
	}
	return command
}

// jailToggle declares one tri-state ai-jail capability flag.
type jailToggle struct {
	name  string
	value *bool
}

// jailToggles lists the ai-jail v1.16 capability flags in their stable
// emission order so the mapping stays declarative instead of a chain of ifs.
//
// docker is absent on purpose: unlike every other capability here it is never
// left to ai-jail's auto mode, so appendDockerDecision owns its emission.
func jailToggles(flags config.JailFlags) []jailToggle {
	return []jailToggle{
		{name: "lockdown", value: flags.Lockdown},
		{name: "private-home", value: flags.PrivateHome},
		{name: "tailscale", value: flags.Tailscale},
		{name: "gpu", value: flags.GPU},
		{name: "display", value: flags.Display},
		{name: "mise", value: flags.Mise},
		{name: "worktree", value: flags.Worktree},
		{name: "landlock", value: flags.Landlock},
		{name: "seccomp", value: flags.Seccomp},
		{name: "rlimits", value: flags.Rlimits},
		{name: "status-bar", value: flags.StatusBar},
		{name: "hide-config", value: flags.HideConfig},
	}
}

// statusBarStyle normalizes the configured status bar theme, returning "" for
// unset or unrecognized values (which keeps the StatusBar boolean toggle in
// charge).
func statusBarStyle(flags config.JailFlags) string {
	switch strings.ToLower(strings.TrimSpace(flags.StatusBarStyle)) {
	case "dark", "light", "pastel":
		return strings.ToLower(strings.TrimSpace(flags.StatusBarStyle))
	}
	return ""
}

// appendJailFlags maps the declarative jail options to the exact ai-jail
// v1.16 CLI flags in a deterministic order: status bar style, toggles, list
// flags, browser profile, claude dir, and the v1.15 exception lists. A
// configured status bar style wins over the StatusBar toggle, so --no-status-bar
// is never emitted together with --status-bar=STYLE.
func appendJailFlags(command []string, flags config.JailFlags) []string {
	style := statusBarStyle(flags)
	if style != "" {
		command = append(command, "--status-bar="+style)
	}
	for _, toggle := range jailToggles(flags) {
		if toggle.name == "status-bar" && style != "" {
			continue
		}
		command = appendJailToggle(command, toggle)
	}
	for _, path := range flags.OverlayMaps {
		command = appendFlagValue(command, "--overlay-map", path)
	}
	for _, path := range flags.Mask {
		command = appendFlagValue(command, "--mask", path)
	}
	for _, path := range flags.DenyPaths {
		command = appendFlagValue(command, "--deny-path", path)
	}
	for _, port := range flags.AllowTCPPorts {
		command = append(command, "--allow-tcp-port", strconv.Itoa(port))
	}
	switch strings.ToLower(strings.TrimSpace(flags.Browser)) {
	case "hard", "soft":
		command = append(command, "--browser="+strings.ToLower(strings.TrimSpace(flags.Browser)))
	case "off":
		command = append(command, "--no-browser")
	}
	command = appendFlagValue(command, "--claude-dir", flags.ClaudeDir)
	for _, path := range flags.MaskExceptions {
		command = appendFlagValue(command, "--mask-except", path)
	}
	for _, path := range flags.DenyPathExceptions {
		command = appendFlagValue(command, "--deny-path-except", path)
	}
	for _, dir := range flags.HideDotdirs {
		command = appendFlagValue(command, "--hide-dotdir", dir)
	}
	return command
}

// appendJailToggle emits the positive or negative form of one capability flag.
// An unset (nil) toggle emits nothing, which leaves the capability in ai-jail's
// auto mode; forcing it on is a different state and must emit the positive
// form, so neither direction is ever silently dropped.
func appendJailToggle(command []string, toggle jailToggle) []string {
	if toggle.value == nil {
		return command
	}
	if *toggle.value {
		return append(command, "--"+toggle.name)
	}
	return append(command, "--no-"+toggle.name)
}

// appendFlagValue appends "--flag value" when value is non-blank.
func appendFlagValue(command []string, flag, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return command
	}
	return append(command, flag, value)
}

// homeMountPath joins suffix to the operator's home directory, falling back
// to $HOME, and reports false when no home is known. Emitting the bare
// relative suffix instead would mount a path whose meaning silently changes
// with the working directory, so the mount is omitted (fail closed).
// filepath.Join also keeps home == "/" correct ("/.config/gh", not a path
// built by hand-trimming separators).
func homeMountPath(home, suffix string) (string, bool) {
	home = strings.TrimSpace(home)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("HOME"))
	}
	if home == "" {
		return "", false
	}
	return filepath.Join(home, suffix), true
}

// Issue is a single validation problem with a stable machine-readable Code.
// Warning issues are advisory: the launch may proceed after reporting them.
type Issue struct {
	Code    string
	Message string
	Warning bool
}

func (i Issue) Error() string { return fmt.Sprintf("%s: %s", i.Code, i.Message) }

// ConstrainToPlatform returns cfg with the integrations unsupported on goos
// removed, plus an explanatory warning for everything that was dropped. Today
// the only constrained platform is Windows, where ai-jail does not exist:
// the jail and every permission that requires it are disabled.
func ConstrainToPlatform(cfg LaunchConfig, goos string, permissions []config.Permission) (LaunchConfig, []Issue) {
	if goos != "windows" {
		return cfg, nil
	}
	hadJail := cfg.UseJail
	cfg, dropped := dropJailAndDependents(cfg, permissions)
	if !hadJail && len(dropped) == 0 {
		return cfg, nil
	}
	message := "ai-jail is not supported on Windows; continuing without sandbox"
	if len(dropped) > 0 {
		message += "; disabled jail-only permissions: " + strings.Join(dropped, ", ")
	}
	return cfg, []Issue{{Code: "jail-unsupported-windows", Message: message, Warning: true}}
}

// dropJailAndDependents turns off UseJail and every jail-backed permission.
// It returns the sorted list of permission IDs that were on and got cleared.
func dropJailAndDependents(cfg LaunchConfig, permissions []config.Permission) (LaunchConfig, []string) {
	if cfg.Permissions == nil {
		cfg.Permissions = make(map[string]bool)
	}
	dropped := make([]string, 0, len(cfg.Permissions))
	dependent := config.JailDependentIDs(permissions)
	for id, enabled := range cfg.Permissions {
		if enabled && dependent[id] {
			cfg.Permissions[id] = false
			dropped = append(dropped, id)
		}
	}
	cfg.UseJail = false
	sort.Strings(dropped)
	return cfg, dropped
}

// Validator checks a LaunchConfig against the host (PATH lookups and mount
// existence) and reports every problem found instead of failing fast.
type Validator struct {
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	// Getwd overrides the process working directory used by jail/cwd checks;
	// empty means os.Getwd.
	Getwd func() (string, error)
	// GOOS overrides the platform used by platform-specific checks; empty
	// means the runtime platform.
	GOOS string
	// Permissions is the effective permission catalog, the source of the
	// platform metadata used by the unsupported-platform check. Empty falls
	// back to the built-in defaults, so callers that do not load a global
	// config still get the shipped platform rules.
	Permissions []config.Permission
}

// NewValidator returns a Validator backed by the real PATH and filesystem.
func NewValidator() Validator {
	return Validator{LookPath: exec.LookPath, Stat: os.Stat, Getwd: os.Getwd}
}

// WithPermissions returns a copy of v that reads platform metadata from the
// given catalog instead of the built-in defaults.
func (v Validator) WithPermissions(permissions []config.Permission) Validator {
	v.Permissions = permissions
	return v
}

// Validate returns every issue found in cfg, or an empty slice when the
// configuration can be launched.
func (v Validator) Validate(cfg LaunchConfig) []Issue {
	issues := make([]Issue, 0)
	lookPath := v.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	stat := v.Stat
	if stat == nil {
		stat = os.Stat
	}
	goos := v.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	onWindows := goos == "windows"
	issues = append(issues, agentIssues(cfg, lookPath)...)
	issues = append(issues, jailIssues(cfg, lookPath, onWindows)...)
	issues = append(issues, allowTCPPortIssues(cfg)...)
	// Getwd is optional: unit tests leave it nil; NewValidator sets os.Getwd.
	if v.Getwd != nil {
		// Advisory only — ai-jail works on macOS; the friction is ai-memory
		// canonicalizing a /Volumes cwd *inside* the sandbox.
		issues = append(issues, jailMemoryVolumeIssues(cfg, v.Getwd, goos)...)
	}
	if cfg.UseMemory {
		if _, err := lookPath(aiMemoryCommand); err != nil {
			issues = append(issues, Issue{Code: "memory-not-found", Message: "ai-memory is required when memory integration is enabled"})
		}
		issues = append(issues, memoryHarnessIssues(cfg)...)
	}
	issues = append(issues, mountIssues(cfg, stat)...)
	issues = append(issues, permissionIssues(cfg, goos, v.permissionCatalog())...)
	issues = append(issues, permissionMountIssues(cfg)...)
	issues = append(issues, undeclaredParamIssues(cfg)...)
	issues = append(issues, catalogBooleanParamIssues(cfg)...)
	issues = append(issues, continueIssues(cfg)...)
	issues = append(issues, freshIssues(cfg)...)
	return issues
}

// permissionCatalog returns the catalog backing the platform checks, falling
// back to the built-in defaults when the caller did not supply one.
func (v Validator) permissionCatalog() []config.Permission {
	if len(v.Permissions) > 0 {
		return v.Permissions
	}
	return config.DefaultGlobal().Permissions
}

// agentIssues reports when the configured agent executable is unavailable.
// Continue sessions have no harness, so there is nothing to look up.
func agentIssues(cfg LaunchConfig, lookPath func(string) (string, error)) []Issue {
	if cfg.ContinueSession {
		return nil
	}
	agentPath := cfg.Agent.Command
	if cfg.Executable != "" {
		agentPath = cfg.Executable
	}
	if _, err := lookPath(agentPath); err != nil {
		return []Issue{{Code: "agent-not-found", Message: fmt.Sprintf("%q is not available in PATH", cfg.Agent.Command)}}
	}
	return nil
}

// memoryHarnessIssues rejects a harness `ai-memory run` does not accept. Without
// it the launcher emits a valid-looking argv and the failure surfaces as an
// opaque clap error inside the jail, long after the point of diagnosis. A
// continue session carries no harness at all, so there is nothing to check.
func memoryHarnessIssues(cfg LaunchConfig) []Issue {
	if cfg.ContinueSession {
		return nil
	}
	harness := memoryRunHarness(cfg.Agent)
	if config.SupportsMemoryRunHarness(harness) {
		return nil
	}
	return []Issue{{
		Code: "memory-harness-unsupported",
		Message: fmt.Sprintf(
			"ai-memory run does not accept harness %q (accepted: %s); relaunch with --no-memory, or declare memory.run_harness in the catalog",
			harness, strings.Join(config.MemoryRunHarnesses(), ", ")),
	}}
}

// jailIssues checks the ai-jail dependency, degrading to warnings where the
// jail cannot apply (Windows) or does not apply (jail disabled).
func jailIssues(cfg LaunchConfig, lookPath func(string) (string, error), onWindows bool) []Issue {
	if cfg.UseJail {
		if onWindows {
			return []Issue{{Code: "jail-unsupported-windows", Message: "ai-jail is not supported on Windows; the sandbox and jail-only options are ignored", Warning: true}}
		}
		if _, err := lookPath("ai-jail"); err != nil {
			return []Issue{{Code: "jail-not-found", Message: "ai-jail is required when sandboxing is enabled"}}
		}
		return nil
	}
	// JailExec is not a user choice: it is set for every non-TUI launch, so
	// including it here fired the warning on plain --no-jail runs where no jail
	// option had been configured at all.
	if !cfg.JailFlags.IsZero() {
		return []Issue{{Code: "jail-options-without-jail", Message: "jail options are set but the jail is disabled; they will be ignored", Warning: true}}
	}
	return nil
}

// allowTCPPortIssues warns when allow_tcp_ports is configured without
// lockdown. ai-jail classifies --allow-tcp-port as lockdown-only, so the
// builder emits the flags but upstream silently ignores them — the operator
// believes ports are open when nothing is.
func allowTCPPortIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseJail || len(cfg.JailFlags.AllowTCPPorts) == 0 {
		return nil
	}
	if cfg.JailFlags.Lockdown != nil && *cfg.JailFlags.Lockdown {
		return nil
	}
	return []Issue{{
		Code:    "allow-tcp-ports-without-lockdown",
		Message: "allow_tcp_ports only takes effect with lockdown enabled; ai-jail ignores --allow-tcp-port otherwise",
		Warning: true,
	}}
}

// permissionMountIssues surfaces the two ways an enabled home-mount
// permission (gh) can silently lose its effect: the mount is omitted because
// no home directory is known, or a configured read-only mount covers the path
// and the dedup keeps the weaker mode. Both keep the operator's explicit
// choice — the warning is what was missing.
func permissionMountIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseJail {
		return nil
	}
	var issues []Issue
	for _, permission := range jailPermissions() {
		if permission.mountHome == "" || !cfg.Permissions[permission.id] {
			continue
		}
		mount, ok := homeMountPath(cfg.HomeDir, permission.mountHome)
		if !ok {
			issues = append(issues, Issue{
				Code:    "permission-mount-without-home",
				Message: fmt.Sprintf("permission %q needs a mount under the home directory, but no home is known; the mount was omitted", permission.id),
				Warning: true,
			})
			continue
		}
		if coveredByMounts(mount, cfg.Mounts) && !coveredByWritableMounts(mount, cfg.Mounts) {
			issues = append(issues, Issue{
				Code:    "permission-mount-downgraded",
				Message: fmt.Sprintf("permission %q needs read-write access to %s, but a configured mount covers it read-only; make that mount rw or narrower", permission.id, mount),
				Warning: true,
			})
		}
	}
	return issues
}

// freshIssues rejects asking to resume and to start fresh at the same time.
// --continue resumes the most recent session; --fresh starts a new one in the
// current workstream. Letting ai-memory arbitrate would make the outcome depend
// on flag order rather than on what the operator asked for.
func freshIssues(cfg LaunchConfig) []Issue {
	if cfg.Fresh && cfg.ContinueSession {
		return []Issue{{
			Code:    "fresh-with-continue",
			Message: "--fresh starts a new session and --continue resumes the most recent one; pick one",
		}}
	}
	return nil
}

// continueIssues reports the input a continue session cannot carry. `ai-memory
// run` without a harness takes the wrapper flags but has nowhere to put
// harness-native arguments or catalog params, so they are dropped — visibly.
func continueIssues(cfg LaunchConfig) []Issue {
	if !cfg.ContinueSession {
		return nil
	}
	dropped := make([]string, 0, 2)
	if len(cfg.ExtraArgs) > 0 {
		dropped = append(dropped, "extra args")
	}
	if len(cfg.ParamValues) > 0 {
		dropped = append(dropped, "harness params")
	}
	if len(dropped) == 0 {
		return nil
	}
	return []Issue{{
		Code:    "continue-ignores-harness-input",
		Message: "a continue session runs ai-memory without a harness; " + strings.Join(dropped, " and ") + " will be ignored",
		Warning: true,
	}}
}

// jailMemoryVolumeIssues warns when Jail + ai-memory run on an external-volume
// project. ai-jail itself works on macOS; ai-memory's managed-run client may
// fail with "canonicalizing managed run cwd … Operation not permitted" inside
// the sandbox for /Volumes paths. Advisory only — do not auto-disable the jail.
func jailMemoryVolumeIssues(cfg LaunchConfig, getwd func() (string, error), goos string) []Issue {
	if !cfg.UseJail || !cfg.UseMemory {
		return nil
	}
	cwd, err := getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		return nil
	}
	if !externalVolumeCwd(cwd, goos) {
		return nil
	}
	return []Issue{{
		Code:    "jail-memory-external-volume",
		Warning: true,
		Message: fmt.Sprintf(
			"project is on external volume %q — Jail is kept on (ai-jail supports macOS); if ai-memory fails with cwd Operation not permitted, retry with --no-memory or from a path under your home directory",
			cwd,
		),
	}}
}

// externalVolumeCwd reports host paths on removable/external media where some
// in-sandbox tools (notably ai-memory managed-run) struggle with cwd realpath.
func externalVolumeCwd(cwd, goos string) bool {
	switch goos {
	case "darwin":
		return strings.HasPrefix(cwd, "/Volumes/")
	case "linux":
		return strings.HasPrefix(cwd, "/media/") || strings.HasPrefix(cwd, "/mnt/") || strings.HasPrefix(cwd, "/run/media/")
	default:
		return false
	}
}

// mountIssues reports configured mounts that do not exist on the host.
func mountIssues(cfg LaunchConfig, stat func(string) (os.FileInfo, error)) []Issue {
	issues := make([]Issue, 0)
	for _, mount := range cfg.Mounts {
		if strings.TrimSpace(mount.Path) == "" {
			continue
		}
		if _, err := stat(mount.Path); err != nil {
			issues = append(issues, Issue{Code: "mount-not-found", Message: fmt.Sprintf("mount path %q does not exist", mount.Path)})
		}
	}
	return issues
}

// permissionIssues checks the cross-permission dependencies (every helper
// permission needs the jail) and warns about enabled permissions that the host
// platform does not support.
func permissionIssues(cfg LaunchConfig, goos string, catalog []config.Permission) []Issue {
	issues := make([]Issue, 0)
	if cfg.Permissions["ssh"] || cfg.Permissions["gh"] || cfg.Permissions["docker"] || cfg.Permissions["gpu"] {
		if !cfg.UseJail {
			issue := Issue{Code: "permission-without-jail", Message: "ssh, gh, docker, and gpu permissions require ai-jail"}
			if goos == "windows" {
				issue.Message = "ssh, gh, docker, and gpu permissions require ai-jail, which is unavailable on Windows; they will be ignored"
				issue.Warning = true
			}
			issues = append(issues, issue)
		}
	}
	issues = append(issues, unsupportedPlatformIssues(cfg, goos, catalog)...)
	return issues
}

// unsupportedPlatformIssues warns (never fails) about enabled permissions
// whose Platforms list excludes goos — for example systemd-user on macOS.
// Platform metadata comes from the effective catalog; unknown IDs have no
// restriction.
func unsupportedPlatformIssues(cfg LaunchConfig, goos string, catalog []config.Permission) []Issue {
	byID := make(map[string]config.Permission, len(catalog))
	for _, permission := range catalog {
		byID[permission.ID] = permission
	}
	enabled := make([]string, 0, len(cfg.Permissions))
	for id, on := range cfg.Permissions {
		if on {
			enabled = append(enabled, id)
		}
	}
	sort.Strings(enabled)
	issues := make([]Issue, 0)
	for _, id := range enabled {
		permission, ok := byID[id]
		if !ok || config.PermissionSupportedOn(permission, goos) {
			continue
		}
		issues = append(issues, Issue{
			Code:    "unsupported-platform",
			Message: fmt.Sprintf("permission %s is not supported on %s", id, goos),
			Warning: true,
		})
	}
	return issues
}

// catalogBooleanParamIssues warns when a takes_value: false catalog param is
// enabled. For those params the emitted flag comes verbatim from the global
// catalog rather than from the operator, so a hand-edited config can declare
// a dangerous flag and have any truthy param_values entry inject it. The
// global config is the trusted location, so this is a visibility warning,
// not a refusal: a launch shows exactly which catalog-declared flags fire.
func catalogBooleanParamIssues(cfg LaunchConfig) []Issue {
	if cfg.ContinueSession {
		return nil
	}
	issues := make([]Issue, 0)
	for _, param := range cfg.Agent.Params {
		if param.TakesValue {
			continue
		}
		value, ok := cfg.ParamValues[param.Name]
		if !ok || !flagEnabled(value) {
			continue
		}
		issues = append(issues, Issue{
			Code: "catalog-flag-param",
			Message: fmt.Sprintf(
				"param %q injects the catalog-declared flag %q; review the global catalog if this is unexpected",
				param.Name, param.Flag),
			Warning: true,
		})
	}
	return issues
}

// undeclaredParamIssues reports param_values whose names are not declared in
// the resolved agent's params block, in sorted order for stable output.
func undeclaredParamIssues(cfg LaunchConfig) []Issue {
	// A continue session has no harness to declare params; continueIssues
	// reports the drop as a warning instead of failing on every name.
	if cfg.ContinueSession {
		return nil
	}
	declared := make(map[string]bool, len(cfg.Agent.Params))
	for _, param := range cfg.Agent.Params {
		declared[param.Name] = true
	}
	undeclared := make([]string, 0, len(cfg.ParamValues))
	for name := range cfg.ParamValues {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(undeclared)
	issues := make([]Issue, 0, len(undeclared))
	for _, name := range undeclared {
		issues = append(issues, Issue{Code: "param-not-declared", Message: fmt.Sprintf("param %q is not declared by agent %q and will be ignored", name, cfg.Agent.Command)})
	}
	return issues
}
