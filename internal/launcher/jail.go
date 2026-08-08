package launcher

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// jailPermission maps a catalog permission id to the ai-jail capability it
// enables. override names the config.JailFlags field that declares the same
// capability: when that field is set, the declarative value wins and the
// permission stays silent, so the argv never contradicts itself with
// "--no-tailscale --tailscale". mountHome replaces the flag with a read-write
// map of a path under $HOME (ai-jail has no dedicated flag for it).
//
// docker is deliberately not in this list: appendDockerDecision owns it,
// because it is the one capability that must be stated even when it is off.
type jailPermission struct {
	id        string
	flag      string
	mountHome string
	override  func(config.JailFlags) *bool
}

// jailPermissions lists the permission-driven jail arguments in their stable
// emission order.
func jailPermissions() []jailPermission {
	return []jailPermission{
		{id: config.PermissionSSH, flag: "--ssh"},
		{id: config.PermissionGitHub, mountHome: ".config/gh"},
		{id: config.PermissionGPU, flag: "--gpu", override: func(f config.JailFlags) *bool { return f.GPU }},
		{id: config.PermissionDisplay, flag: "--display", override: func(f config.JailFlags) *bool { return f.Display }},
		{id: config.PermissionPictures, flag: "--pictures"},
		{id: config.PermissionTailscale, flag: "--tailscale", override: func(f config.JailFlags) *bool { return f.Tailscale }},
		{id: config.PermissionSystemd, flag: "--systemd-user"},
		{id: config.PermissionMise, flag: "--mise", override: func(f config.JailFlags) *bool { return f.Mise }},
		{id: config.PermissionWorktree, flag: "--worktree", override: func(f config.JailFlags) *bool { return f.Worktree }},
	}
}

// appendPermissionArgs emits the jail arguments enabled by the permission map,
// skipping every capability the declarative jail_flags block already decided.
func appendPermissionArgs(command []string, cfg LaunchConfig) []string {
	for _, permission := range jailPermissions() {
		if !cfg.Permissions[permission.id] {
			continue
		}
		if permission.override != nil && permission.override(cfg.JailFlags) != nil {
			continue
		}
		if permission.mountHome != "" {
			mount, ok := homeMountPath(cfg.HomeDir, permission.mountHome)
			if !ok {
				// Fail closed: without a home directory the mount would be a
				// relative path whose meaning changes with the cwd.
				continue
			}
			if coveredByMounts(mount, cfg.Mounts) {
				continue
			}
			command = append(command, "--rw-map", mount)
			continue
		}
		command = append(command, permission.flag)
	}
	return command
}

// jailToggle declares one tri-state ai-jail capability flag.
type jailToggle struct {
	name  string
	value *bool
}

// jailToggles lists the ai-jail v1.16 capability flags in their stable
// emission order so the mapping stays declarative instead of a chain of ifs.
//
// docker is absent on purpose: unlike every other capability here it is never
// left to ai-jail's auto mode, so appendDockerDecision owns its emission.
func jailToggles(flags config.JailFlags) []jailToggle {
	return []jailToggle{
		{name: "lockdown", value: flags.Lockdown},
		{name: "private-home", value: flags.PrivateHome},
		{name: config.PermissionTailscale, value: flags.Tailscale},
		{name: config.PermissionGPU, value: flags.GPU},
		{name: config.PermissionDisplay, value: flags.Display},
		{name: config.PermissionMise, value: flags.Mise},
		{name: config.PermissionWorktree, value: flags.Worktree},
		{name: "landlock", value: flags.Landlock},
		{name: "seccomp", value: flags.Seccomp},
		{name: "rlimits", value: flags.Rlimits},
		{name: "status-bar", value: flags.StatusBar},
		{name: "hide-config", value: flags.HideConfig},
		{name: "save-config", value: flags.SaveConfig},
	}
}

// statusBarStyle normalizes the configured status bar theme, returning "" for
// unset or unrecognized values (which keeps the StatusBar boolean toggle in
// charge).
func statusBarStyle(flags config.JailFlags) string {
	switch strings.ToLower(strings.TrimSpace(flags.StatusBarStyle)) {
	case "dark", "light", "pastel":
		return strings.ToLower(strings.TrimSpace(flags.StatusBarStyle))
	}
	return ""
}

// appendJailFlags maps the declarative jail options to the exact ai-jail
// v1.16 CLI flags in a deterministic order: status bar style, toggles, list
// flags, browser profile, claude dir, and the v1.15 exception lists. A
// configured status bar style wins over the StatusBar toggle, so --no-status-bar
// is never emitted together with --status-bar=STYLE.
func appendJailFlags(command []string, flags config.JailFlags) []string {
	style := statusBarStyle(flags)
	if style != "" {
		command = append(command, "--status-bar="+style)
	}
	for _, toggle := range jailToggles(flags) {
		if toggle.name == "status-bar" && style != "" {
			continue
		}
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
	command = appendFlagValue(command, "--claude-dir", flags.ClaudeDir)
	for _, path := range flags.MaskExceptions {
		command = appendFlagValue(command, "--mask-except", path)
	}
	for _, path := range flags.DenyPathExceptions {
		command = appendFlagValue(command, "--deny-path-except", path)
	}
	for _, dir := range flags.HideDotdirs {
		command = appendFlagValue(command, "--hide-dotdir", dir)
	}
	return command
}

// appendJailToggle emits the positive or negative form of one capability flag.
// An unset (nil) toggle emits nothing, which leaves the capability in ai-jail's
// auto mode; forcing it on is a different state and must emit the positive
// form, so neither direction is ever silently dropped.
func appendJailToggle(command []string, toggle jailToggle) []string {
	if toggle.value == nil {
		return command
	}
	if *toggle.value {
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

// homeMountPath joins suffix to the operator's home directory, falling back
// to $HOME, and reports false when no home is known. Emitting the bare
// relative suffix instead would mount a path whose meaning silently changes
// with the working directory, so the mount is omitted (fail closed).
// filepath.Join also keeps home == "/" correct ("/.config/gh", not a path
// built by hand-trimming separators).
func homeMountPath(home, suffix string) (string, bool) {
	home = strings.TrimSpace(home)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("HOME"))
	}
	if home == "" {
		return "", false
	}
	return filepath.Join(home, suffix), true
}
