package container

import (
	"fmt"
	"path"
	"runtime"
	"sort"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// DependencyKind distinguishes executable toolchains from data that can be
// safely reused by a different container invocation.
type DependencyKind string

const (
	// DependencyToolchain identifies a native manager or SDK tree.
	DependencyToolchain DependencyKind = "toolchain"
	// DependencyPackage identifies a package-manager cache or repository.
	DependencyPackage DependencyKind = "package-cache"
	// DependencyConfig identifies an editor configuration or state tree.
	DependencyConfig DependencyKind = "config"
	// DependencyBuildCache identifies compiler/build output caches.
	DependencyBuildCache DependencyKind = "build-cache"
)

// DependencyMount is a resolved host-to-container bind mount. Unlike the
// project and agent state mounts, dependency mounts deliberately have separate
// source and target paths so Windows hosts can feed a Linux container.
type DependencyMount struct {
	ID            string
	Kind          DependencyKind
	HostPath      string
	ContainerPath string
	Mode          string
}

// DependencyStatus makes automatic skips explainable in dry-run and
// diagnostics. A missing default cache is harmless; a missing explicit source
// is an error returned by ResolveDependencyMounts.
type DependencyStatus struct {
	ID            string
	Action        string // mounted or skipped
	Reason        string
	HostPath      string
	ContainerPath string
}

// DependencyResolution is the complete, deterministic result consumed by
// docker run and Compose.
type DependencyResolution struct {
	Mounts []DependencyMount
	Env    []string
	Status []DependencyStatus
}

type dependencyDefinition struct {
	ID                 string
	Technology         string
	Kind               DependencyKind
	HostPaths          map[string]string
	ContainerPath      string
	Environment        map[string]string
	CompatibleHostGOOS []string
	Stacks             []string
}

// dependencyDefinitions is the built-in cross-platform map. Host paths are
// intentionally the smallest useful subdirectories: Maven repository instead
// of all of .m2, Gradle caches instead of gradle.properties, and Cargo
// registry/git instead of cargo/bin or rustup toolchains.
var dependencyDefinitions = []dependencyDefinition{
	{
		ID: "go.module-cache", Technology: "go", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": goModuleCachePath, "darwin": goModuleCachePath, "windows": goModuleCachePath},
		ContainerPath: "/home/ai-launcher/go/pkg/mod", Environment: map[string]string{"GOMODCACHE": dependencyTarget},
		Stacks: []string{"go"},
	},
	{
		ID: "go.build-cache", Technology: "go", Kind: DependencyBuildCache,
		HostPaths:     map[string]string{"linux": "~/.cache/go-build", "darwin": "~/Library/Caches/go-build", "windows": "%LOCALAPPDATA%/go-build"},
		ContainerPath: "/home/ai-launcher/.cache/go-build", Environment: map[string]string{"GOCACHE": dependencyTarget},
		CompatibleHostGOOS: []string{"linux"}, Stacks: []string{"go"},
	},
	{
		ID: "rust.cargo-registry", Technology: "rust", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": cargoRegistryPath, "darwin": cargoRegistryPath, "windows": cargoRegistryPath},
		ContainerPath: "/home/ai-launcher/.cargo/registry", Environment: map[string]string{"CARGO_HOME": "/home/ai-launcher/.cargo"},
		Stacks: []string{"rust"},
	},
	{
		ID: "rust.cargo-git", Technology: "rust", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": cargoGitPath, "darwin": cargoGitPath, "windows": cargoGitPath},
		ContainerPath: "/home/ai-launcher/.cargo/git", Environment: map[string]string{"CARGO_HOME": "/home/ai-launcher/.cargo"},
		Stacks: []string{"rust"},
	},
	{
		ID: "python.pip-cache", Technology: "python", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": "~/.cache/pip", "darwin": "~/Library/Caches/pip", "windows": "%LOCALAPPDATA%/pip/Cache"},
		ContainerPath: "/home/ai-launcher/.cache/pip", Environment: map[string]string{"PIP_CACHE_DIR": dependencyTarget},
		Stacks: []string{"python"},
	},
	{
		ID: "node.nvm", Technology: "node", Kind: DependencyToolchain,
		HostPaths:     map[string]string{"linux": "~/.nvm", "darwin": "~/.nvm", "windows": "%APPDATA%/nvm"},
		ContainerPath: "/opt/nvm", Environment: map[string]string{"NVM_DIR": dependencyTarget},
		CompatibleHostGOOS: []string{"linux"}, Stacks: []string{"node"},
	},
	{
		ID: "node.npm-cache", Technology: "node", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": "~/.npm", "darwin": "~/.npm", "windows": "%LOCALAPPDATA%/npm-cache"},
		ContainerPath: "/home/ai-launcher/.npm", Environment: map[string]string{"npm_config_cache": dependencyTarget},
		Stacks: []string{"node"},
	},
	{
		ID: "editor.neovim-config", Technology: "neovim", Kind: DependencyConfig,
		HostPaths:     map[string]string{"linux": "~/.config/nvim", "darwin": "~/.config/nvim", "windows": "%LOCALAPPDATA%/nvim"},
		ContainerPath: "/home/ai-launcher/.config/nvim",
		Stacks:        []string{"neovim"},
	},
	{
		ID: "editor.neovim-data", Technology: "neovim", Kind: DependencyConfig,
		HostPaths:     map[string]string{"linux": "~/.local/share/nvim", "darwin": "~/.local/share/nvim", "windows": "%LOCALAPPDATA%/nvim-data"},
		ContainerPath: "/home/ai-launcher/.local/share/nvim",
		Stacks:        []string{"neovim"},
	},
	{
		ID: "editor.neovim-state", Technology: "neovim", Kind: DependencyConfig,
		HostPaths:     map[string]string{"linux": "~/.local/state/nvim", "darwin": "~/.local/state/nvim"},
		ContainerPath: "/home/ai-launcher/.local/state/nvim",
		Stacks:        []string{"neovim"},
	},
	{
		ID: "editor.neovim-cache", Technology: "neovim", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": "~/.cache/nvim", "darwin": "~/Library/Caches/nvim", "windows": "%LOCALAPPDATA%/nvim-data"},
		ContainerPath: "/home/ai-launcher/.cache/nvim",
		Stacks:        []string{"neovim"},
	},
	{
		ID: "java.sdkman", Technology: "java", Kind: DependencyToolchain,
		HostPaths:     map[string]string{"linux": "~/.sdkman", "darwin": "~/.sdkman"},
		ContainerPath: "/opt/sdkman", Environment: map[string]string{"SDKMAN_DIR": dependencyTarget},
		CompatibleHostGOOS: []string{"linux"}, Stacks: []string{"java"},
	},
	{
		ID: "java.maven-repository", Technology: "java", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": mavenRepositoryPath, "darwin": mavenRepositoryPath, "windows": mavenRepositoryPath},
		ContainerPath: "/home/ai-launcher/.m2/repository", Environment: map[string]string{"MAVEN_OPTS": "-Dmaven.repo.local=" + dependencyTarget},
		Stacks: []string{"java", "maven"},
	},
	{
		ID: "gradle.cache", Technology: "gradle", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": gradleCachePath, "darwin": gradleCachePath, "windows": gradleCachePath},
		ContainerPath: "/home/ai-launcher/.gradle/caches", Environment: map[string]string{"GRADLE_USER_HOME": "/home/ai-launcher/.gradle"},
		Stacks: []string{"gradle"},
	},
	{
		ID: "gradle.wrapper", Technology: "gradle", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": gradleWrapperPath, "darwin": gradleWrapperPath, "windows": gradleWrapperPath},
		ContainerPath: "/home/ai-launcher/.gradle/wrapper/dists", Environment: map[string]string{"GRADLE_USER_HOME": "/home/ai-launcher/.gradle"},
		Stacks: []string{"gradle"},
	},
	{
		ID: "cpp.ccache", Technology: "cpp", Kind: DependencyBuildCache,
		HostPaths:     map[string]string{"linux": "~/.cache/ccache", "darwin": "~/Library/Caches/ccache", "windows": "%LOCALAPPDATA%/ccache"},
		ContainerPath: "/home/ai-launcher/.cache/ccache", Environment: map[string]string{"CCACHE_DIR": dependencyTarget},
		CompatibleHostGOOS: []string{"linux"}, Stacks: []string{"cpp"},
	},
	{
		ID: "dotnet.nuget-packages", Technology: ".net", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": nugetPackagesPath, "darwin": nugetPackagesPath, "windows": nugetPackagesPath},
		ContainerPath: "/home/ai-launcher/.nuget/packages", Environment: map[string]string{"NUGET_PACKAGES": dependencyTarget},
	},
	{
		ID: "ruby.bundler-cache", Technology: "ruby", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": bundlerCachePath, "darwin": bundlerCachePath, "windows": bundlerCachePath},
		ContainerPath: "/home/ai-launcher/.bundle/cache", Environment: map[string]string{"BUNDLE_USER_CACHE": dependencyTarget},
	},
	{
		ID: "php.composer-cache", Technology: "php", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": "~/.cache/composer", "darwin": "~/.composer/cache", "windows": "%LOCALAPPDATA%/Composer"},
		ContainerPath: "/home/ai-launcher/.cache/composer", Environment: map[string]string{"COMPOSER_CACHE_DIR": dependencyTarget},
	},
	{
		ID: "dart.pub-cache", Technology: "dart", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": "~/.pub-cache", "darwin": "~/.pub-cache", "windows": "%APPDATA%/Pub/Cache"},
		ContainerPath: "/home/ai-launcher/.pub-cache", Environment: map[string]string{"PUB_CACHE": dependencyTarget},
	},
	{
		ID: "elixir.mix-home", Technology: "elixir", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": "~/.mix", "darwin": "~/.mix", "windows": "%USERPROFILE%/.mix"},
		ContainerPath: "/home/ai-launcher/.mix", Environment: map[string]string{"MIX_HOME": dependencyTarget},
	},
	{
		ID: "elixir.hex-home", Technology: "elixir", Kind: DependencyPackage,
		HostPaths:     map[string]string{"linux": "~/.hex", "darwin": "~/.hex", "windows": "%USERPROFILE%/.hex"},
		ContainerPath: "/home/ai-launcher/.hex", Environment: map[string]string{"HEX_HOME": dependencyTarget},
	},
}

// DependencyDefinitions returns the catalog in stable ID order for diagnostics
// and tests. Callers receive a copy of the slice and maps are not exposed.
func DependencyDefinitions() []DependencyMountInfo {
	defs := make([]DependencyMountInfo, 0, len(dependencyDefinitions))
	for _, definition := range dependencyDefinitions {
		defs = append(defs, DependencyMountInfo{ID: definition.ID, Technology: definition.Technology, Kind: definition.Kind, Stacks: append([]string(nil), definition.Stacks...), CompatibleHostGOOS: append([]string(nil), definition.CompatibleHostGOOS...)})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].ID < defs[j].ID })
	return defs
}

