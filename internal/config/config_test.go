package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestDefaultConfigurationsExposeLauncherContract(t *testing.T) {
	global := DefaultGlobal()
	local := DefaultLocal()

	if global.Version != CurrentVersion || local.Version != CurrentVersion {
		t.Fatalf("defaults use versions global=%q local=%q, want %q", global.Version, local.Version, CurrentVersion)
	}
	if local.Agent != "claude" || !local.Options.Jail || !local.Options.Memory {
		t.Fatalf("unsafe local defaults: %#v", local)
	}
	if len(global.Agents) == 0 || len(global.Permissions) == 0 || len(global.DefaultMounts) == 0 {
		t.Fatalf("global catalog must have agents, permissions, and mounts: %#v", global)
	}
	if global.MemoryServerURL != DefaultMemoryServerURL {
		t.Fatalf("default ai-memory URL = %q; want %q", global.MemoryServerURL, DefaultMemoryServerURL)
	}
	var lockedJail bool
	for _, permission := range global.Permissions {
		if permission.ID == "jail" {
			lockedJail = permission.Default && permission.Locked
		}
	}
	if !lockedJail {
		t.Fatal("default jail permission must be enabled and locked")
	}
}

func TestDefaultGlobalIncludesCommonCLIHarnesses(t *testing.T) {
	global := DefaultGlobal()
	wanted := []string{"claude", "codex", "opencode", "kimi", "kilo", "mimo", "agy", "pi", "crush", "omp", "cursor-agent", "oc", "gemini", "qwen", "aider", "goose", "kiro-cli", "openclaw", "hermes", "cline"}
	available := make(map[string]bool, len(global.Agents))
	for _, agent := range global.Agents {
		available[agent.Command] = true
	}
	for _, command := range wanted {
		if !available[command] {
			t.Errorf("default catalog does not include %q", command)
		}
	}
	for _, agent := range global.Agents {
		if agent.Command == "kilo" && !reflect.DeepEqual(agent.Aliases, []string{"kilocode", "kilo-code"}) {
			t.Fatalf("kilo aliases = %#v", agent.Aliases)
		}
		if agent.Command == "mimo" && !reflect.DeepEqual(agent.Aliases, []string{"mimocode", "mimo-code"}) {
			t.Fatalf("mimo aliases = %#v", agent.Aliases)
		}
		if agent.Command == "pi" && (agent.Memory == nil || agent.Memory.InstallMCP || !agent.Memory.InstallHooks) {
			t.Fatalf("Pi memory integration = %#v; want hooks-only", agent.Memory)
		}
		if agent.Command == "kimi" && (agent.Memory == nil || agent.Memory.Client != "kimi-code" || agent.Memory.Agent != "kimi-code") {
			t.Fatalf("Kimi memory integration = %#v", agent.Memory)
		}
	}
}

func TestLoadGlobalUsesDefaultsAndMergesOmittedSections(t *testing.T) {
	tempDir := t.TempDir()
	missing := filepath.Join(tempDir, "missing.yaml")

	got, err := LoadGlobal(missing)
	if err != nil {
		t.Fatalf("LoadGlobal(missing) error = %v", err)
	}
	if !reflect.DeepEqual(got, DefaultGlobal()) {
		t.Fatalf("LoadGlobal(missing) = %#v; want defaults", got)
	}

	path := filepath.Join(tempDir, "global.yaml")
	if err := os.WriteFile(path, []byte("agents:\n  - name: Test\n    command: test-agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if got.Version != CurrentVersion || len(got.Agents) != 1 || got.Agents[0].Command != "test-agent" {
		t.Fatalf("configured global fields were not retained: %#v", got)
	}
	if !reflect.DeepEqual(got.Permissions, DefaultGlobal().Permissions) || !reflect.DeepEqual(got.DefaultMounts, DefaultGlobal().DefaultMounts) {
		t.Fatalf("omitted global sections were not defaulted: %#v", got)
	}
}

func TestSaveGlobalRoundTripsConfiguredAgentPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	want := DefaultGlobal()
	want.Agents = append(want.Agents, Agent{
		Name: "Xpto", Command: "xpto", Aliases: []string{"xpto-cli"}, Path: "/opt/xpto/bin/xpto", SupportsMemory: true,
		Memory: &MemoryIntegration{Client: "xpto", Agent: "xpto", InstallMCP: true, InstallHooks: true},
		Release: &GitHubRelease{
			Repository: "acme/xpto",
			Assets:     map[string]string{"linux-amd64": "xpto-linux-amd64.tar.gz"},
			Binary:     "xpto", ChecksumAsset: "checksums.txt",
		},
	})
	want.Tools = append(want.Tools, Tool{Name: "helper", Command: "helper", Release: &GitHubRelease{Repository: "acme/helper", Assets: map[string]string{"darwin-arm64": "helper.zip"}, Binary: "helper"}})
	if err := SaveGlobal(path, want); err != nil {
		t.Fatalf("SaveGlobal() error = %v", err)
	}
	got, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	var found bool
	for _, agent := range got.Agents {
		if agent.Name == "Xpto" {
			found = true
			if agent.Command != "xpto" || agent.Path != "/opt/xpto/bin/xpto" || !reflect.DeepEqual(agent.Aliases, []string{"xpto-cli"}) {
				t.Fatalf("saved agent = %#v", agent)
			}
			if agent.Release == nil || agent.Release.Repository != "acme/xpto" || agent.Release.ChecksumAsset != "checksums.txt" {
				t.Fatalf("saved release = %#v", agent.Release)
			}
			if agent.Memory == nil || agent.Memory.Client != "xpto" || !agent.Memory.InstallHooks {
				t.Fatalf("saved memory integration = %#v", agent.Memory)
			}
		}
	}
	if !found {
		t.Fatal("saved agent was not found")
	}
	if len(got.Tools) != len(want.Tools) || got.Tools[len(got.Tools)-1].Release == nil {
		t.Fatalf("saved tools = %#v", got.Tools)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("global config mode = %04o; want 0600", info.Mode().Perm())
	}
}

