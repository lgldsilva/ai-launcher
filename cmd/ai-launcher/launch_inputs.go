package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

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
	local, warnings, localErr := config.LoadLocalWithWarnings(opts.localPath)
	if localErr != nil {
		warnf(errOut, valueFormat, localErr)
	}
	for _, warning := range warnings {
		warnf(errOut, valueFormat, warning)
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
	clone.Options.Services = append([]string(nil), local.Options.Services...)
	clone.Options.ContainerPorts = append([]config.PortMapping(nil), local.Options.ContainerPorts...)
	clone.Options.ContainerEnvironment = cloneStringMap(local.Options.ContainerEnvironment)
	clone.Options.ContainerServicePorts = cloneServicePortMappings(local.Options.ContainerServicePorts)
	clone.Options.ContainerDependencies = local.Options.ContainerDependencies.Clone()
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
	// --no-network is the negative form of a permission that defaults on, so
	// it clears rather than sets. Applied last: an explicit opt-out wins over
	// whatever the workspace file or the catalog default asked for.
	if flagsWasSet(flags, "no-network") && opts.noNetwork {
		permissions[config.PermissionNetwork] = false
	}
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
	// Resolve after platform/jail constraints so a TUI that later toggles
	// jail+memory is not the only caller that fills MemoryExecutable. The
	// TUI also calls ResolveHostBinaries before each Build.
	launchConfig = launcher.ResolveHostBinaries(launchConfig)
	if launchConfig.UseDocker && worktreePassthroughEnabled(launchConfig) {
		launchConfig = applyDockerWorktreeDiscovery(launchConfig, errOut)
	}
	return launchConfig
}

func worktreePassthroughEnabled(cfg launcher.LaunchConfig) bool {
	if cfg.Permissions[config.PermissionWorktree] {
		return true
	}
	return cfg.JailFlags.Worktree != nil && *cfg.JailFlags.Worktree
}

// worktreeSearchRoots lists the directories to ask Git about, most specific
// first. It reads the container project directory and the process cwd — never
// the ai-memory Workspace/Project scopes, which are names and not paths.
func worktreeSearchRoots(cfg launcher.LaunchConfig) []string {
	seen := make(map[string]struct{}, 2)
	roots := make([]string, 0, 2)
	for _, candidate := range []string{cfg.ProjectDir, mustGetwd()} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		roots = append(roots, candidate)
	}
	return roots
}

// applyDockerWorktreeDiscovery resolves only the Git worktrees registered by
// the selected repository. A Docker container otherwise sees just the current
// project bind mount, which makes sibling or external worktrees unavailable
// even when the operator explicitly enabled the worktree permission.
func applyDockerWorktreeDiscovery(cfg launcher.LaunchConfig, errOut io.Writer) launcher.LaunchConfig {
	var (
		mounts   []config.Mount
		lastErr  error
		lastRoot string
		found    bool
	)
	for _, root := range worktreeSearchRoots(cfg) {
		lastRoot = root
		candidate, err := launcher.DiscoverGitWorktreeMounts(root)
		if err != nil {
			lastErr = err
			continue
		}
		mounts = candidate
		found = true
		break
	}
	if !found && lastErr != nil {
		warnf(errOut, "worktree permission enabled but Git worktrees could not be discovered from %s: %v", lastRoot, lastErr)
		return cfg
	}
	for _, mount := range mounts {
		_, _ = fmt.Fprintf(errOut, "ai-launcher: discovered Git worktree %s (%s), exposing it to the container\n", mount.Path, mount.Mode)
	}
	seen := make(map[string]struct{}, len(cfg.Docker.WorktreeMounts)+len(mounts))
	for _, path := range cfg.Docker.WorktreeMounts {
		path = filepath.Clean(strings.TrimSpace(path))
		if path != "." && path != "" {
			seen[path] = struct{}{}
		}
	}
	for _, mount := range mounts {
		path := filepath.Clean(strings.TrimSpace(mount.Path))
		if path == "." || path == "" {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		cfg.Docker.WorktreeMounts = append(cfg.Docker.WorktreeMounts, path)
	}
	return cfg
}
