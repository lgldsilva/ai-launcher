package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/installer"
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
	// Home overrides the home directory used to locate the global ai-jail
	// config; nil skips the symlinked-config checks, as a nil Getwd does.
	Home func() (string, error)
	// Root reports whether the process is uid 0. Nil uses os.Geteuid.
	// A root host skips sudo in the bwrap-not-found suggestion, as does a
	// host with no sudo binary. Tests set Root so that text does not depend
	// on the uid running the suite.
	Root func() bool
	// BwrapProbe stats bwrap candidates for ai-jail's ownership rule. Nil
	// counts any bwrap on PATH and any BWRAP_BIN as usable, so a Validator
	// literal in a test never reads the host's /usr/bin/bwrap.
	BwrapProbe installer.BwrapProbe
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
	return Validator{LookPath: exec.LookPath, Stat: os.Stat, Getwd: os.Getwd, Home: os.UserHomeDir, BwrapProbe: installer.StatBwrap}
}

// processIsRoot reports uid 0. A set Root wins, so tests can force either
// answer; otherwise the real process uid is used.
func (v Validator) processIsRoot() bool {
	if v.Root != nil {
		return v.Root()
	}
	return os.Geteuid() == 0
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
	issues = append(issues, agentIssues(cfg, lookPath)...)
	if cfg.UseDocker {
		issues = append(issues, dockerIssues(cfg, lookPath)...)
	} else {
		issues = append(issues, jailIssues(cfg, lookPath, installer.BwrapHost{
			GOOS:     goos,
			BwrapBin: cfg.BwrapBin,
			Root:     v.processIsRoot(),
			LookPath: lookPath,
			Probe:    v.BwrapProbe,
		})...)
	}
	issues = append(issues, allowTCPPortIssues(cfg)...)
	issues = append(issues, allowHostIssues(cfg)...)
	issues = append(issues, agentStateIssues(cfg)...)
	issues = append(issues, jailToggleFloorIssues(cfg)...)
	issues = append(issues, forwardPortIssues(cfg)...)
	issues = append(issues, memoryRunFlagIssues(cfg, stat)...)
	issues = append(issues, lockdownNetworkIssues(cfg)...)
	issues = append(issues, secretsIssues(cfg)...)
	issues = append(issues, envFromFileIssues(cfg, stat)...)
	issues = append(issues, containerNetworkInternalIssues(cfg)...)
	issues = append(issues, containerNetworkAllowedDomainsIssues(cfg)...)
	// Getwd is optional: unit tests leave it nil; NewValidator sets os.Getwd.
	if v.Getwd != nil {
		// Advisory only — ai-jail works on macOS; the friction is ai-memory
		// canonicalizing a /Volumes cwd *inside* the sandbox.
		issues = append(issues, jailMemoryVolumeIssues(cfg, v.Getwd, goos)...)
	}
	if !cfg.UseDocker {
		issues = append(issues, jailConfigSymlinkIssues(cfg, v.Getwd, v.Home)...)
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
// Below the floor is fatal, for either of two reasons depending on how far
// below. An ai-jail older than the argv dialect rejects flags it does not know
// with "unknown option", raised inside the wrapper where an operator reads it
// as the launcher being broken. An ai-jail that parses the argv fine but
// mishandles it — every macOS build below 1.20.1 could exec a binary it never
// vetted — fails silently instead, which is worse. Failing here names the
// actual problem in both cases.
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
				"ai-jail %s is older than the required %s; see CHANGELOG.md for what that floor covers — upgrade ai-jail, or launch with --no-jail",
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
		if minVer := config.MemoryHarnessMinVersion(harness); minVer != "" {
			version := strings.TrimSpace(cfg.MemoryVersion)
			if version != "" && compareVersions(version, minVer) < 0 {
				return []Issue{{
					Code: "memory-harness-version",
					Message: fmt.Sprintf(
						"ai-memory %s does not accept harness %q (added in %s); upgrade ai-memory, or relaunch with --no-memory",
						version, harness, minVer),
				}}
			}
		}
		return nil
	}
	return []Issue{{
		Code: "memory-harness-unsupported",
		Message: fmt.Sprintf(
			"ai-memory run does not accept harness %q (accepted: %s); relaunch with --no-memory, or declare memory.run_harness in the catalog",
			harness, strings.Join(config.MemoryRunHarnesses(), ", ")),
	}}
}