// DependencyMountInfo is the public, non-path portion of a dependency
// definition used by diagnostics and catalog tests.
type DependencyMountInfo struct {
	ID                 string
	Technology         string
	Kind               DependencyKind
	Stacks             []string
	CompatibleHostGOOS []string
}

// ResolveDependencyMounts resolves the selected stack dependencies and YAML
// overrides. Default paths that do not exist are reported as skipped; an
// explicitly enabled or explicitly sourced path fails closed instead of
// pretending the requested sharing worked.
func ResolveDependencyMounts(home, goos string, stackIDs []string, settings config.DependencySettings, exists func(string) bool) (DependencyResolution, error) {
	if strings.TrimSpace(home) == "" {
		return DependencyResolution{}, nil
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	if exists == nil {
		exists = ExistsOnHost
	}

	definitions := dependencyDefinitionMap()
	selected, err := selectedDependencyIDs(definitions, stackIDs, settings)
	if err != nil {
		return DependencyResolution{}, err
	}
	ids := filteredDependencyIDs(selected, settings)

	resolution := DependencyResolution{}
	seenMount := make(map[string]struct{}, len(ids))
	seenEnv := make(map[string]struct{})
	for _, dependencyID := range ids {
		mount, status, envEntries, err := resolveDependency(definitions[dependencyID], settings.Overrides[dependencyID], dependencyID, home, goos, exists)
		if err != nil {
			return DependencyResolution{}, err
		}
		if mount.ID == "" {
			resolution.Status = append(resolution.Status, status)
			continue
		}
		mountKey := mount.HostPath + "\x00" + mount.ContainerPath
		if _, duplicate := seenMount[mountKey]; duplicate {
			continue
		}
		seenMount[mountKey] = struct{}{}
		resolution.Mounts = append(resolution.Mounts, mount)
		resolution.Status = append(resolution.Status, status)
		for _, entry := range envEntries {
			if _, duplicate := seenEnv[entry]; duplicate {
				continue
			}
			seenEnv[entry] = struct{}{}
			resolution.Env = append(resolution.Env, entry)
		}
	}
	return resolution, nil
}

func dependencyDefinitionMap() map[string]dependencyDefinition {
	definitions := make(map[string]dependencyDefinition, len(dependencyDefinitions))
	for _, definition := range dependencyDefinitions {
		definitions[definition.ID] = definition
	}
	return definitions
}

func selectedDependencyIDs(definitions map[string]dependencyDefinition, stackIDs []string, settings config.DependencySettings) (map[string]bool, error) {
	selected := make(map[string]bool)
	for _, stackID := range stackIDs {
		stack, ok := StackByID(stackID)
		if !ok {
			continue
		}
		for _, dependencyID := range stack.DependencyIDs {
			selected[dependencyID] = true
		}
	}
	for dependencyID, override := range settings.Overrides {
		if _, ok := definitions[dependencyID]; !ok {
			return nil, fmt.Errorf("unknown container dependency %q", dependencyID)
		}
		if override.Enabled != nil && *override.Enabled {
			selected[dependencyID] = true
		}
	}
	return selected, nil
}

func filteredDependencyIDs(selected map[string]bool, settings config.DependencySettings) []string {
	ids := make([]string, 0, len(selected))
	for dependencyID := range selected {
		override := settings.Overrides[dependencyID]
		if strings.EqualFold(strings.TrimSpace(settings.Policy), "none") && (override.Enabled == nil || !*override.Enabled) {
			continue
		}
		if override.Enabled != nil && !*override.Enabled {
			continue
		}
		ids = append(ids, dependencyID)
	}
	sort.Strings(ids)
	return ids
}

func resolveDependency(definition dependencyDefinition, override config.DependencyOverride, dependencyID, home, goos string, exists func(string) bool) (DependencyMount, DependencyStatus, []string, error) {
	defaultPath, ok := definition.HostPaths[goos]
	hostRaw := defaultPath
	if source, found := override.Sources[goos]; found && strings.TrimSpace(source) != "" {
		hostRaw = source
	} else if strings.TrimSpace(override.Source) != "" {
		hostRaw = override.Source
	}
	explicit := dependencyOverrideIsExplicit(override)
	if !ok && strings.TrimSpace(hostRaw) == "" {
		if explicit {
			return DependencyMount{}, DependencyStatus{}, nil, fmt.Errorf("container dependency %q has no built-in host path for platform %s; set source or sources.%s explicitly", dependencyID, goos, goos)
		}
		return DependencyMount{}, DependencyStatus{ID: dependencyID, Action: "skipped", Reason: "no host path for platform " + goos}, nil, nil
	}
	hostPath := expandDependencyPath(hostRaw, home)
	containerPath := definition.ContainerPath
	if strings.TrimSpace(override.Target) != "" {
		containerPath = strings.TrimSpace(override.Target)
	}
	if !isAbsoluteContainerPath(containerPath) {
		return DependencyMount{}, DependencyStatus{}, nil, fmt.Errorf("container dependency %q target %q is not an absolute POSIX path", dependencyID, containerPath)
	}
	mode := strings.ToLower(strings.TrimSpace(override.Mode))
	if mode == "" {
		mode = config.MountReadWrite
	}
	if mode != config.MountReadOnly && mode != config.MountReadWrite {
		return DependencyMount{}, DependencyStatus{}, nil, fmt.Errorf("container dependency %q has invalid mode %q", dependencyID, override.Mode)
	}
	statusReason := "source exists"
	if !compatibleHostPlatform(definition.CompatibleHostGOOS, goos) {
		if explicit && override.AllowIncompatible {
			statusReason = "explicitly allowed despite host/container platform mismatch"
		} else if explicit {
			return DependencyMount{}, DependencyStatus{}, nil, fmt.Errorf("container dependency %q is a %s toolchain/cache incompatible with host platform %s; set allow_incompatible: true to force it", dependencyID, definition.Kind, goos)
		} else {
			return DependencyMount{}, DependencyStatus{ID: dependencyID, Action: "skipped", Reason: "host toolchain is not compatible with the Linux container", HostPath: hostPath, ContainerPath: containerPath}, nil, nil
		}
	}
	if !exists(hostPath) {
		if explicit {
			return DependencyMount{}, DependencyStatus{}, nil, fmt.Errorf("container dependency %q source does not exist: %s", dependencyID, hostPath)
		}
		return DependencyMount{}, DependencyStatus{ID: dependencyID, Action: "skipped", Reason: "source does not exist", HostPath: hostPath, ContainerPath: containerPath}, nil, nil
	}
	return DependencyMount{ID: dependencyID, Kind: definition.Kind, HostPath: hostPath, ContainerPath: containerPath, Mode: mode}, DependencyStatus{ID: dependencyID, Action: "mounted", Reason: statusReason, HostPath: hostPath, ContainerPath: containerPath}, dependencyEnvironmentEntries(definition.Environment, containerPath), nil
}

func dependencyOverrideIsExplicit(override config.DependencyOverride) bool {
	return override.Enabled != nil || strings.TrimSpace(override.Source) != "" || len(override.Sources) > 0 || strings.TrimSpace(override.Target) != "" || strings.TrimSpace(override.Mode) != ""
}

func dependencyEnvironmentEntries(environment map[string]string, containerPath string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, key+"="+strings.ReplaceAll(environment[key], dependencyTarget, containerPath))
	}
	return entries
}

