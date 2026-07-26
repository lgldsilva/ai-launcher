package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
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
	if len(global.Agents) == 0 || len(global.Permissions) == 0 {
		t.Fatalf("global catalog must have agents and permissions: %#v", global)
	}
	wantMounts := DefaultMountCandidates(runtime.GOOS)
	if !reflect.DeepEqual(global.DefaultMounts, wantMounts) {
		t.Fatalf("default mounts = %#v; want platform candidates %#v", global.DefaultMounts, wantMounts)
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
	wanted := []string{"claude", "codex", "opencode", "kimi", "kilo", "mimo", "agy", "pi", "crush", "omp", "cursor-agent", "grok", "zero", "devin", "oc", "gemini", "qwen", "aider", "goose", "kiro-cli", "openclaw", "hermes", "cline"}
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
		if agent.Command == "openclaw" && (agent.Memory == nil || !agent.Memory.InstallMCP || agent.Memory.InstallHooks) {
			t.Fatalf("OpenClaw memory integration = %#v; want MCP-only", agent.Memory)
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
	// Agents merge per entry: the user's addition survives alongside the
	// built-ins instead of replacing the whole catalog.
	if got.Version != CurrentVersion {
		t.Fatalf("version = %q; want %q", got.Version, CurrentVersion)
	}
	if _, ok := agentByCommand(got.Agents, "test-agent"); !ok {
		t.Fatalf("configured agent was not retained: %#v", got.Agents)
	}
	if _, ok := agentByCommand(got.Agents, "claude"); !ok {
		t.Fatalf("built-in agents were dropped by a user entry: %#v", got.Agents)
	}
	if !reflect.DeepEqual(got.Permissions, DefaultGlobal().Permissions) || !reflect.DeepEqual(got.DefaultMounts, DefaultGlobal().DefaultMounts) {
		t.Fatalf("omitted global sections were not defaulted: %#v", got)
	}
}

func TestLoadGlobalMigratesLegacyMemoryServerURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "global.yaml")
	body := "memory_server_url: " + legacyMemoryServerURL + "\nagents:\n  - name: Test\n    command: test-agent\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if got.MemoryServerURL != DefaultMemoryServerURL {
		t.Fatalf("MemoryServerURL = %q; want migrated default %q", got.MemoryServerURL, DefaultMemoryServerURL)
	}
	// Explicit non-legacy override must be kept.
	custom := "https://aimemory.example.test"
	if err := os.WriteFile(path, []byte("memory_server_url: "+custom+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal(custom) error = %v", err)
	}
	if got.MemoryServerURL != custom {
		t.Fatalf("custom MemoryServerURL = %q; want %q", got.MemoryServerURL, custom)
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

func TestDefaultGlobalDeclaresHarnessParamsAndYoloFlags(t *testing.T) {
	agents := make(map[string]Agent)
	for _, agent := range DefaultGlobal().Agents {
		agents[agent.Command] = agent
	}
	yoloFlags := map[string]string{
		"claude":   "--dangerously-skip-permissions",
		"codex":    "--dangerously-bypass-approvals-and-sandbox",
		"opencode": "--auto",
		"pi":       "--approve",
		"crush":    "--yolo",
		"kimi":     "--yolo",
	}
	for command, flag := range yoloFlags {
		if agents[command].YoloFlag != flag {
			t.Errorf("%s yolo flag = %q; want %q", command, agents[command].YoloFlag, flag)
		}
	}
	for _, command := range []string{"claude", "codex", "kimi", "gemini"} {
		params := agents[command].Params
		if len(params) == 0 || params[0].Name != "model" || params[0].Flag != "--model" || !params[0].TakesValue {
			t.Errorf("%s params = %#v; want a model param first", command, params)
		}
	}
	kimi := agents["kimi"]
	if len(kimi.Params) != 2 || kimi.Params[1].Name != "query" || kimi.Params[1].Flag != "--query" || !kimi.Params[1].TakesValue {
		t.Fatalf("kimi params = %#v; want model and query", kimi.Params)
	}
	if len(agents["crush"].Params) != 0 {
		t.Fatalf("crush params = %#v; want none (extra_args pass-through)", agents["crush"].Params)
	}
}

func TestLoadGlobalParsesAgentParamsAndYoloFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "global.yaml")
	contents := []byte(`agents:
  - name: Custom
    command: custom-cli
    yolo_flag: --force-yolo
    params:
      - name: model
        flag: --model
        description: Model to run
        takes_value: true
      - name: fast
        flag: --fast
        takes_value: false
permissions:
  - id: jail
    name: Jail
    default: true
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	agent, ok := agentByCommand(got.Agents, "custom-cli")
	if !ok {
		t.Fatalf("configured agent was not retained: %#v", got.Agents)
	}
	if agent.YoloFlag != "--force-yolo" {
		t.Fatalf("yolo flag = %q", agent.YoloFlag)
	}
	want := []Param{
		{Name: "model", Flag: "--model", Description: "Model to run", TakesValue: true},
		{Name: "fast", Flag: "--fast"},
	}
	if !reflect.DeepEqual(agent.Params, want) {
		t.Fatalf("params = %#v; want %#v", agent.Params, want)
	}
}

func TestProfileSaveLoadDeleteListRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	global := DefaultGlobal()
	review := Profile{
		Agent:       "claude",
		Permissions: map[string]bool{"ssh": true},
		Mounts:      []Mount{{Path: "/reference", Mode: "read-only"}},
		Options: &Options{
			Jail: true, Memory: true, Yolo: true,
			ExtraArgs:   []string{"--verbose"},
			ParamValues: map[string]string{"model": "sonnet"},
		},
	}
	quick := Profile{Agent: "kimi"}
	if err := SetProfile(&global, "review", review); err != nil {
		t.Fatal(err)
	}
	if err := SetProfile(&global, "quick", quick); err != nil {
		t.Fatal(err)
	}
	if err := SaveGlobal(path, global); err != nil {
		t.Fatalf("SaveGlobal() error = %v", err)
	}

	loaded, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if names := ProfileNames(loaded); !reflect.DeepEqual(names, []string{"quick", "review"}) {
		t.Fatalf("ProfileNames() = %#v", names)
	}
	if !reflect.DeepEqual(loaded.Profiles["review"], review) {
		t.Fatalf("loaded profile = %#v; want %#v", loaded.Profiles["review"], review)
	}
	if !reflect.DeepEqual(loaded.Profiles["quick"], quick) {
		t.Fatalf("loaded minimal profile = %#v; want %#v", loaded.Profiles["quick"], quick)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("global config mode = %04o; want 0600", info.Mode().Perm())
	}

	if !DeleteProfile(&loaded, "quick") {
		t.Fatal("DeleteProfile(quick) = false; want true")
	}
	if DeleteProfile(&loaded, "quick") {
		t.Fatal("DeleteProfile(quick) twice = true; want false")
	}
	if err := SaveGlobal(path, loaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if names := ProfileNames(reloaded); !reflect.DeepEqual(names, []string{"review"}) {
		t.Fatalf("profiles after delete = %#v", names)
	}
}

func TestSetProfileRejectsInvalidInput(t *testing.T) {
	if err := SetProfile(nil, "p", Profile{Agent: "claude"}); err == nil {
		t.Fatal("SetProfile(nil global) = nil; want error")
	}
	global := Global{}
	if err := SetProfile(&global, "  ", Profile{Agent: "claude"}); err == nil {
		t.Fatal("SetProfile(empty name) = nil; want error")
	}
	if err := SetProfile(&global, "p", Profile{}); err == nil {
		t.Fatal("SetProfile(empty agent) = nil; want error")
	}
	if DeleteProfile(nil, "p") {
		t.Fatal("DeleteProfile(nil global) = true; want false")
	}
	if names := ProfileNames(Global{}); len(names) != 0 {
		t.Fatalf("ProfileNames(empty) = %#v", names)
	}
}

func TestProfileSummaryDescribesSelection(t *testing.T) {
	full := Profile{
		Agent:  "claude",
		Mounts: []Mount{{Path: "/a"}, {Path: "/b"}},
		Options: &Options{
			Jail: true, Memory: true, Yolo: true,
			ParamValues: map[string]string{"model": "sonnet"},
		},
	}
	if got, want := ProfileSummary(full), "jail,memory,yolo params=1 mounts=2"; got != want {
		t.Fatalf("ProfileSummary(full) = %q; want %q", got, want)
	}
	minimal := Profile{Agent: "kimi", Options: &Options{}}
	if got := ProfileSummary(minimal); got != "" {
		t.Fatalf("ProfileSummary(minimal) = %q; want empty", got)
	}
	if got := ProfileSummary(Profile{Agent: "kimi"}); got != "" {
		t.Fatalf("ProfileSummary(nil options) = %q; want empty", got)
	}
}

// TestPropertyProfileRoundTripPreservesAllFields saves and reloads generated
// profiles through the global config, checking every field survives.
func TestPropertyProfileRoundTripPreservesAllFields(t *testing.T) {
	token := rapid.StringMatching(`[a-z0-9\-_]{1,12}`)
	boolMap := rapid.MapOf(token, rapid.Bool())
	textMap := rapid.MapOf(token, token)
	rapid.Check(t, func(rt *rapid.T) {
		permissions := boolMap.Draw(rt, "permissions")
		if len(permissions) == 0 {
			permissions = nil
		}
		paramValues := textMap.Draw(rt, "paramValues")
		if len(paramValues) == 0 {
			paramValues = nil
		}
		var mounts []Mount
		if rapid.Bool().Draw(rt, "hasMount") {
			mounts = []Mount{{Path: "/workspace/" + token.Draw(rt, "mount"), Mode: "rw"}}
		}
		var extraArgs []string
		if rapid.Bool().Draw(rt, "hasExtraArg") {
			extraArgs = []string{"--" + token.Draw(rt, "extraArg")}
		}
		want := Profile{
			Agent:       "agent-" + token.Draw(rt, "agent"),
			Permissions: permissions,
			Mounts:      mounts,
			Options: &Options{
				Jail:          rapid.Bool().Draw(rt, "jail"),
				Memory:        rapid.Bool().Draw(rt, "memory"),
				Yolo:          rapid.Bool().Draw(rt, "yolo"),
				NewWorkstream: token.Draw(rt, "workstream"),
				ExtraArgs:     extraArgs,
				ParamValues:   paramValues,
			},
		}
		global := DefaultGlobal()
		if err := SetProfile(&global, "generated", want); err != nil {
			rt.Fatalf("SetProfile() error = %v", err)
		}
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := SaveGlobal(path, global); err != nil {
			rt.Fatalf("SaveGlobal() error = %v", err)
		}
		loaded, err := LoadGlobal(path)
		if err != nil {
			rt.Fatalf("LoadGlobal() error = %v", err)
		}
		if got := loaded.Profiles["generated"]; !reflect.DeepEqual(got, want) {
			rt.Fatalf("profile round-trip mismatch: got %#v; want %#v", got, want)
		}
	})
}

func TestTouchRecentAgentOrdersAndDedupes(t *testing.T) {
	cfg := Global{}
	TouchRecentAgent(&cfg, "claude")
	TouchRecentAgent(&cfg, "codex")
	TouchRecentAgent(&cfg, "claude")
	TouchRecentAgent(&cfg, "  ")
	TouchRecentAgent(nil, "x")
	if !reflect.DeepEqual(cfg.RecentAgents, []string{"claude", "codex"}) {
		t.Fatalf("RecentAgents = %#v; want claude then codex", cfg.RecentAgents)
	}
	for i := 0; i < recentAgentsMax+5; i++ {
		TouchRecentAgent(&cfg, fmt.Sprintf("agent-%d", i))
	}
	if len(cfg.RecentAgents) != recentAgentsMax {
		t.Fatalf("RecentAgents length = %d; want cap %d", len(cfg.RecentAgents), recentAgentsMax)
	}
}

func TestDefaultMountCandidatesByGOOS(t *testing.T) {
	cases := map[string][]string{
		"linux":   {"/storage", "/storage/Projetos", "/storage/cache"},
		"darwin":  {"/Volumes/MSD512", "/Volumes/MSD512/Projetos"},
		"windows": nil,
		"freebsd": nil,
	}
	for goos, want := range cases {
		if got := DefaultMountCandidates(goos); !reflect.DeepEqual(got, want) {
			t.Errorf("DefaultMountCandidates(%q) = %#v; want %#v", goos, got, want)
		}
	}
}

func TestExistingPathsKeepsOnlyPresentEntries(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present")
	if err := os.MkdirAll(present, 0o750); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")
	got := ExistingPaths([]string{"", "  ", missing, present, present + "-gone"})
	if !reflect.DeepEqual(got, []string{present}) {
		t.Fatalf("ExistingPaths = %#v; want only %q", got, present)
	}
	if ExistingPaths(nil) != nil {
		t.Fatal("ExistingPaths(nil) must return nil")
	}
}

func TestPermissionSupportedOn(t *testing.T) {
	cases := []struct {
		name       string
		permission Permission
		goos       string
		want       bool
	}{
		{"empty platforms matches every OS", Permission{ID: "ssh"}, "windows", true},
		{"empty platforms matches linux", Permission{ID: "ssh"}, "linux", true},
		{"listed OS matches", Permission{ID: "display", Platforms: []string{"linux", "darwin"}}, "darwin", true},
		{"single listed OS matches", Permission{ID: "systemd-user", Platforms: []string{"linux"}}, "linux", true},
		{"unlisted OS does not match", Permission{ID: "systemd-user", Platforms: []string{"linux"}}, "darwin", false},
		{"windows excluded from the desktop pair", Permission{ID: "pictures", Platforms: []string{"linux", "darwin"}}, "windows", false},
	}
	for _, tc := range cases {
		if got := PermissionSupportedOn(tc.permission, tc.goos); got != tc.want {
			t.Errorf("%s: PermissionSupportedOn(%q, %q) = %t; want %t", tc.name, tc.permission.ID, tc.goos, got, tc.want)
		}
	}
}

func TestDefaultGlobalIncludesV115Permissions(t *testing.T) {
	byID := make(map[string]Permission)
	for _, permission := range DefaultGlobal().Permissions {
		byID[permission.ID] = permission
	}
	cases := []struct {
		id        string
		def       bool
		platforms []string
	}{
		// display, mise and worktree stay off: ai-jail auto-detects them, and
		// enabling the permission forces them on rather than mirroring a default.
		{"display", false, []string{"linux"}},
		{"pictures", false, []string{"linux", "darwin"}},
		{"tailscale", false, []string{"linux", "darwin"}},
		{"systemd-user", false, []string{"linux"}},
		{"mise", false, nil},
		{"worktree", false, nil},
	}
	for _, tc := range cases {
		permission, ok := byID[tc.id]
		if !ok {
			t.Errorf("DefaultGlobal() missing permission %q", tc.id)
			continue
		}
		if permission.Default != tc.def {
			t.Errorf("permission %q default = %t; want %t", tc.id, permission.Default, tc.def)
		}
		if !reflect.DeepEqual(permission.Platforms, tc.platforms) {
			t.Errorf("permission %q platforms = %#v; want %#v", tc.id, permission.Platforms, tc.platforms)
		}
		if len(permission.Requires) != 1 || permission.Requires[0] != "jail" {
			t.Errorf("permission %q requires = %#v; want [jail]", tc.id, permission.Requires)
		}
	}
}

func TestMergePermissionsAppendsNewDefaults(t *testing.T) {
	defaults := DefaultGlobal().Permissions
	user := []Permission{
		{ID: "jail", Name: "Custom Jail", Default: true, Locked: true},
		{ID: "ssh", Name: "SSH access", Default: true, Requires: []string{"jail"}},
		{ID: "custom", Name: "Custom permission", Default: false},
	}
	got := mergePermissions(defaults, user)
	byID := make(map[string]Permission, len(got))
	for _, permission := range got {
		byID[permission.ID] = permission
	}
	if byID["jail"].Name != "Custom Jail" {
		t.Errorf("user jail entry was not preserved: %#v", byID["jail"])
	}
	if !byID["ssh"].Default {
		t.Errorf("user ssh default=true was not preserved: %#v", byID["ssh"])
	}
	for _, id := range []string{"display", "pictures", "tailscale", "systemd-user", "mise", "worktree"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("new default permission %q missing after merge", id)
		}
	}
	if _, ok := byID["custom"]; !ok {
		t.Error("user-defined custom permission was dropped")
	}
}

func TestMergePermissionsEmptyUserKeepsDefaults(t *testing.T) {
	defaults := DefaultGlobal().Permissions
	got := mergePermissions(defaults, nil)
	if !reflect.DeepEqual(got, defaults) {
		t.Errorf("mergePermissions(defaults, nil) = %#v; want defaults", got)
	}
}

func TestJailFlagsIsZeroCoversV115Fields(t *testing.T) {
	if !(JailFlags{}).IsZero() {
		t.Fatal("IsZero() = false for an empty JailFlags")
	}
	for name, mutate := range map[string]func(*JailFlags){
		"mask_exceptions":      func(f *JailFlags) { f.MaskExceptions = []string{"/srv/shared"} },
		"deny_path_exceptions": func(f *JailFlags) { f.DenyPathExceptions = []string{"/var/cache"} },
		"hide_dotdirs":         func(f *JailFlags) { f.HideDotdirs = []string{".aws"} },
		"status_bar_style":     func(f *JailFlags) { f.StatusBarStyle = "dark" },
	} {
		var flags JailFlags
		mutate(&flags)
		if flags.IsZero() {
			t.Errorf("IsZero() = true with %s set", name)
		}
	}
}

// The harness lookup must trim user input and scan the whole list: dropping
// the TrimSpace, breaking after the first entry, or flipping the comparison
// all change which names `ai-memory run` accepts.
func TestSupportsMemoryRunHarnessTrimsAndScansTheWholeList(t *testing.T) {
	for _, name := range []string{"claude", "grok", "kimi", "  claude  ", "\topencode\n"} {
		if !SupportsMemoryRunHarness(name) {
			t.Errorf("SupportsMemoryRunHarness(%q) = false; want true", name)
		}
	}
	for _, name := range []string{"", "   ", "not-a-harness", "claude-extra"} {
		if SupportsMemoryRunHarness(name) {
			t.Errorf("SupportsMemoryRunHarness(%q) = true; want false", name)
		}
	}
}

// The MRU cap is a literal contract: the list must hold exactly 32 entries,
// newest first, with the oldest evicted beyond that.
func TestTouchRecentAgentCapsAtExactly32Entries(t *testing.T) {
	cfg := Global{}
	for i := 0; i < 40; i++ {
		TouchRecentAgent(&cfg, fmt.Sprintf("agent-%02d", i))
	}
	if len(cfg.RecentAgents) != 32 {
		t.Fatalf("RecentAgents length = %d; want exactly 32", len(cfg.RecentAgents))
	}
	if cfg.RecentAgents[0] != "agent-39" || cfg.RecentAgents[31] != "agent-08" {
		t.Fatalf("RecentAgents boundaries = %q ... %q; want agent-39 ... agent-08",
			cfg.RecentAgents[0], cfg.RecentAgents[31])
	}
}

// Re-touching an existing entry moves it to the front but must keep every
// entry that followed it — a `continue` turned into `break` drops the tail.
func TestTouchRecentAgentDedupesWithoutDroppingTheTail(t *testing.T) {
	cfg := Global{RecentAgents: []string{"a", "x", "b"}}
	TouchRecentAgent(&cfg, "x")
	if !reflect.DeepEqual(cfg.RecentAgents, []string{"x", "a", "b"}) {
		t.Fatalf("RecentAgents = %#v; want [x a b]", cfg.RecentAgents)
	}
}

// SetProfile trims the agent before storing, so a padded name does not
// poison the profile snapshot.
func TestSetProfileTrimsAgentWhitespace(t *testing.T) {
	global := Global{}
	if err := SetProfile(&global, "p", Profile{Agent: "  claude  "}); err != nil {
		t.Fatal(err)
	}
	if global.Profiles["p"].Agent != "claude" {
		t.Fatalf("stored agent = %q; want trimmed %q", global.Profiles["p"].Agent, "claude")
	}
}

// ProfileNames must come out sorted no matter how the map iterates.
func TestProfileNamesSortsRegardlessOfMapOrder(t *testing.T) {
	global := Global{}
	for _, name := range []string{"delta", "alpha", "charlie", "bravo", "echo"} {
		if err := SetProfile(&global, name, Profile{Agent: "claude"}); err != nil {
			t.Fatal(err)
		}
	}
	got := ProfileNames(global)
	want := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProfileNames() = %#v; want sorted %#v", got, want)
	}
}

// Single-entry summaries are the boundary the len() > 0 checks guard: one
// toggle, one param or one mount must each render, and an option set with no
// toggles must not start the summary with a stray space.
func TestProfileSummaryRendersSingleEntriesAndSkipsEmptyGroups(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
		want    string
	}{
		{"one toggle", Profile{Agent: "claude", Options: &Options{Jail: true}}, "jail"},
		{"params without toggles", Profile{Agent: "claude", Options: &Options{ParamValues: map[string]string{"model": "sonnet"}}}, "params=1"},
		{"one mount", Profile{Agent: "claude", Mounts: []Mount{{Path: "/a"}}}, "mounts=1"},
		{"single toggle with single mount", Profile{Agent: "claude", Mounts: []Mount{{Path: "/a"}}, Options: &Options{Memory: true}}, "memory mounts=1"},
	}
	for _, tc := range cases {
		if got := ProfileSummary(tc.profile); got != tc.want {
			t.Errorf("%s: ProfileSummary() = %q; want %q", tc.name, got, tc.want)
		}
	}
}

// A padded path must be cleaned before the stat and stored in trimmed form.
func TestExistingPathsTrimsSurroundingWhitespace(t *testing.T) {
	present := filepath.Join(t.TempDir(), "present")
	if err := os.MkdirAll(present, 0o750); err != nil {
		t.Fatal(err)
	}
	got := ExistingPaths([]string{"  " + present + "\t"})
	if !reflect.DeepEqual(got, []string{present}) {
		t.Fatalf("ExistingPaths = %#v; want [%q]", got, present)
	}
}

// The transitive closure must converge regardless of declaration order: with
// the chain declared leaf-first, one pass is not enough, so a dropped
// `changed = true` leaves the middle of the chain unmarked.
func TestJailDependentIDsResolvesTransitiveChainInAnyOrder(t *testing.T) {
	permissions := []Permission{
		{ID: "gpu", Requires: []string{"docker"}},
		{ID: "docker", Requires: []string{"jail"}},
		{ID: "jail"},
	}
	dependent := JailDependentIDs(permissions)
	for _, id := range []string{"jail", "docker", "gpu"} {
		if !dependent[id] {
			t.Errorf("JailDependentIDs() missing %q with leaf-first declaration order", id)
		}
	}
}

// A document that neither the list form nor the scalar form can decode must
// surface an error instead of silently producing a zero Options.
func TestOptionsUnmarshalRejectsUndecodableDocuments(t *testing.T) {
	var options Options
	if err := yaml.Unmarshal([]byte("- 1\n- 2\n"), &options); err == nil {
		t.Fatal("UnmarshalYAML(sequence) = nil; want the list-form error")
	}

	// The scalar fallback only applies when the list form fails: a mapping
	// with a scalar extra_args decodes through the second attempt.
	var scalar Options
	if err := yaml.Unmarshal([]byte("jail: true\nextra_args: --model sonnet\n"), &scalar); err != nil {
		t.Fatalf("UnmarshalYAML(scalar extra_args) error = %v", err)
	}
	if !reflect.DeepEqual(scalar.ExtraArgs, []string{"--model", "sonnet"}) || !scalar.Jail {
		t.Fatalf("scalar form decoded to %#v", scalar)
	}
}

// Both save paths must stamp the schema version into the file itself, not
// just into the in-memory struct.
func TestSaveFunctionsStampCurrentVersionIntoTheFile(t *testing.T) {
	readVersion := func(t *testing.T, path string) string {
		t.Helper()
		raw, err := os.ReadFile(path) // #nosec G304 -- test-written file
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		version, _ := document["version"].(string)
		return version
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	if err := SaveGlobal(globalPath, Global{Version: "stale", Agents: []Agent{{Name: "X", Command: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if got := readVersion(t, globalPath); got != CurrentVersion {
		t.Fatalf("saved global version = %q; want %q", got, CurrentVersion)
	}

	localPath := filepath.Join(t.TempDir(), "local.yaml")
	if err := SaveLocal(localPath, Local{Version: "stale", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if got := readVersion(t, localPath); got != CurrentVersion {
		t.Fatalf("saved local version = %q; want %q", got, CurrentVersion)
	}
}

// SaveRecentAgents is a surgical update: it stamps the version and the MRU
// list while leaving every other key in the document untouched.
func TestSaveRecentAgentsPreservesOtherKeysAndStampsTheDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "global.yaml")
	body := "version: '1.0'\nmemory_server_url: https://example.test\nagents:\n  - name: Claude\n    command: claude\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveRecentAgents(path, []string{"kimi", "claude"}); err != nil {
		t.Fatalf("SaveRecentAgents() error = %v", err)
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- test-written file
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document["version"] != CurrentVersion {
		t.Fatalf("version = %#v; want %q", document["version"], CurrentVersion)
	}
	if document["memory_server_url"] != "https://example.test" {
		t.Fatalf("memory_server_url = %#v; the untouched key must survive", document["memory_server_url"])
	}
	agents, ok := document["agents"].([]any)
	if !ok || len(agents) != 1 {
		t.Fatalf("agents = %#v; the untouched list must survive", document["agents"])
	}
	recent, ok := document["recent_agents"].([]any)
	if !ok || len(recent) != 2 || recent[0] != "kimi" || recent[1] != "claude" {
		t.Fatalf("recent_agents = %#v; want [kimi claude]", document["recent_agents"])
	}
}

// An empty existing file decodes to a nil document; the save must treat it
// like a missing file instead of choking on the nil map.
func TestSaveRecentAgentsHandlesAnEmptyExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "global.yaml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveRecentAgents(path, []string{"claude"}); err != nil {
		t.Fatalf("SaveRecentAgents(empty file) error = %v", err)
	}
	loaded, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if !reflect.DeepEqual(loaded.RecentAgents, []string{"claude"}) {
		t.Fatalf("RecentAgents = %#v; want [claude]", loaded.RecentAgents)
	}
}

// An empty memory_server_url inherits the default, while a single configured
// tool must not be replaced by the built-in pair.
func TestLoadGlobalDefaultsEmptyMemoryURLAndKeepsConfiguredTools(t *testing.T) {
	tempDir := t.TempDir()
	emptyURL := filepath.Join(tempDir, "empty-url.yaml")
	if err := os.WriteFile(emptyURL, []byte("agents:\n  - name: Test\n    command: test-agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadGlobal(emptyURL)
	if err != nil {
		t.Fatal(err)
	}
	if got.MemoryServerURL != DefaultMemoryServerURL {
		t.Fatalf("MemoryServerURL = %q; want default %q", got.MemoryServerURL, DefaultMemoryServerURL)
	}
	if len(got.Tools) != len(DefaultGlobal().Tools) {
		t.Fatalf("Tools = %#v; omitted tools must fall back to the built-ins", got.Tools)
	}

	customTool := filepath.Join(tempDir, "custom-tool.yaml")
	body := "tools:\n  - name: mine\n    command: mine\n"
	if err := os.WriteFile(customTool, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = LoadGlobal(customTool)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Command != "mine" {
		t.Fatalf("Tools = %#v; a configured tool list must be kept as-is", got.Tools)
	}
}

// UpsertAgent trims the identifying fields before validating and storing, so
// a padded entry does not slip past the required-field checks or get
// persisted with whitespace.
func TestUpsertAgentTrimsNameCommandAndPath(t *testing.T) {
	global := Global{}
	if err := UpsertAgent(&global, Agent{Name: "  Xpto  ", Command: " xpto ", Path: " /opt/xpto "}); err != nil {
		t.Fatal(err)
	}
	stored := global.Agents[0]
	if stored.Name != "Xpto" || stored.Command != "xpto" || stored.Path != "/opt/xpto" {
		t.Fatalf("stored agent = %#v; want all fields trimmed", stored)
	}
	// A padded re-upsert of the same command must update, not duplicate.
	if err := UpsertAgent(&global, Agent{Name: "Xpto", Command: "  xpto", Path: "/new"}); err != nil {
		t.Fatal(err)
	}
	if len(global.Agents) != 1 || global.Agents[0].Path != "/new" {
		t.Fatalf("agents after padded re-upsert = %#v", global.Agents)
	}
}

// The upsert match is on name OR command: dropping the name side of the
// disjunction turns a rename into a duplicate entry.
func TestUpsertAgentMatchesOnNameAlone(t *testing.T) {
	global := Global{Agents: []Agent{{Name: "Xpto", Command: "xpto", Path: "/old"}}}
	if err := UpsertAgent(&global, Agent{Name: "Xpto", Command: "renamed-xpto", Path: "/new"}); err != nil {
		t.Fatal(err)
	}
	if len(global.Agents) != 1 || global.Agents[0].Command != "renamed-xpto" || global.Agents[0].Path != "/new" {
		t.Fatalf("agents = %#v; want the existing entry updated in place", global.Agents)
	}
}