// bubblewrapIssue reports a Linux jail with no bwrap that ai-jail would run.
// A bwrap on PATH or in BWRAP_BIN counts only when ai-jail's ownership rule
// accepts it. The suggested command is the first package manager on PATH.
// --install-system-deps is only suggested when the plan has a command the
// launcher can run; otherwise the flag would print the same hint again.
func bubblewrapIssue(host installer.BwrapHost) (Issue, bool) {
	plan := installer.ResolveBubblewrap(host)
	if !plan.Needed {
		return Issue{}, false
	}
	message := "ai-jail on Linux needs bwrap from the bubblewrap package"
	if plan.Untrusted != "" {
		message = installer.UntrustedBwrapReason(plan.Untrusted) + ". Install the distro bubblewrap package"
	}
	if plan.Argv != nil {
		message += ". Run `ai-launcher --install --agent ai-jail --install-system-deps`, or: " + plan.Text
	} else {
		message += ": " + plan.Text
	}
	message += ". Ubuntu 24.04+ and Debian 13+ may also need the AppArmor exception in the ai-jail README"
	return Issue{Code: "bwrap-not-found", Message: message}, true
}

// jailIssues checks the ai-jail dependency, degrading to warnings where the
// jail cannot apply (Windows) or does not apply (jail disabled).
func jailIssues(cfg LaunchConfig, lookPath func(string) (string, error), host installer.BwrapHost) []Issue {
	if cfg.UseJail {
		if host.GOOS == config.PlatformWindows {
			return []Issue{{Code: "jail-unsupported-windows", Message: "ai-jail is not supported on Windows; the sandbox and jail-only options are ignored", Warning: true}}
		}
		if _, err := lookPath(config.AIJailCommand); err != nil {
			return []Issue{{Code: "jail-not-found", Message: "ai-jail is required when sandboxing is enabled"}}
		}
		if issue, ok := bubblewrapIssue(host); ok {
			return []Issue{issue}
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
// allowHostIssues refuses an allow list the installed ai-jail cannot apply,
// and a list that contradicts an explicit unrestricted network. It covers both
// lists that select filtered egress: allow_hosts (ai-jail 2.0) and
// allow_private_hosts (ai-jail 2.11).
func allowHostIssues(cfg LaunchConfig) []Issue {
	if len(cfg.JailFlags.AllowHosts) == 0 && len(cfg.JailFlags.AllowPrivateHosts) == 0 {
		return nil
	}
	hosts, err := config.NormalizeAllowHosts(cfg.JailFlags.AllowHosts)
	if err != nil {
		return []Issue{{
			Code:    "allow-host-invalid",
			Message: err.Error(),
		}}
	}
	privateHosts, err := config.NormalizeAllowHosts(cfg.JailFlags.AllowPrivateHosts)
	if err != nil {
		return []Issue{{
			Code:    "allow-private-host-invalid",
			Message: strings.Replace(err.Error(), "allow host", "allow private host", 1),
		}}
	}
	if len(hosts) == 0 && len(privateHosts) == 0 {
		return nil
	}
	if !cfg.UseJail {
		// --no-jail already reports jail-options-without-jail for any set
		// flag. Docker replaces the jail and never emits --allow-host, so
		// the list would look like a network lock that is not applied.
		if cfg.UseDocker {
			return []Issue{{
				Code:    "allow-host-without-jail",
				Message: "jail_flags.allow_hosts and allow_private_hosts have no effect on the container backend; the container keeps its own network",
				Warning: true,
			}}
		}
		return nil
	}
	if explicitNetworkOn(cfg) {
		return []Issue{{
			Code: "allow-host-conflicts-with-network",
			Message: "jail_flags.allow_hosts and allow_private_hosts are filtered egress and cannot be combined with jail_flags.network: true; " +
				"ai-jail rejects --allow-host and --allow-private-host together with --network",
		}}
	}
	version := strings.TrimSpace(cfg.JailVersion)
	seen := version
	if seen == "" {
		seen = "unknown"
	}
	var issues []Issue
	if len(hosts) > 0 && !jailSupportsAllowHost(version) {
		issues = append(issues, Issue{
			Code: "allow-host-requires-ai-jail-2",
			Message: fmt.Sprintf(
				"jail_flags.allow_hosts needs ai-jail >= %s (installed: %s); upgrade ai-jail, or remove allow_hosts",
				config.MinAllowHostAIJailVersion, seen),
		})
	}
	if len(privateHosts) > 0 && !jailSupportsFlag(version, config.MinAllowPrivateHostAIJailVersion) {
		issues = append(issues, Issue{
			Code: "allow-private-host-requires-ai-jail-2-11",
			Message: fmt.Sprintf(
				"jail_flags.allow_private_hosts needs ai-jail >= %s (installed: %s); upgrade ai-jail, or remove allow_private_hosts",
				config.MinAllowPrivateHostAIJailVersion, seen),
		})
	}
	if len(issues) > 0 {
		return issues
	}
	return allowHostMemoryIssues(cfg, append(append([]string(nil), hosts...), privateHosts...))
}

// allowHostMemoryIssues refuses filtered egress that cannot reach ai-memory.
// The proxy ai-jail 2.2 installs is CONNECT-only and clears NO_PROXY, so a
// plain-HTTP memory server — including the default 127.0.0.1:49374 — gets
// 405 on POST /workstream/runs. An https server works only when its host is
// on the allow list.
func allowHostMemoryIssues(cfg LaunchConfig, hosts []string) []Issue {
	if !cfg.UseMemory {
		return nil
	}
	scheme, host := memoryServerEndpoint(cfg.MemoryServerURL)
	if scheme != "https" || host == "" {
		called := strings.TrimSpace(cfg.MemoryServerURL)
		if called == "" {
			called = defaultAIMemoryServerURL + " (ai-memory's default)"
		}
		return []Issue{{
			Code: "allow-host-memory-needs-https",
			Message: "jail_flags.allow_hosts installs a CONNECT-only proxy that answers plain HTTP with 405, " +
				"so ai-memory cannot reach " + called + "; set memory_server_url to an https origin and add that host to allow_hosts, or drop the allow lists",
		}}
	}
	if !allowHostCovers(hosts, host) {
		return []Issue{{
			Code: "allow-host-omits-memory-server",
			Message: fmt.Sprintf(
				"memory server host %s is not covered by jail_flags.allow_hosts, so filtered egress would block ai-memory; add the host or drop allow_hosts",
				host),
		}}
	}
	return nil
}

// agentStateIssues refuses a configured agent_state the installed ai-jail
// cannot apply. The gate mirrors allow-host-requires-ai-jail-2: an unknown
// version is refused too, because ai-jail below 2.6.0 rejects --agent-state
// outright and the refusal must surface in pre-flight instead of as an opaque
// clap error inside the jail (invariant 6b). An unset agent_state emits
// nothing below 2.6.0 and is always valid.
func agentStateIssues(cfg LaunchConfig) []Issue {
	if cfg.JailFlags.AgentState == nil {
		return nil
	}
	if !cfg.UseJail {
		// --no-jail already reports jail-options-without-jail for any set
		// jail_flags key; repeating it here would double-report.
		return nil
	}
	if jailSupportsAgentState(cfg.JailVersion) {
		return nil
	}
	seen := strings.TrimSpace(cfg.JailVersion)
	if seen == "" {
		seen = "unknown"
	}
	return []Issue{{
		Code: "agent-state-requires-ai-jail-2-6",
		Message: fmt.Sprintf(
			"jail_flags.agent_state needs ai-jail >= %s (installed: %s); upgrade ai-jail, or remove agent_state",
			config.MinAgentStateAIJailVersion, seen),
	}}
}

// jailToggleFloorIssues refuses configured capability toggles the installed
// ai-jail cannot apply, one issue per floor batch so the code names the
// release that introduced the flags. An unreadable version is refused too —
// the probe staying silent must not turn into "new enough".
func jailToggleFloorIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseJail {
		return nil
	}
	byFloor := make(map[string][]string)
	for _, toggle := range jailToggles(cfg.JailFlags) {
		if toggle.floor == "" || toggle.value == nil {
			continue
		}
		if !jailSupportsFlag(cfg.JailVersion, toggle.floor) {
			byFloor[toggle.floor] = append(byFloor[toggle.floor], toggle.name)
		}
	}
	if len(byFloor) == 0 {
		return nil
	}
	seen := strings.TrimSpace(cfg.JailVersion)
	if seen == "" {
		seen = "unknown"
	}
	floors := make([]string, 0, len(byFloor))
	for floor := range byFloor {
		floors = append(floors, floor)
	}
	sort.Strings(floors)
	issues := make([]Issue, 0, len(floors))
	for _, floor := range floors {
		names := byFloor[floor]
		sort.Strings(names)
		issues = append(issues, Issue{
			Code: toggleFloorCode(floor),
			Message: fmt.Sprintf(
				"jail_flags %s need ai-jail >= %s (installed: %s); upgrade ai-jail, or remove them",
				strings.Join(names, ", "), floor, seen),
		})
	}
	return issues
}

// toggleFloorCode names the refusal for one floor batch.
func toggleFloorCode(floor string) string {
	switch floor {
	case config.MinAuditLogAIJailVersion:
		return "jail-toggles-require-ai-jail-2"
	case config.MinTogglesAIJailVersion:
		return "jail-toggles-require-ai-jail-2-5"
	case config.MinModernTogglesAIJailVersion:
		return "jail-toggles-require-ai-jail-2-9"
	case config.MinMacOSChromiumAIJailVersion:
		return "jail-toggles-require-ai-jail-2-12"
	}
	return "jail-toggles-require-newer-ai-jail"
}

// secretsIssues refuses --secret entries the installed ai-jail cannot apply
// or that cannot work: the substitution happens in the filtered-egress
// proxy, so secrets without allow_hosts are inert, and KEY=host is the whole
// form.
func secretsIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseJail || len(cfg.JailFlags.Secrets) == 0 {
		return nil
	}
	if !jailSupportsFlag(cfg.JailVersion, config.MinSecretAIJailVersion) {
		seen := strings.TrimSpace(cfg.JailVersion)
		if seen == "" {
			seen = "unknown"
		}
		return []Issue{{
			Code: "secrets-require-ai-jail-2-2",
			Message: fmt.Sprintf(
				"jail_flags.secrets needs ai-jail >= %s (installed: %s); upgrade ai-jail, or remove secrets",
				config.MinSecretAIJailVersion, seen),
		}}
	}
	if !filteredEgress(cfg) {
		return []Issue{{
			Code:    "secrets-require-filtered-egress",
			Message: "jail_flags.secrets substitutes values in the filtered-egress proxy, so it needs jail_flags.allow_hosts or allow_private_hosts; add the host to a list or remove secrets",
		}}
	}
	var invalid []string
	for _, entry := range cfg.JailFlags.Secrets {
		key, host, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(host) == "" || strings.ContainsAny(entry, " \t") {
			invalid = append(invalid, entry)
		}
	}
	if len(invalid) > 0 {
		return []Issue{{
			Code: "secret-invalid",
			Message: fmt.Sprintf(
				"jail_flags.secrets entries %s are not KEY=host; the child sees KEY's placeholder and the proxy substitutes it only for requests to host",
				strings.Join(invalid, ", ")),
		}}
	}
	return nil
}

// envFromFileIssues refuses --env-from-file entries the installed ai-jail
// cannot apply or cannot read. ai-jail enforces the user-owned and
// outside-the-project rules itself at launch; the mode check (0600 or
// stricter) and existence are the parts pre-flight can see, and either
// failing would abort inside the wrapper.
func envFromFileIssues(cfg LaunchConfig, stat func(string) (os.FileInfo, error)) []Issue {
	if !cfg.UseJail || len(cfg.JailFlags.EnvFromFiles) == 0 {
		return nil
	}
	if !jailSupportsFlag(cfg.JailVersion, config.MinEnvFromFileAIJailVersion) {
		seen := strings.TrimSpace(cfg.JailVersion)
		if seen == "" {
			seen = "unknown"
		}
		return []Issue{{
			Code: "env-from-files-require-ai-jail-2",
			Message: fmt.Sprintf(
				"jail_flags.env_from_files needs ai-jail >= %s (installed: %s); upgrade ai-jail, or remove env_from_files",
				config.MinEnvFromFileAIJailVersion, seen),
		}}
	}
	if stat == nil {
		return nil
	}
	var missing, loose []string
	for _, path := range cfg.JailFlags.EnvFromFiles {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		info, err := stat(path)
		if err != nil {
			missing = append(missing, path)
			continue
		}
		if info.Mode().Perm()&0o077 != 0 {
			loose = append(loose, fmt.Sprintf("%s (mode %04o)", path, info.Mode().Perm()))
		}
	}
	if len(missing) > 0 {
		return []Issue{{
			Code: "env-from-file-not-found",
			Message: fmt.Sprintf(
				"jail_flags.env_from_files entries %s do not exist; ai-jail reads them (user-owned, mode 0600, outside the project) before the launch",
				strings.Join(missing, ", ")),
		}}
	}
	if len(loose) > 0 {
		return []Issue{{
			Code: "env-from-file-not-locked-down",
			Message: fmt.Sprintf(
				"jail_flags.env_from_files entries %s are not mode 0600; ai-jail refuses group- or world-readable env files",
				strings.Join(loose, ", ")),
		}}
	}
	return nil
}

// forwardPortIssues refuses --forward-port configurations ai-jail would abort
// on at launch, mirroring the upstream rules: the flag exists only from
// 2.9.0, unrestricted network makes the relay pointless (the host loopback is
// already reachable), ports live in 1024..65535, and at most 256 relays run
// per launch.
func forwardPortIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseJail || len(cfg.JailFlags.ForwardPorts) == 0 {
		return nil
	}
	if !jailSupportsFlag(cfg.JailVersion, config.MinForwardPortAIJailVersion) {
		seen := strings.TrimSpace(cfg.JailVersion)
		if seen == "" {
			seen = "unknown"
		}
		return []Issue{{
			Code: "forward-ports-require-ai-jail-2-9",
			Message: fmt.Sprintf(
				"jail_flags.forward_ports needs ai-jail >= %s (installed: %s); upgrade ai-jail, or remove forward_ports",
				config.MinForwardPortAIJailVersion, seen),
		}}
	}
	if unrestrictedNetwork(cfg) {
		return []Issue{{
			Code: "forward-ports-conflict-with-network",
			Message: "jail_flags.forward_ports is a filtered-egress relay and cannot be combined with unrestricted network; " +
				"ai-jail rejects --forward-port together with --network",
		}}
	}
	var invalid []string
	for _, port := range cfg.JailFlags.ForwardPorts {
		if port < 1024 || port > 65535 {
			invalid = append(invalid, strconv.Itoa(port))
		}
	}
	if len(invalid) > 0 {
		return []Issue{{
			Code: "forward-port-invalid",
			Message: fmt.Sprintf(
				"jail_flags.forward_ports entries %s fall outside 1024-65535; ai-jail refuses ports below 1024 and there is no port above 65535",
				strings.Join(invalid, ", ")),
		}}
	}
	if len(cfg.JailFlags.ForwardPorts) > forwardPortsMax {
		return []Issue{{
			Code:    "forward-ports-too-many",
			Message: fmt.Sprintf("jail_flags.forward_ports has %d entries; ai-jail runs at most %d relays per launch", len(cfg.JailFlags.ForwardPorts), forwardPortsMax),
		}}
	}
	return nil
}

