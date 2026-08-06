package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/catalog"
	launchcmd "github.com/lgldsilva/ai-launcher/internal/cmd"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"github.com/lgldsilva/ai-launcher/internal/selfupdate"
	"github.com/lgldsilva/ai-launcher/internal/tui"
)

// Build metadata injected via -ldflags "-X main.version=..." at build time
// (Makefile and .goreleaser.yaml). These identify the BINARY release; they are
// unrelated to config.CurrentVersion, which versions the config file schema.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const (
	// flagNoJail is the CLI flag name for opting out of the sandbox; it is
	// looked up by name after parsing to tell "unset" apart from "false".
	flagNoJail = "no-jail"
	// permissionSystemdUser is the catalog permission id behind the
	// --systemd-user flag.
	permissionSystemdUser = config.PermissionSystemd
	// warningLabel prefixes non-fatal diagnostics on stderr.
	warningLabel = "warning:"
	valueFormat  = "%v"
	// aiMemoryCommand is the upstream memory CLI, resolved on PATH for the
	// read-only surfaces this binary forwards to it.
	aiMemoryCommand = config.AIMemoryCommand
	// memoryWorkstreamIDEnv is the variable ai-memory exports into a managed
	// child so it can address its own workstream. The launcher does not own it
	// (it is absent from ownedMemoryEnvKeys), so it survives into an agent
	// launched from here and is the default for --workstream-search.
	memoryWorkstreamIDEnv = "AI_MEMORY_WORKSTREAM_ID"
)

// workstreamSearchTimeout bounds the ai-memory query so a hung or unreachable
// server cannot wedge the terminal. It is a search, not a session.
const workstreamSearchTimeout = 30 * time.Second

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

func warnf(errOut io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(errOut, warningLabel+" "+format+"\n", args...)
}

