package container

import (
	"fmt"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// InstallConfig renders the minimal global config the docker build COPYs into
// the image and runs `ai-launcher --install` against (design C1). It carries
// only the agents selected for the image plus the ai-memory tool when the
// selection needs it, so the in-image installer installs exactly what the
// image is supposed to host — nothing more, nothing less.
//
// The rendered YAML is intentionally minimal: the launcher's installer reads
// the release recipes from this config and nothing else. Permissions,
// mounts, profiles and the memory server URL are irrelevant inside the image
// build and are omitted (the server URL and token never enter the image).
func InstallConfig(global config.Global, agents []AgentInstall) (string, error) {
	if len(agents) == 0 {
		return "", ErrNoAgents
	}
	byCommand := make(map[string]config.Agent, len(global.Agents))
	for _, agent := range global.Agents {
		byCommand[strings.TrimSpace(agent.Command)] = agent
	}

	var b strings.Builder
	b.WriteString("version: \"2.1\"\n")
	b.WriteString("agents:\n")
	seenMemory := false
	for _, install := range agents {
		agent, ok := byCommand[install.Command]
		if !ok {
			return "", fmt.Errorf("agent %q is not in the catalog; cannot build an install config", install.Command)
		}
		renderInstallAgent(&b, agent)
		if agent.SupportsMemory {
			seenMemory = true
		}
	}
	// The ai-memory tool is required by any memory-enabled agent: the wrapper
	// and its managed runner must exist in the image too. Only emit the tools
	// block when the catalog actually provides the recipe — an empty block
	// would render as an empty YAML mapping.
	if seenMemory {
		var rendered bool
		for _, tool := range global.Tools {
			if tool.Command == config.AIMemoryCommand {
				if !rendered {
					b.WriteString("tools:\n")
					rendered = true
				}
				renderInstallTool(&b, tool)
			}
		}
	}
	return b.String(), nil
}

// renderInstallAgent writes one catalog agent as an install-config entry.
func renderInstallAgent(b *strings.Builder, agent config.Agent) {
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
	if agent.SupportsMemory {
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
