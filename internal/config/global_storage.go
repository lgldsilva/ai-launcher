package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/lgldsilva/ai-launcher/internal/fsatomic"
)

// LoadGlobal reads the global config, falling back to DefaultGlobal for a
// missing file and merging built-in defaults for omitted sections.
func LoadGlobal(path string) (Global, error) {
	cfg, _, err := LoadGlobalWithWarnings(path)
	return cfg, err
}

// LoadGlobalWithWarnings is LoadGlobal plus a non-fatal warning for every key
// the file declares that the schema does not know. Unknown keys load fine —
// a config written by a newer launcher must not hard-fail here — but the next
// SaveGlobal silently drops them, so the caller should surface them.
func LoadGlobalWithWarnings(path string) (Global, []string, error) {
	defaults := DefaultGlobal()
	if path == "" {
		return defaults, nil, nil
	}
	b, err := os.ReadFile(path) // #nosec G304 -- path is the user-supplied global config location by design
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil, nil
	}
	if err != nil {
		return defaults, nil, fmt.Errorf("read global config: %w", err)
	}
	var cfg Global
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return defaults, nil, fmt.Errorf("parse global config %s: %w", path, err)
	}
	cfg = mergeGlobalDefaults(defaults, cfg)
	if err := ValidateVersion(cfg.Version); err != nil {
		return defaults, nil, err
	}
	return cfg, unknownKeyWarnings("global config", path, b, Global{}), nil
}

// SaveGlobal persists the global catalog atomically and with user-only
// permissions. It is used by the --add command and keeps the catalog
// extensible without requiring a new launcher build.
func SaveGlobal(path string, cfg Global) error {
	if path == "" {
		return errors.New("global config path is empty")
	}
	cfg.Version = CurrentVersion
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	return writeGlobalAtomically(path, b)
}

// SaveRecentAgents persists only the most-recently-used list, leaving every
// other key in the file untouched. The launch path used to call SaveGlobal on
// every run, which wrote the *merged* in-memory catalog back to disk — freezing
// that release's built-in agents and permissions into the user's config, so a
// later release's additions never appeared.
func SaveRecentAgents(path string, recent []string) error {
	if path == "" {
		return errors.New("global config path is empty")
	}
	document := make(map[string]any)
	b, err := os.ReadFile(path) // #nosec G304 -- path is the user's config location by design
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, &document); err != nil {
			return fmt.Errorf("parse global config %s: %w", path, err)
		}
		if document == nil {
			document = make(map[string]any)
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read global config: %w", err)
	}
	document["version"] = CurrentVersion
	document["recent_agents"] = recent
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	return writeGlobalAtomically(path, encoded)
}

// trustedLocalConfigsMax caps the provenance list so a long history of saves
// cannot grow the global config without bound.
const trustedLocalConfigsMax = 50

// localConfigHash returns the hex SHA-256 of a local config file's bytes.
func localConfigHash(path string) (string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- path is the user's config location by design
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalLocalPath returns a stable absolute path for trust binding.
// EvalSymlinks collapses aliases so two spellings of the same file share one
// record; on failure the cleaned absolute form is used.
func canonicalLocalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs), nil
	}
	return filepath.Clean(resolved), nil
}

// LocalConfigTrusted reports whether the local config at path is the same file
// (canonical path) and the same bytes the launcher saved. A repository-shipped
// .ai-launch.yaml has no record; editing after save changes the hash; copying
// identical bytes to another path does not inherit trust.
func LocalConfigTrusted(global Global, path string) bool {
	path = localConfigReadPath(path)
	hash, err := localConfigHash(path)
	if err != nil {
		return false
	}
	canonical, err := canonicalLocalPath(path)
	if err != nil {
		return false
	}
	for _, trusted := range global.TrustedLocalConfigs {
		// An empty Path is a schema-2.0 record (bare hash, no file bound). It
		// never grants trust: the comparison below would already reject it,
		// but saying so explicitly keeps the rule from depending on the
		// coincidence that a canonical path is never empty.
		if trusted.Path == "" {
			continue
		}
		if trusted.Hash == hash && trusted.Path == canonical {
			return true
		}
	}
	return false
}

