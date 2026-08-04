package launcher

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestAgentRequiredMountsCreatesAndReturnsStateDir(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, ".claude")
	got := AgentRequiredMounts(config.Agent{Command: "claude"}, dir, "darwin")
	if len(got) != 1 || got[0].Path != want || got[0].Mode != config.MountReadWrite {
		t.Fatalf("AgentRequiredMounts() = %#v; want one rw mount for %s", got, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("expected %s to be created", want)
	}
}

func TestAgentRequiredMountsEmptyForUnknownAgent(t *testing.T) {
	dir := t.TempDir()
	if got := AgentRequiredMounts(config.Agent{Command: "unknown-agent"}, dir, "darwin"); len(got) != 0 {
		t.Fatalf("AgentRequiredMounts() = %#v; want empty", got)
	}
}

func TestAgentRequiredMountsEmptyOnWindows(t *testing.T) {
	dir := t.TempDir()
	if got := AgentRequiredMounts(config.Agent{Command: "claude"}, dir, "windows"); len(got) != 0 {
		t.Fatalf("AgentRequiredMounts() = %#v; want empty on windows", got)
	}
}

func TestAgentRequiredMountsSkipsPathsThatCannotBeCreated(t *testing.T) {
	// Use an existing file as the home so MkdirAll fails.
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := AgentRequiredMounts(config.Agent{Command: "claude"}, file, "darwin"); len(got) != 0 {
		t.Fatalf("AgentRequiredMounts() = %#v; want empty when path is not a directory", got)
	}
}

func TestAgentRequiredMountsRespectsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom-claude")
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	got := AgentRequiredMounts(config.Agent{Command: "claude"}, "/unused-home", "darwin")
	if len(got) != 1 || got[0].Path != custom {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, custom)
	}
}

func TestAgentRequiredMountsResolvesTildePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "~/custom-claude")
	got := AgentRequiredMounts(config.Agent{Command: "claude"}, dir, "darwin")
	want := filepath.Join(dir, "custom-claude")
	if len(got) != 1 || got[0].Path != want {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, want)
	}
}

func TestAgentRequiredMountsResolvesTildeWithoutLeadingSlash(t *testing.T) {
	// Regression: filepath.Join(home, "/.claude") treats the remainder as
	// absolute and resolves to /.claude instead of under home.
	dir := t.TempDir()
	got := AgentRequiredMounts(config.Agent{Command: "claude"}, dir, "darwin")
	want := filepath.Join(dir, ".claude")
	if len(got) != 1 || got[0].Path != want {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, want)
	}
}

func TestAgentRequiredMountsUsesCatalogCommandWhenResolved(t *testing.T) {
	// When the PATH alias differs from the catalog command, the launcher must
	// still mount the agent's canonical state directories.
	dir := t.TempDir()
	agent := config.Agent{Name: "Cursor Agent", Command: "cursor", CatalogCommand: "cursor-agent"}
	got := AgentRequiredMounts(agent, dir, "darwin")
	want := filepath.Join(dir, ".cursor")
	if len(got) != 1 || got[0].Path != want {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, want)
	}
}

func TestAgentRequiredMountsForKiloRespectsXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg-config"))
	got := AgentRequiredMounts(config.Agent{Command: "kilo"}, "/unused-home", "linux")
	want := filepath.Join(dir, "xdg-config", "kilo")
	found := false
	for _, m := range got {
		if m.Path == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("AgentRequiredMounts() = %#v; want mount for %s", got, want)
	}
}

func TestAgentRequiredMountsForCodex(t *testing.T) {
	dir := t.TempDir()
	got := AgentRequiredMounts(config.Agent{Command: "codex"}, dir, "linux")
	want := filepath.Join(dir, ".codex")
	if len(got) != 1 || got[0].Path != want {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, want)
	}
}

func TestAgentRequiredMountsForAider(t *testing.T) {
	dir := t.TempDir()
	got := AgentRequiredMounts(config.Agent{Command: "aider"}, dir, "linux")
	want := filepath.Join(dir, ".aider")
	if len(got) != 1 || got[0].Path != want {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, want)
	}
}

func TestAgentRequiredMountsForGrok(t *testing.T) {
	dir := t.TempDir()
	got := AgentRequiredMounts(config.Agent{Command: "grok"}, dir, "darwin")
	want := filepath.Join(dir, ".grok")
	if len(got) != 1 || got[0].Path != want {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, want)
	}
}

func TestAgentRequiredMountsForGemini(t *testing.T) {
	dir := t.TempDir()
	got := AgentRequiredMounts(config.Agent{Command: "gemini"}, dir, "linux")
	want := filepath.Join(dir, ".gemini")
	if len(got) != 1 || got[0].Path != want {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, want)
	}
}

func TestAgentRequiredMountsForCursor(t *testing.T) {
	dir := t.TempDir()
	got := AgentRequiredMounts(config.Agent{Command: "cursor-agent"}, dir, "darwin")
	want := filepath.Join(dir, ".cursor")
	if len(got) != 1 || got[0].Path != want {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, want)
	}
}

func TestAgentRequiredMountsForOpencode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	dir := t.TempDir()
	got := AgentRequiredMounts(config.Agent{Command: "opencode"}, dir, "linux")
	want := []string{
		filepath.Join(dir, ".config", "opencode"),
		filepath.Join(dir, ".local", "share", "opencode"),
		filepath.Join(dir, ".local", "state", "opencode"),
		filepath.Join(dir, ".cache", "opencode"),
	}
	if len(got) != len(want) {
		t.Fatalf("AgentRequiredMounts() = %#v; want %d mounts", got, len(want))
	}
	for i, path := range want {
		if got[i].Path != path || got[i].Mode != config.MountReadWrite {
			t.Fatalf("mount %d = %#v; want %q rw", i, got[i], path)
		}
	}
}