// forwardPortsMax mirrors ai-jail's relay cap for --forward-port.
const forwardPortsMax = 256

// memoryRunFlagIssues refuses ai-memory run options the installed pair
// cannot apply. --profile and --env-file precede the harness, so a jailed
// launch needs both the ai-memory floor for the flag and an ai-jail new
// enough to forward unknown pre-harness flags instead of bailing on them
// (the 1.x managed-harness parser falls back to the outer command and the
// agent silently loses its state mounts). memory_jail is a host-launch
// option: ai-memory ignores --jail inside ai-jail, and a container image has
// no ai-jail to run — configured anyway, that is a warning, not a refusal,
// because the launch itself is valid.
func memoryRunFlagIssues(cfg LaunchConfig, stat func(string) (os.FileInfo, error)) []Issue {
	if !cfg.UseMemory {
		return nil
	}
	var issues []Issue
	issues = append(issues, memoryProfileIssues(cfg)...)
	issues = append(issues, memoryEnvFileIssues(cfg, stat)...)
	issues = append(issues, memoryJailIssues(cfg)...)
	issues = append(issues, forceUnlockIssues(cfg)...)
	issues = append(issues, memoryExitPromptIssues(cfg)...)
	return issues
}

// memoryExitPromptIssues refuses memory_exit_prompt: false below ai-memory
// 3.3.0. Nil and explicit true emit nothing, so there is nothing to refuse.
// The flag rides after the harness, so no ai-jail pre-harness floor applies.
func memoryExitPromptIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseMemory || cfg.MemoryExitPrompt == nil || *cfg.MemoryExitPrompt {
		return nil
	}
	if MemorySupportsNoExitPrompt(cfg.MemoryVersion) {
		return nil
	}
	return []Issue{versionFloorIssue(
		"memory-exit-prompt-requires-ai-memory-3-3",
		"memory_exit_prompt",
		cfg.MemoryVersion,
		config.MinNoExitPromptAIMemoryVersion,
	)}
}