// cliOptions holds every command-line flag value in one place so run() stays
// readable and the flag-to-config mapping can be applied as a unit.
type cliOptions struct {
	mounts, rwMounts               stringList
	params, stacks                 stringList
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
	memory, noMemory               bool
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
	flags.Var(&o.stacks, "stack", "toolchain stack for the docker image (go, python, rust, java, maven, gradle, node, cpp; repeatable)")
	flags.BoolVar(&o.memory, "memory", false, "enable ai-memory")
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
	flags.StringVar(&o.profile, "profile", "", "load the named profile from the global config as the base selection (precedence: built-in defaults < local .ai-launch.yaml < profile < explicit flags)")
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
		{"stack", func() { local.Options.Stacks = o.stacks }},
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

// disableMemoryIfUnsupported turns the ai-memory layer off when the selected
// agent would build a command that ai-memory run cannot execute. This covers
// both agents that declare supports_memory: false and agents whose catalog
// entry is stale/wrong and names a run_harness that ai-memory does not accept
// (for example cursor-agent). Unresolved agents are left alone.
//
// dockerRunConfigFromOptions derives the docker run inputs from the saved
// selection: the image selection normalizes the requested stacks plus the
// selected agent, pinned to a placeholder version — the catalog pins the real
// release version when the installer runs (design C1/C2). Home is forwarded
// so credential mounts resolve; the caller keeps the launch config pure.
func dockerRunConfigFromOptions(agent config.Agent, stacks []string, home string) container.RunConfig {
	selection, err := container.Normalize(stacks, []container.AgentInstall{{
		Command: agent.Command,
		Kind:    container.InstallRelease,
		Version: "0.0.0-pending",
	}}, nil)
	if err != nil {
		// An invalid stack selection surfaces as docker-selection-invalid in
		// preflight; here we keep the raw stacks so the error is attributable.
		return container.RunConfig{
			Selection: container.Selection{Stacks: stacks},
			HomeDir:   home,
		}
	}
	return container.RunConfig{
		Selection:      selection,
		HomeDir:        home,
		AddHostGateway: true,
	}
}

func disableMemoryIfUnsupported(agent config.Agent, resolved bool, memory *bool, errOut io.Writer) {
	if !resolved || !*memory {
		return
	}
	if !agent.SupportsMemory {
		warnf(errOut, "agent %q does not support ai-memory; disabling memory for this launch", agent.Command)
		*memory = false
		return
	}
	harness := agent.Command
	if agent.Memory != nil {
		if h := strings.TrimSpace(agent.Memory.RunHarness); h != "" {
			harness = h
		}
	}
	if !config.SupportsMemoryRunHarness(harness) {
		warnf(errOut, "ai-memory run does not accept harness %q for agent %q; disabling memory for this launch", harness, agent.Command)
		*memory = false
	}
}

// disableYoloIfUnsupported turns the dangerous-mode flag off when the selected
// agent does not declare support for it. Without this, toggling --yolo for an
// agent like Cursor would append a raw --yolo argument that the agent does not
// understand. Unresolved agents are left alone.
func disableYoloIfUnsupported(agent config.Agent, resolved bool, yolo *bool, errOut io.Writer) {
	if resolved && *yolo && !agent.SupportsYolo {
		warnf(errOut, "agent %q does not support --yolo; disabling it for this launch", agent.Command)
		*yolo = false
	}
}

// resolveAgentSelection picks the agent (the --agent flag wins over the local
// config). An unresolved agent is still synthesized here; enforceLocalConfigTrust
// decides whether it may be used. The returned bool is true when the agent was
// actually found in the catalog (so callers can tell a real catalog entry from
// a synthesized fallback).
func resolveAgentSelection(catalogue catalog.Catalog, flagAgent string, local config.Local) (catalog.AgentStatus, bool) {
	selected := flagAgent
	if selected == "" {
		selected = local.Agent
	}
	status, err := catalogue.Resolve(selected)
	if err != nil {
		return catalog.AgentStatus{Agent: config.Agent{Name: selected, Command: selected}}, false
	}
	if status.ResolvedCommand != "" {
		// Keep the configured catalog name for display, but invoke the alias
		// that was actually found on this machine (for example kilocode).
		// Preserve the original catalog command so launcher internals (state
		// directory mounts, memory harness mapping, etc.) keep working.
		status.Agent.CatalogCommand = status.Agent.Command
		status.Agent.Command = status.ResolvedCommand
	}
	return status, true
}

// localTrustFrom records what the workspace file itself asked for, so the trust
// check can tell an operator's choice from a repository's.
//
// It reads the config as LoadLocal returned it, BEFORE any profile is layered
// on: a profile lives in the global config, which ARCHITECTURE invariant 2b
// lists as trusted alongside the command line. Deriving this from the merged
// selection made a profile's own `jail: false` indistinguishable from a
// repository lowering the sandbox — `--profile` was refused outright, and the
// error blamed a .ai-launch.yaml that had said nothing of the sort.
//
// Only the fields the file still decides are checked. A value a trusted source
// replaced never reaches the argv, so refusing the launch over it would block
// on input that was already discarded: `agent: other-cli` in the workspace file
// is harmless once --agent or a profile picks something else.
func localTrustFrom(catalogue catalog.Catalog, flagAgent string, raw config.Local, profile *config.Profile, noLocalConfig bool) localTrust {
	overrides := profileOverrides(profile)
	trust := localTrust{
		optionsRaw: !overrides.options && !noLocalConfig,
		fromFile:   flagAgent == "" && !overrides.agent && !noLocalConfig,
	}
	if trust.fromFile {
		trust.agent = raw.Agent
		_, err := catalogue.Resolve(raw.Agent)
		trust.agentKnown = err == nil
	}
	// jail defaults to true so an overridden block never reads as "the file
	// turned the sandbox off"; the profile's own value is trusted on its own.
	trust.jail = true
	trust.docker = false
	if trust.optionsRaw {
		trust.jail = raw.Options.Jail
		trust.docker = raw.Options.Docker
		trust.yolo = raw.Options.Yolo
		trust.extraArgs = append([]string(nil), raw.Options.ExtraArgs...)
		trust.jailFlags = raw.Options.JailFlags
		trust.paramValues = raw.Options.ParamValues
		trust.workspace = raw.Options.Workspace
		trust.project = raw.Options.Project
	}
	if !overrides.mounts && !noLocalConfig {
		trust.mounts = raw.Mounts
	}
	// Permissions the profile replaced are trusted global input — only the
	// file-owned map is subject to the CLI-flag opt-in check.
	if !overrides.permissions && !noLocalConfig {
		trust.rawPermissions = copyPermissions(raw.Permissions)
	}
	return trust
}

// profileBlocks names the selection blocks a profile replaced. It mirrors the
// conditions in applyProfile one-for-one: whatever the profile takes over stops
// being the workspace file's responsibility, so the two must be read together.
//
// One field per `if` in applyProfile, deliberately. A block that applyProfile
// replaces without a field here has no way to be excused from the trust gate,
// and the launcher refuses the run over a value it already discarded — the
// defect this type exists to prevent, described at length on localTrustFrom.
// TestProfileBlocksCoversEveryApplyProfileBlock is the mechanical guard.
type profileBlocks struct {
	agent       bool
	permissions bool
	mounts      bool
	options     bool
}

func profileOverrides(profile *config.Profile) profileBlocks {
	if profile == nil {
		return profileBlocks{}
	}
	return profileBlocks{
		agent:       strings.TrimSpace(profile.Agent) != "",
		permissions: profile.Permissions != nil,
		mounts:      profile.Mounts != nil,
		options:     profile.Options != nil,
	}
}

// localTrust captures the security-relevant values a workspace-local
// .ai-launch.yaml supplied, before CLI flags are layered on top of them.
type localTrust struct {
	optionsRaw     bool // false when profile replaces options block
	agent          string
	agentKnown     bool
	fromFile       bool // false once --agent was given: the operator's own choice.
	jail           bool
	docker         bool // container backend requested by the file
	mounts         []config.Mount
	rawPermissions map[string]bool   // permissions from file (profile may overwrite)
	yolo           bool              // file value when profile does not own options
	extraArgs      []string          // file value when profile does not own options
	jailFlags      config.JailFlags  // file value when profile does not own options
	paramValues    map[string]string // file value when profile does not own options
	workspace      string            // file value when profile does not own options
	project        string            // file value when profile does not own options
}

// enforceLocalConfigTrust refuses a workspace-local config that lowers the
// security posture on its own. A .ai-launch.yaml travels with the repository,
// so for any checkout the operator did not write it is attacker-supplied
// input: left unchecked it picks the executed binary, turns the sandbox off,
// and mounts whatever it likes. What the operator types on the command line
// stays fully trusted — the boundary is around the file, not around the user.
//
// savedLocally is true when the file's hash matches one the launcher recorded
// in the trusted global config at save time: proven proof the operator wrote
// it, which no cloned repository can forge. Such a file is honored like
// operator input.
//
// Refusal rather than a prompt: a launcher run is routinely non-interactive
// (scripts, CI, the --dry-run diagnostic), and the plan requires those to
// refuse. Each refusal names the explicit opt-in that accepts the risk.
func enforceLocalConfigTrust(flags *flag.FlagSet, global config.Global, trust localTrust, savedLocally bool) error {
	if savedLocally {
		return nil
	}
	if trust.fromFile && strings.TrimSpace(trust.agent) != "" && !trust.agentKnown {
		return fmt.Errorf("local config selects agent %q, which the catalog cannot resolve; "+
			"run it explicitly with --agent %s, or register it in the global catalog with --add",
			trust.agent, trust.agent)
	}
	if globalRequiresJail(global) && !trust.jail &&
		!flagsWasSet(flags, flagNoJail) && !flagsWasSet(flags, "sandbox") {
		return errors.New("local config disables the sandbox (options.jail: false) " +
			"while the global catalog defaults it on; pass --no-jail to accept that explicitly")
	}
	// F-docker — the container backend is a sandbox change, not just a toggle:
	// it replaces ai-jail with a docker container. A workspace-local file
	// turning it on without operator consent swaps the sandbox under the
	// checkout, so it needs the same explicit opt-in as jail: false. Saving
	// the selection records the file as operator-written; profiles are trusted
	// global input.
	if err := enforceDockerBackendConsent(flags, trust); err != nil {
		return err
	}

	// F2 — sensitive mounts: each file-supplied mount must not expose a denied
	// tree or a container-control socket. CLI --mount/--rw-map and launcher-saved
	// files skip this check (the operator's own explicit choice).
	if err := enforceMountConsent(trust); err != nil {
		return err
	}

	// F1 — permissions: unsaved-local-file permissions must match explicit CLI flags.
	if err := enforcePermissionConsent(flags, trust); err != nil {
		return err
	}

	// F3 — jail_flags: non-zero flags weaken the sandbox posture and require
	// explicit operator consent via profile or save. No per-flag CLI toggle
	// exists yet; the opt-in is saving or selecting a profile.
	if trust.optionsRaw && !trust.jailFlags.IsZero() {
		return errors.New("local config sets options.jail_flags without operator save or profile; profiles and --save are needed to accept custom jail behaviour")
	}

	// F6 — yolo / extra_args: dangerous options require explicit consent.
	if trust.optionsRaw && trust.yolo && !flagsWasSet(flags, "yolo") {
		return errors.New("local config sets options.yolo: true; run with --yolo or save the selection to accept")
	}
	if trust.optionsRaw && len(trust.extraArgs) > 0 {
		if !flagsWasSet(flags, "extra-args") && !flagsWasSet(flags, "args") {
			return errors.New("local config lists options.extra_args without --args/--extra-args; pass the flag to accept")
		}
	}

	// param_values: catalog-declared harness flags, filled in by the file.
	//
	// Narrower than extra_args — a value can only land behind a flag the
	// catalog already declares, so a repository cannot invent an argument. It
	// can still choose one: `model` picks what the agent runs as, and a param
	// declared `takes_value: false` is a bare flag the file turns on, which is
	// why pre-flight already warns `catalog-flag-param` about those. Choosing
	// the model and the flags of the process about to read the checkout is an
	// operator decision, so it takes the same opt-in as everything else here.
	if trust.optionsRaw && len(trust.paramValues) > 0 && !flagsWasSet(flags, "param") {
		names := make([]string, 0, len(trust.paramValues))
		for name := range trust.paramValues {
			names = append(names, name)
		}
		sort.Strings(names) // map order is random; the message must not be
		return fmt.Errorf("local config sets options.param_values (%s) without --param; "+
			"pass --param name=value to accept explicitly, or save the selection",
			strings.Join(names, ", "))
	}

	if err := enforceMemoryScopeConsent(flags, trust); err != nil {
		return err
	}
	if err := enforceProjectJailSymlinkConsent(trust, savedLocally); err != nil {
		return err
	}

	return nil
}

// enforceDockerBackendConsent refuses an unsaved local config that enables
// the docker container backend. Docker replaces ai-jail as the sandbox, so a
// repository file must not switch it without the operator repeating the choice
// on the command line (mirrors the jail: false consent gate).
func enforceDockerBackendConsent(flags *flag.FlagSet, trust localTrust) error {
	if trust.optionsRaw && trust.docker && !flagsWasSet(flags, "docker-backend") {
		return errors.New("local config enables the docker container backend (options.docker: true) " +
			"without operator consent; pass --docker-backend to accept explicitly, or save the selection")
	}
	return nil
}

// enforceMountConsent refuses file-supplied mounts that expose a denied tree,
// the filesystem root, or a non-absolute path. These are the same rules the
// jail applies to operator mounts; the docker backend inherits them.
func enforceMountConsent(trust localTrust) error {
	for _, mount := range trust.mounts {
		path := strings.TrimSpace(mount.Path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("local config mount %q is not an absolute path", path)
		}
		if filepath.Clean(path) == string(filepath.Separator) {
			return fmt.Errorf("local config mount %q would expose the filesystem root", path)
		}
		if reason := launcher.DeniedMount(path); reason != nil {
			return fmt.Errorf("local config mount %q is refused — %s; save the selection or use --mount to accept explicitly", path, reason.Reason)
		}
	}
	return nil
}

// enforceMemoryScopeConsent refuses an unsaved local config that sets
// ai-memory workspace/project/workstream scopes. Those values are forwarded
// verbatim to `ai-memory run`, so a repository file must not redirect an
// authenticated token to another scope without the operator repeating the
// same value on the command line.
func enforceMemoryScopeConsent(flags *flag.FlagSet, trust localTrust) error {
	if !trust.optionsRaw {
		return nil
	}
	if strings.TrimSpace(trust.workspace) != "" && !flagsWasSet(flags, "workspace") {
		return fmt.Errorf("local config sets options.workspace %q without --workspace; "+
			"pass --workspace %s to accept explicitly, or save the selection",
			trust.workspace, trust.workspace)
	}
	if strings.TrimSpace(trust.project) != "" && !flagsWasSet(flags, "project") {
		return fmt.Errorf("local config sets options.project %q without --project; "+
			"pass --project %s to accept explicitly, or save the selection",
			trust.project, trust.project)
	}
	return nil
}

// enforceProjectJailSymlinkConsent refuses when the project has a symlinked
// .ai-jail file. A checkout-controlled symlink changes what ai-jail reads and
// writes when config masking is disabled, so it must not happen without
// operator consent. When the local file is ignored entirely the symlink is not
// attributed to repository input, hence the check is gated on trust.optionsRaw.
func enforceProjectJailSymlinkConsent(trust localTrust, savedLocally bool) error {
	if savedLocally || !trust.optionsRaw {
		return nil
	}
	link, target, ok := projectJailConfigSymlink()
	if !ok {
		return nil
	}
	if target == "" {
		return fmt.Errorf("%s is a broken symlink; ai-jail config masking cannot be disabled safely. "+
			"Remove the symlink, launch with --no-local-config, or save the selection", link)
	}
	return fmt.Errorf("%s is a symlink to %s; ai-jail config masking would be disabled for this launch. "+
		"Pass --no-local-config, or save the selection to accept", link, target)
}

// enforcePermissionConsent requires an explicit CLI flag for every permission
// the unsaved local file turns on. CLI --<permission> flags and saved
// selections skip this (the operator's own explicit choice). Extracted from
// enforceLocalConfigTrust (F1) to keep that function under the gocognit cap.
func enforcePermissionConsent(flags *flag.FlagSet, trust localTrust) error {
	permissionFlagName := map[string]string{
		config.PermissionSSH:       config.PermissionSSH,
		"github":                   config.PermissionGitHub,
		config.PermissionGitHub:    config.PermissionGitHub,
		config.PermissionDocker:    config.PermissionDocker,
		config.PermissionGPU:       config.PermissionGPU,
		config.PermissionDisplay:   config.PermissionDisplay,
		config.PermissionPictures:  config.PermissionPictures,
		config.PermissionTailscale: config.PermissionTailscale,
		config.PermissionSystemd:   permissionSystemdUser,
		config.PermissionMise:      config.PermissionMise,
		config.PermissionWorktree:  config.PermissionWorktree,
	}
	for permID, enabled := range trust.rawPermissions {
		if !enabled {
			continue
		}
		flagName, known := permissionFlagName[permID]
		if !known {
			continue // unknown permission id — catalogue normalisation drops it later
		}
		if !flagsWasSet(flags, flagName) {
			return fmt.Errorf("local config enables permission %q (true); pass --%s to accept explicitly", permID, flagName)
		}
	}
	return nil
}

// globalRequiresJail reports whether the trusted global catalog defaults the
// sandbox on. An operator who turned it off globally is not being downgraded
// by a local config that agrees with them.
func globalRequiresJail(global config.Global) bool {
	for _, permission := range global.Permissions {
		if permission.ID == config.PermissionJail {
			return permission.Default
		}
	}
	return true
}

// reportPreflight prints every pre-flight issue and reports whether any of them
// is fatal. Warnings are labelled as such; anything else blocks the launch.
func reportPreflight(errOut io.Writer, issues []launcher.Issue) bool {
	fatal := false
	for _, issue := range issues {
		if issue.Warning {
			warnf(errOut, valueFormat, issue)
		} else {
			_, _ = fmt.Fprintln(errOut, "error:", issue)
		}
		if !issue.Warning {
			fatal = true
		}
	}
	return fatal
}

// reportInfoFlags handles the flags that only print information and exit,
// before any configuration is loaded. handled is true when one of them ran.
func reportInfoFlags(opts cliOptions, out io.Writer) (bool, error) {
	switch {
	case opts.showVersion:
		_, _ = fmt.Fprintf(out, "ai-launcher %s (%s, %s)\n", version, commit, date)
		return true, nil
	case opts.doctor:
		return true, reportDoctor(out)
	}
	return false, nil
}

// reportDoctor prints the installed upstream CLI versions against the floor
// this launcher composes with. It is the explicit home of the --version probe:
// pre-flight validation stays hermetic so a launch never pays for extra
// processes, and the TUI event loop never blocks on them.
func reportDoctor(out io.Writer) error {
	_, _ = fmt.Fprintf(out, "ai-launcher %s (%s, %s)\n", version, commit, date)
	stale := false
	for _, status := range launcher.UpstreamReport(nil, "") {
		switch {
		case status.Missing:
			_, _ = fmt.Fprintf(out, "%-10s not found in PATH (required >= %s)\n", status.Command, status.Minimum)
		case status.Version == "":
			_, _ = fmt.Fprintf(out, "%-10s %s (version unreadable; required >= %s)\n", status.Command, status.Path, status.Minimum)
		case status.TooOld:
			stale = true
			_, _ = fmt.Fprintf(out, "%-10s %s is older than the required %s; upgrade with --upgrade\n", status.Command, status.Version, status.Minimum)
		default:
			_, _ = fmt.Fprintf(out, "%-10s %s (>= %s)\n", status.Command, status.Version, status.Minimum)
		}
	}
	if stale {
		_, _ = fmt.Fprintln(out, "\nAn older upstream may accept a different flag surface than the one ai-launcher emits.")
	}
	return nil
}

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
	flags, opts, handled, err := parseCommandLine(args, out, errOut)
	if handled || err != nil {
		return err
	}
	home := userHome()
	resolveConfigPaths(&opts, home)
	global, handled, err := dispatchSideCommands(flags, &opts, home, out, errOut)
	if handled || err != nil {
		return err
	}
	inputs, err := resolveLaunchInputs(flags, &opts, global)
	if err != nil {
		return err
	}
	if inputs.handled {
		return nil
	}
	disableMemoryIfUnsupported(inputs.status.Agent, inputs.resolved, &inputs.local.Options.Memory, errOut)
	disableYoloIfUnsupported(inputs.status.Agent, inputs.resolved, &inputs.local.Options.Yolo, errOut)
	if len(inputs.positionalArgs) > 0 {
		inputs.local.Options.ExtraArgs = append(inputs.local.Options.ExtraArgs, inputs.positionalArgs...)
	}
	launchConfig := launcher.LaunchConfig{
		Agent:           inputs.status.Agent,
		Executable:      inputs.status.Path,
		HomeDir:         home,
		MemoryServerURL: inputs.global.MemoryServerURL,
		MemoryAuthToken: inputs.global.MemoryAuthToken,
		UseJail:         inputs.local.Options.Jail && !inputs.local.Options.Docker,
		UseDocker:       inputs.local.Options.Docker,
		Docker:          dockerRunConfigFromOptions(inputs.status.Agent, inputs.local.Options.Stacks, home),
		UseMemory:       inputs.local.Options.Memory,
		ContinueSession: opts.continueSession,
		JailExec:        len(args) > 0,
		NewWorkstream:   inputs.local.Options.NewWorkstream,
		Workstream:      inputs.local.Options.Workstream,
		Workspace:       inputs.local.Options.Workspace,
		Project:         inputs.local.Options.Project,
		JailFlags:       inputs.local.Options.JailFlags,
		Permissions:     inputs.permissions,
		Mounts:          inputs.mounts,
		Yolo:            inputs.local.Options.Yolo,
		Fresh:           inputs.local.Options.Fresh,
		ExtraArgs:       inputs.local.Options.ExtraArgs,
		ParamValues:     inputs.local.Options.ParamValues,
	}
	launchConfig = finalizeLaunchConfig(launchConfig, global, home, errOut)
	if opts.saveProfile != "" {
		return saveProfileCommand(opts.globalPath, global, opts.saveProfile, launchConfig, out)
	}
	return launch(launchRequest{args: args, opts: opts, global: global, local: inputs.local,
		launchConfig: launchConfig, in: in, out: out, errOut: errOut})
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

func resolveLaunchInputs(flags *flag.FlagSet, opts *cliOptions, global config.Global) (resolvedLaunchInputs, error) {
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
	if err := enforceLocalConfigTrust(flags, global, trust, config.LocalConfigTrusted(global, opts.localPath)); err != nil {
		return resolvedLaunchInputs{}, err
	}
	permissions := resolvePermissions(flags, opts, local, catalogue)
	mounts, err := opts.applyToLocal(flags, &local, global.DefaultMounts)
	if err != nil {
		return resolvedLaunchInputs{}, err
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
	if err := flags.Parse(args); err != nil {
		return nil, opts, false, err
	}
	handled, err := reportInfoFlags(opts, out)
	return flags, opts, handled, err
}

// userHome returns the operator's home directory, or "" when it cannot be
// determined.
func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// resolveConfigPaths fills in the default config paths for the flags the
// operator left unset.
func resolveConfigPaths(opts *cliOptions, home string) {
	if opts.globalPath == "" && home != "" {
		opts.globalPath = filepath.Join(home, ".config", "ai-launch", "config.yaml")
	}
	if opts.localPath == "" {
		opts.localPath = filepath.Join(mustGetwd(), ".ai-launch.yaml")
	}
}

// runGlobalCommands handles the commands that only need the trusted global
// config. handled is true when one of them ran.
func runGlobalCommands(opts *cliOptions, global config.Global, home string, out, errOut io.Writer) (bool, error) {
	if opts.install || opts.upgrade {
		return true, launchcmd.InstallConfigured(global, opts.agent, home, opts.upgrade, out, errOut)
	}
	if opts.listProfiles {
		listProfiles(global, out)
		return true, nil
	}
	if opts.deleteProfile != "" {
		return true, deleteProfile(opts.globalPath, global, opts.deleteProfile, out)
	}
	if strings.TrimSpace(opts.workstreamSearch) != "" {
		return true, searchWorkstream(opts, global, home, out, errOut)
	}
	return false, nil
}

// searchWorkstream forwards a query to `ai-memory workstream-search`.
//
// The launcher already creates workstreams (`--new`) and resumes them
// (`--workstream`), so it owned the write side of the ledger and gave no way
// to read it back. The delta ai-memory injects into the next harness is
// size-limited by design; the searchable ledger is where an old decision
// actually lives, and needing a second tool to reach it defeats the point of
// having one command that knows about workstreams.
//
// Deliberately not jail-wrapped, unlike a launch. This is a read-only query
// against the ai-memory server over HTTP, in the same class as --doctor: no
// harness runs, nothing touches the checkout, and paying for a sandbox would
// only mean the operator's terminal cannot see the answer.
//
// The workstream is addressed by id, not by the name --workstream takes:
// upstream's `run --workstream` selects by name while `workstream-search`
// requires --workstream-id, and no subcommand maps one to the other. The id is
// resolved by resolveWorkstreamID and always emitted, so the argv says which
// ledger was read instead of depending on what the child inherits.
func searchWorkstream(opts *cliOptions, global config.Global, home string, out, errOut io.Writer) error {
	memoryPath, err := exec.LookPath(aiMemoryCommand)
	if err != nil {
		return fmt.Errorf("%s not found in PATH; install it with --install", aiMemoryCommand)
	}
	workstreamID, err := resolveWorkstreamID(opts)
	if err != nil {
		return err
	}
	args := []string{"workstream-search", "--workstream-id", workstreamID}
	if opts.searchLimit > 0 {
		args = append(args, "--limit", strconv.Itoa(opts.searchLimit))
	}
	if opts.searchJSON {
		args = append(args, "--json")
	}
	args = append(args, opts.workstreamSearch)

	ctx, cancel := context.WithTimeout(context.Background(), workstreamSearchTimeout)
	defer cancel()
	// #nosec G204 G702 -- memoryPath is the LookPath result for a fixed tool name;
	// every argument is a launcher-controlled flag or the operator's own query.
	command := exec.CommandContext(ctx, memoryPath, args...)
	command.Env = launcher.Environment(launcher.LaunchConfig{
		UseMemory:       true,
		HomeDir:         home,
		MemoryServerURL: global.MemoryServerURL,
		MemoryAuthToken: global.MemoryAuthToken,
	})
	command.Stdout = out
	command.Stderr = errOut
	if err := command.Run(); err != nil {
		return fmt.Errorf("workstream-search: %w", err)
	}
	return nil
}

// resolveWorkstreamID answers which workstream a search reads, preferring the
// explicit flag over the inherited environment.
//
// Without it the launcher forwarded a query with no --workstream-id at all and
// upstream's own argument parser refused the command, so the feature failed
// outside a managed session — which is the session it exists to serve. Falling
// back to the environment keeps the zero-argument form working for an agent
// this launcher started; the flag is what makes it usable from a bare shell.
//
// --workstream is deliberately not consulted: it carries the workstream *name*
// that `ai-memory run --workstream` selects by, and upstream offers no way to
// turn a name into the id this subcommand wants. Guessing they are the same
// would send a search to a workstream the operator did not ask for.
func resolveWorkstreamID(opts *cliOptions) (string, error) {
	if id := strings.TrimSpace(opts.workstreamID); id != "" {
		return id, nil
	}
	if id := strings.TrimSpace(os.Getenv(memoryWorkstreamIDEnv)); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("--workstream-search needs a workstream id: pass --workstream-id, or run it from an agent this launcher started, where %s is set", memoryWorkstreamIDEnv)
}

// loadLocalSelection loads the workspace config and layers the requested
// profile over it. It returns both the merged selection and the file as it was
// read, because the two answer different questions: the merged one drives the
// launch, while the raw one is the only honest input to the trust boundary
// (see localTrustFrom).
func loadLocalSelection(opts *cliOptions, global config.Global, errOut io.Writer) (merged, raw config.Local, applied *config.Profile, err error) {
	if opts.noLocalConfig {
		defaults := config.DefaultLocal()
		return defaults, defaults, nil, nil
	}
	local, localErr := config.LoadLocal(opts.localPath)
	if localErr != nil {
		warnf(errOut, valueFormat, localErr)
	}
	raw = cloneLocal(local)
	if opts.profile == "" {
		return local, raw, nil, nil
	}
	profile, ok := global.Profiles[opts.profile]
	if !ok {
		return local, raw, nil, fmt.Errorf("profile %q not found in %s", opts.profile, opts.globalPath)
	}
	applyProfile(&local, profile)
	return local, raw, &profile, nil
}

// cloneLocal deep-copies the mutable parts of a local config so applyProfile
// cannot reach the snapshot the trust check reads.
func cloneLocal(local config.Local) config.Local {
	clone := local
	clone.Permissions = copyPermissions(local.Permissions)
	clone.Mounts = append([]config.Mount(nil), local.Mounts...)
	clone.Options.ExtraArgs = append([]string(nil), local.Options.ExtraArgs...)
	return clone
}

// resolvePermissions merges the configured permissions with the CLI flag
// overrides and normalizes the result. Normalization resolves permission
// dependencies (every jail-backed permission requires jail) and drops ids the
// catalog does not declare. It has to run *after* every input is merged:
// normalizing first left a dependency pulled in by a CLI flag unresolved, so
// the argv came out missing it while the TUI, which re-normalizes on each
// toggle, produced the right one.
func resolvePermissions(flags *flag.FlagSet, opts *cliOptions, local config.Local, catalogue catalog.Catalog) map[string]bool {
	permissions := copyPermissions(local.Permissions)
	applyBoolFlag(flags, config.PermissionSSH, permissions, config.PermissionSSH, opts.ssh)
	applyBoolFlag(flags, config.PermissionGitHub, permissions, config.PermissionGitHub, opts.gh)
	applyBoolFlag(flags, config.PermissionDocker, permissions, config.PermissionDocker, opts.docker)
	applyBoolFlag(flags, config.PermissionGPU, permissions, config.PermissionGPU, opts.gpu)
	applyBoolFlag(flags, config.PermissionDisplay, permissions, config.PermissionDisplay, opts.display)
	applyBoolFlag(flags, config.PermissionPictures, permissions, config.PermissionPictures, opts.pictures)
	applyBoolFlag(flags, config.PermissionTailscale, permissions, config.PermissionTailscale, opts.tailscale)
	applyBoolFlag(flags, permissionSystemdUser, permissions, permissionSystemdUser, opts.systemdUser)
	applyBoolFlag(flags, config.PermissionMise, permissions, config.PermissionMise, opts.mise)
	applyBoolFlag(flags, config.PermissionWorktree, permissions, config.PermissionWorktree, opts.worktree)
	return catalogue.NormalizePermissions(permissions)
}

// finalizeLaunchConfig drops the integrations the host cannot run (ai-jail on
// Windows) with an explanatory warning, then adds the mounts and flags the
// launcher infers from the host. macOS keeps the jail — sandbox-exec is
// supported.
func finalizeLaunchConfig(launchConfig launcher.LaunchConfig, global config.Global, home string, errOut io.Writer) launcher.LaunchConfig {
	launchConfig, platformIssues := launcher.ConstrainToPlatform(launchConfig, runtime.GOOS, global.Permissions)
	for _, issue := range platformIssues {
		warnf(errOut, valueFormat, issue)
	}
	if launchConfig.UseJail {
		launchConfig = applyJailAutoDetection(launchConfig, home, errOut)
	}
	return launchConfig
}

// upgradeResolveTag and upgradeApply are seams: tests stub them to exercise
// the upgrade wiring without network access or executable replacement.
var upgradeResolveTag = func(ctx context.Context, updater *selfupdate.Updater, wantVersion string) (string, error) {
	if wantVersion != "" {
		return wantVersion, nil
	}
	return updater.LatestTag(ctx)
}

var upgradeApply = func(ctx context.Context, updater *selfupdate.Updater, tag string) error {
	return updater.Apply(ctx, tag)
}

// runUpgrade implements `ai-launcher upgrade [--check] [--version vX.Y.Z]`,
// the self-update of the running binary from this repository's GitHub
// releases. Checksum verification is strict (a missing checksums.txt is a
// hard error) because the flow overwrites the running executable.
func runUpgrade(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	flags.SetOutput(errOut)
	check := flags.Bool("check", false, "only report whether an update is available")
	wantVersion := flags.String("version", "", "install a specific release tag (default: latest)")
	flags.Usage = func() {
		_, _ = fmt.Fprint(errOut, `usage: ai-launcher upgrade [--check] [--version vX.Y.Z]

Self-update the ai-launcher binary from the project's GitHub releases.
Downloads the archive for this OS/arch, verifies its SHA-256 against the
release checksums.txt (mandatory), and atomically replaces the running
executable.

Environment overrides:
  AI_LAUNCHER_UPDATE_API    release API base (default `+selfupdate.DefaultAPIBaseURL+`)
  AI_LAUNCHER_UPDATE_URL    download base URL (default `+selfupdate.DefaultDownloadBaseURL+`)
  AI_LAUNCHER_UPDATE_TOKEN  GitHub token for private releases (sent as a
                            Bearer header; never printed or logged)
`)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	apiBase := envOr("AI_LAUNCHER_UPDATE_API", selfupdate.DefaultAPIBaseURL)
	downloadBase := envOr("AI_LAUNCHER_UPDATE_URL", selfupdate.DefaultDownloadBaseURL)
	token := os.Getenv("AI_LAUNCHER_UPDATE_TOKEN")
	// Fail closed: the endpoint overrides come from the environment, so a
	// hostile parent process could redirect the update flow to its own host.
	// The Bearer token is only ever sent to the default release host; with an
	// overridden endpoint it is withheld (and said so) rather than exfiltrated.
	if token != "" && (apiBase != selfupdate.DefaultAPIBaseURL || downloadBase != selfupdate.DefaultDownloadBaseURL) {
		warnf(errOut, "AI_LAUNCHER_UPDATE_TOKEN withheld: the update endpoint is overridden and the token is only sent to the default release host")
		token = ""
	}
	updater := &selfupdate.Updater{
		CurrentVersion:  version,
		APIBaseURL:      apiBase,
		DownloadBaseURL: downloadBase,
		Token:           token,
	}
	ctx := context.Background()
	tag, err := upgradeResolveTag(ctx, updater, *wantVersion)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "current: %s\nlatest:  %s\n", version, tag)
	if *check {
		if selfupdate.SameVersion(version, tag) {
			_, _ = fmt.Fprintln(out, "already up to date.")
		} else {
			_, _ = fmt.Fprintln(out, "an update is available — run `ai-launcher upgrade`.")
		}
		return nil
	}
	if *wantVersion == "" && selfupdate.SameVersion(version, tag) {
		_, _ = fmt.Fprintf(out, "already up to date (%s)\n", tag)
		return nil
	}
	if err := upgradeApply(ctx, updater, tag); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "ai-launcher updated: %s → %s\n", version, tag)
	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
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
			Docker:        launch.UseDocker,
			Stacks:        launch.Docker.Selection.Stacks,
			Memory:        launch.UseMemory,
			Yolo:          launch.Yolo,
			Fresh:         launch.Fresh,
			NewWorkstream: launch.NewWorkstream,
			Workstream:    launch.Workstream,
			Workspace:     launch.Workspace,
			Project:       launch.Project,
			JailFlags:     launch.JailFlags,
			ExtraArgs:     launch.ExtraArgs,
			ParamValues:   launch.ParamValues,
		},
	}
}

