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
	got, err := InstallConfig(global, []AgentInstall{{Command: "claude", Kind: InstallScript, Script: "curl x | bash", NeedsMemory: true}})
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
	got, err := InstallConfig(global, []AgentInstall{{Command: "oc", Kind: InstallScript, Script: "curl oc | bash", NeedsMemory: true}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if !strings.Contains(got, "run_harness: \"opencode\"") {
		t.Fatalf("InstallConfig() missing run_harness:\n%s", got)
	}
}

func TestInstallConfigRendersSelectedSemidxTool(t *testing.T) {
	global := config.DefaultGlobal()
	var semidx config.Tool
	for _, tool := range global.Tools {
		if tool.Command == config.SemidxCommand {
			semidx = tool
		}
	}
	if semidx.Release == nil {
		t.Fatal("pre-condition: semidx must have a release recipe")
	}
	got, err := InstallConfigWithTools(global,
		[]AgentInstall{{Command: "kilo", Kind: InstallRelease, Version: "1.0.0"}},
		[]ToolInstall{{Command: config.SemidxCommand, Version: "0.44.9", Kind: InstallRelease, Release: semidx.Release}},
	)
	if err != nil {
		t.Fatalf("InstallConfigWithTools() error = %v", err)
	}
	if !strings.Contains(got, "command: \"semidx\"") || !strings.Contains(got, "repository: \"lgldsilva/semidx\"") {
		t.Fatalf("install config missing semidx recipe:\n%s", got)
	}
}

// With no selected tools and no memory-enabled agent the install config must
// not render a tools section at all.
func TestInstallConfigOmitsToolsSectionWhenEmpty(t *testing.T) {
	global := config.DefaultGlobal()
	got, err := InstallConfig(global, []AgentInstall{{Command: "kilo", Kind: InstallRelease, Version: "1.0.0"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if strings.Contains(got, "tools:") {
		t.Fatalf("InstallConfig() rendered an empty tools section:\n%s", got)
	}
}

// A selected tool that is not in the catalog cannot be rendered; the error
// must name the tool instead of silently dropping it from the image.
func TestInstallConfigWithToolsRejectsUnknownTool(t *testing.T) {
	global := config.DefaultGlobal()
	release := &config.GitHubRelease{Repository: "acme/nope", Binary: "nope"}
	_, err := InstallConfigWithTools(global,
		[]AgentInstall{{Command: "kilo", Kind: InstallRelease, Version: "1.0.0"}},
		[]ToolInstall{{Command: "not-in-catalog", Version: "1.0.0", Kind: InstallRelease, Release: release}},
	)
	if err == nil || !strings.Contains(err.Error(), "not in the catalog") {
		t.Fatalf("InstallConfigWithTools() error = %v; want unknown-tool error", err)
	}
}

func TestInstallConfigWithToolsRejectsDuplicateTool(t *testing.T) {
	global := config.DefaultGlobal()
	var semidx config.Tool
	for _, tool := range global.Tools {
		if tool.Command == config.SemidxCommand {
			semidx = tool
		}
	}
	if semidx.Release == nil {
		t.Fatal("pre-condition: semidx must have a release recipe")
	}
	tool := ToolInstall{Command: config.SemidxCommand, Version: "0.44.9", Kind: InstallRelease, Release: semidx.Release}
	_, err := InstallConfigWithTools(global,
		[]AgentInstall{{Command: "kilo", Kind: InstallRelease, Version: "1.0.0"}},
		[]ToolInstall{tool, tool},
	)
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("InstallConfigWithTools() error = %v; want duplicate-tool error", err)
	}
}

func TestInstallConfigWithToolsRejectsUnpinnedTool(t *testing.T) {
	global := config.DefaultGlobal()
	_, err := InstallConfigWithTools(global,
		[]AgentInstall{{Command: "kilo", Kind: InstallRelease, Version: "1.0.0"}},
		[]ToolInstall{{Command: "semidx", Version: "latest", Kind: InstallRelease, Release: &config.GitHubRelease{Repository: "lgldsilva/semidx"}}},
	)
	if err == nil {
		t.Fatal("InstallConfigWithTools() with an unpinned tool should error")
	}
}

// Multiple aliases render as one YAML flow list with a separator per alias.
func TestInstallConfigRendersMultipleAliases(t *testing.T) {
	global := config.DefaultGlobal()
	agent := config.Agent{Name: "Y", Command: "y", Aliases: []string{"y-one", "y-two"}, SourceURL: "https://example.com/y.sh"}
	global.Agents = append(global.Agents, agent)
	got, err := InstallConfig(global, []AgentInstall{{Command: "y", Kind: InstallScript, Script: "curl y | bash"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if !strings.Contains(got, `aliases: ["y-one", "y-two"]`) {
		t.Fatalf("InstallConfig() aliases malformed:\n%s", got)
	}
}

// A release recipe with a checksum asset renders it; one without must not
// emit the key at all (the in-image installer treats absence as unsigned).
func TestInstallConfigRendersChecksumAsset(t *testing.T) {
	global := config.DefaultGlobal()
	withSum := config.Agent{
		Name:    "S",
		Command: "s",
		Release: &config.GitHubRelease{
			Repository:    "acme/s",
			Assets:        map[string]string{"linux-amd64": "s_linux_amd64.tar.gz"},
			Binary:        "s",
			ChecksumAsset: "checksums.txt",
		},
	}
	withoutSum := config.Agent{
		Name:    "N",
		Command: "n",
		Release: &config.GitHubRelease{
			Repository: "acme/n",
			Assets:     map[string]string{"linux-amd64": "n_linux_amd64.tar.gz"},
			Binary:     "n",
		},
	}
	global.Agents = append(global.Agents, withSum, withoutSum)
	got, err := InstallConfig(global, []AgentInstall{
		{Command: "s", Kind: InstallRelease, Version: "1.0.0"},
		{Command: "n", Kind: InstallRelease, Version: "2.0.0"},
	})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if !strings.Contains(got, `checksum_asset: "checksums.txt"`) {
		t.Fatalf("InstallConfig() missing checksum_asset:\n%s", got)
	}
	// The checksum-less agent's block must not gain the key.
	nBlock := got[strings.Index(got, `command: "n"`):]
	if strings.Contains(nBlock, "checksum_asset") {
		t.Fatalf("checksum-less agent rendered a checksum_asset:\n%s", nBlock)
	}
}

// A release recipe the operator explicitly marked unverified keeps that flag
// inside the rendered release block for the in-image installer.
func TestInstallConfigRendersReleaseAllowUnverified(t *testing.T) {
	global := config.DefaultGlobal()
	agent := config.Agent{
		Name:    "V",
		Command: "v",
		Release: &config.GitHubRelease{
			Repository:      "acme/v",
			Assets:          map[string]string{"linux-amd64": "v_linux_amd64.tar.gz"},
			Binary:          "v",
			AllowUnverified: true,
		},
	}
	global.Agents = append(global.Agents, agent)
	got, err := InstallConfig(global, []AgentInstall{{Command: "v", Kind: InstallRelease, Version: "1.0.0"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if !strings.Contains(got, "      allow_unverified: true") {
		t.Fatalf("InstallConfig() missing release-level allow_unverified:\n%s", got)
	}
}

// An agent without aliases must not render an empty aliases key: the
// in-image installer would see a malformed entry.
func TestInstallConfigOmitsAliasesWhenAbsent(t *testing.T) {
	global := config.DefaultGlobal()
	agent := config.Agent{Name: "Noalias", Command: "noalias", SourceURL: "https://example.com/noalias.sh"}
	global.Agents = append(global.Agents, agent)
	got, err := InstallConfig(global, []AgentInstall{{Command: "noalias", Kind: InstallScript, Script: "curl x | bash"}})
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	if strings.Contains(got, "aliases:") {
		t.Fatalf("InstallConfig() rendered aliases for an agent without any:\n%s", got)
	}
}