// forceUnlockIssues refuses force_unlock below its ai-memory floor. The flag
// rides after the harness, so no ai-jail pre-harness floor applies. Without
// memory the flag is inert (Build emits nothing), so there is nothing to refuse.
func forceUnlockIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseMemory || !cfg.ForceUnlock {
		return nil
	}
	if MemorySupportsForceUnlock(cfg.MemoryVersion) {
		return nil
	}
	return []Issue{versionFloorIssue("memory-force-unlock-requires-ai-memory-2-6", "force_unlock", cfg.MemoryVersion, config.MinForceUnlockAIMemoryVersion)}
}

// memoryProfileIssues refuses memory_profile below its ai-memory floor, and
// below the ai-jail that can forward a pre-harness flag.
func memoryProfileIssues(cfg LaunchConfig) []Issue {
	if strings.TrimSpace(cfg.MemoryProfile) == "" {
		return nil
	}
	if !MemorySupportsProfile(cfg.MemoryVersion) {
		return []Issue{versionFloorIssue("memory-profile-requires-ai-memory-2-6", "memory_profile", cfg.MemoryVersion, config.MinProfileAIMemoryVersion)}
	}
	if cfg.UseJail && !jailSupportsFlag(cfg.JailVersion, config.MinPreHarnessAIJailVersion) {
		return []Issue{preHarnessJailIssue("memory_profile")}
	}
	return nil
}

