package config

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// ValidateVersion reports whether a config schema version is compatible with
// this build: empty (pre-versioning) or any entry in LoadableVersions.
func ValidateVersion(version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil
	}
	for _, loadable := range LoadableVersions {
		if version == loadable {
			return nil
		}
	}
	return fmt.Errorf("unsupported config version %q (supported: %s)",
		version, strings.Join(LoadableVersions, ", "))
}

func mergeGlobalDefaults(defaults, cfg Global) Global {
	if cfg.Version == "" {
		cfg.Version = defaults.Version
	}
	if strings.TrimSpace(cfg.MemoryServerURL) == "" {
		cfg.MemoryServerURL = defaults.MemoryServerURL
	}
	cfg.Agents = mergeAgents(defaults.Agents, cfg.Agents)
	cfg.Permissions = mergePermissions(defaults.Permissions, cfg.Permissions)
	if len(cfg.Tools) == 0 {
		cfg.Tools = defaults.Tools
	}
	if len(cfg.DefaultMounts) == 0 {
		cfg.DefaultMounts = defaults.DefaultMounts
	}
	return cfg
}

// mergeAgents merges the built-in agent catalog with the user's, per entry,
// keyed by Command. A user field that is set wins; a field the user left empty
// falls back to the built-in default, so an entry written by hand — or
// materialized by an older release — no longer loses Memory.RunHarness,
// YoloFlag or Params. Replacing the list wholesale is what made the builder
// need a hardcoded "oc" remap.
func mergeAgents(defaults, user []Agent) []Agent {
	if len(user) == 0 {
		return defaults
	}
	byCommand := make(map[string]Agent, len(user))
	for _, agent := range user {
		byCommand[agent.Command] = agent
	}
	merged := make([]Agent, 0, len(defaults)+len(user))
	seen := make(map[string]bool, len(defaults))
	for _, base := range defaults {
		seen[base.Command] = true
		if override, ok := byCommand[base.Command]; ok {
			merged = append(merged, mergeAgent(base, override))
			continue
		}
		merged = append(merged, base)
	}
	for _, agent := range user {
		if !seen[agent.Command] {
			merged = append(merged, agent)
		}
	}
	return merged
}

// mergeAgent overlays one user entry on its built-in default. Booleans are
// taken from the user entry as-is: false is a meaningful value there (it is how
// an operator turns off memory support for an agent).
func mergeAgent(base, override Agent) Agent {
	merged := override
	if merged.Name == "" {
		merged.Name = base.Name
	}
	if len(merged.Aliases) == 0 {
		merged.Aliases = base.Aliases
	}
	if merged.SourceURL == "" {
		merged.SourceURL = base.SourceURL
	}
	if merged.NpmPackage == "" {
		merged.NpmPackage = base.NpmPackage
	}
	if !merged.AllowUnverified {
		merged.AllowUnverified = base.AllowUnverified
	}
	if !merged.SetupInteractive {
		merged.SetupInteractive = base.SetupInteractive
	}
	if !merged.NeedsNode {
		merged.NeedsNode = base.NeedsNode
	}
	if merged.Description == "" {
		merged.Description = base.Description
	}
	if merged.YoloFlag == "" {
		merged.YoloFlag = base.YoloFlag
	}
	if len(merged.Params) == 0 {
		merged.Params = base.Params
	}
	if merged.Release == nil {
		merged.Release = base.Release
	}
	if merged.Memory == nil {
		merged.Memory = base.Memory
	}
	return merged
}

// mergePermissions merges the built-in permission defaults with the user's
// configured list by ID. User entries win on conflict; defaults with IDs the
// user does not have are appended so new launcher releases surface their new
// permissions without clobbering the user's customizations.
func mergePermissions(defaults, user []Permission) []Permission {
	if len(user) == 0 {
		return defaults
	}
	byID := make(map[string]Permission, len(user))
	for _, permission := range user {
		byID[permission.ID] = permission
	}
	merged := make([]Permission, 0, len(defaults)+len(user))
	for _, permission := range defaults {
		if existing, ok := byID[permission.ID]; ok {
			merged = append(merged, existing)
		} else {
			merged = append(merged, permission)
		}
	}
	// Preserve any user-defined permissions that are not in the built-in
	// defaults (custom permissions the operator added by hand).
	for _, permission := range user {
		found := false
		for _, def := range defaults {
			if def.ID == permission.ID {
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, permission)
		}
	}
	return merged
}

// hasOptionsBlock reports whether the document declares a top-level options
// block at all. Presence has to come from the parsed document: a substring
// search over the raw bytes matches "options:" or "jail:" anywhere in the file
// — nested in a permissions block, in a comment, in a mount path — and a false
// positive there silently skips the safety default, leaving Options.Jail at
// Go's zero value. Writing the config that looks like it enables the sandbox
// was exactly what turned it off.
//
// The probe deliberately decodes into a plain map instead of Options: Options
// has a custom unmarshaler that fills in defaults, which would erase the
// distinction between "declared" and "absent".
func hasOptionsBlock(data []byte) bool {
	var probe struct {
		Options map[string]any `yaml:"options"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Options != nil
}
