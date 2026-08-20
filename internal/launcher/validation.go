package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
)

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
	if goos != config.PlatformWindows {
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
	onWindows := goos == config.PlatformWindows
	issues = append(issues, agentIssues(cfg, lookPath)...)
	if cfg.UseDocker {
		issues = append(issues, dockerIssues(cfg, lookPath)...)
	} else {
		issues = append(issues, jailIssues(cfg, lookPath, onWindows)...)
	}
	issues = append(issues, allowTCPPortIssues(cfg)...)
	issues = append(issues, containerNetworkInternalIssues(cfg)...)
	issues = append(issues, containerNetworkAllowedDomainsIssues(cfg)...)
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
	issues = append(issues, memoryScopePathIssues(cfg, stat)...)
	issues = append(issues, jailVersionIssues(cfg)...)
	return issues
}

// jailVersionIssues judges the ai-jail version ResolveHostBinaries detected.
// It reads cfg.JailVersion and execs nothing, so pre-flight stays hermetic.
//
// Below the floor is fatal. The launcher composes one dialect of the ai-jail
// flag surface, and an older build rejects flags it does not know with
// "unknown option" — raised inside the wrapper, where an operator reads it as
// the launcher being broken. Failing here names the actual problem.
//
// At or above the untested bound is a warning, not a refusal: a newer upstream
// may work perfectly, and blocking would strand everyone the day a release
// lands. `--doctor` carries the detail; this is the reminder at launch time.
//
// An empty version means the probe could not read one. That is not evidence of
// anything, so it is left alone.
func jailVersionIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseJail {
		return nil
	}
	version := strings.TrimSpace(cfg.JailVersion)
	if version == "" {
		return nil
	}
	if compareVersions(version, config.MinAIJailVersion) < 0 {
		return []Issue{{
			Code: "jail-version-too-old",
			Message: fmt.Sprintf(
				"ai-jail %s is older than the required %s; the launcher emits flags this build rejects — upgrade ai-jail, or launch with --no-jail",
				version, config.MinAIJailVersion),
		}}
	}
	if compareVersions(version, config.UntestedAIJailVersion) >= 0 {
		return []Issue{{
			Code: "jail-version-untested",
			Message: fmt.Sprintf(
				"ai-jail %s is newer than the validated range (>= %s, < %s); run `ai-launcher --doctor` for what changed",
				version, config.MinAIJailVersion, config.UntestedAIJailVersion),
			Warning: true,
		}}
	}
	return nil
}