// memoryEnvFileIssues refuses memory_env_file below its floors and when the
// path does not exist.
func memoryEnvFileIssues(cfg LaunchConfig, stat func(string) (os.FileInfo, error)) []Issue {
	envFile := strings.TrimSpace(cfg.MemoryEnvFile)
	if envFile == "" {
		return nil
	}
	if !MemorySupportsEnvFile(cfg.MemoryVersion) {
		return []Issue{versionFloorIssue("memory-env-file-requires-ai-memory-2-5", "memory_env_file", cfg.MemoryVersion, config.MinEnvFileAIMemoryVersion)}
	}
	var issues []Issue
	if cfg.UseJail && !jailSupportsFlag(cfg.JailVersion, config.MinPreHarnessAIJailVersion) {
		issues = append(issues, preHarnessJailIssue("memory_env_file"))
	}
	if stat != nil {
		if _, err := stat(envFile); err != nil {
			issues = append(issues, Issue{
				Code:    "memory-env-file-not-found",
				Message: fmt.Sprintf("memory_env_file %q does not exist; ai-memory reads it before spawning the harness", envFile),
			})
		}
	}
	return issues
}

// memoryJailIssues refuses memory_jail below its ai-memory floor and warns
// when the launcher's own jail or the container backend would ignore it.
func memoryJailIssues(cfg LaunchConfig) []Issue {
	if strings.TrimSpace(cfg.MemoryJail) == "" {
		return nil
	}
	var issues []Issue
	if !MemorySupportsJail(cfg.MemoryVersion) {
		issues = append(issues, versionFloorIssue("memory-jail-requires-ai-memory-2-5-2", "memory_jail", cfg.MemoryVersion, config.MinMemoryJailAIMemoryVersion))
	}
	switch {
	case cfg.UseJail:
		issues = append(issues, Issue{
			Code:    "memory-jail-with-launcher-jail",
			Warning: true,
			Message: "memory_jail is ignored here: this launch is already jailed by the launcher, and ai-memory skips --jail inside ai-jail; remove memory_jail to stop the reminder",
		})
	case cfg.UseDocker:
		issues = append(issues, Issue{
			Code:    "memory-jail-in-container",
			Warning: true,
			Message: "memory_jail cannot apply in the container backend: the image has no ai-jail, and ai-memory refuses to run unjailed when --jail is set; remove memory_jail for container launches",
		})
	}
	return issues
}

