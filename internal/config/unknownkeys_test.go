package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func warningText(warnings []string) string {
	return strings.Join(warnings, "\n")
}

func TestLoadLocalWarnsOnUnknownKeys(t *testing.T) {
	path := writeLocal(t, "version: \"2.1\"\nagent: codex\nbogus_toggle: true\noptions:\n  yolo: true\n  container_host_gatway: false\n")
	loaded, warnings, err := LoadLocalWithWarnings(path)
	if err != nil {
		t.Fatalf("LoadLocalWithWarnings() error = %v; unknown keys must not fail the load", err)
	}
	if loaded.Agent != "codex" || !loaded.Options.Yolo {
		t.Fatalf("LoadLocalWithWarnings() = %#v; known values must still apply", loaded)
	}
	text := warningText(warnings)
	for _, want := range []string{"bogus_toggle", "options.container_host_gatway", path} {
		if !strings.Contains(text, want) {
			t.Errorf("warnings = %q; want a warning naming %q", text, want)
		}
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v; want exactly the two unknown keys", warnings)
	}
}

func TestLoadLocalKnownKeysProduceNoWarnings(t *testing.T) {
	path := writeLocal(t, "version: \"2.1\"\nagent: codex\npermissions:\n  ssh: true\n  anything-goes: false\nmounts:\n  - path: /srv/data\n    mode: ro\noptions:\n  yolo: true\n  param_values:\n    model: v1\n")
	_, warnings, err := LoadLocalWithWarnings(path)
	if err != nil {
		t.Fatalf("LoadLocalWithWarnings() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v; a fully known config must not warn (free-form maps accept any key)", warnings)
	}
}

// A config written by a NEWER launcher — with fields this build does not have —
// must keep loading: the values are ignored, the warning says so, and nothing
// fails. Strict decoding would hard-fail here, which is exactly why the fix
// warns instead.
func TestLoadLocalAcceptsConfigFromFutureLauncher(t *testing.T) {
	path := writeLocal(t, "version: \"2.1\"\nagent: codex\noptions:\n  yolo: true\n  future_flight_mode: enabled\n")
	loaded, warnings, err := LoadLocalWithWarnings(path)
	if err != nil {
		t.Fatalf("LoadLocalWithWarnings() error = %v; a newer-launcher config must load", err)
	}
	if !loaded.Options.Yolo {
		t.Error("Options.Yolo = false; the known keys of a future config must still apply")
	}
	if !strings.Contains(warningText(warnings), "options.future_flight_mode") {
		t.Fatalf("warnings = %v; want the future key named", warnings)
	}
}

func TestLoadGlobalWarnsOnUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "global.yaml")
	body := "version: \"2.1\"\nmemori_server_url: http://localhost:37777\nagents:\n  - name: My CLI\n    command: mycli\n    supports_memory: true\n    supports_yolo: false\n    holo_flag: --yolo\nprofiles:\n  dev:\n    agent: codex\n    options:\n      yolo: true\n      permisions:\n        x: y\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, warnings, err := LoadGlobalWithWarnings(path)
	if err != nil {
		t.Fatalf("LoadGlobalWithWarnings() error = %v; unknown keys must not fail the load", err)
	}
	// The user entry is merged into the built-in catalog, so assert on it
	// rather than on the list length.
	var mine Agent
	for _, agent := range loaded.Agents {
		if agent.Command == "mycli" {
			mine = agent
		}
	}
	if mine.Name != "My CLI" {
		t.Fatalf("agent \"mycli\" = %#v; known values must still apply", mine)
	}
	text := warningText(warnings)
	for _, want := range []string{"memori_server_url", "agents[0].holo_flag", "profiles.dev.options.permisions", path} {
		if !strings.Contains(text, want) {
			t.Errorf("warnings = %q; want a warning naming %q", text, want)
		}
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v; want exactly the three unknown keys", warnings)
	}
}