// launchAction is what run() does with the composed argv.
type launchAction int

const (
	actionExecute launchAction = iota
	actionPrint
)

// decideLaunchAction maps the confirmed launch intent to the post-TUI
// behavior: Enter in the TUI and any CLI invocation execute the composed
// argv; only an explicit dry-run (the --dry-run flag, or Ctrl+D/d inside the
// TUI, which never leaves the TUI) prints it.
func decideLaunchAction(dryRun bool) launchAction {
	if dryRun {
		return actionPrint
	}
	return actionExecute
}

// launchRequest bundles everything launch needs to confirm, validate, and
// execute a selection, keeping the flow readable without a long parameter
// list.
type launchRequest struct {
	args         []string
	opts         cliOptions
	global       config.Global
	local        config.Local
	launchConfig launcher.LaunchConfig
	in           io.Reader
	out          io.Writer
	errOut       io.Writer
}

// launch confirms the configuration (TUI when interactive), optionally saves
// it, and builds, validates, and executes the resulting argv.
//
// In the interactive (TUI) flow a launch failure is recoverable in place:// the TUI is re-opened seeded with the failure so the operator can adjust
// (toggle New workstream / ai-memory off) and retry with r, or quit, without
// restarting ai-launcher. The CLI flow (argv present) stays one-shot and
// exits with the error, since prompts are not an option there.
func launch(req launchRequest) error {
	tuiFlow := len(req.args) == 0
	status := ""
	autoArmedNew := false // true when --new was armed to recover from a 409 (cleared on success)
	for {
		proceed, err := req.confirmSelection(status)
		if err != nil || !proceed {
			return err
		}
		argv, err := launcher.Build(req.launchConfig)
		if err != nil {
			return err
		}
		// Validate before printing: --dry-run is the advertised diagnostic surface,
		// so it must not present a command pre-flight would reject. The argv is
		// still printed when there are issues — seeing what would run is the point.
		printOnly := decideLaunchAction(req.opts.dryRun) == actionPrint
		issues := launcher.NewValidator().WithPermissions(req.global.Permissions).Validate(req.launchConfig)
		fatal := reportPreflight(req.errOut, issues)
		if printOnly {
			_, _ = fmt.Fprintln(req.out, shellJoin(argv))
		}
		if fatal {
			return errors.New("pre-flight validation failed")
		}
		if printOnly {
			return nil
		}
		req.rememberRecentAgent()
		argv, err = req.prepareDockerIfNeeded(argv)
		if err != nil {
			return err
		}
		if err := req.execute(argv); err != nil {
			if !tuiFlow {
				return err
			}
			// TUI flow: surface the failure and re-open the selection screen.
			status = launchFailureStatus(err.Error())
			// A busy workstream (ai-memory 409 "workstream is already active")
			// is recovered by starting a parallel stream: arm --new so the
			// next r sidesteps the conflict instead of re-hitting it. The
			// operator is warned and can still turn it off (or wait ~90s for
			// the lease to expire). The recovery name is dropped again on a
			// successful launch so it is not baked into the saved selection.
			if isWorkstreamConflict(err.Error()) && strings.TrimSpace(req.launchConfig.NewWorkstream) == "" {
				req.launchConfig.NewWorkstream = fmt.Sprintf("recovery-%d", time.Now().Unix())
				autoArmedNew = true
				status = "Workstream busy (409) — armed 'New workstream' (" + req.launchConfig.NewWorkstream +
					") for the retry so the next r starts a parallel stream.\n" +
					"Turn it off if you'd rather wait ~90s, or q to quit.\n" + launchFailureHint(err.Error())
			}
			continue
		}
		if autoArmedNew && req.launchConfig.NewWorkstream != "" {
			req.launchConfig.NewWorkstream = ""
			if err := saveLocalSelection(req.opts.globalPath, true, req.opts.localPath, req.local, req.launchConfig); err != nil {
				warnf(req.errOut, "could not clear the recovery workstream from %s: %v", req.opts.localPath, err)
			}
		}
		return nil
	}
}

