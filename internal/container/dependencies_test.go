package container

import (
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestResolveDependencyMountsByPlatform(t *testing.T) {
	allExist := func(string) bool { return true }

	linux, err := ResolveDependencyMounts("/home/u", "linux", []string{"go", "node", "java", "gradle", "python", "rust"}, config.DependencySettings{}, allExist)
	if err != nil {
		t.Fatalf("linux resolution error = %v", err)
	}
	for _, id := range []string{"go.module-cache", "go.build-cache", "node.nvm", "node.npm-cache", "java.sdkman", "java.maven-repository", "gradle.cache", "gradle.wrapper", "python.pip-cache", "rust.cargo-registry", "rust.cargo-git"} {
		mount := dependencyMountByID(linux.Mounts, id)
		if mount.ID == "" {
			t.Errorf("linux resolution missing %s: %#v", id, linux)
		}
		if strings.HasSuffix(mount.HostPath, ".m2") || strings.HasSuffix(mount.HostPath, ".gradle") || strings.HasSuffix(mount.HostPath, ".cargo") {
			t.Errorf("linux resolution mounted a tool root for %s: %q", id, mount.HostPath)
		}
	}
	if EnvironmentValue(linux.Env, "GOMODCACHE") != "/home/ai-launcher/go/pkg/mod" || EnvironmentValue(linux.Env, "NVM_DIR") != "/opt/nvm" || EnvironmentValue(linux.Env, "GRADLE_USER_HOME") != "/home/ai-launcher/.gradle" {
		t.Fatalf("linux dependency environment = %#v", linux.Env)
	}

	darwin, err := ResolveDependencyMounts("/Users/u", "darwin", []string{"go", "node", "java", "python"}, config.DependencySettings{}, allExist)
	if err != nil {
		t.Fatalf("darwin resolution error = %v", err)
	}
	for _, id := range []string{"go.module-cache", "node.npm-cache", "python.pip-cache", "java.maven-repository"} {
		if dependencyMountByID(darwin.Mounts, id).ID == "" {
			t.Errorf("darwin resolution missing portable dependency %s: %#v", id, darwin.Mounts)
		}
	}
	for _, id := range []string{"go.build-cache", "node.nvm", "java.sdkman"} {
		if dependencyMountByID(darwin.Mounts, id).ID != "" {
			t.Errorf("darwin resolution mounted incompatible toolchain %s: %#v", id, darwin.Mounts)
		}
	}
	if EnvironmentValue(darwin.Env, "GOCACHE") != "" || EnvironmentValue(darwin.Env, "NVM_DIR") != "" {
		t.Fatalf("darwin environment contains skipped toolchains: %#v", darwin.Env)
	}
}

func TestDependencyDefinitionsAreStableAndCoverSelectedEcosystems(t *testing.T) {
	definitions := DependencyDefinitions()
	if len(definitions) < 18 {
		t.Fatalf("dependency definitions = %d; want the common ecosystem catalog", len(definitions))
	}
	for index := 1; index < len(definitions); index++ {
		if definitions[index-1].ID >= definitions[index].ID {
			t.Fatalf("dependency definitions are not sorted: %#v", definitions)
		}
	}
	for _, id := range []string{"dotnet.nuget-packages", "ruby.bundler-cache", "php.composer-cache", "elixir.mix-home", "dart.pub-cache", "editor.neovim-config", "editor.neovim-data"} {
		if dependencyDefinitionInfoByID(definitions, id).ID == "" {
			t.Errorf("catalog missing ecosystem dependency %q", id)
		}
	}
}

func TestResolveDependencyMountsForNeovimUsesPlatformEditorPaths(t *testing.T) {
	allExist := func(string) bool { return true }
	resolution, err := ResolveDependencyMounts("/Users/u", "darwin", []string{"neovim"}, config.DependencySettings{}, allExist)
	if err != nil {
		t.Fatalf("ResolveDependencyMounts(neovim) error = %v", err)
	}
	for _, id := range []string{"editor.neovim-config", "editor.neovim-data", "editor.neovim-state", "editor.neovim-cache"} {
		if dependencyMountByID(resolution.Mounts, id).ID == "" {
			t.Errorf("Neovim resolution missing %s: %#v", id, resolution)
		}
	}
	if dependencyMountByID(resolution.Mounts, "editor.neovim-config").HostPath != "/Users/u/.config/nvim" || dependencyMountByID(resolution.Mounts, "editor.neovim-cache").HostPath != "/Users/u/Library/Caches/nvim" {
		t.Fatalf("Neovim platform paths = %#v", resolution.Mounts)
	}
}

func TestResolveDependencyMountsOverridesAndPolicy(t *testing.T) {
	enabled := true
	resolution, err := ResolveDependencyMounts("C:/Users/u", "windows", nil, config.DependencySettings{
		Policy: "none",
		Overrides: map[string]config.DependencyOverride{
			"node.npm-cache": {Enabled: &enabled, Source: `%LOCALAPPDATA%\\npm-cache`, Target: "/home/ai-launcher/.npm", Mode: config.MountReadOnly},
		},
	}, func(path string) bool {
		return path == "C:/Users/u/AppData/Local/npm-cache"
	})
	if err != nil {
		t.Fatalf("explicit Windows override error = %v", err)
	}
	if len(resolution.Mounts) != 1 || resolution.Mounts[0].HostPath != "C:/Users/u/AppData/Local/npm-cache" || resolution.Mounts[0].Mode != config.MountReadOnly {
		t.Fatalf("Windows resolution = %#v; want one read-only npm mount", resolution)
	}
	if EnvironmentValue(resolution.Env, "npm_config_cache") != "/home/ai-launcher/.npm" {
		t.Fatalf("Windows dependency environment = %#v", resolution.Env)
	}

	toolchain, err := ResolveDependencyMounts("C:/Users/u", "windows", nil, config.DependencySettings{Overrides: map[string]config.DependencyOverride{
		"java.sdkman": {Enabled: &enabled, Source: `C:\\Users\\u\\sdkman`, AllowIncompatible: true},
	}}, func(path string) bool { return path == "C:/Users/u/sdkman" })
	if err != nil {
		t.Fatalf("explicit Windows toolchain override error = %v", err)
	}
	if dependencyMountByID(toolchain.Mounts, "java.sdkman").ID == "" {
		t.Fatalf("Windows toolchain override did not mount: %#v", toolchain)
	}
}

func TestResolveDependencyMountsFailsClosed(t *testing.T) {
	enabled := true
	tests := []struct {
		name     string
		settings config.DependencySettings
		goos     string
		want     string
	}{
		{
			name: "incompatible toolchain",
			settings: config.DependencySettings{Overrides: map[string]config.DependencyOverride{
				"node.nvm": {Enabled: &enabled},
			}},
			goos: "darwin", want: "incompatible",
		},
		{
			name: "missing explicit source",
			settings: config.DependencySettings{Overrides: map[string]config.DependencyOverride{
				"node.npm-cache": {Enabled: &enabled, Source: "~/missing"},
			}},
			goos: "linux", want: "does not exist",
		},
		{
			name: "invalid mode",
			settings: config.DependencySettings{Overrides: map[string]config.DependencyOverride{
				"node.npm-cache": {Enabled: &enabled, Mode: "delete"},
			}},
			goos: "linux", want: "invalid mode",
		},
		{
			name: "invalid target",
			settings: config.DependencySettings{Overrides: map[string]config.DependencyOverride{
				"node.npm-cache": {Enabled: &enabled, Target: "relative"},
			}},
			goos: "linux", want: "absolute POSIX",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveDependencyMounts("/home/u", tt.goos, nil, tt.settings, func(string) bool { return false })
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want substring %q", err, tt.want)
			}
		})
	}

	forced, err := ResolveDependencyMounts("/Users/u", "darwin", nil, config.DependencySettings{Overrides: map[string]config.DependencyOverride{
		"node.nvm": {Enabled: &enabled, AllowIncompatible: true},
	}}, func(string) bool { return true })
	if err != nil {
		t.Fatalf("allow_incompatible error = %v", err)
	}
	if dependencyMountByID(forced.Mounts, "node.nvm").ID == "" || !strings.Contains(forced.Status[0].Reason, "explicitly allowed") {
		t.Fatalf("forced incompatible resolution = %#v", forced)
	}
}

