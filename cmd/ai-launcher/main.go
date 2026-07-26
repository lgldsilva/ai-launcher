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
	noJail, sandbox                bool
	memory, noMemory               bool
	yolo, noYolo                   bool
	dryRun, save, install, upgrade bool
	listProfiles                   bool
	continueSession                bool
	showVersion                    bool
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
	if flagsWasSet(flags, "no-memory") && o.noMemory {
		local.Options.Memory = false
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
	mountConfig := append([]config.Mount(nil), local.Mounts...)
	if flagsWasSet(flags, "mount") || flagsWasSet(flags, "map") || flagsWasSet(flags, "rw-map") {
		mountConfig = nil
		for _, value := range o.mounts {
			mountConfig = append(mountConfig, parseMount(value, "ro"))
		}
		for _, value := range o.rwMounts {
			mountConfig = append(mountConfig, parseMount(value, "rw"))
		}
	} else if len(mountConfig) == 0 {
		for _, value := range defaultMounts {
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
	if opts.showVersion {
		_, _ = fmt.Fprintf(out, "ai-launcher %s (%s, %s)\n", version, commit, date)
		return nil
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
	local, localErr := config.LoadLocal(opts.localPath)
	if localErr != nil {
		return localErr
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
		ExtraArgs:       local.Options.ExtraArgs,
		ParamValues:     local.Options.ParamValues,
	}
	// Drop integrations the platform cannot run (ai-jail on Windows) with an
	// explanatory warning instead of an error.
	var platformIssues []launcher.Issue
	launchConfig, platformIssues = launcher.ConstrainToPlatform(launchConfig, runtime.GOOS, global.Permissions)
	for _, issue := range platformIssues {
		_, _ = fmt.Fprintln(errOut, "warning:", issue)
	}
	if launchConfig.UseJail {
		// ai-jail recreates home dotfile symlinks inside the sandbox without
		// their targets; mount the resolved targets so they keep resolving.
		launchConfig.Mounts = launcher.MergeAutoMounts(launchConfig.Mounts, launcher.HomeSymlinkMounts(home))
		if launchConfig.JailFlags.HideConfig == nil {
			launchConfig.JailFlags.HideConfig = symlinkedProjectJailConfig()
		}
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
	updater := &selfupdate.Updater{
		CurrentVersion:  version,
		APIBaseURL:      envOr("AI_LAUNCHER_UPDATE_API", selfupdate.DefaultAPIBaseURL),
		DownloadBaseURL: envOr("AI_LAUNCHER_UPDATE_URL", selfupdate.DefaultDownloadBaseURL),
		Token:           os.Getenv("AI_LAUNCHER_UPDATE_TOKEN"),
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
	if decideLaunchAction(opts.dryRun) == actionPrint {
		_, _ = fmt.Fprintln(out, shellJoin(argv))
		return nil
	}
	issues := launcher.NewValidator().Validate(launchConfig)
	fatal := false
	for _, issue := range issues {
		_, _ = fmt.Fprintln(errOut, "warning:", issue)
		if !issue.Warning {
			fatal = true
		}
	}
	if fatal {
		return errors.New("pre-flight validation failed")
	}
	if in == os.Stdin && out == os.Stdout && errOut == os.Stderr {
		return launcher.ReplaceWithEnv(argv, launcher.Environment(launchConfig))
	}
	return launcher.PTYExecutor{}.RunWithEnv(context.Background(), argv, launcher.Environment(launchConfig), in, out, errOut)
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