// runTUI is the interactive selection loop; a variable so tests can confirm
// a selection without a terminal. The trailing string seeds the initial
// status line (used to surface a launch failure on re-open).
var runTUI = tui.RunWithMessage

// confirmSelection runs the interactive TUI when the launch came from one, or
// persists the selection for a CLI invocation. proceed is false when the flow
// ends here (a --save run, a TUI cancellation, or a TUI error).
func (r *launchRequest) confirmSelection(initialStatus string) (bool, error) {
	if len(r.args) > 0 {
		if err := saveLocalSelection(r.opts.globalPath, r.opts.save, r.opts.localPath, r.local, r.launchConfig); err != nil {
			return false, err
		}
		return !r.opts.save, nil
	}
	confirmed, err := runTUI(r.global, r.launchConfig, tui.Hooks{
		Save: func(updated launcher.LaunchConfig) error {
			return saveLocalSelection(r.opts.globalPath, true, r.opts.localPath, r.local, updated)
		},
		SaveProfile: func(name string, updated launcher.LaunchConfig) error {
			if err := config.SetProfile(&r.global, name, profileFromLaunch(updated)); err != nil {
				return err
			}
			return config.SaveGlobal(r.opts.globalPath, r.global)
		},
	}, initialStatus, tui.Options{
		NoColor:       r.opts.noColor,
		HighContrast:  r.opts.highContrast,
		GlobalPath:    r.opts.globalPath,
		LocalPath:     r.opts.localPath,
		NoLocalConfig: r.opts.noLocalConfig,
	})
	if err != nil {
		// A cancellation is a quiet exit; any other failure is reported.
		return false, classifyTUIError(err)
	}
	r.launchConfig = confirmed
	// Autosave: running a selection means wanting it back on the next open —
	// with provenance recorded, or the trust boundary would refuse the file
	// the launcher itself wrote. Best-effort: a failed save never blocks a
	// launch, it warns.
	if err := saveLocalSelection(r.opts.globalPath, true, r.opts.localPath, r.local, confirmed); err != nil {
		warnf(r.errOut, "could not save the selection to %s: %v", r.opts.localPath, err)
	}
	return true, nil
}