func TestResolveDependencyMountsReportsAutomaticSkipsAndUnknownIDs(t *testing.T) {
	resolution, err := ResolveDependencyMounts("/home/u", "linux", []string{"go"}, config.DependencySettings{}, func(path string) bool {
		return strings.HasSuffix(path, "/pkg/mod")
	})
	if err != nil {
		t.Fatalf("automatic resolution error = %v", err)
	}
	if len(resolution.Mounts) != 1 || resolution.Mounts[0].ID != "go.module-cache" {
		t.Fatalf("automatic mounts = %#v", resolution.Mounts)
	}
	if len(resolution.Status) != 2 || dependencyStatusByID(resolution.Status, "go.build-cache").Action != "skipped" || dependencyStatusByID(resolution.Status, "go.build-cache").Reason != "source does not exist" {
		t.Fatalf("automatic status = %#v", resolution.Status)
	}

	_, err = ResolveDependencyMounts("/home/u", "linux", nil, config.DependencySettings{Overrides: map[string]config.DependencyOverride{
		"unknown.cache": {},
	}}, func(string) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "unknown container dependency") {
		t.Fatalf("unknown dependency error = %v", err)
	}
}

func TestDependencyPathExpansion(t *testing.T) {
	tests := []struct {
		raw, home, want string
	}{
		{"~/.cache/pip", "/Users/u", "/Users/u/.cache/pip"},
		{`%LOCALAPPDATA%\\npm-cache`, `C:\\Users\\u`, "C:/Users/u/AppData/Local/npm-cache"},
		{`${HOME}/.m2`, "/home/u", "/home/u/.m2"},
		{`C:\\Users\\u\\.cargo`, "/home/u", "C:/Users/u/.cargo"},
	}
	for _, tt := range tests {
		if got := HostDependencyPath(tt.raw, tt.home); got != tt.want {
			t.Errorf("HostDependencyPath(%q, %q) = %q; want %q", tt.raw, tt.home, got, tt.want)
		}
	}
}

func dependencyMountByID(mounts []DependencyMount, id string) DependencyMount {
	for _, mount := range mounts {
		if mount.ID == id {
			return mount
		}
	}
	return DependencyMount{}
}

func dependencyStatusByID(statuses []DependencyStatus, id string) DependencyStatus {
	for _, status := range statuses {
		if status.ID == id {
			return status
		}
	}
	return DependencyStatus{}
}

func dependencyDefinitionInfoByID(definitions []DependencyMountInfo, id string) DependencyMountInfo {
	for _, definition := range definitions {
		if definition.ID == id {
			return definition
		}
	}
	return DependencyMountInfo{}
}
