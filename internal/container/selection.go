package container

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// InstallKind tells the Dockerfile generator how an agent is installed inside
// the image. Release means the managed installer (internal/installer) resolves
// a pinned GitHub release with checksum verification — implemented by running
// the launcher binary inside a scratch container before commit (design C1).
// Script means a curl|bash source_url recipe emitted as a RUN layer (only with
// explicit allow_unverified, invariant 4). HostBinary means the agent has no
// recipe and is bind-mounted from the host instead of installed (item 13).
type InstallKind string

const (
	// InstallRelease is the checksum-verified path, always preferred.
	InstallRelease InstallKind = "release"
	// InstallScript is the curl|bash source_url path, gated by allow_unverified.
	InstallScript InstallKind = "script"
	// InstallNpm is a global npm package install (`npm install -g <pkg>`),
	// used by agents distributed on npm (gemini, qwen, crush, openclaw...).
	// Requires Node/npm in the image, which the DevProfile provides via nvm.
	InstallNpm InstallKind = "npm"
	// InstallHostBinary marks agents that are bind-mounted, never built in.
	InstallHostBinary InstallKind = "host"
)

// AgentInstall is one agent in the image selection. Version is REQUIRED for
// release installs (design C2): the selection hash includes it, so an image
// tagged from a pinned version is deterministic — "latest" would make the tag
// lie as upstream moves.
type AgentInstall struct {
	Command string
	Version string
	Kind    InstallKind
	// NeedsNode marks script installers that require Node/npm at build time.
	// npm installs imply this automatically; the explicit bit covers vendors
	// such as Pi whose official shell installer delegates to npm.
	NeedsNode bool
	// NeedsMemory means the image must contain the ai-memory wrapper and its
	// managed native runner. It is part of the selection because the Dockerfile
	// changes when memory is enabled; leaving it out would make the image tag
	// lie and could produce a container with a harness but no memory layer.
	NeedsMemory bool
	// Script is the full RUN line for InstallScript kinds (e.g.
	// "RUN curl -fsSL https://example.com/install.sh | bash").
	Script string
	// NpmPackage is the npm package name for InstallNpm installs.
	NpmPackage string
	// AllowSetupFailure marks installers whose post-install step opens an
	// interactive login (devin's `setup`). The binary is installed before that
	// step; ScriptLine accepts that non-interactive status only when the
	// executable is present after the installer finishes.
	AllowSetupFailure bool
	// HostPath is the resolved host binary directory for InstallHostBinary.
	HostPath string
}

// ToolInstall describes an auxiliary CLI installed in the image. Tools are
// not harnesses: they are placed on PATH for MCP servers and development
// workflows (for example semidx), while the selected agent remains the
// container command.
type ToolInstall struct {
	Command string
	Version string
	Kind    InstallKind
	Release *config.GitHubRelease
}

// Validate checks the structural invariants of an auxiliary tool install.
func (t ToolInstall) Validate() error {
	if strings.TrimSpace(t.Command) == "" {
		return fmt.Errorf("tool command is required")
	}
	if t.Kind != InstallRelease {
		return fmt.Errorf("tool %q: only release installs are supported", t.Command)
	}
	if strings.TrimSpace(t.Version) == "" || strings.EqualFold(t.Version, "latest") {
		return fmt.Errorf("tool %q: release installs require a pinned version", t.Command)
	}
	if t.Release == nil {
		return fmt.Errorf("tool %q: release recipe is required", t.Command)
	}
	return nil
}

// Validate checks the structural invariants of one agent install: non-empty
// command, a version for release installs, a script for script installs, a
// host path for host installs. It returns an error describing the first
// violation.
func (a AgentInstall) Validate() error {
	if strings.TrimSpace(a.Command) == "" {
		return fmt.Errorf("agent command is required")
	}
	switch a.Kind {
	case InstallRelease:
		if strings.TrimSpace(a.Version) == "" {
			return fmt.Errorf("agent %q: release installs require a pinned version (never latest)", a.Command)
		}
		if strings.EqualFold(a.Version, "latest") {
			return fmt.Errorf("agent %q: version %q is not deterministic; pin a release tag (design C2)", a.Command, a.Version)
		}
	case InstallScript:
		if strings.TrimSpace(a.Script) == "" {
			return fmt.Errorf("agent %q: script installs require a RUN script", a.Command)
		}
	case InstallNpm:
		if strings.TrimSpace(a.NpmPackage) == "" {
			return fmt.Errorf("agent %q: npm installs require an npm package name", a.Command)
		}
	case InstallHostBinary:
		if strings.TrimSpace(a.HostPath) == "" {
			return fmt.Errorf("agent %q: host-binary installs require HostPath", a.Command)
		}
	case "":
		return fmt.Errorf("agent %q: install kind is required", a.Command)
	default:
		return fmt.Errorf("agent %q: unknown install kind %q", a.Command, a.Kind)
	}
	return nil
}