// versionFloorIssue renders one "configured below the upstream floor"
// refusal with the unreadable-version rule applied.
func versionFloorIssue(code, key, version, floor string) Issue {
	seen := strings.TrimSpace(version)
	if seen == "" {
		seen = "unknown"
	}
	return Issue{
		Code: code,
		Message: fmt.Sprintf(
			"%s needs ai-memory >= %s (installed: %s); upgrade ai-memory, or remove it",
			key, floor, seen),
	}
}

// preHarnessJailIssue names the ai-jail side of a pre-harness wrapper flag:
// the option itself parses on the installed ai-memory, but the jail's
// managed-harness parser would bail on the unknown dash token before the
// harness and silently drop the agent-state mounts.
func preHarnessJailIssue(key string) Issue {
	return Issue{
		Code: "memory-run-flag-requires-ai-jail-2-9",
		Message: fmt.Sprintf(
			"%s precedes the harness, and this launch is jailed: ai-jail >= 2.9.0 is required to forward it (older parsers fall back to the outer command and the agent loses its state mounts); upgrade ai-jail, disable the jail for this launch, or remove %s",
			key, key),
	}
}

// unrestrictedNetwork reports whether this launch would carry --network:
// an explicit jail_flags network on (filtered egress replaces it), or the
// network permission without filtered egress.
func unrestrictedNetwork(cfg LaunchConfig) bool {
	if filteredEgress(cfg) {
		return false
	}
	return explicitNetworkOn(cfg) || cfg.Permissions[config.PermissionNetwork]
}

