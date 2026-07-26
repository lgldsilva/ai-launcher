package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/catalog"
	launchcmd "github.com/lgldsilva/ai-launcher/internal/cmd"
	"github.com/lgldsilva/ai-launcher/internal/config"
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
	workspace, project             string
	profile, saveProfile           string
	deleteProfile                  string
	ssh, gh, docker, gpu           bool
	display, pictures              bool
	tailscale, systemdUser         bool
	mise, worktree                 bool
	noJail, sandbox                bool
	memory, noMemory               bool
	yolo, noYolo                   bool
	fresh                          bool
	dryRun, save, install, upgrade bool
	listProfiles                   bool
	continueSession                bool
	showVersion, doctor            bool
}

func (o *cliOptions) register(flags *flag.FlagSet) {
	flags.StringVar(&o.agent, "agent", "", "agent command (claude, codex, opencode, ...)")
	flags.BoolVar(&o.ssh, "ssh", false, "enable SSH permission")
	flags.BoolVar(&o.gh, "gh", false, "optional: map ~/.config/gh into the jail (for host gh auth; not required)")
	flags.BoolVar(&o.docker, "docker", false, "enable Docker permission")
	flags.BoolVar(&o.gpu, "gpu", false, "enable GPU permission")
	flags.BoolVar(&o.display, "display", false, "force X11/Wayland display passthrough in the jail (Linux only)")
	flags.BoolVar(&o.pictures, "pictures", false, "map the Pictures folder into the jail (Linux/macOS)")
	flags.BoolVar(&o.tailscale, "tailscale", false, "expose the Tailscale socket in the jail (Linux/macOS)")
	flags.BoolVar(&o.systemdUser, "systemd-user", false, "expose the systemd user bus in the jail (Linux only)")
	flags.BoolVar(&o.mise, "mise", false, "enable the ai-jail mise integration")
	flags.BoolVar(&o.worktree, "worktree", false, "enable Git worktree passthrough in the jail")
	flags.BoolVar(&o.noJail, "no-jail", false, "run without ai-jail")
	flags.BoolVar(&o.sandbox, "sandbox", false, "enable ai-jail (alias for the default sandbox)")
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
	flags.StringVar(&o.addName, "add", "", "add or update an agent in the global catalog")
	flags.StringVar(&o.addPath, "path", "", "executable path used with --add")
	flags.StringVar(&o.addCommand, "command", "", "command name used with --add; defaults to the executable basename")
	flags.StringVar(&o.addDescription, "description", "", "description used with --add")
	flags.StringVar(&o.profile, "profile", "", "load the named profile from the global config as the base selection (precedence: built-in defaults < local .ai-launch.yaml < profile < explicit flags)")
	flags.StringVar(&o.saveProfile, "save-profile", "", "save the fully merged selection as the named profile in the global config and exit without launching")
	flags.BoolVar(&o.listProfiles, "list-profiles", false, "list the profiles saved in the global config and exit")
	flags.StringVar(&o.deleteProfile, "delete-profile", "", "delete the named profile from the global config and exit")
	flags.BoolVar(&o.showVersion, "version", false, "print the binary version and exit")
	flags.BoolVar(&o.doctor, "doctor", false, "report the installed upstream tool versions against the supported floor and exit")
}