func TestUpsertAgentIsIdempotentByNameOrCommand(t *testing.T) {
	global := Global{Agents: []Agent{{Name: "Xpto", Command: "xpto", Path: "/old/xpto"}}}
	if err := UpsertAgent(&global, Agent{Name: "Xpto", Command: "xpto", Path: "/new/xpto"}); err != nil {
		t.Fatal(err)
	}
	if len(global.Agents) != 1 || global.Agents[0].Path != "/new/xpto" {
		t.Fatalf("updated agents = %#v", global.Agents)
	}
	if err := UpsertAgent(&global, Agent{Name: "Other", Command: "xpto", Path: "/other/xpto"}); err != nil {
		t.Fatal(err)
	}
	if len(global.Agents) != 1 || global.Agents[0].Name != "Other" {
		t.Fatalf("command collision was not updated: %#v", global.Agents)
	}
}

func TestSaveGlobalRejectsInvalidTargets(t *testing.T) {
	if err := SaveGlobal("", DefaultGlobal()); err == nil {
		t.Fatal("SaveGlobal(empty path) = nil; want error")
	}
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveGlobal(filepath.Join(blocker, "config.yaml"), DefaultGlobal()); err == nil {
		t.Fatal("SaveGlobal(path below regular file) = nil; want error")
	}
}

func TestUpsertAgentRejectsInvalidAgents(t *testing.T) {
	if err := UpsertAgent(nil, Agent{Name: "Xpto", Command: "xpto"}); err == nil {
		t.Fatal("UpsertAgent(nil) = nil; want error")
	}
	global := Global{}
	if err := UpsertAgent(&global, Agent{Command: "xpto"}); err == nil {
		t.Fatal("UpsertAgent(empty name) = nil; want error")
	}
	if err := UpsertAgent(&global, Agent{Name: "Xpto"}); err == nil {
		t.Fatal("UpsertAgent(empty command) = nil; want error")
	}
}