// rememberRecentAgent records the harness for the TUI MRU list (best-effort;
// never block launch). Only the MRU list is persisted: writing the whole
// merged catalog on every launch froze that release's built-ins into the
// user's config.
func (r *launchRequest) rememberRecentAgent() {
	if r.launchConfig.ContinueSession {
		return
	}
	if cmd := strings.TrimSpace(r.launchConfig.Agent.Command); cmd != "" {
		config.TouchRecentAgent(&r.global, cmd)
		_ = config.SaveRecentAgents(r.opts.globalPath, r.global.RecentAgents)
	}
}

// execute runs the composed argv. After the interactive TUI, ai-launcher stays
// the parent (PTY) so a child that exits immediately (ai-memory TLS, jail cwd,
// etc.) is reported as our error instead of looking like the UI just vanished.
// CLI non-TUI launches still Replace into the child for a thin process tree.
func (r *launchRequest) execute(argv []string) error {
	label := r.launchConfig.Agent.Command
	if r.launchConfig.ContinueSession {
		label = "continue session"
	}
	cmdLine := shellJoin(argv)
	// Always announce what is about to run so a TUI close is never silent.
	_, _ = fmt.Fprintf(r.errOut, "ai-launcher: starting %s\n", label)
	_, _ = fmt.Fprintf(r.errOut, "ai-launcher: %s\n", cmdLine)

	useReplace := len(r.args) > 0 && r.in == os.Stdin && r.out == os.Stdout && r.errOut == os.Stderr
	if useReplace {
		if err := launcher.ReplaceWithEnv(argv, launcher.Environment(r.launchConfig)); err != nil {
			return fmt.Errorf("failed to start %s: %w\ncommand: %s", label, err, cmdLine)
		}
		return nil
	}
	execErr := (launcher.PTYExecutor{}).RunWithEnv(context.Background(), argv, launcher.Environment(r.launchConfig), r.in, r.out, r.errOut)
	if execErr != nil {
		return fmt.Errorf("failed to start %s: %w\ncommand: %s\n%s", label, execErr, cmdLine, launchFailureHint(execErr.Error()))
	}
	return nil
}