// applyToLocal folds every explicitly-set flag into the local configuration
// and returns the effective mount list. Flag mounts replace configured ones;
// when neither flags nor the local/profile config define any mount, the
// global default_mounts are suggested (read-write by default, with the same
// optional :ro/:rw suffix as --mount).
func (o *cliOptions) applyToLocal(flags *flag.FlagSet, local *config.Local, defaultMounts []string) ([]config.Mount, error) {
	if flagsWasSet(flags, "no-jail") {
		local.Options.Jail = !o.noJail
	}
	if flagsWasSet(flags, "sandbox") {
		local.Options.Jail = o.sandbox
	}
	if flagsWasSet(flags, "memory") {
		local.Options.Memory = o.memory
	}
	// Same semantic as --no-jail and --no-yolo: the flag inverts. Special-casing
	// this one to "only ever disable" made --no-memory=false silently inert.
	if flagsWasSet(flags, "no-memory") {
		local.Options.Memory = !o.noMemory
	}
	if flagsWasSet(flags, "fresh") {
		local.Options.Fresh = o.fresh
	}
	if flagsWasSet(flags, "yolo") {
		local.Options.Yolo = o.yolo
	}
	if flagsWasSet(flags, "no-yolo") {
		local.Options.Yolo = !o.noYolo
	}
	if flagsWasSet(flags, "new") {
		local.Options.NewWorkstream = o.newWorkstream
	}
	if flagsWasSet(flags, "workstream") {
		local.Options.Workstream = o.workstream
	}
	if flagsWasSet(flags, "workspace") {
		local.Options.Workspace = o.workspace
	}
	if flagsWasSet(flags, "project") {
		local.Options.Project = o.project
	}
	mountConfig, err := o.buildMountConfig(flags, local.Mounts, defaultMounts)
	if err != nil {
		return nil, err
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

// resolveAgentSelection picks the agent (the --agent flag wins over the local
// config) and records what the workspace file asked for, so the trust check can
// tell an operator's choice from a repository's. An unresolved agent is still
// synthesized here; enforceLocalConfigTrust decides whether it may be used.
func resolveAgentSelection(catalogue catalog.Catalog, flagAgent string, local config.Local) (catalog.AgentStatus, localTrust) {
	selected := flagAgent
	if selected == "" {
		selected = local.Agent
	}
	status, err := catalogue.Resolve(selected)
	trust := localTrust{
		agent:      local.Agent,
		agentKnown: err == nil,
		fromFile:   flagAgent == "",
		jail:       local.Options.Jail,
		mounts:     local.Mounts,
	}
	if err != nil {
		return catalog.AgentStatus{Agent: config.Agent{Name: selected, Command: selected}}, trust
	}
	if status.ResolvedCommand != "" {
		// Keep the configured catalog name for display, but invoke the alias
		// that was actually found on this machine (for example kilocode).
		status.Agent.Command = status.ResolvedCommand
	}
	return status, trust
}

// localTrust captures the security-relevant values a workspace-local
// .ai-launch.yaml supplied, before CLI flags are layered on top of them.
type localTrust struct {
	agent      string
	agentKnown bool
	// fromFile is false once --agent was given: the operator's own choice.
	fromFile bool
	jail     bool
	mounts   []config.Mount
}

// enforceLocalConfigTrust refuses a workspace-local config that lowers the
// security posture on its own. A .ai-launch.yaml travels with the repository,
// so for any checkout the operator did not write it is attacker-supplied
// input: left unchecked it picks the executed binary, turns the sandbox off,
// and mounts whatever it likes. What the operator types on the command line
// stays fully trusted — the boundary is around the file, not around the user.
//
// Refusal rather than a prompt: a launcher run is routinely non-interactive
// (scripts, CI, the --dry-run diagnostic), and the plan requires those to
// refuse. Each refusal names the explicit opt-in that accepts the risk.
func enforceLocalConfigTrust(flags *flag.FlagSet, global config.Global, trust localTrust) error {
	if trust.fromFile && strings.TrimSpace(trust.agent) != "" && !trust.agentKnown {
		return fmt.Errorf("local config selects agent %q, which the catalog cannot resolve; "+
			"run it explicitly with --agent %s, or register it in the global catalog with --add",
			trust.agent, trust.agent)
	}
	if globalRequiresJail(global) && !trust.jail &&
		!flagsWasSet(flags, "no-jail") && !flagsWasSet(flags, "sandbox") {
		return errors.New("local config disables the sandbox (options.jail: false) " +
			"while the global catalog defaults it on; pass --no-jail to accept that explicitly")
	}
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
	}
	return nil
}

// globalRequiresJail reports whether the trusted global catalog defaults the
// sandbox on. An operator who turned it off globally is not being downgraded
// by a local config that agrees with them.
func globalRequiresJail(global config.Global) bool {
	for _, permission := range global.Permissions {
		if permission.ID == "jail" {
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
		label := "error:"
		if issue.Warning {
			label = "warning:"
		}
		_, _ = fmt.Fprintln(errOut, label, issue)
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

func run(args []string, in io.Reader, out, errOut io.Writer) error {
	// A positional `upgrade` as the first argument is the self-update
	// subcommand (mirroring semidx). It must be intercepted before the launch
	// flag parsing, which would otherwise treat it as extra agent arguments.
	// It is unrelated to the --upgrade flag, which forces a reinstall of the
	// configured third-party tools.
	if len(args) > 0 && args[0] == "upgrade" {
		return runUpgrade(args[1:], out, errOut)
	}
	flags := flag.NewFlagSet("ai-launcher", flag.ContinueOnError)
	flags.SetOutput(errOut)
	var opts cliOptions
	opts.register(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if handled, err := reportInfoFlags(opts, out); handled {
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
		return launchcmd.AddAgent(opts.globalPath, opts.addName, opts.addPath, opts.addCommand, opts.addDescription, out)
	}
	global, globalErr := config.LoadGlobal(opts.globalPath)
	if globalErr != nil {
		_, _ = fmt.Fprintln(errOut, "warning:", globalErr)
	}
	if opts.install || opts.upgrade {
		return launchcmd.InstallConfigured(global, opts.agent, home, opts.upgrade, out, errOut)
	}
	if opts.listProfiles {
		listProfiles(global, out)
		return nil
	}
	if opts.deleteProfile != "" {
		return deleteProfile(opts.globalPath, global, opts.deleteProfile, out)
	}
	// A corrupt local config degrades exactly like a corrupt global one:
	// warn and continue with safe defaults. LoadLocal already returns
	// DefaultLocal() on error, so blocking every launch on a broken
	// .ai-launch.yaml was pure asymmetry, not extra safety.
	local, localErr := config.LoadLocal(opts.localPath)
	if localErr != nil {
		_, _ = fmt.Fprintln(errOut, "warning:", localErr)
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

	status, trust := resolveAgentSelection(catalogue, opts.agent, local)
	// Normalization resolves permission dependencies (gpu requires docker) and
	// drops ids the catalog does not declare. It has to run *after* every input
	// is merged: normalizing first left a dependency pulled in by a CLI flag
	// unresolved, so --gpu produced an argv without --docker while the TUI,
	// which re-normalizes on each toggle, produced the right one.
	permissions := copyPermissions(local.Permissions)
	applyBoolFlag(flags, "ssh", permissions, "ssh", opts.ssh)
	applyBoolFlag(flags, "gh", permissions, "gh", opts.gh)
	applyBoolFlag(flags, "docker", permissions, "docker", opts.docker)
	applyBoolFlag(flags, "gpu", permissions, "gpu", opts.gpu)
	applyBoolFlag(flags, "display", permissions, "display", opts.display)
	applyBoolFlag(flags, "pictures", permissions, "pictures", opts.pictures)
	applyBoolFlag(flags, "tailscale", permissions, "tailscale", opts.tailscale)
	applyBoolFlag(flags, "systemd-user", permissions, "systemd-user", opts.systemdUser)
	applyBoolFlag(flags, "mise", permissions, "mise", opts.mise)
	applyBoolFlag(flags, "worktree", permissions, "worktree", opts.worktree)
	permissions = catalogue.NormalizePermissions(permissions)
	if err := enforceLocalConfigTrust(flags, global, trust); err != nil {
		return err
	}
	mountConfig, err := opts.applyToLocal(flags, &local, global.DefaultMounts)
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
		MemoryAuthToken: global.MemoryAuthToken,
		UseJail:         local.Options.Jail,
		UseMemory:       local.Options.Memory,
		ContinueSession: opts.continueSession,
		JailExec:        len(args) > 0,
		NewWorkstream:   local.Options.NewWorkstream,
		Workstream:      local.Options.Workstream,
		Workspace:       local.Options.Workspace,
		Project:         local.Options.Project,
		JailFlags:       local.Options.JailFlags,
		Permissions:     permissions,
		Mounts:          mountConfig,
		Yolo:            local.Options.Yolo,
		Fresh:           local.Options.Fresh,
		ExtraArgs:       local.Options.ExtraArgs,
		ParamValues:     local.Options.ParamValues,
	}
	// Drop integrations the host cannot run (ai-jail on Windows) with an
	// explanatory warning. macOS keeps the jail — sandbox-exec is supported.
	var platformIssues []launcher.Issue
	launchConfig, platformIssues = launcher.ConstrainToPlatform(launchConfig, runtime.GOOS, global.Permissions)
	for _, issue := range platformIssues {
		_, _ = fmt.Fprintln(errOut, "warning:", issue)
	}
	if launchConfig.UseJail {
		launchConfig = applyJailAutoDetection(launchConfig, home, errOut)
	}
	if opts.saveProfile != "" {
		return saveProfileCommand(opts.globalPath, global, opts.saveProfile, launchConfig, out)
	}
	return launch(args, opts, global, local, launchConfig, in, out, errOut)
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
		_, _ = fmt.Fprintln(errOut, "warning: AI_LAUNCHER_UPDATE_TOKEN withheld: the update endpoint is overridden and the token is only sent to the default release host")
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

// launch confirms the configuration (TUI when interactive), optionally saves
// it, and builds, validates, and executes the resulting argv.
func launch(args []string, opts cliOptions, global config.Global, local config.Local, launchConfig launcher.LaunchConfig, in io.Reader, out, errOut io.Writer) error {
	fromTUI := len(args) == 0
	if fromTUI {
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
			// A cancellation is a quiet exit; any other failure is reported.
			return classifyTUIError(err)
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
	// Validate before printing: --dry-run is the advertised diagnostic surface,
	// so it must not present a command pre-flight would reject. The argv is
	// still printed when there are issues — seeing what would run is the point.
	printOnly := decideLaunchAction(opts.dryRun) == actionPrint
	issues := launcher.NewValidator().WithPermissions(global.Permissions).Validate(launchConfig)
	fatal := reportPreflight(errOut, issues)
	if printOnly {
		_, _ = fmt.Fprintln(out, shellJoin(argv))
	}
	if fatal {
		return errors.New("pre-flight validation failed")
	}
	if printOnly {
		return nil
	}
	// Remember the harness for the TUI MRU list (best-effort; never block
	// launch). Only the MRU list is persisted: writing the whole merged catalog
	// on every launch froze that release's built-ins into the user's config.
	if !launchConfig.ContinueSession {
		if cmd := strings.TrimSpace(launchConfig.Agent.Command); cmd != "" {
			config.TouchRecentAgent(&global, cmd)
			_ = config.SaveRecentAgents(opts.globalPath, global.RecentAgents)
		}
	}
	label := launchConfig.Agent.Command
	if launchConfig.ContinueSession {
		label = "continue session"
	}
	cmdLine := shellJoin(argv)
	// Always announce what is about to run so a TUI close is never silent.
	_, _ = fmt.Fprintf(errOut, "ai-launcher: starting %s\n", label)
	_, _ = fmt.Fprintf(errOut, "ai-launcher: %s\n", cmdLine)

	// After the interactive TUI, keep ai-launcher as the parent (PTY) so a
	// child that exits immediately (ai-memory TLS, jail cwd, etc.) is reported
	// as our error instead of looking like the UI just vanished.
	// CLI non-TUI launches still Replace into the child for a thin process tree.
	useReplace := !fromTUI && in == os.Stdin && out == os.Stdout && errOut == os.Stderr
	if useReplace {
		if err := launcher.ReplaceWithEnv(argv, launcher.Environment(launchConfig)); err != nil {
			return fmt.Errorf("failed to start %s: %w\ncommand: %s", label, err, cmdLine)
		}
		return nil
	}
	execErr := (launcher.PTYExecutor{}).RunWithEnv(context.Background(), argv, launcher.Environment(launchConfig), in, out, errOut)
	if execErr != nil {
		return fmt.Errorf("failed to start %s: %w\ncommand: %s\n%s", label, execErr, cmdLine, launchFailureHint(execErr.Error()))
	}
	return nil
}

// launchFailureHint maps common child stderr/exit text to a short recovery tip.
func launchFailureHint(errText string) string {
	switch {
	case strings.Contains(errText, "409") && strings.Contains(errText, "workstream is already active"):
		return "hint: another ai-memory run still holds this workstream (or left a stale lock).\n" +
			"  - wait until the lease time in the error, then retry\n" +
			"  - if the owner PID is dead: just retry (lease expires in ~1–2 min)\n" +
			"  - or start a parallel stream: ai-launcher --agent <name> --new my-stream\n" +
			"  - or skip memory: ai-launcher --agent <name> --no-memory"
	case strings.Contains(errText, "certificate") || strings.Contains(errText, "TLS") || strings.Contains(errText, "x509"):
		return "hint: memory server TLS failed — check memory_server_url (expect *.internal.lgldsilva.com.br) or use --no-memory"
	case strings.Contains(errText, "canonicalizing managed run cwd") || strings.Contains(errText, "Operation not permitted"):
		return "hint: ai-memory inside the jail failed on this cwd — try --no-memory (keep Jail) or run from a path under $HOME"
	default:
		return "hint: try --no-memory if the memory server fails, or --no-jail if the project is on an external volume"
	}
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

// symlinkedProjectJailConfig returns a pointer to false when the working
// directory's .ai-jail file is a symlink, nil otherwise. ai-jail's default
// --hide-config masks <project>/.ai-jail with a bind mount, which bwrap
// cannot create over a symlink ("Can't create file ... No such file or
// directory"), so the mask must be disabled for that project.
func symlinkedProjectJailConfig() *bool {
	wd, err := os.Getwd()
	if err != nil {
		return nil
	}
	info, err := os.Lstat(filepath.Join(wd, ".ai-jail"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
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
		_, _ = fmt.Fprintf(errOut, "warning: not auto-mounting %s -> %s (%s)\n", entry.Link, entry.Target, entry.Reason)
	}
	for _, mount := range auto {
		_, _ = fmt.Fprintf(errOut, "ai-launcher: auto-mounting %s (%s), a home symlink target outside $HOME\n", mount.Path, mount.Mode)
	}
	cfg.Mounts = launcher.MergeAutoMounts(cfg.Mounts, auto)
	if cfg.JailFlags.HideConfig != nil {
		return cfg
	}
	if hide := symlinkedProjectJailConfig(); hide != nil {
		_, _ = fmt.Fprintln(errOut, "warning: .ai-jail in this project is a symlink; ai-jail config masking is disabled for this launch (--no-hide-config)")
		cfg.JailFlags.HideConfig = hide
	}
	return cfg
}

// buildMountConfig resolves the effective mount list: explicit --mount / --map
// / --rw-map flags replace the configured list entirely; otherwise the
// configured mounts stand, falling back to the catalog default_mounts.
func (o *cliOptions) buildMountConfig(flags *flag.FlagSet, configured []config.Mount, defaultMounts []string) ([]config.Mount, error) {
	if flagsWasSet(flags, "mount") || flagsWasSet(flags, "map") || flagsWasSet(flags, "rw-map") {
		mounts, err := parseMounts(o.mounts, "ro")
		if err != nil {
			return nil, err
		}
		writable, err := parseMounts(o.rwMounts, "rw")
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
	return parseMounts(config.ExistingPaths(defaultMounts), "rw")
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
		case "ro", "rw", "read-only":
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