// Selection is the canonical, order-independent description of an agent box
// image. Stacks are sorted stack IDs; Agents are deduplicated by command and
// sorted by command so the Dockerfile and the image tag are deterministic
// regardless of the order the user ticked the checkboxes.
type Selection struct {
	// Stacks is a validated, sorted list of stack IDs (see ValidStackIDs).
	Stacks []string
	// Agents is the validated, sorted-by-command list of agents to install.
	Agents []AgentInstall
	// Tools are auxiliary CLIs installed on PATH for the selected image.
	Tools []ToolInstall
	// IncludeDevProfile controls the shared tool layer (git, ssh, jq, ...).
	// Defaults to true; tests and the "empty box" path can disable it.
	IncludeDevProfile bool
}

// Normalize returns a canonical Selection: stacks validated+sorted, agents
// validated+deduplicated+sorted, IncludeDevProfile defaulting to true. The
// result is the input to both the Dockerfile generator and the image tag
// hasher, which is what makes the tag honest about the contents.
func Normalize(stacks []string, agents []AgentInstall, includeDevProfile *bool) (Selection, error) {
	normalized, err := ValidStackIDs(stacks)
	if err != nil {
		return Selection{}, err
	}
	profile := true
	if includeDevProfile != nil {
		profile = *includeDevProfile
	}

	byCommand := make(map[string]AgentInstall, len(agents))
	order := make([]string, 0, len(agents))
	for _, agent := range agents {
		agent.Command = strings.TrimSpace(agent.Command)
		if err := agent.Validate(); err != nil {
			return Selection{}, err
		}
		if _, dup := byCommand[agent.Command]; dup {
			return Selection{}, fmt.Errorf("agent %q listed more than once", agent.Command)
		}
		byCommand[agent.Command] = agent
		order = append(order, agent.Command)
	}
	sort.Strings(order)
	canonicalAgents := make([]AgentInstall, 0, len(order))
	for _, command := range order {
		canonicalAgents = append(canonicalAgents, byCommand[command])
	}

	return Selection{Stacks: normalized, Agents: canonicalAgents, IncludeDevProfile: profile}, nil
}

// Validate re-checks a Selection without normalizing; it is used by tests and
// by callers that already hold a canonical selection.
func (s Selection) Validate() error {
	if _, err := ValidStackIDs(s.Stacks); err != nil {
		return err
	}
	for _, agent := range s.Agents {
		if err := agent.Validate(); err != nil {
			return err
		}
	}
	seenTools := make(map[string]struct{}, len(s.Tools))
	for _, tool := range s.Tools {
		if err := tool.Validate(); err != nil {
			return err
		}
		if _, duplicate := seenTools[tool.Command]; duplicate {
			return fmt.Errorf("tool %q listed more than once", tool.Command)
		}
		seenTools[tool.Command] = struct{}{}
	}
	return nil
}

// AgentExecutable returns the argv[0] the container should invoke for this
// selection: the single installed agent's command name, or the host path for
// a host-mounted agent. An empty result means the selection has no agents.
func (s Selection) AgentExecutable() string {
	if len(s.Agents) == 0 {
		return ""
	}
	agent := s.Agents[0]
	if agent.Kind == InstallHostBinary {
		return agent.HostPath
	}
	return agent.Command
}

// SelectionNeedsMemory reports whether the generated image must install
// ai-memory for one of its selected harnesses.
func SelectionNeedsMemory(s Selection) bool {
	for _, agent := range s.Agents {
		if agent.NeedsMemory {
			return true
		}
	}
	return false
}

// SelectionNeedsTools reports whether the image contains auxiliary tools.
func SelectionNeedsTools(s Selection) bool { return len(s.Tools) > 0 }