// dockerRunner runs docker argv as a child process with the launcher's own
// stdin/stdout/stderr wired through, so `docker build` progress streams and
// interactive containers behave like the CLI the operator expects. It is a
// container.Runner so EnsureImage can call it.
func (r *launchRequest) dockerRunner(argv []string) (int, error) {
	// #nosec G204 -- argv is composed internally by launcher.Build (docker run
	// from the container backend); it is never raw user input. The image tag
	// and mount paths come from the selection the operator saved.
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Stdin = r.in
	cmd.Stdout = r.out
	cmd.Stderr = r.errOut
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// prepareDockerIfNeeded ensures the tagged image exists (building it when
// missing) and applies MCP config overlays to the docker run argv. It is a
// no-op when the docker backend is off, keeping the hot launch path free of
// the branch. It returns the argv to execute; the original argv is not
// mutated. A dry-run never reaches here — the image check happens only for a
// real launch.
func (r *launchRequest) prepareDockerIfNeeded(argv []string) ([]string, error) {
	if !r.launchConfig.UseDocker {
		return argv, nil
	}
	sel := r.launchConfig.Docker.Selection
	if sel.Validate() != nil {
		return argv, errors.New("docker backend has an invalid image selection")
	}

	// The image needs the launcher binary only when a release-recipe agent is
	// selected (design C1); script and host-binary agents build without it.
	needLauncher := false
	for _, agent := range sel.Agents {
		if agent.Kind == container.InstallRelease {
			needLauncher = true
			break
		}
	}
	var launcherLinux string
	if needLauncher {
		var err error
		launcherLinux, err = buildLauncherLinux(r.out, r.errOut)
		if err != nil {
			return argv, err
		}
	}

	installConfigYAML, err := container.InstallConfig(r.global, sel.Agents)
	if err != nil {
		return argv, fmt.Errorf("docker backend: %w", err)
	}
	tag, cleanup, err := container.EnsureImage(sel, installConfigYAML, launcherLinux, r.dockerRunner)
	if err != nil {
		return argv, err
	}
	// The image build context is a temp dir; once the image is built it can go.
	cleanup()

	// Apply MCP config overlays: rewrite loopback URLs in known config files
	// into temp copies mounted over the originals (R7 item 31). The argv built
	// by launcher.Build mounts the original paths; appending the overlay mount
	// after it makes the rewritten copy win inside the container.
	overlayDir, err := os.MkdirTemp("", "ai-launcher-overlay-")
	if err != nil {
		return argv, err
	}
	// Keep the overlay dir alive until the process exits; a leaked temp dir on
	// a failed launch is the acceptable trade for a mount that must exist
	// before docker run.
	_ = overlayDir
	for _, mount := range overlayCandidates(r.launchConfig.HomeDir) {
		overlay := container.PlanOverlay(mount, overlayDir, container.RewriteLocalhost, false)
		if overlay == nil {
			continue
		}
		argv = append(argv, "-v", overlay.OverlayMountSpec())
	}
	_ = tag
	return argv, nil
}

// overlayCandidates returns the known config files worth scanning for
// loopback URLs. The agent config dirs are bind-mounted by the launcher; the
// loose files that store MCP server URLs get per-file overlays.
func overlayCandidates(home string) []string {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".config", "opencode", "opencode.json"),
	}
}

