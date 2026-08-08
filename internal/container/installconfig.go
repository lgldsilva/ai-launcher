package container

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// InstallConfig renders the minimal global config the docker build COPYs into
// the image and runs `ai-launcher --install` against (design C1). It carries
// only the agents selected for the image plus selected auxiliary tools (and
// ai-memory when the selection needs it), so the in-image installer installs
// exactly what the image is supposed to host — nothing more, nothing less.
//
// The rendered YAML is intentionally minimal: the launcher's installer reads
// the release recipes from this config and nothing else. Permissions,
// mounts, profiles and the memory server URL are irrelevant inside the image
// build and are omitted (the server URL and token never enter the image).
func InstallConfig(global config.Global, agents []AgentInstall) (string, error) {
	return InstallConfigWithTools(global, agents, nil)
}

// InstallConfigWithTools is InstallConfig plus explicitly selected auxiliary
// CLIs, such as semidx. The compatibility wrapper above keeps callers that
// only build an agent image unchanged.
func InstallConfigWithTools(global config.Global, agents []AgentInstall, selectedTools []ToolInstall) (string, error) {
	if len(agents) == 0 {
		return "", ErrNoAgents
	}
	var b strings.Builder
	b.WriteString("version: \"2.1\"\n")
	seenMemory, err := renderSelectedAgents(&b, global, agents)
	if err != nil {
		return "", err
	}
	tools, err := selectedToolConfigs(global, selectedTools, seenMemory)
	if err != nil {
		return "", err
	}
	if len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range tools {
			renderInstallTool(&b, tool)
		}
	}
	return b.String(), nil
}

func renderSelectedAgents(b *strings.Builder, global config.Global, agents []AgentInstall) (bool, error) {
	byCommand := make(map[string]config.Agent, len(global.Agents))
	for _, agent := range global.Agents {
		byCommand[strings.TrimSpace(agent.Command)] = agent
	}
	b.WriteString("agents:\n")
	seenMemory := false
	for _, install := range agents {
		agent, ok := byCommand[install.Command]
		if !ok {
			return false, fmt.Errorf("agent %q is not in the catalog; cannot build an install config", install.Command)
		}
		renderInstallAgent(b, agent, install.NeedsMemory)
		if install.NeedsMemory {
			seenMemory = true
		}
	}
	return seenMemory, nil
}

func selectedToolConfigs(global config.Global, selectedTools []ToolInstall, includeMemory bool) ([]config.Tool, error) {
	// The ai-memory tool is required by any memory-enabled agent. Add it to the
	// selected auxiliary tools while preserving explicit tool order below.
	toolByCommand := make(map[string]ToolInstall, len(selectedTools)+1)
	for _, selected := range selectedTools {
		if err := selected.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := toolByCommand[selected.Command]; duplicate {
			return nil, fmt.Errorf("tool %q listed more than once", selected.Command)
		}
		toolByCommand[selected.Command] = selected
	}
	if includeMemory {
		if _, exists := toolByCommand[config.AIMemoryCommand]; !exists {
			for _, tool := range global.Tools {
				if tool.Command == config.AIMemoryCommand {
					toolByCommand[tool.Command] = ToolInstall{Command: tool.Command, Version: "0.0.0-recipe", Kind: InstallRelease, Release: tool.Release}
					break
				}
			}
		}
	}
	if len(toolByCommand) == 0 {
		return nil, nil
	}
	commands := make([]string, 0, len(toolByCommand))
	for command := range toolByCommand {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	result := make([]config.Tool, 0, len(commands))
	for _, command := range commands {
		tool, ok := findCatalogTool(global.Tools, command)
		if !ok {
			return nil, fmt.Errorf("tool %q is not in the catalog; cannot build an install config", command)
		}
		result = append(result, tool)
	}
	return result, nil
}

func findCatalogTool(tools []config.Tool, command string) (config.Tool, bool) {
	for _, tool := range tools {
		if tool.Command == command {
			return tool, true
		}
	}
	return config.Tool{}, false
}

// renderInstallAgent writes one catalog agent as an install-config entry.
func renderInstallAgent(b *strings.Builder, agent config.Agent, needsMemory bool) {
	fmt.Fprintf(b, "  - name: %q\n", agent.Name)
	fmt.Fprintf(b, "    command: %q\n", agent.Command)
	if len(agent.Aliases) > 0 {
		b.WriteString("    aliases: [")
		for i, alias := range agent.Aliases {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%q", alias)
		}
		b.WriteString("]\n")
	}
	if needsMemory {
		b.WriteString("    supports_memory: true\n")
		if agent.Memory != nil {
			b.WriteString("    memory:\n")
			fmt.Fprintf(b, "      client: %q\n", agent.Memory.Client)
			fmt.Fprintf(b, "      agent: %q\n", agent.Memory.Agent)
			if agent.Memory.RunHarness != "" {
				fmt.Fprintf(b, "      run_harness: %q\n", agent.Memory.RunHarness)
			}
			b.WriteString("      install_mcp: false\n")
			b.WriteString("      install_hooks: false\n")
		}
	}
	if agent.Release != nil {
		renderRelease(b, agent.Release.Repository, agent.Release.Assets, agent.Release.Binary, agent.Release.ChecksumAsset, agent.Release.AllowUnverified)
	}
	if agent.SourceURL != "" {
		fmt.Fprintf(b, "    source_url: %q\n", agent.SourceURL)
	}
	if agent.AllowUnverified {
		b.WriteString("    allow_unverified: true\n")
	}
}

// renderInstallTool writes one catalog tool (ai-memory) as an install-config
// entry. Memory tool recipes are always checksum-verified releases.
func renderInstallTool(b *strings.Builder, tool config.Tool) {
	fmt.Fprintf(b, "  - name: %q\n", tool.Name)
	fmt.Fprintf(b, "    command: %q\n", tool.Command)
	fmt.Fprintf(b, "    description: %q\n", tool.Description)
	if tool.Release != nil {
		renderRelease(b, tool.Release.Repository, tool.Release.Assets, tool.Release.Binary, tool.Release.ChecksumAsset, tool.Release.AllowUnverified)
	}
}

// renderRelease writes a shared GitHub release recipe block. Asset keys are
// iterated in sorted order so the rendered YAML is deterministic run to run.
func renderRelease(b *strings.Builder, repository string, assets map[string]string, binary, checksumAsset string, allowUnverified bool) {
	b.WriteString("    release:\n")
	fmt.Fprintf(b, "      repository: %q\n", repository)
	b.WriteString("      assets:\n")
	for _, key := range SortedAssetKeys(assets) {
		fmt.Fprintf(b, "        %q: %q\n", key, assets[key])
	}
	fmt.Fprintf(b, "      binary: %q\n", binary)
	if checksumAsset != "" {
		fmt.Fprintf(b, "      checksum_asset: %q\n", checksumAsset)
	}
	if allowUnverified {
		b.WriteString("      allow_unverified: true\n")
	}
}