// memoryScopePathIssues warns when an ai-memory scope looks like a filesystem
// path. `ai-memory run --workspace` takes a *name*, but the container backend
// used to borrow Workspace as its project directory and the autosave wrote the
// working directory back into options.workspace. A workspace saved by that
// path keeps launching — it just creates a phantom, path-shaped workspace in
// the memory database, splitting the project's history in two without ever
// failing. The launcher no longer writes it; this is how an operator finds the
// one already in their file.
func memoryScopePathIssues(cfg LaunchConfig, stat func(string) (os.FileInfo, error)) []Issue {
	if !cfg.UseMemory {
		return nil
	}
	var issues []Issue
	for _, scope := range []struct{ flag, value string }{
		{"workspace", cfg.Workspace},
		{"project", cfg.Project},
	} {
		value := strings.TrimSpace(scope.value)
		if value == "" || !filepath.IsAbs(value) {
			continue
		}
		if _, err := stat(value); err != nil {
			continue
		}
		issues = append(issues, Issue{
			Code: "memory-scope-looks-like-a-path",
			Message: fmt.Sprintf(
				"%s %q is an existing directory, but `ai-memory run --%s` takes a name; "+
					"an earlier docker launch could have saved the working directory here — "+
					"remove it from options.%s and use --project-dir for the container directory",
				scope.flag, value, scope.flag, scope.flag),
			Warning: true,
		})
	}
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
	// In docker mode the agent runs inside the image (installed by the build
	// from its recipe), so it does not need to exist on the host PATH — a
	// fresh machine with no local gemini still launches it in a container.
	if cfg.UseDocker {
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
		if _, err := lookPath(config.AIJailCommand); err != nil {
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

// dockerIssues checks the container backend prerequisites: the selected
// runtime CLI must be on PATH (daemon reachability happens at run time, in the
// executor, because it requires talking to the runtime). An empty image
// selection is a configuration error, not a warning: the backend was enabled
// but nobody said what to build.
func dockerIssues(cfg LaunchConfig, lookPath func(string) (string, error)) []Issue {
	runtime := container.RuntimeOrDefault(cfg.Docker.Runtime)
	command := runtime.Command()
	if _, err := lookPath(command); err != nil {
		return []Issue{{Code: command + "-not-found", Message: fmt.Sprintf("%s is required when the container backend is enabled", command)}}
	}
	if cfg.Docker.Selection.Validate() != nil {
		return []Issue{{Code: "docker-selection-invalid", Message: "docker backend is enabled but the image selection is invalid; choose stacks and agents"}}
	}
	if strings.TrimSpace(cfg.Docker.Selection.AgentExecutable()) == "" {
		return []Issue{{Code: "docker-no-agent", Message: "docker backend is enabled but no agent is selected for the image"}}
	}
	if err := container.ValidateProjectDir(EffectiveProjectDir(cfg)); err != nil {
		return []Issue{{
			Code:    "docker-project-dir-not-absolute",
			Message: fmt.Sprintf("%v; the container mounts it at the identical path and Docker reads a relative source as a named volume", err),
		}}
	}
	socketRequested := cfg.Docker.MountDockerSocket || cfg.Permissions[config.PermissionDocker]
	if socketRequested && strings.TrimSpace(cfg.Docker.DockerSocketPath) == "" && runtime.SocketPath() == "" {
		return []Issue{{
			Code:    "container-socket-unavailable",
			Message: fmt.Sprintf("%s has no default control socket to mount; set an explicit socket path if this runtime exposes one", runtime.Name()),
			Warning: true,
		}}
	}
	return nil
}

// allowTCPPortIssues refuses a launch that would carry --allow-tcp-port.
//
// This used to be a warning, and it used to be about lockdown: through
// ai-jail 1.17.x the flag was lockdown-only and silently ignored otherwise,
// so the operator believed ports were open when nothing was. From 1.18.0 the
// flag fails closed — ai-jail aborts the launch outright, with or without
// lockdown, because UDP cannot be constrained through that interface. Verified
// against 1.18.2:
//
//	✗ --allow-tcp-port is disabled because UDP cannot be isolated;
//	  use explicit --network for unrestricted network access
//
// Since the supported floor is 1.18.2, there is no version left where the
// flag does anything but stop the launch, so pre-flight says so first — with
// the launcher's own message rather than one raised inside the wrapper.
//
// The message names the project .ai-jail too: ai-jail merges its
// allow_tcp_ports with the CLI's, so the ports can come from a file the
// launcher does not control, and an operator who clears jail_flags and keeps
// failing needs to know where else to look.
func allowTCPPortIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseJail || len(cfg.JailFlags.AllowTCPPorts) == 0 {
		return nil
	}
	return []Issue{{
		Code: "allow-tcp-ports-unsupported",
		Message: "ai-jail refuses to launch with --allow-tcp-port (UDP cannot be constrained through it); " +
			"remove jail_flags.allow_tcp_ports — and check the project .ai-jail, whose ports are merged in — " +
			"and use the network permission for outbound access",
	}}
}

// containerNetworkInternalIssues warns about the two ways an internal
// Compose network surprises an operator: it blocks ALL outbound traffic
// including the agent's own LLM API calls, and it silently does nothing when
// no infrastructure service is selected (the plain docker run backend has no
// Compose network to mark internal — BuildCompose only runs when at least
// one service is chosen).
func containerNetworkInternalIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseDocker || !cfg.Docker.NetworkInternal {
		return nil
	}
	if len(cfg.Services) == 0 {
		return []Issue{{
			Code:    "internal-network-requires-compose",
			Message: "container_network_internal only takes effect on the Compose path (at least one service selected); the plain docker run backend ignores it and network access remains open",
			Warning: true,
		}}
	}
	if len(cfg.ContainerNetworkAllowedDomains) > 0 {
		return []Issue{{
			Code:    "internal-network-restricts-agent",
			Message: "the internal Compose network restricts outbound traffic to container_network_allowed_domains via an egress proxy; confirm every domain the agent's own API and every selected service need is included — a missing domain fails silently as a blocked connection, not a clear error",
			Warning: true,
		}}
	}
	return []Issue{{
		Code:    "internal-network-blocks-agent",
		Message: "the internal Compose network blocks ALL outbound traffic, including the agent's own LLM API calls; confirm the agent can reach its API another way (for example a host-side proxy) before relying on this",
		Warning: true,
	}}
}

// containerNetworkAllowedDomainsIssues warns when the domain allowlist is
// configured but the internal network isn't active (or Docker isn't the
// backend) — addEgressProxy never fires, so the allowlist is a silent no-op
// (open egress, not restricted egress) rather than the safety boundary the
// operator likely expects.
func containerNetworkAllowedDomainsIssues(cfg LaunchConfig) []Issue {
	if len(cfg.ContainerNetworkAllowedDomains) == 0 {
		return nil
	}
	if cfg.UseDocker && cfg.Docker.NetworkInternal {
		return nil
	}
	return []Issue{{
		Code:    "container-network-allowed-domains-without-internal-network",
		Message: "container_network_allowed_domains is configured but container_network_internal is not on; the egress proxy is never injected and the domain list has no effect — network access remains fully open",
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
// platform does not support. In docker mode the jail-requiring permissions map
// to container mounts instead (C3), so the jail requirement is lifted.
func permissionIssues(cfg LaunchConfig, goos string, catalog []config.Permission) []Issue {
	issues := make([]Issue, 0)
	if cfg.Permissions[config.PermissionSSH] || cfg.Permissions[config.PermissionGitHub] || cfg.Permissions[config.PermissionDocker] || cfg.Permissions[config.PermissionGPU] {
		if !cfg.UseJail && !cfg.UseDocker {
			issue := Issue{Code: "permission-without-jail", Message: "ssh, gh, docker, and gpu permissions require ai-jail"}
			if goos == config.PlatformWindows {
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