// buildLauncherLinux cross-compiles this binary for linux/amd64 into a temp
// file and returns its path. The image build runs the launcher's installer
// inside the container, so the binary must be a linux executable.
func buildLauncherLinux(out, errOut io.Writer) (string, error) {
	src := os.Getenv("GO_SRC_PATH")
	if src == "" {
		// The launcher was built from this module; resolve the module root
		// from the executable's own package by walking up to go.mod.
		wd, err := os.Getwd()
		if err == nil {
			for dir := wd; ; dir = filepath.Dir(dir) {
				if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
					src = dir
					break
				}
				if filepath.Dir(dir) == dir {
					break
				}
			}
		}
	}
	if src == "" {
		return "", errors.New("cannot locate the ai-launcher module to cross-compile; set GO_SRC_PATH")
	}
	tmp, err := os.CreateTemp("", "ai-launcher-linux-")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	_ = tmp.Close()
	cmd := exec.Command("go", "build", "-o", path, filepath.Join(src, "cmd", "ai-launcher")) // #nosec G204 G702 -- fixed argv, no shell, module root from the filesystem or GO_SRC_PATH
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("cross-compile launcher for the image: %w", err)
	}
	return path, nil
}

// isWorkstreamConflict reports whether a launch error is ai-memory's
// "409 workstream is already active" — a busy or stale lease that a parallel
// workstream (--new) sidesteps. The launcher uses it to arm that recovery.
func isWorkstreamConflict(errText string) bool {
	return strings.Contains(errText, "409") && strings.Contains(errText, "workstream is already active")
}

// launchFailureHint maps common child stderr/exit text to a short recovery tip.
func launchFailureHint(errText string) string {
	switch {
	case strings.Contains(errText, "409") && strings.Contains(errText, "workstream is already active"):
		return "hint: another ai-memory run still holds this workstream (or left a stale lease).\n" +
			"  - wait until the lease time in the error, then retry\n" +
			"  - if the owner PID is dead: just retry (the lease expires within 90 seconds)\n" +
			"  - or start a parallel stream: ai-launcher --agent <name> --new my-stream\n" +
			"  - or skip memory: ai-launcher --agent <name> --no-memory"
	case strings.Contains(errText, "401") || strings.Contains(errText, "Unauthorized") || strings.Contains(errText, "auth required"):
		return "hint: ai-memory rejected the token (401). Set memory_auth_token in the global config (~/.config/ai-launch/config.yaml), or use --no-memory"
	case strings.Contains(errText, "certificate") || strings.Contains(errText, "TLS") || strings.Contains(errText, "x509"):
		return "hint: memory server TLS failed — check memory_server_url (expect *.internal.lgldsilva.com.br) or use --no-memory"
	case strings.Contains(errText, "canonicalizing managed run cwd") || strings.Contains(errText, "Operation not permitted"):
		return "hint: ai-memory inside the jail failed on this cwd — try --no-memory (keep Jail) or run from a path under $HOME"
	case strings.Contains(errText, "keychain"):
		return "hint: the agent could not access the macOS keychain inside the jail — try launching from a path under $HOME, or run with --no-jail"
	default:
		return "hint: try --no-memory if the memory server fails, or --no-jail if the project is on an external volume"
	}
}

// launchFailureStatus turns an execute error into a concise status block for
// the TUI recovery screen: a one-line call to action plus the matching hint.
// It is shown when the TUI is re-opened after a launch failure so the operator
// knows what went wrong and how to recover before pressing r again.
func launchFailureStatus(errText string) string {
	return "Last launch failed — adjust and press r to retry, or q to quit:\n" + launchFailureHint(errText)
}

func saveIfRequested(save bool, path string, local config.Local, launch launcher.LaunchConfig) error {
	if !save {
		return nil
	}
	local.Agent = launch.Agent.Command
	local.Permissions = launch.Permissions
	local.Mounts = launch.Mounts
	local.Options.Jail = launch.UseJail
	local.Options.Docker = launch.UseDocker
	local.Options.Stacks = launch.Docker.Selection.Stacks
	local.Options.Memory = launch.UseMemory
	local.Options.Yolo = launch.Yolo
	local.Options.Fresh = launch.Fresh
	local.Options.NewWorkstream = launch.NewWorkstream
	local.Options.Workstream = launch.Workstream
	local.Options.Workspace = launch.Workspace
	local.Options.Project = launch.Project
	local.Options.JailFlags = launch.JailFlags
	local.Options.ExtraArgs = launch.ExtraArgs
	local.Options.ParamValues = launch.ParamValues
	return config.SaveLocal(path, local)
}

// saveLocalSelection persists the selection and records the written file's
// hash in the trusted global config. Provenance is what lets the next launch
// honor the operator's own saved choices (including jail: false) instead of
// refusing the file as repo-supplied input — the trust boundary is about who
// wrote the file, and only the launcher can add a hash to the global config.
func saveLocalSelection(globalPath string, save bool, path string, local config.Local, launch launcher.LaunchConfig) error {
	if err := saveIfRequested(save, path, local, launch); err != nil || !save {
		return err
	}
	return config.RecordTrustedLocalConfig(globalPath, path)
}