func TestAgentRequiredMountsForMimoWithEnv(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "mimo-root")
	t.Setenv("MIMOCODE_HOME", custom)
	got := AgentRequiredMounts(config.Agent{Command: "mimo"}, "/unused", "linux")
	if len(got) != 1 || got[0].Path != custom {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, custom)
	}
}

func TestAgentRequiredMountsForOpenclawWithEnv(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "openclaw-state")
	t.Setenv("OPENCLAW_STATE_DIR", custom)
	got := AgentRequiredMounts(config.Agent{Command: "openclaw"}, "/unused", "linux")
	if len(got) != 1 || got[0].Path != custom {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, custom)
	}
}

func TestAgentRequiredMountsForClineWithEnv(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "cline-data")
	t.Setenv("CLINE_DATA_DIR", custom)
	got := AgentRequiredMounts(config.Agent{Command: "cline"}, "/unused", "linux")
	if len(got) != 1 || got[0].Path != custom {
		t.Fatalf("AgentRequiredMounts() = %#v; want one mount for %s", got, custom)
	}
}

func TestAgentCredentialStoreEnvCursorOnDarwin(t *testing.T) {
	key, value := AgentCredentialStoreEnv(config.Agent{Command: "cursor-agent"}, "darwin")
	if key != "AGENT_CLI_CREDENTIAL_STORE" || value != "file" {
		t.Fatalf("AgentCredentialStoreEnv() = %q, %q; want AGENT_CLI_CREDENTIAL_STORE=file", key, value)
	}
}

func TestAgentCredentialStoreEnvEmptyForOthers(t *testing.T) {
	key, value := AgentCredentialStoreEnv(config.Agent{Command: "claude"}, "darwin")
	if key != "" || value != "" {
		t.Fatalf("AgentCredentialStoreEnv() = %q, %q; want empty", key, value)
	}
}

func TestAgentCredentialStoreEnvEmptyOnLinux(t *testing.T) {
	key, value := AgentCredentialStoreEnv(config.Agent{Command: "cursor-agent"}, "linux")
	if key != "" || value != "" {
		t.Fatalf("AgentCredentialStoreEnv() = %q, %q; want empty on linux", key, value)
	}
}

func TestEnvOrHomeReturnsEnvWhenSet(t *testing.T) {
	t.Setenv("TEST_HOME_OVERRIDE", "/custom/path")
	if got := envOrHome("/home", "TEST_HOME_OVERRIDE", ".sub"); got != "/custom/path" {
		t.Fatalf("envOrHome() = %q; want /custom/path", got)
	}
}

func TestEnvOrHomeReturnsHomeSubPath(t *testing.T) {
	t.Setenv("TEST_HOME_OVERRIDE", "")
	if got := envOrHome("/home", "TEST_HOME_OVERRIDE", ".sub"); got != "/home/.sub" {
		t.Fatalf("envOrHome() = %q; want /home/.sub", got)
	}
}

func TestEnvOrHomeReturnsAbsoluteSubPath(t *testing.T) {
	t.Setenv("TEST_HOME_OVERRIDE", "")
	if got := envOrHome("/home", "TEST_HOME_OVERRIDE", "/absolute/sub"); got != "/absolute/sub" {
		t.Fatalf("envOrHome() = %q; want /absolute/sub", got)
	}
}

func TestEnsureDirCreatesMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a", "b")
	if err := ensureDir(target); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("expected directory %s", target)
	}
}

func TestEnsureDirKeepsExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := ensureDir(dir); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureDirFailsForFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureDir(file); err == nil {
		t.Fatal("ensureDir should fail when path is a file")
	}
}

func TestXDGDdirsRespectEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	t.Setenv("XDG_DATA_HOME", "/data")
	t.Setenv("XDG_STATE_HOME", "/state")
	t.Setenv("XDG_CACHE_HOME", "/cache")
	got := xdgDirs("/home", "app")
	want := []string{"/cfg/app", "/data/app", "/state/app", "/cache/app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xdgDirs() = %#v; want %#v", got, want)
	}
}

// TestAgentRequiredMountsCoversAllCatalogAgents exercises every agent known to
// agentStateDirs so SonarCloud coverage stays above the per-file gate.
func TestAgentRequiredMountsCoversAllCatalogAgents(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("MIMOCODE_HOME", "")
	t.Setenv("OPENCLAW_STATE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")

	cases := []string{
		"claude", "codex", "opencode", "oc", "kimi", "kilo", "mimo",
		"agy", "pi", "omp", "cursor-agent", "grok", "zero", "devin",
		"gemini", "qwen", "aider", "goose", "kiro-cli", "openclaw",
		"hermes", "cline",
	}
	dir := t.TempDir()
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got := AgentRequiredMounts(config.Agent{Command: cmd}, dir, "darwin")
			if cmd == "zero" {
				if len(got) != 0 {
					t.Fatalf("zero should need no state mounts, got %#v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("agent %q produced no state mounts", cmd)
			}
			for _, m := range got {
				if m.Mode != config.MountReadWrite {
					t.Fatalf("mount %q is not rw", m.Path)
				}
			}
		})
	}
}
