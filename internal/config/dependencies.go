package config

// DependencySettings controls the host directories shared by the Docker
// backend. The global value is machine-wide and trusted; the local value is
// project-owned and therefore remains subject to the launcher's trust gate.
type DependencySettings struct {
	// Policy is currently "safe" (the default) or "none". Safe selects the
	// built-in dependencies for the chosen stacks; none disables automatic
	// selection while still allowing an explicit enabled override.
	Policy    string                        `yaml:"policy,omitempty"`
	Overrides map[string]DependencyOverride `yaml:"overrides,omitempty"`
}

// DependencyOverride changes one built-in dependency without requiring a
// binary rebuild. Source is the fallback for the current host; Sources wins
// when it contains the current GOOS key. Paths are expanded without invoking
// a shell.
type DependencyOverride struct {
	Enabled           *bool             `yaml:"enabled,omitempty"`
	Source            string            `yaml:"source,omitempty"`
	Sources           map[string]string `yaml:"sources,omitempty"`
	Target            string            `yaml:"target,omitempty"`
	Mode              string            `yaml:"mode,omitempty"`
	AllowIncompatible bool              `yaml:"allow_incompatible,omitempty"`
}

// IsZero reports whether no dependency policy or override was configured.
func (s DependencySettings) IsZero() bool {
	return s.Policy == "" && len(s.Overrides) == 0
}

// Clone returns an independent copy suitable for layering local config or a
// profile without sharing mutable maps with the source document.
func (s DependencySettings) Clone() DependencySettings {
	clone := DependencySettings{Policy: s.Policy}
	if s.Overrides == nil {
		return clone
	}
	clone.Overrides = make(map[string]DependencyOverride, len(s.Overrides))
	for id, override := range s.Overrides {
		copyOverride := override
		if override.Enabled != nil {
			value := *override.Enabled
			copyOverride.Enabled = &value
		}
		if override.Sources != nil {
			copyOverride.Sources = make(map[string]string, len(override.Sources))
			for platform, source := range override.Sources {
				copyOverride.Sources[platform] = source
			}
		}
		clone.Overrides[id] = copyOverride
	}
	return clone
}

// MergeDependencySettings overlays local project settings on the trusted
// global settings. An override is merged field by field so a project can
// change only enabled/source/target/mode while retaining the machine default
// for the other fields.
func MergeDependencySettings(global, local DependencySettings) DependencySettings {
	merged := global.Clone()
	if local.Policy != "" {
		merged.Policy = local.Policy
	}
	if len(local.Overrides) == 0 {
		return merged
	}
	if merged.Overrides == nil {
		merged.Overrides = make(map[string]DependencyOverride, len(local.Overrides))
	}
	for id, localOverride := range local.Overrides {
		base := merged.Overrides[id]
		if localOverride.Enabled != nil {
			value := *localOverride.Enabled
			base.Enabled = &value
		}
		if localOverride.Source != "" {
			base.Source = localOverride.Source
		}
		if localOverride.Sources != nil {
			base.Sources = make(map[string]string, len(localOverride.Sources))
			for platform, source := range localOverride.Sources {
				base.Sources[platform] = source
			}
		}
		if localOverride.Target != "" {
			base.Target = localOverride.Target
		}
		if localOverride.Mode != "" {
			base.Mode = localOverride.Mode
		}
		if localOverride.AllowIncompatible {
			base.AllowIncompatible = true
		}
		merged.Overrides[id] = base
	}
	return merged
}