// projectJailConfigSymlink reports whether the working directory's .ai-jail
// entry is a symlink and, if so, where it resolves. A checkout-controlled
// symlink here can change what ai-jail reads and writes when config masking is
// disabled, so the trust boundary needs to see it before the launch proceeds.
func projectJailConfigSymlink() (link, target string, ok bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", false
	}
	link = filepath.Join(wd, ".ai-jail")
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", "", false
	}
	target, err = filepath.EvalSymlinks(link)
	if err != nil {
		return link, "", true
	}
	return link, target, true
}

// symlinkedProjectJailConfig returns a pointer to false when the working
// directory's .ai-jail file is a symlink, nil otherwise. ai-jail's default
// --hide-config masks <project>/.ai-jail with a bind mount, which bwrap
// cannot create over a symlink ("Can't create file ... No such file or
// directory"), so the mask must be disabled for that project.
func symlinkedProjectJailConfig() *bool {
	_, _, ok := projectJailConfigSymlink()
	if !ok {
		return nil
	}
	disable := false
	return &disable
}

// applyJailAutoDetection adds the mounts and flags the launcher infers from the
// host, announcing each one. ai-jail recreates home dotfile symlinks inside the
// sandbox without their targets, so the resolved targets are mounted to keep
// them resolving; bwrap cannot mask a symlinked .ai-jail, so config masking is
// turned off for such a project. Both are automatic, which is exactly why they
// are printed: a silent widening of the sandbox is one an operator will trust.
func applyJailAutoDetection(cfg launcher.LaunchConfig, home string, errOut io.Writer) launcher.LaunchConfig {
	auto, refused := launcher.HomeSymlinkMounts(home)
	for _, entry := range refused {
		warnf(errOut, "not auto-mounting %s -> %s (%s)", entry.Link, entry.Target, entry.Reason)
	}
	for _, mount := range auto {
		_, _ = fmt.Fprintf(errOut, "ai-launcher: auto-mounting %s (%s), a home symlink target outside $HOME\n", mount.Path, mount.Mode)
	}
	required := launcher.AgentRequiredMounts(cfg.Agent, home, runtime.GOOS)
	for _, mount := range required {
		_, _ = fmt.Fprintf(errOut, "ai-launcher: auto-mounting %s (%s), required by %s\n", mount.Path, mount.Mode, cfg.Agent.Command)
	}
	cfg.Mounts = launcher.MergeAutoMounts(cfg.Mounts, append(auto, required...))
	if cfg.JailFlags.HideConfig != nil {
		return cfg
	}
	if hide := symlinkedProjectJailConfig(); hide != nil {
		warnf(errOut, ".ai-jail in this project is a symlink; ai-jail config masking is disabled for this launch (--no-hide-config)")
		cfg.JailFlags.HideConfig = hide
	}
	return cfg
}

// buildMountConfig resolves the effective mount list: explicit --mount / --map
// / --rw-map flags replace the configured list entirely; otherwise the
// configured mounts stand, falling back to the catalog default_mounts.
func (o *cliOptions) buildMountConfig(flags *flag.FlagSet, configured []config.Mount, defaultMounts []string) ([]config.Mount, error) {
	if flagsWasSet(flags, "mount") || flagsWasSet(flags, "map") || flagsWasSet(flags, "rw-map") {
		mounts, err := parseMounts(o.mounts, config.MountReadOnly)
		if err != nil {
			return nil, err
		}
		writable, err := parseMounts(o.rwMounts, config.MountReadWrite)
		if err != nil {
			return nil, err
		}
		return append(mounts, writable...), nil
	}
	if len(configured) > 0 {
		return append([]config.Mount(nil), configured...), nil
	}
	// Built-in / catalog default_mounts are suggestions. Skip paths that do not
	// exist on this host so a Linux layout on macOS (or an unplugged external
	// volume) does not fail pre-flight validation.
	return parseMounts(config.ExistingPaths(defaultMounts), config.MountReadWrite)
}

// parseMounts parses every value with the same default mode, failing on the
// first invalid entry.
func parseMounts(values []string, defaultMode string) ([]config.Mount, error) {
	mounts := make([]config.Mount, 0, len(values))
	for _, value := range values {
		mount, err := parseMount(value, defaultMode)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, mount)
	}
	return mounts, nil
}

// copyPermissions clones the map so the CLI flag pass never mutates the loaded
// local config, which is still needed unchanged for --save.
func copyPermissions(permissions map[string]bool) map[string]bool {
	clone := make(map[string]bool, len(permissions))
	for id, enabled := range permissions {
		clone[id] = enabled
	}
	return clone
}

// parseMount splits "PATH[:MODE]" from the last colon only, so a directory
// whose final element is literally "ro" keeps it. The path must be absolute —
// a relative mount is ambiguous once the sandbox changes the working
// directory — and is cleaned, matching what the TUI mount manager already does.
func parseMount(value, defaultMode string) (config.Mount, error) {
	path, mode := value, defaultMode
	if index := strings.LastIndex(value, ":"); index >= 0 {
		switch suffix := value[index+1:]; suffix {
		case config.MountReadOnly, config.MountReadWrite, config.MountReadOnlyLabel:
			path, mode = value[:index], suffix
		}
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return config.Mount{}, fmt.Errorf("mount %q has no path", value)
	}
	if !filepath.IsAbs(path) {
		return config.Mount{}, fmt.Errorf("mount %q is not an absolute path", value)
	}
	return config.Mount{Path: filepath.Clean(path), Mode: mode}, nil
}

// classifyTUIError maps a TUI result to a process outcome. Only a deliberate
// cancellation is a quiet exit; anything else (a terminal that cannot be
// initialized, for example) is a real failure and must not exit 0 in silence.
func classifyTUIError(err error) error {
	if err == nil || errors.Is(err, tui.ErrCancelled) {
		return nil
	}
	return err
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
		// Sanitize after quoting so ESC/CSI from repo config cannot reprogram
		// the terminal when dry-run or the launch banner print the argv.
		parts[i] = launcher.SanitizeDisplay(shellQuote(value))
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
	parser := &argParser{}
	for _, r := range input {
		parser.feed(r)
	}
	return parser.finish()
}

// argParser holds the state of a splitArgs scan: the accumulated words, the
// word being built, and the quoting/escaping mode.
type argParser struct {
	result    []string
	current   strings.Builder
	quote     rune
	escaped   bool
	haveValue bool
}

// feed consumes one rune in the current scanning mode.
func (p *argParser) feed(r rune) {
	if p.escaped {
		p.write(r)
		p.escaped = false
		return
	}
	if p.quote != 0 {
		p.feedQuoted(r)
		return
	}
	p.feedBare(r)
}

// feedQuoted consumes one rune inside a quoted section. A backslash only
// escapes inside double quotes; inside single quotes it is literal.
func (p *argParser) feedQuoted(r rune) {
	switch {
	case r == p.quote:
		p.quote = 0
	case p.quote != '\'' && r == '\\':
		p.escaped = true
	default:
		p.write(r)
	}
}

// feedBare consumes one rune outside any quoting.
func (p *argParser) feedBare(r rune) {
	switch r {
	case '\'', '"':
		p.quote = r
		p.haveValue = true
	case '\\':
		p.escaped = true
		p.haveValue = true
	case ' ', '\t', '\n', '\r':
		p.flush()
	default:
		p.write(r)
	}
}

// write appends a rune to the word being built.
func (p *argParser) write(r rune) {
	p.current.WriteRune(r)
	p.haveValue = true
}

// flush closes the word being built, if any.
func (p *argParser) flush() {
	if p.haveValue {
		p.result = append(p.result, p.current.String())
		p.current.Reset()
		p.haveValue = false
	}
}

// finish validates the end state and returns the accumulated words.
func (p *argParser) finish() ([]string, error) {
	if p.escaped {
		return nil, errors.New("trailing escape")
	}
	if p.quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	p.flush()
	return p.result, nil
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
