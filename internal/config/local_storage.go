package config

import (
	"errors"
	"fmt"
	"github.com/goccy/go-yaml"
	"io"
	"os"
	"path/filepath"
)

// LoadLocal reads the workspace-local config, trying the directory layout
// before the legacy file when either conventional path is supplied. An
// explicit non-conventional path remains an exact path for callers that use a
// temporary or application-specific config location. Missing files fall back
// to DefaultLocal and omitted option keys retain safe defaults. Files larger
// than maxLocalConfigBytes are rejected before full allocation.
func LoadLocal(path string) (Local, error) {
	cfg := DefaultLocal()
	if path == "" {
		return cfg, nil
	}
	path = localConfigReadPath(path)
	// #nosec G304 -- path is the user-supplied local config location by design
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read local config: %w", err)
	}
	defer func() { _ = f.Close() }()
	// Read at most limit+1 so oversize is detected without slurping a multi-GB
	// hostile file into memory.
	limited := io.LimitReader(f, int64(maxLocalConfigBytes)+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return cfg, fmt.Errorf("read local config: %w", err)
	}
	if len(b) > maxLocalConfigBytes {
		return cfg, fmt.Errorf("local config %s exceeds %d byte limit", path, maxLocalConfigBytes)
	}
	var loaded Local
	if err := yaml.Unmarshal(b, &loaded); err != nil {
		return cfg, fmt.Errorf("parse local config %s: %w", path, err)
	}
	if loaded.Version == "" {
		loaded.Version = CurrentVersion
	}
	if loaded.Agent == "" {
		loaded.Agent = cfg.Agent
	}
	if loaded.Permissions == nil {
		loaded.Permissions = map[string]bool{}
	}
	// An omitted options block must retain safe defaults. When the block IS
	// present, Options.UnmarshalYAML has already restored the default for each
	// key the document left out, so explicit false values remain meaningful.
	if !hasOptionsBlock(b) {
		loaded.Options = cfg.Options
	}
	if err := ValidateVersion(loaded.Version); err != nil {
		return cfg, err
	}
	return loaded, nil
}

// LocalSaveResult describes the effective path written by SaveLocalResult and
// whether a legacy workspace file was migrated as part of that save.
type LocalSaveResult struct {
	Path       string
	Migrated   bool
	LegacyPath string
	BackupPath string
}

// SaveLocal persists the local config atomically and with user-only
// permissions. It writes the directory layout when path is one of the
// conventional workspace paths. A legacy file is moved to a recoverable
// .bak path after the new file has been written; migration emits a warning on
// stderr for direct callers.
func SaveLocal(path string, cfg Local) error {
	result, err := SaveLocalResult(path, cfg)
	if err == nil && result.Migrated {
		_, _ = fmt.Fprintf(os.Stderr, "warning: migrated legacy local config %s to %s (backup: %s)\n",
			result.LegacyPath, result.Path, result.BackupPath)
	}
	return err
}

// SaveLocalResult is SaveLocal with provenance about the effective path and
// any legacy migration. It does not print, allowing the CLI to route the
// warning through its configured stderr writer.
func SaveLocalResult(path string, cfg Local) (LocalSaveResult, error) {
	target, legacy, err := localConfigWritePaths(path)
	if err != nil {
		return LocalSaveResult{}, err
	}
	cfg.Version = CurrentVersion
	if cfg.Permissions == nil {
		cfg.Permissions = map[string]bool{}
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return LocalSaveResult{}, fmt.Errorf("encode local config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return LocalSaveResult{}, fmt.Errorf("create config directory: %w", err)
	}
	if err := atomicWriteFile(target, b, 0o600); err != nil {
		return LocalSaveResult{}, wrapAtomicWriteError("local config", err)
	}
	result := LocalSaveResult{Path: target}
	if legacy == "" || legacy == target || !localPathExists(legacy) {
		return result, nil
	}
	backup, err := nextLegacyBackupPath(legacy)
	if err != nil {
		return LocalSaveResult{}, fmt.Errorf("prepare local config migration: %w", err)
	}
	if err := os.Rename(legacy, backup); err != nil {
		return LocalSaveResult{}, fmt.Errorf("migrate local config: %w", err)
	}
	result.Migrated = true
	result.LegacyPath = legacy
	result.BackupPath = backup
	return result, nil
}

func localPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func localConfigReadPath(path string) string {
	if path == "" {
		return path
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		if filepath.Base(filepath.Clean(path)) != localConfigDirectoryName {
			return path
		}
		newPath := filepath.Join(path, localConfigFileName)
		if localPathExists(newPath) {
			return newPath
		}
		legacy := LegacyLocalConfigPath(filepath.Dir(filepath.Clean(path)))
		if localPathExists(legacy) {
			return legacy
		}
		return newPath
	}

	clean := filepath.Clean(path)
	base := filepath.Base(clean)
	switch base {
	case localConfigFileName:
		if filepath.Base(filepath.Dir(clean)) == localConfigDirectoryName {
			if localPathExists(clean) {
				return clean
			}
			legacy := LegacyLocalConfigPath(filepath.Dir(filepath.Dir(clean)))
			if localPathExists(legacy) {
				return legacy
			}
		}
	case legacyLocalConfigName:
		newPath := LocalConfigPath(filepath.Dir(clean))
		if localPathExists(newPath) {
			return newPath
		}
	}
	return path
}

func localConfigWritePaths(path string) (target, legacy string, err error) {
	if path == "" {
		return "", "", errors.New("local config path is empty")
	}
	clean := filepath.Clean(path)
	if filepath.Base(clean) == localConfigDirectoryName {
		if info, statErr := os.Stat(clean); statErr == nil && !info.IsDir() {
			return clean, "", nil
		}
		return filepath.Join(clean, localConfigFileName), LegacyLocalConfigPath(filepath.Dir(clean)), nil
	}
	if info, statErr := os.Stat(clean); statErr == nil && info.IsDir() {
		return clean, "", nil
	}
	if filepath.Base(clean) == legacyLocalConfigName {
		return LocalConfigPath(filepath.Dir(clean)), clean, nil
	}
	if filepath.Base(clean) == localConfigFileName && filepath.Base(filepath.Dir(clean)) == localConfigDirectoryName {
		return clean, LegacyLocalConfigPath(filepath.Dir(filepath.Dir(clean))), nil
	}
	return path, "", nil
}

func nextLegacyBackupPath(legacy string) (string, error) {
	for index := 0; ; index++ {
		candidate := legacy + ".bak"
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", legacy+".bak", index)
		}
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}
