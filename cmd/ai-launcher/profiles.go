package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

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
	runtimePreference := strings.TrimSpace(launch.ContainerRuntime)
	if runtimePreference == "" && launch.Docker.Runtime != nil {
		runtimePreference = launch.Docker.Runtime.Name()
	}
	return config.Profile{
		Agent:       launch.Agent.Command,
		Permissions: launch.Permissions,
		Mounts:      launch.Mounts,
		Options: &config.Options{
			Jail:                  launch.UseJail,
			Docker:                launch.UseDocker,
			ContainerRuntime:      config.EffectiveContainerRuntime(runtimePreference),
			ContainerContext:      strings.TrimSpace(launch.ContainerContext),
			Stacks:                launch.Docker.Selection.Stacks,
			Services:              append([]string(nil), launch.Services...),
			ContainerEnvironment:  cloneStringMap(launch.ContainerEnvironment),
			ContainerServicePorts: cloneServicePortMappings(launch.ContainerServicePorts),
			ContainerDependencies: launch.ContainerDependencies.Clone(),
			ContainerTmux:         launch.ContainerTmux,
			ContainerMemory:       launch.Docker.MemoryLimit,
			ContainerCPUs:         launch.Docker.CPULimit,
			ContainerPIDs:         launch.Docker.PIDsLimit,
			ContainerPorts:        append([]config.PortMapping(nil), launch.Docker.ExposedPorts...),
			ContainerNetwork:      launch.Docker.NetworkName,
			Memory:                launch.UseMemory,
			Yolo:                  launch.Yolo,
			Fresh:                 launch.Fresh,
			NewWorkstream:         launch.NewWorkstream,
			Workstream:            launch.Workstream,
			Workspace:             launch.Workspace,
			Project:               launch.Project,
			JailFlags:             launch.JailFlags,
			ExtraArgs:             launch.ExtraArgs,
			ParamValues:           launch.ParamValues,
		},
	}
}
