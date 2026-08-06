package container

import (
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestInstallConfig(t *testing.T) {
	global := config.DefaultGlobal()
	// kilo is the only built-in agent with a GitHub release recipe (the
	// mainstream CLIs install via script); it has no memory integration, so
	// this also exercises the no-memory-tool path.
	var kilo config.Agent
	for _, agent := range global.Agents {
		if agent.Command == "kilo" {
			kilo = agent
		}
	}
	if kilo.Release == nil {
		t.Fatal("pre-condition: kilo must have a release recipe")
	}
	got, err := InstallConfig(global, []AgentInstall{{Command: "kilo", Kind: InstallRelease, Version: "1.0.0"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	for _, want := range []string{
		"version:",
		"agents:",
		"command: \"kilo\"",
		"repository:",
		"assets:",
		"binary:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("InstallConfig() missing %q:\n%s", want, got)
		}
	}
	// The install config must not carry the memory server URL or token: the
	// image build does not need them and they are secrets.
	if strings.Contains(got, "memory_server_url") || strings.Contains(got, "memory_auth_token") {
		t.Fatalf("InstallConfig() leaked memory credentials:\n%s", got)
	}
	// kilo has no memory integration, so the ai-memory tool must be absent.
	if strings.Contains(got, "ai-memory") {
		t.Fatalf("InstallConfig() for a non-memory agent must not include the memory tool:\n%s", got)
	}
}

func TestInstallConfigUnknownAgent(t *testing.T) {
	_, err := InstallConfig(config.DefaultGlobal(), []AgentInstall{{Command: "not-in-catalog", Kind: InstallRelease, Version: "1.0"}})
	if err == nil {
		t.Fatal("InstallConfig() with an unknown agent should error")
	}
}

func TestInstallConfigNoAgents(t *testing.T) {
	if _, err := InstallConfig(config.DefaultGlobal(), nil); err != ErrNoAgents {
		t.Fatalf("InstallConfig(nil) = %v; want ErrNoAgents", err)
	}
}

func TestInstallConfigOmitsMemoryToolWhenNotNeeded(t *testing.T) {
	global := config.DefaultGlobal()
	// codex supports memory; use a non-memory agent instead. kiro-cli has no
	// memory integration.
	var kiro config.Agent
	for _, agent := range global.Agents {
		if agent.Command == "kiro-cli" {
			kiro = agent
		}
	}
	if kiro.Command == "" {
		t.Skip("kiro-cli not in catalog")
	}
	got, err := InstallConfig(global, []AgentInstall{{Command: "kiro-cli", Kind: InstallHostBinary, HostPath: "/opt/kiro/bin"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if strings.Contains(got, "ai-memory") {
		t.Fatalf("InstallConfig() for a non-memory agent must not include the memory tool:\n%s", got)
	}
}

// A script-installed agent (source_url) must render its recipe into the
// install config so the in-image installer can fetch it.
func TestInstallConfigScriptAgent(t *testing.T) {
	global := config.DefaultGlobal()
	// Synthesize a script-installed, memory-enabled agent: the built-in agents
	// install via the native installer, not a source_url script.
	agent := config.Agent{
		Name:           "Claude",
		Command:        "claude",
		SourceURL:      "https://example.com/claude.sh",
		SupportsMemory: true,
		Memory:         &config.MemoryIntegration{Client: "claude-code", Agent: "claude-code", RunHarness: "claude"},
	}
	global.Agents = append(global.Agents, agent)
	got, err := InstallConfig(global, []AgentInstall{{Command: "claude", Kind: InstallScript, Script: "curl x | bash"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if !strings.Contains(got, "source_url:") {
		t.Fatalf("InstallConfig() missing source_url:\n%s", got)
	}
	// The agent supports memory, so the ai-memory tool must be present.
	if !strings.Contains(got, "command: \"ai-memory\"") {
		t.Fatalf("InstallConfig() missing the ai-memory tool for a memory agent:\n%s", got)
	}
	if !strings.Contains(got, "supports_memory: true") {
		t.Fatalf("InstallConfig() missing supports_memory:\n%s", got)
	}
}

// An agent with aliases renders them so the in-image installer registers the
// same command names the host catalog has.
func TestInstallConfigRendersAliases(t *testing.T) {
	global := config.DefaultGlobal()
	// Find an agent with aliases, or synthesize one.
	agent := config.Agent{Name: "X", Command: "x", Aliases: []string{"x-alias"}, SourceURL: "https://example.com/x.sh"}
	global.Agents = append(global.Agents, agent)
	got, err := InstallConfig(global, []AgentInstall{{Command: "x", Kind: InstallScript, Script: "curl x | bash"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if !strings.Contains(got, "aliases:") || !strings.Contains(got, "\"x-alias\"") {
		t.Fatalf("InstallConfig() missing aliases:\n%s", got)
	}
}

// The allow_unverified flag must survive into the install config so the
// in-image installer honors the operator's explicit choice.
func TestInstallConfigRendersAllowUnverified(t *testing.T) {
	global := config.DefaultGlobal()
	agent := config.Agent{Name: "U", Command: "u", SourceURL: "https://example.com/u.sh", AllowUnverified: true}
	global.Agents = append(global.Agents, agent)
	got, err := InstallConfig(global, []AgentInstall{{Command: "u", Kind: InstallScript, Script: "curl u | bash"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if !strings.Contains(got, "allow_unverified: true") {
		t.Fatalf("InstallConfig() missing allow_unverified:\n%s", got)
	}
}

// A memory-enabled agent with a run_harness remap keeps it in the install
// config so the in-image ai-memory wiring targets the right harness.
func TestInstallConfigRendersRunHarness(t *testing.T) {
	global := config.DefaultGlobal()
	var oc config.Agent
	for _, agent := range global.Agents {
		if agent.Command == "oc" {
			oc = agent
		}
	}
	if oc.Memory == nil || oc.Memory.RunHarness != "opencode" {
		t.Skip("oc run_harness not in catalog")
	}
	got, err := InstallConfig(global, []AgentInstall{{Command: "oc", Kind: InstallScript, Script: "curl oc | bash"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if !strings.Contains(got, "run_harness: \"opencode\"") {
		t.Fatalf("InstallConfig() missing run_harness:\n%s", got)
	}
}