// The known-key set is derived from the structs' yaml tags by reflection, so
// it cannot drift from the schema — and this round-trip proves it: a config
// carrying EVERY field the structs declare must load without a single
// warning. A tag renamed in the struct but not in the detector (or vice
// versa) fails here.
func TestUnknownKeyDetectionAcceptsEveryDeclaredKey(t *testing.T) {
	on := true
	off := false
	full := Local{
		Version:     CurrentVersion,
		Agent:       "codex",
		Permissions: map[string]bool{"ssh": true},
		Mounts:      []Mount{{Path: "/srv/data", Mode: MountReadOnly}},
		Options: Options{
			Jail:                           true,
			Memory:                         true,
			Yolo:                           true,
			Docker:                         true,
			ContainerRuntime:               "docker",
			ContainerContext:               "ctx",
			ContainerHostGateway:           &on,
			ContainerNetworkInternal:       &off,
			ContainerNetworkAllowedDomains: []string{"example.com"},
			Stacks:                         []string{"go"},
			Services:                       []string{"postgres"},
			ContainerMemory:                "4g",
			ContainerCPUs:                  "2",
			ContainerPIDs:                  256,
			ContainerPorts:                 []PortMapping{{Host: 5432, Internal: 5432, Protocol: "tcp"}},
			ContainerNetwork:               "bridge",
			ContainerEnvironment:           map[string]string{"PGUSER": "dev"},
			ContainerServicePorts:          map[string][]PortMapping{"postgres": {{Host: 5432, Internal: 5432}}},
			ContainerTmux: TmuxSettings{
				Enabled:         true,
				Config:          "/home/dev/.tmux.conf",
				LocalConfig:     "/home/dev/.tmux.conf.local",
				OhMyTmuxDir:     "/home/dev/.tmux",
				AdditionalPaths: []string{"/home/dev/.tmux/plugins"},
			},
			ContainerDependencies: DependencySettings{
				Policy: "safe",
				Overrides: map[string]DependencyOverride{"go": {
					Enabled:           &on,
					Source:            "/opt/go",
					Sources:           map[string]string{"darwin": "/opt/go"},
					Target:            "/go",
					Mode:              "rw",
					AllowIncompatible: true,
				}},
			},
			Fresh:         true,
			NewWorkstream: "ws",
			Workstream:    "ws-id",
			Workspace:     "ws-name",
			Project:       "proj",
			JailFlags: JailFlags{
				Lockdown:           &on,
				PrivateHome:        &on,
				Docker:             &off,
				Tailscale:          &off,
				GPU:                &on,
				Display:            &on,
				Mise:               &on,
				Worktree:           &on,
				Landlock:           &on,
				Seccomp:            &on,
				Rlimits:            &on,
				StatusBar:          &on,
				HideConfig:         &off,
				SaveConfig:         &off,
				Browser:            "soft",
				ClaudeDir:          "/home/dev/.claude",
				OverlayMaps:        []string{"/a:/b"},
				Mask:               []string{"/secret"},
				DenyPaths:          []string{"/deny"},
				AllowTCPPorts:      []int{8080},
				MaskExceptions:     []string{"/secret/ok"},
				DenyPathExceptions: []string{"/deny/ok"},
				HideDotdirs:        []string{".gnupg"},
				StatusBarStyle:     "dark",
			},
			ExtraArgs:   []string{"--verbose"},
			ParamValues: map[string]string{"model": "v1"},
		},
	}
	localPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveLocal(localPath, full); err != nil {
		t.Fatalf("SaveLocal() error = %v", err)
	}
	if _, warnings, err := LoadLocalWithWarnings(localPath); err != nil || len(warnings) != 0 {
		t.Fatalf("LoadLocalWithWarnings(saved full local config) = %v, %v; want no warnings", warnings, err)
	}

	fullGlobal := Global{
		Version:         CurrentVersion,
		MemoryServerURL: "http://localhost:37777",
		MemoryAuthToken: "token",
		Agents: []Agent{{
			Name:             "Codex",
			Command:          "codex",
			Aliases:          []string{"cx"},
			Path:             "/usr/local/bin/codex",
			NpmPackage:       "@openai/codex",
			NeedsNode:        true,
			AllowUnverified:  true,
			SetupInteractive: true,
			SupportsMemory:   true,
			SupportsYolo:     true,
			Description:      "Codex CLI",
			YoloFlag:         "--yolo",
			Params:           []Param{{Name: "model", Flag: "--model", Description: "model", TakesValue: true}},
			Release: &GitHubRelease{
				Repository:      "openai/codex",
				Assets:          map[string]string{"darwin-arm64": "codex-*-darwin-arm64.tar.gz"},
				Binary:          "codex",
				ChecksumAsset:   "checksums.txt",
				AllowUnverified: true,
			},
			Memory: &MemoryIntegration{
				Client:          "codex",
				Agent:           "codex",
				RunHarness:      "codex",
				InstallMCP:      true,
				InstallHooks:    true,
				MCPConfigFile:   "~/.codex/config.toml",
				HooksConfigFile: "~/.codex/hooks.json",
			},
			CatalogCommand: "codex",
		}},
		Tools: []Tool{{
			Name:            "ai-jail",
			Command:         "ai-jail",
			Aliases:         []string{"jail"},
			Path:            "/usr/local/bin/ai-jail",
			SourceURL:       "https://example.com/ai-jail.sh",
			AllowUnverified: true,
			Description:     "sandbox",
			Release: &GitHubRelease{
				Repository:      "example/ai-jail",
				Assets:          map[string]string{"linux-amd64": "ai-jail-*-linux-amd64"},
				Binary:          "ai-jail",
				ChecksumAsset:   "checksums.txt",
				AllowUnverified: true,
			},
		}},
		Permissions: []Permission{{
			ID:        "ssh",
			Name:      "SSH",
			Default:   true,
			Locked:    true,
			Requires:  []string{"jail"},
			Platforms: []string{"darwin"},
		}},
		DefaultMounts:         []string{"/srv/data"},
		ContainerDependencies: DependencySettings{Policy: "none", Overrides: map[string]DependencyOverride{"go": {Source: "/opt/go"}}},
		RecentAgents:          []string{"codex"},
		Profiles:              map[string]Profile{"dev": {Agent: "codex", Permissions: map[string]bool{"ssh": true}, Mounts: []Mount{{Path: "/x"}}, Options: &full.Options}},
		TrustedLocalConfigs:   []TrustedLocalEntry{{Path: "/repo/.ai-launcher/config.yaml", Hash: "abc123"}},
	}
	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	if err := SaveGlobal(globalPath, fullGlobal); err != nil {
		t.Fatalf("SaveGlobal() error = %v", err)
	}
	if _, warnings, err := LoadGlobalWithWarnings(globalPath); err != nil || len(warnings) != 0 {
		t.Fatalf("LoadGlobalWithWarnings(saved full global config) = %v, %v; want no warnings", warnings, err)
	}
}
