// Package launcher builds and validates the argv used to start an AI agent.
package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// Environment returns the inherited environment with the configured ai-memory
// server URL and auth token applied to child processes. ai-memory reads these
// variables for its runtime wrapper and generated integrations. The token is
// a bearer secret: it is passed through the environment only and must never
// be written to logs.
func Environment(cfg LaunchConfig) []string {
	env := append([]string(nil), os.Environ()...)
	if cfg.UseMemory {
		env = upsertEnv(env, "AI_MEMORY_SERVER_URL", strings.TrimSpace(cfg.MemoryServerURL))
		env = upsertEnv(env, "AI_MEMORY_AUTH_TOKEN", strings.TrimSpace(cfg.MemoryAuthToken))
	}
	return env
}

// upsertEnv replaces or appends a KEY=value entry when value is non-empty,
// returning env unchanged otherwise.
func upsertEnv(env []string, key, value string) []string {
	if value == "" {
		return env
	}
	prefix := key + "="
	filtered := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
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
	ExtraArgs       []string
	ParamValues     map[string]string
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
		command = append(command, "ai-memory", "run")
		command = appendMemoryScope(command, cfg)
		if cfg.NewWorkstream != "" {
			command = append(command, "--new", cfg.NewWorkstream)
		} else if cfg.Workstream != "" {
			command = append(command, "--workstream", cfg.Workstream)
		}
		command = append(command, cfg.Agent.Command)
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

// buildContinue composes "ai-memory run" without a harness, which auto-resumes
// the most recent session of the checkout, jail-wrapped when the jail is on.
func buildContinue(cfg LaunchConfig) ([]string, error) {
	if !cfg.UseMemory {
		return nil, errors.New("continuing a session requires ai-memory")
	}
	command := make([]string, 0, 8)
	if cfg.UseJail {
		command = appendJailArgs(command, cfg)
	}
	command = append(command, "ai-memory", "run")
	return appendMemoryScope(command, cfg), nil
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
	command = appendJailFlags(command, cfg.JailFlags)
	if cfg.Permissions["ssh"] {
		command = append(command, "--ssh")
	}
	if cfg.Permissions["gh"] {
		home := cfg.HomeDir
		if home == "" {
			home = os.Getenv("HOME")
		}
		command = append(command, "--rw-map", filepathOrEmpty(home, ".config/gh"))
	}
	if cfg.Permissions["docker"] {
		command = append(command, "--docker")
	}
	if cfg.Permissions["gpu"] {
		command = append(command, "--gpu")
	}
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

// jailToggle declares one tri-state ai-jail capability flag. Default-on
// toggles emit only the --no- form (when explicitly disabled); default-off
// toggles emit both forms.
type jailToggle struct {
	name      string
	value     *bool
	defaultOn bool
}

// jailToggles lists the ai-jail v1.15 capability flags in their stable
// emission order so the mapping stays declarative instead of a chain of ifs.
func jailToggles(flags config.JailFlags) []jailToggle {
	return []jailToggle{
		{name: "lockdown", value: flags.Lockdown},
		{name: "private-home", value: flags.PrivateHome},
		{name: "tailscale", value: flags.Tailscale},
		{name: "gpu", value: flags.GPU, defaultOn: true},
		{name: "landlock", value: flags.Landlock, defaultOn: true},
		{name: "seccomp", value: flags.Seccomp, defaultOn: true},
		{name: "rlimits", value: flags.Rlimits, defaultOn: true},
		{name: "status-bar", value: flags.StatusBar, defaultOn: true},
	}
}

// appendJailFlags maps the declarative jail options to the exact ai-jail
// v1.15 CLI flags in a deterministic order: toggles, list flags, browser
// profile, and claude dir.
func appendJailFlags(command []string, flags config.JailFlags) []string {
	for _, toggle := range jailToggles(flags) {
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
	return appendFlagValue(command, "--claude-dir", flags.ClaudeDir)
}

// appendJailToggle emits the positive or negative form of one capability
// flag; an unset (nil) toggle keeps the ai-jail default and emits nothing.
func appendJailToggle(command []string, toggle jailToggle) []string {
	if toggle.value == nil {
		return command
	}
	if *toggle.value {
		if toggle.defaultOn {
			return command
		}
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

func filepathOrEmpty(home, suffix string) string {
	if home == "" {
		return suffix
	}
	return strings.TrimRight(home, "/") + "/" + suffix
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
	dropped := make([]string, 0, len(cfg.Permissions))
	dependent := config.JailDependentIDs(permissions)
	for id, enabled := range cfg.Permissions {
		if enabled && dependent[id] {
			cfg.Permissions[id] = false
			dropped = append(dropped, id)
		}
	}
	if !cfg.UseJail && len(dropped) == 0 {
		return cfg, nil
	}
	cfg.UseJail = false
	sort.Strings(dropped)
	message := "ai-jail is not supported on Windows; continuing without sandbox"
	if len(dropped) > 0 {
		message += "; disabled jail-only permissions: " + strings.Join(dropped, ", ")
	}
	return cfg, []Issue{{Code: "jail-unsupported-windows", Message: message, Warning: true}}
}

// Validator checks a LaunchConfig against the host (PATH lookups and mount
// existence) and reports every problem found instead of failing fast.
type Validator struct {
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	// GOOS overrides the platform used by platform-specific checks; empty
	// means the runtime platform.
	GOOS string
}

// NewValidator returns a Validator backed by the real PATH and filesystem.
func NewValidator() Validator {
	return Validator{LookPath: exec.LookPath, Stat: os.Stat}
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
	if !cfg.ContinueSession {
		agentPath := cfg.Agent.Command
		if cfg.Executable != "" {
			agentPath = cfg.Executable
		}
		if _, err := lookPath(agentPath); err != nil {
			issues = append(issues, Issue{Code: "agent-not-found", Message: fmt.Sprintf("%q is not available in PATH", cfg.Agent.Command)})
		}
	}
	if cfg.UseJail {
		switch {
		case onWindows:
			issues = append(issues, Issue{Code: "jail-unsupported-windows", Message: "ai-jail is not supported on Windows; the sandbox and jail-only options are ignored", Warning: true})
		default:
			if _, err := lookPath("ai-jail"); err != nil {
				issues = append(issues, Issue{Code: "jail-not-found", Message: "ai-jail is required when sandboxing is enabled"})
			}
		}
	} else if !cfg.JailFlags.IsZero() || cfg.JailExec {
		issues = append(issues, Issue{Code: "jail-options-without-jail", Message: "jail options are set but the jail is disabled; they will be ignored", Warning: true})
	}
	if cfg.UseMemory {
		if _, err := lookPath("ai-memory"); err != nil {
			issues = append(issues, Issue{Code: "memory-not-found", Message: "ai-memory is required when memory integration is enabled"})
		}
	}
	for _, mount := range cfg.Mounts {
		if strings.TrimSpace(mount.Path) == "" {
			continue
		}
		if _, err := stat(mount.Path); err != nil {
			issues = append(issues, Issue{Code: "mount-not-found", Message: fmt.Sprintf("mount path %q does not exist", mount.Path)})
		}
	}
	if cfg.Permissions["ssh"] || cfg.Permissions["gh"] || cfg.Permissions["docker"] || cfg.Permissions["gpu"] {
		if !cfg.UseJail {
			issue := Issue{Code: "permission-without-jail", Message: "ssh, gh, docker, and gpu permissions require ai-jail"}
			if onWindows {
				issue.Message = "ssh, gh, docker, and gpu permissions require ai-jail, which is unavailable on Windows; they will be ignored"
				issue.Warning = true
			}
			issues = append(issues, issue)
		}
	}
	if cfg.Permissions["gpu"] && !cfg.Permissions["docker"] {
		issues = append(issues, Issue{Code: "gpu-without-docker", Message: "gpu permission requires docker"})
	}
	issues = append(issues, undeclaredParamIssues(cfg)...)
	return issues
}

// undeclaredParamIssues reports param_values whose names are not declared in
// the resolved agent's params block, in sorted order for stable output.
func undeclaredParamIssues(cfg LaunchConfig) []Issue {
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