func compatibleHostPlatform(platforms []string, goos string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, platform := range platforms {
		if platform == goos {
			return true
		}
	}
	return false
}

func isAbsoluteContainerPath(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "/") && path.Clean(value) != "/"
}

// expandDependencyPath expands only the documented home/platform variables;
// it never invokes a shell or evaluates arbitrary command substitutions.
func expandDependencyPath(raw, home string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || home == "" {
		return ""
	}
	userProfile := home
	appData := joinHostPath(home, "AppData/Roaming")
	localAppData := joinHostPath(home, "AppData/Local")
	for key, value := range map[string]string{
		"${HOME}": home, "$HOME": home,
		"${USERPROFILE}": userProfile, "$USERPROFILE": userProfile, "%USERPROFILE%": userProfile,
		"${APPDATA}": appData, "$APPDATA": appData, "%APPDATA%": appData,
		"${LOCALAPPDATA}": localAppData, "$LOCALAPPDATA": localAppData, "%LOCALAPPDATA%": localAppData,
	} {
		raw = strings.ReplaceAll(raw, key, value)
	}
	if raw == "~" {
		return home
	}
	if strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, "~\\") {
		return joinHostPath(home, raw[2:])
	}
	if strings.HasPrefix(raw, "/") || isWindowsAbsolutePath(raw) {
		return cleanHostPath(raw)
	}
	return joinHostPath(home, raw)
}

func isWindowsAbsolutePath(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func joinHostPath(base, child string) string {
	return cleanHostPath(strings.TrimRight(base, "/\\") + "/" + strings.TrimLeft(child, "/\\"))
}

func cleanHostPath(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	cleaned := path.Clean(value)
	if len(value) >= 2 && value[1] == ':' && len(cleaned) >= 2 && cleaned[1] == ':' {
		return cleaned
	}
	return cleaned
}

// HostDependencyPath is exported for diagnostics and focused tests. It uses
// the same expansion rules as ResolveDependencyMounts.
func HostDependencyPath(raw, home string) string {
	return expandDependencyPath(raw, home)
}

// HostGOOS returns the host platform used by the dependency resolver.
func HostGOOS() string { return runtime.GOOS }

// EnvironmentValue returns the value for a resolved dependency environment
// entry, useful to inspect a resolution without parsing argv.
func EnvironmentValue(entries []string, key string) string {
	prefix := key + "="
	for _, entry := range entries {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
