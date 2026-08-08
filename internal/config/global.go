package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Global is the machine-wide catalog of agents, tools, and permissions.
type Global struct {
	Version         string       `yaml:"version"`
	MemoryServerURL string       `yaml:"memory_server_url,omitempty"`
	MemoryAuthToken string       `yaml:"memory_auth_token,omitempty"`
	Agents          []Agent      `yaml:"agents"`
	Tools           []Tool       `yaml:"tools,omitempty"`
	Permissions     []Permission `yaml:"permissions"`
	DefaultMounts   []string     `yaml:"default_mounts,omitempty"`
	// ContainerDependencies is the trusted machine-wide dependency-directory
	// override catalog. Workspace-local settings are layered over this value
	// only after the normal local-config trust checks.
	ContainerDependencies DependencySettings `yaml:"container_dependencies,omitempty"`
	// RecentAgents is the most-recently-used agent commands (newest first).
	// The TUI lists installed agents in this order so the usual tools float
	// to the top. Updated on every successful launch.
	RecentAgents []string           `yaml:"recent_agents,omitempty"`
	Profiles     map[string]Profile `yaml:"profiles,omitempty"`
	// TrustedLocalConfigs records launcher-saved local configs bound to both
	// content hash and canonical path. A bare hash (legacy) is never enough:
	// identical bytes in another checkout must not inherit trust (ARCHITECTURE
	// invariant 2b).
	TrustedLocalConfigs []TrustedLocalEntry `yaml:"trusted_local_configs,omitempty"`
}

// TrustedLocalEntry is one provenance record: the canonical absolute path of a
// local config the launcher saved, plus the SHA-256 of its bytes at save time.
type TrustedLocalEntry struct {
	Path string `yaml:"path"`
	Hash string `yaml:"hash"`
}

// UnmarshalYAML accepts the current map form and the schema-2.0 form, which was
// a bare hash string with no path bound to it.
//
// The legacy form is read but deliberately not honored: it decodes to an entry
// with an empty Path, and LocalConfigTrusted compares against a canonical
// absolute path that is never empty, so it can never match. That is the point
// of the schema change — a hash alone proves the bytes, not the file, so
// identical bytes in a cloned checkout must not inherit the operator's trust.
//
// Reading it anyway is what keeps the rest of the global config alive. Rejecting
// the scalar here would fail the whole document, and LoadGlobal falls back to
// DefaultGlobal on a parse error: an operator upgrading from 2.0 would silently
// lose their --add agents, their profiles, their MRU list and their memory
// token, on top of the trust records they were always going to have to re-save.
// The stale rows are dropped from disk by the next RecordTrustedLocalConfig.
func (t *TrustedLocalEntry) UnmarshalYAML(data []byte) error {
	type trustedLocalEntryWithoutMethods TrustedLocalEntry
	var mapForm trustedLocalEntryWithoutMethods
	if err := yaml.Unmarshal(data, &mapForm); err == nil {
		*t = TrustedLocalEntry(mapForm)
		return nil
	}
	var legacyHash string
	if err := yaml.Unmarshal(data, &legacyHash); err != nil {
		return fmt.Errorf("parse trusted local config entry: %w", err)
	}
	*t = TrustedLocalEntry{Hash: strings.TrimSpace(legacyHash)}
	return nil
}

// recentAgentsMax is the cap on the MRU list stored in the global config.
const recentAgentsMax = 32

// TouchRecentAgent records command as the most recently used agent (newest
// first), deduplicating and capping the list. Empty commands are ignored.
func TouchRecentAgent(cfg *Global, command string) {
	if cfg == nil {
		return
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	out := make([]string, 0, len(cfg.RecentAgents)+1)
	out = append(out, command)
	for _, existing := range cfg.RecentAgents {
		if existing == command {
			continue
		}
		out = append(out, existing)
	}
	if len(out) > recentAgentsMax {
		out = out[:recentAgentsMax]
	}
	cfg.RecentAgents = out
}

// Profile is a named, reusable selection snapshot stored in the global
// config. It mirrors the workspace-local selection shape (agent, permissions,
// mounts, options). A nil Options pointer means the profile does not override
// the option toggles, so the loader's safe defaults remain in effect.
type Profile struct {
	Agent       string          `yaml:"agent"`
	Permissions map[string]bool `yaml:"permissions,omitempty"`
	Mounts      []Mount         `yaml:"mounts,omitempty"`
	Options     *Options        `yaml:"options,omitempty"`
}

// SetProfile validates and stores a named profile in the global config,
// creating the profiles map on first use.
func SetProfile(global *Global, name string, profile Profile) error {
	if global == nil {
		return errors.New("global config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("profile name is required")
	}
	profile.Agent = strings.TrimSpace(profile.Agent)
	if profile.Agent == "" {
		return errors.New("profile agent is required")
	}
	if global.Profiles == nil {
		global.Profiles = make(map[string]Profile)
	}
	global.Profiles[name] = profile
	return nil
}

// DeleteProfile removes a named profile, reporting whether it existed.
func DeleteProfile(global *Global, name string) bool {
	if global == nil {
		return false
	}
	if _, ok := global.Profiles[name]; !ok {
		return false
	}
	delete(global.Profiles, name)
	return true
}

// ProfileNames returns the saved profile names in sorted order.
func ProfileNames(global Global) []string {
	names := make([]string, 0, len(global.Profiles))
	for name := range global.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ProfileSummary returns a compact, human-readable description of a profile
// for --list-profiles and the TUI profiles section.
func ProfileSummary(profile Profile) string {
	var parts []string
	if profile.Options != nil {
		var toggles []string
		if profile.Options.Jail {
			toggles = append(toggles, "jail")
		}
		if profile.Options.Memory {
			toggles = append(toggles, "memory")
		}
		if profile.Options.Yolo {
			toggles = append(toggles, "yolo")
		}
		if len(toggles) > 0 {
			parts = append(parts, strings.Join(toggles, ","))
		}
		if len(profile.Options.ParamValues) > 0 {
			parts = append(parts, fmt.Sprintf("params=%d", len(profile.Options.ParamValues)))
		}
	}
	if len(profile.Mounts) > 0 {
		parts = append(parts, fmt.Sprintf("mounts=%d", len(profile.Mounts)))
	}
	return strings.Join(parts, " ")
}

// Local is the per-workspace launcher configuration.
type Local struct {
	Version     string          `yaml:"version"`
	Agent       string          `yaml:"agent"`
	Permissions map[string]bool `yaml:"permissions,omitempty"`
	Mounts      []Mount         `yaml:"mounts,omitempty"`
	Options     Options         `yaml:"options"`
}