// lockdownNetworkIssues warns about lockdown combined with unrestricted
// network on ai-jail 2.6.2+: that release makes --lockdown always force
// --unshare-net, so the launch is offline no matter what --network says.
// Before it the combination kept the network — the same argv, opposite
// outcome — which is why this is surfaced instead of left to ai-jail. A
// warning, not a refusal: ai-jail resolves the contradiction itself, and the
// operator only needs to know which side won.
func lockdownNetworkIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseJail {
		return nil
	}
	if cfg.JailFlags.Lockdown == nil || !*cfg.JailFlags.Lockdown || !unrestrictedNetwork(cfg) {
		return nil
	}
	if !jailSupportsFlag(cfg.JailVersion, config.MinLockdownUnshareAIJailVersion) {
		return nil
	}
	return []Issue{{
		Code:    "lockdown-forces-offline",
		Warning: true,
		Message: fmt.Sprintf(
			"jail_flags lockdown: true with unrestricted network is offline on ai-jail >= %s: --lockdown forces --unshare-net there; drop network or lockdown to say what you mean",
			config.MinLockdownUnshareAIJailVersion),
	}}
}

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

// containerNetworkInternalIssues reports the two ways an internal Compose
// network surprises an operator. They differ in severity:
//
//   - With at least one service, the network takes effect and blocks ALL
//     outbound traffic including the agent's own LLM API calls. That is a
//     legitimate operator choice, so it stays a warning — the operator may
//     well intend total egress blocking.
//   - With no service selected, the toggle is a silent no-op: the plain
//     docker run backend has no Compose network to mark internal, so the
//     operator believes egress is blocked while it remains fully open. That
//     is a false sense of security, so it is fatal — the launch is refused
//     rather than allowed to proceed under an ineffective setting.
func containerNetworkInternalIssues(cfg LaunchConfig) []Issue {
	if !cfg.UseDocker || !cfg.Docker.NetworkInternal {
		return nil
	}
	if len(cfg.Services) == 0 {
		return []Issue{{
			Code:    "internal-network-requires-compose",
			Message: "container_network_internal only takes effect on the Compose path (at least one service selected); the plain docker run backend ignores it and network access remains open — add a --service or remove container_network_internal",
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

// containerNetworkAllowedDomainsIssues rejects a domain allowlist that cannot
// take effect. addEgressProxy only fires when both container_network_internal
// is on and a domain list is present, so an allowlist without the internal
// network (or without the docker backend) is a silent no-op: egress stays
// fully open, which is the opposite of the safety boundary the operator is
// configuring the list to enforce. That false sense of security is fatal —
// the launch is refused rather than allowed to proceed with an inert list.
//
// The docker-only guard mirrors containerNetworkInternalIssues: the allowlist
// is a docker-backend setting with no meaning under ai-jail, so a persisted
// list must NOT block a jail-mode launch (for example an operator who toggles
// the backend off temporarily). Without this guard the fatal below would
// refuse a launch the setting has no effect on.
func containerNetworkAllowedDomainsIssues(cfg LaunchConfig) []Issue {
	if len(cfg.ContainerNetworkAllowedDomains) == 0 {
		return nil
	}
	if !cfg.UseDocker {
		return nil
	}
	if cfg.Docker.NetworkInternal {
		return nil
	}
	return []Issue{{
		Code:    "container-network-allowed-domains-without-internal-network",
		Message: "container_network_allowed_domains is configured but container_network_internal is not on; the egress proxy is never injected and the domain list has no effect — network access remains fully open, so enable container_network_internal or remove the allowlist",
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