func TestLoadConfigErrorsReturnSafeDefaults(t *testing.T) {
	tempDir := t.TempDir()
	malformed := filepath.Join(tempDir, "malformed.yaml")
	if err := os.WriteFile(malformed, []byte("agents: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	global, err := LoadGlobal(malformed)
	if err == nil || !strings.Contains(err.Error(), "parse global config") {
		t.Fatalf("LoadGlobal(malformed) error = %v; want parse error", err)
	}
	if !reflect.DeepEqual(global, DefaultGlobal()) {
		t.Fatalf("LoadGlobal(malformed) = %#v; want safe defaults", global)
	}

	unsupported := filepath.Join(tempDir, "unsupported.yaml")
	if err := os.WriteFile(unsupported, []byte("version: '99'\nagent: pi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	local, err := LoadLocal(unsupported)
	if err == nil || !strings.Contains(err.Error(), "unsupported config version") {
		t.Fatalf("LoadLocal(unsupported) error = %v; want version error", err)
	}
	if !reflect.DeepEqual(local, DefaultLocal()) {
		t.Fatalf("LoadLocal(unsupported) = %#v; want safe defaults", local)
	}
}

func TestLoadLocalSuppliesDefaultsForMissingFields(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "local.yaml")
	if err := os.WriteFile(path, []byte("permissions:\n  ssh: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != CurrentVersion || got.Agent != "claude" || !got.Permissions["ssh"] || !got.Options.Jail || !got.Options.Memory {
		t.Fatalf("LoadLocal() did not fill safe defaults: %#v", got)
	}
}

// Regression: a partial options mapping must not silently turn off another
// security default. The user explicitly chose yolo, not an unsafe launcher.
func TestLoadLocalPartialOptionsKeepEachOmittedSafetyDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.yaml")
	if err := os.WriteFile(path, []byte("options:\n  yolo: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Options.Jail || !got.Options.Memory || !got.Options.Yolo {
		t.Fatalf("partial options = %#v; omitted jail and memory must remain enabled", got.Options)
	}
}

func TestLoadLocalAcceptsDocumentedScalarExtraArgs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.yaml")
	contents := []byte("options:\n  extra_args: --model sonnet-4\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadLocal(path)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	want := []string{"--model", "sonnet-4"}
	if !reflect.DeepEqual(got.Options.ExtraArgs, want) {
		t.Fatalf("scalar extra args = %#v; want %#v", got.Options.ExtraArgs, want)
	}
}

// Regression: the persisted YAML distinguishes explicitly false values from
// an omitted options block. Saving must therefore be lossless for both flags.
func TestSaveLoadLocalPreservesExplicitDisabledOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "local.yaml")
	want := Local{Agent: "codex", Permissions: map[string]bool{"ssh": true}, Options: Options{Jail: false, Memory: false}}
	if err := SaveLocal(path, want); err != nil {
		t.Fatalf("SaveLocal() error = %v", err)
	}
	got, err := LoadLocal(path)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if got.Options.Jail || got.Options.Memory {
		t.Fatalf("disabled options changed after round trip: %#v", got.Options)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %04o; want 0600", info.Mode().Perm())
	}
}

// TestPropertySaveLoadPreservesConfiguredFields exercises serialization with
// generated strings and both permission/mount variants using rapid.
func TestPropertySaveLoadPreservesConfiguredFields(t *testing.T) {
	token := rapid.StringMatching(`[a-z0-9\-_]{1,12}`)
	rapid.Check(t, func(rt *rapid.T) {
		mode := "rw"
		if rapid.Bool().Draw(rt, "readOnly") {
			mode = "read-only"
		}
		workstream := token.Draw(rt, "workstream")
		want := Local{
			Agent:       "agent-" + token.Draw(rt, "agent"),
			Permissions: map[string]bool{"ssh": rapid.Bool().Draw(rt, "ssh")},
			Mounts:      []Mount{{Path: "/workspace/" + workstream, Mode: mode}},
			Options: Options{
				Jail:          true,
				Memory:        true,
				Yolo:          rapid.Bool().Draw(rt, "yolo"),
				NewWorkstream: workstream,
				ExtraArgs:     []string{"--" + token.Draw(rt, "extraArg")},
			},
		}
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := SaveLocal(path, want); err != nil {
			rt.Fatalf("SaveLocal() error = %v", err)
		}
		got, err := LoadLocal(path)
		if err != nil {
			rt.Fatalf("LoadLocal() error = %v", err)
		}
		if got.Agent != want.Agent || !reflect.DeepEqual(got.Permissions, want.Permissions) || !reflect.DeepEqual(got.Mounts, want.Mounts) || !reflect.DeepEqual(got.Options, want.Options) {
			rt.Fatalf("local config round-trip mismatch: got %#v; want %#v", got, want)
		}
	})
}

func TestValidateVersionAcceptsCompatibleVersionsAndRejectsOthers(t *testing.T) {
	for _, version := range []string{"", "1", "1.0", CurrentVersion, "  " + CurrentVersion + "\t"} {
		if err := ValidateVersion(version); err != nil {
			t.Errorf("ValidateVersion(%q) = %v; want nil", version, err)
		}
	}
	for _, version := range []string{"0", "1.1", "2", "99"} {
		if err := ValidateVersion(version); err == nil {
			t.Errorf("ValidateVersion(%q) = nil; want error", version)
		}
	}
}
