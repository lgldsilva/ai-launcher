package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

func saveIfRequestedResult(save bool, path string, local config.Local, launch launcher.LaunchConfig) (config.LocalSaveResult, error) {
	if !save {
		return config.LocalSaveResult{}, nil
	}
	local.Agent = launch.Agent.Command
	local.Permissions = launch.Permissions
	local.Mounts = launch.Mounts
	local.Options.Jail = launch.UseJail
	local.Options.Docker = launch.UseDocker
	local.Options.ContainerRuntime = config.EffectiveContainerRuntime(launch.ContainerRuntime)
	local.Options.ContainerContext = strings.TrimSpace(launch.ContainerContext)
	local.Options.Stacks = launch.Docker.Selection.Stacks
	local.Options.Services = append([]string(nil), launch.Services...)
	local.Options.ContainerNetworkAllowedDomains = append([]string(nil), launch.ContainerNetworkAllowedDomains...)
	local.Options.ContainerMemory = launch.Docker.MemoryLimit
	local.Options.ContainerCPUs = launch.Docker.CPULimit
	local.Options.ContainerPIDs = launch.Docker.PIDsLimit
	local.Options.ContainerPorts = append([]config.PortMapping(nil), launch.Docker.ExposedPorts...)
	local.Options.ContainerNetwork = launch.Docker.NetworkName
	local.Options.ContainerNetworkInternal = launch.ContainerNetworkInternal
	local.Options.ContainerHostGateway = launch.ContainerHostGateway
	local.Options.ContainerEnvironment = cloneStringMap(launch.ContainerEnvironment)
	local.Options.ContainerServicePorts = cloneServicePortMappings(launch.ContainerServicePorts)
	local.Options.ContainerDependencies = launch.ContainerDependencies.Clone()
	local.Options.ContainerTmux = launch.ContainerTmux
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
	return config.SaveLocalResult(path, local)
}

// saveLocalSelection persists the selection and records the written file's
// hash in the trusted global config. Provenance is what lets the next launch
// honor the operator's own saved choices (including jail: false) instead of
// refusing the file as repo-supplied input — the trust boundary is about who
// wrote the file, and only the launcher can add a hash to the global config.
func saveLocalSelection(globalPath string, save bool, path string, local config.Local, launch launcher.LaunchConfig, warningOut ...io.Writer) error {
	result, err := saveIfRequestedResult(save, path, local, launch)
	if err != nil || !save {
		return err
	}
	if result.Migrated {
		out := io.Writer(os.Stderr)
		if len(warningOut) > 0 && warningOut[0] != nil {
			out = warningOut[0]
		}
		warnf(out, "migrated legacy local config %s to %s (backup: %s)", result.LegacyPath, result.Path, result.BackupPath)
	}
	return config.RecordTrustedLocalConfig(globalPath, result.Path)
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

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func cloneServicePortMappings(values map[string][]config.PortMapping) map[string][]config.PortMapping {
	if values == nil {
		return nil
	}
	clone := make(map[string][]config.PortMapping, len(values))
	for serviceID, mappings := range values {
		clone[serviceID] = append([]config.PortMapping(nil), mappings...)
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
