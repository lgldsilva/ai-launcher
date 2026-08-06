package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// installState is the on-disk record of what the launcher's installer has
// installed on this host, keyed by target path. It is how the docker backend
// learns the REAL version of an agent without querying GitHub: the tag of the
// last verified install. Reading it makes the image tag honest (design C2) —
// the selection hashes the actual version the image will install, so a
// catalog/agent update produces a new tag and a fresh build instead of a
// lying cache hit.
type installState struct {
	Installs map[string]installStateEntry `json:"installs"`
}

type installStateEntry struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Asset      string `json:"asset"`
	Path       string `json:"path"`
	UpdatedAt  string `json:"updated_at"`
}

// AgentVersion returns the pinned version the selection should carry for an
// agent on this host: the tag of the last verified install, when the
// install-state records one. It returns "" when the state is missing,
// unreadable, or has no entry — the caller then falls back to a stable
// placeholder (and the image tag simply reflects the recipe, not a version).
//
// statePath is injectable for tests; nil uses the default home location.
func AgentVersion(home, command string, statePath string) string {
	if strings.TrimSpace(home) == "" || strings.TrimSpace(command) == "" {
		return ""
	}
	path := statePath
	if path == "" {
		path = filepath.Join(home, ".config", "ai-launch", "install-state.json")
	}
	// #nosec G304 -- path is the fixed install-state location (or the caller's
	// override); the command string only selects which entry to read.
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var state installState
	if err := json.Unmarshal(raw, &state); err != nil {
		return ""
	}
	// The install-state is keyed by target path (usually ~/.local/bin/<cmd>);
	// match on the basename of the recorded path plus the repository so a
	// stale entry from another tool cannot answer for this agent.
	best := ""
	for _, entry := range state.Installs {
		if !strings.HasSuffix(filepath.Base(entry.Path), command) {
			continue
		}
		tag := strings.TrimSpace(entry.Tag)
		if tag == "" || strings.EqualFold(tag, "latest") {
			continue
		}
		// Prefer the newest entry when several paths match.
		if tag > best {
			best = tag
		}
	}
	return best
}

// SortedAssetKeys returns the asset map keys in stable order, so rendered
// YAML (install config) and any hashing over the recipe do not vary run to
// run (map iteration order is random).
func SortedAssetKeys(assets map[string]string) []string {
	keys := make([]string, 0, len(assets))
	for key := range assets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
