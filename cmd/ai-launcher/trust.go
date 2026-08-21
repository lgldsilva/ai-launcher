package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

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
		trust.docker = raw.Options.Docker || hasContainerResources(raw.Options)
		trust.dockerReason = dockerBackendReason(raw.Options)
		trust.yolo = raw.Options.Yolo
		trust.extraArgs = append([]string(nil), raw.Options.ExtraArgs...)
		trust.jailFlags = raw.Options.JailFlags
		trust.paramValues = raw.Options.ParamValues
		trust.workspace = raw.Options.Workspace
		trust.project = raw.Options.Project
		trust.dependencies = raw.Options.ContainerDependencies.Clone()
		trust.tmuxAdditionalPaths = append([]string(nil), raw.Options.ContainerTmux.AdditionalPaths...)
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
	optionsRaw          bool // false when profile replaces options block
	agent               string
	agentKnown          bool
	fromFile            bool // false once --agent was given: the operator's own choice.
	jail                bool
	docker              bool   // container backend requested by the file
	dockerReason        string // the file key that requested it, for the refusal message
	mounts              []config.Mount
	rawPermissions      map[string]bool           // permissions from file (profile may overwrite)
	yolo                bool                      // file value when profile does not own options
	extraArgs           []string                  // file value when profile does not own options
	jailFlags           config.JailFlags          // file value when profile does not own options
	paramValues         map[string]string         // file value when profile does not own options
	workspace           string                    // file value when profile does not own options
	project             string                    // file value when profile does not own options
	dependencies        config.DependencySettings // file value when profile does not own options
	tmuxAdditionalPaths []string                  // file value when profile does not own options
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
// Refusal rather than a prompt for one-shot commands: a launcher run is
// routinely non-interactive (scripts, CI, the --dry-run diagnostic). The bare
// TUI is the interactive consent surface for the complete selection: it shows
// the agent, permissions, mounts, options, and container choices before the
// operator presses Run. It must therefore be allowed to open even when the
// workspace file contains values that would require a one-shot CLI opt-in.
func enforceLocalConfigTrust(flags *flag.FlagSet, global config.Global, trust localTrust, savedLocally, interactiveTUI bool) error {
	if savedLocally {
		return nil
	}
	if trust.fromFile && strings.TrimSpace(trust.agent) != "" && !trust.agentKnown {
		return fmt.Errorf("local config selects agent %q, which the catalog cannot resolve; "+
			"run it explicitly with --agent %s, or register it in the global catalog with --add",
			trust.agent, trust.agent)
	}
	// The agent is a hard boundary rather than a consent toggle: the TUI can
	// only ask the operator to choose from the catalog, so it must not open
	// with an unresolved repository-supplied executable already selected.
	if interactiveTUI {
		return nil
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
	if trust.optionsRaw && !trust.dependencies.IsZero() &&
		(trust.docker || flagsWasSet(flags, "docker-backend") || flagsWasSet(flags, "container-runtime") || containerResourceFlagSet(flags)) &&
		!flagsWasSet(flags, "save") {
		return errors.New("local config sets options.container_dependencies without operator consent; save the selection or move the dependency policy to trusted global config")
	}
	// container_tmux.additional_paths mounts arbitrary host files read-only
	// into the container, so it is the same class of repository-supplied mount
	// as container_dependencies: an unsaved local file must not widen what the
	// container sees without the operator saving or selecting a profile.
	if trust.optionsRaw && len(trust.tmuxAdditionalPaths) > 0 &&
		(trust.docker || flagsWasSet(flags, "docker-backend") || flagsWasSet(flags, "container-runtime") || containerResourceFlagSet(flags)) &&
		!flagsWasSet(flags, "save") {
		return errors.New("local config sets options.container_tmux.additional_paths without operator consent; save the selection or move the tmux settings to a trusted profile")
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
	if trust.optionsRaw && trust.docker &&
		!flagsWasSet(flags, "docker-backend") && !flagsWasSet(flags, "container-runtime") && !containerResourceFlagSet(flags) {
		return fmt.Errorf("local config enables the docker container backend (%s) "+
			"without operator consent; pass --docker-backend or --container-runtime to accept explicitly, or save the selection",
			trust.dockerReason)
	}
	return nil
}

// dockerBackendReason names the workspace-file key that asked for the container
// backend, so the refusal points at the line the operator has to look at. The
// message used to say "options.docker: true" unconditionally, which sent
// anyone whose file selected the backend through options.services or a
// resource limit looking for a key that is not there.
func dockerBackendReason(options config.Options) string {
	switch {
	case options.Docker:
		return "options.docker: true"
	case len(options.Services) > 0:
		return "options.services: " + strings.Join(options.Services, ", ")
	default:
		return "container resources under options"
	}
}

func containerResourceFlagSet(flags *flag.FlagSet) bool {
	for _, name := range []string{"container-memory", "cpus", "pids", "publish", "service", "service-port", "network", "container-context"} {
		if flagsWasSet(flags, name) {
			return true
		}
	}
	return false
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

// enforceDockerfileSymlink refuses a checkout-controlled Dockerfile symlink
// whenever container mode is active. The Dockerfile is a derived artifact,
// and materializing or trusting it through a symlink could write outside the
// workspace or make the selected build read an operator-unexpected file.
func enforceDockerfileSymlink(containerActive bool) error {
	if !containerActive {
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve workspace for container Dockerfile: %w", err)
	}
	path := filepath.Join(config.LocalConfigDir(wd), "Dockerfile")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect container Dockerfile: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	target, targetErr := filepath.EvalSymlinks(path)
	if targetErr != nil {
		return fmt.Errorf("%s is a broken symlink; container Dockerfile must be a regular file", path)
	}
	return fmt.Errorf("%s is a symlink to %s; container Dockerfile must be a regular file", path, target)
}

// enforcePermissionConsent requires an explicit CLI flag for every permission
// the unsaved local file turns on. The bare TUI is handled by the complete
// interactive branch in enforceLocalConfigTrust before this one-shot helper is
// reached. CLI --<permission> flags and saved selections skip this (the
// operator's own explicit choice). Extracted from enforceLocalConfigTrust (F1)
// to keep that function under the gocognit cap.
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