// RecordTrustedLocalConfig notes path+hash of a launcher-saved local config in
// the global config, leaving every other key untouched (same discipline as
// SaveRecentAgents). The next launch honors that exact path and content.
func RecordTrustedLocalConfig(globalPath, localPath string) error {
	if globalPath == "" {
		return errors.New("global config path is empty")
	}
	localPath = localConfigReadPath(localPath)
	hash, err := localConfigHash(localPath)
	if err != nil {
		return fmt.Errorf("hash local config: %w", err)
	}
	canonical, err := canonicalLocalPath(localPath)
	if err != nil {
		return fmt.Errorf("canonical local path: %w", err)
	}
	document, err := readGlobalDocument(globalPath)
	if err != nil {
		return err
	}
	entry := TrustedLocalEntry{Path: canonical, Hash: hash}
	entries := append(trustedEntriesExcluding(document, entry), entry)
	if len(entries) > trustedLocalConfigsMax {
		entries = entries[len(entries)-trustedLocalConfigsMax:]
	}
	// Encode as plain maps so the on-disk shape stays readable and does not
	// depend on struct tags when the rest of the document is map[string]any.
	serialized := make([]map[string]string, 0, len(entries))
	for _, e := range entries {
		serialized = append(serialized, map[string]string{"path": e.Path, "hash": e.Hash})
	}
	document["version"] = CurrentVersion
	document["trusted_local_configs"] = serialized
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	return writeGlobalAtomically(globalPath, encoded)
}

// readGlobalDocument loads the global config as a plain YAML document so a
// single key can be updated without rewriting the merged catalog. A missing
// file yields an empty document.
func readGlobalDocument(globalPath string) (map[string]any, error) {
	document := make(map[string]any)
	b, err := os.ReadFile(globalPath) // #nosec G304 -- path is the user's config location by design
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, &document); err != nil {
			return nil, fmt.Errorf("parse global config %s: %w", globalPath, err)
		}
		if document == nil {
			document = make(map[string]any)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("read global config: %w", err)
	}
	return document, nil
}

// trustedEntriesExcluding returns the document's recorded trusted-config
// entries in order, dropping malformed rows and any entry equal to the one
// being re-recorded (caller re-appends it at the tail). Legacy bare-hash
// strings are dropped: they cannot bind a path and must not grant trust.
func trustedEntriesExcluding(document map[string]any, skip TrustedLocalEntry) []TrustedLocalEntry {
	existing, ok := document["trusted_local_configs"].([]any)
	if !ok {
		return nil
	}
	out := make([]TrustedLocalEntry, 0, len(existing))
	for _, raw := range existing {
		parsed, ok := parseTrustedLocalEntry(raw)
		if !ok {
			continue // legacy bare hash or malformed row
		}
		if parsed.Path == skip.Path && parsed.Hash == skip.Hash {
			continue
		}
		out = append(out, parsed)
	}
	return out
}

// parseTrustedLocalEntry accepts the map form written by RecordTrustedLocalConfig.
// Bare strings (pre-path-binding hashes) are rejected so they never grant trust.
func parseTrustedLocalEntry(raw any) (TrustedLocalEntry, bool) {
	switch value := raw.(type) {
	case map[string]any:
		path, _ := value["path"].(string)
		hash, _ := value["hash"].(string)
		path = strings.TrimSpace(path)
		hash = strings.TrimSpace(hash)
		if path == "" || hash == "" {
			return TrustedLocalEntry{}, false
		}
		return TrustedLocalEntry{Path: path, Hash: hash}, true
	case map[string]string:
		path := strings.TrimSpace(value["path"])
		hash := strings.TrimSpace(value["hash"])
		if path == "" || hash == "" {
			return TrustedLocalEntry{}, false
		}
		return TrustedLocalEntry{Path: path, Hash: hash}, true
	default:
		return TrustedLocalEntry{}, false
	}
}

// atomicWriteFile writes data through a temporary file and renames it into
// place. Callers create the destination directory and add their domain label
// to errors, while fsatomic owns the failure-prone write lifecycle.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	return fsatomic.WriteFile(path, data, mode, ".ai-launch-atomic-*.tmp")
}

func wrapAtomicWriteError(label string, err error) error {
	var failure *fsatomic.WriteError
	if errors.As(err, &failure) {
		if label == "local config" && failure.Stage == "create temporary" {
			return fmt.Errorf("create temporary config: %w", failure.Err)
		}
		return fmt.Errorf("%s %s: %w", failure.Stage, label, failure.Err)
	}
	return err
}

// writeGlobalAtomically writes the global config through a temporary file with
// user-only permissions, so a crash mid-write never leaves a partial catalog.
func writeGlobalAtomically(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create global config directory: %w", err)
	}
	if err := atomicWriteFile(path, b, 0o600); err != nil {
		return wrapAtomicWriteError("global config", err)
	}
	return nil
}
