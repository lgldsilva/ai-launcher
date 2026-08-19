package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"golang.org/x/term"
)

// interactiveLaunch reports whether stdin and stdout are both terminals, the
// condition under which the docker backend allocates a pseudo-TTY (-it). A
// non-interactive invocation (scripts, CI, piped output) stays with -i so the
// container does not try to draw a terminal that is not there.
func interactiveLaunch(opts cliOptions) bool {
	if opts.dryRun {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// dockerRunConfigFromOptions derives the docker run inputs from the saved
// selection. It resolves the REAL installed version of the agent (design C2)
// from the host install-state so the content-hashed image tag reflects the
// version the image will actually install — a catalog/agent update then
// produces a new tag and a fresh build instead of a lying cache hit. The
// memory integration (server URL + native binary mount), the interactive
// flag, and the host UID/GID mapping (R9.5) are populated here too.
type dockerRunOptions struct {
	agent             config.Agent
	stacks            []string
	home              string
	memoryServerURL   string
	memoryAuthToken   string
	runtimePreference string
	interactive       bool
	memoryEnabled     bool
}

func dockerRunConfigFromOptions(options dockerRunOptions) (container.RunConfig, error) {
	selection, err := dockerSelectionFromAgent(options.agent, options.stacks, options.home, options.memoryEnabled)
	if err != nil {
		// An invalid stack selection surfaces as docker-selection-invalid in
		// preflight; here we keep the raw stacks so the error is attributable.
		return container.RunConfig{
			Selection: container.Selection{Stacks: options.stacks},
			HomeDir:   options.home,
		}, nil
	}
	runConfig := container.RunConfig{
		Selection:      selection,
		HomeDir:        options.home,
		AddHostGateway: effectiveContainerHostGateway(nil),
		Interactive:    options.interactive,
		UID:            os.Getuid(),
		GID:            os.Getgid(),
		Env:            memoryEnvEntries(options.agent, options.memoryServerURL, options.memoryAuthToken),
	}
	runtime, err := container.DetectRuntime(config.EffectiveContainerRuntime(options.runtimePreference))
	if err != nil {
		return runConfig, err
	}
	runConfig.Runtime = runtime
	return runConfig, nil
}

func dockerSelectionFromAgent(agent config.Agent, stacks []string, home string, memoryEnabled bool) (container.Selection, error) {
	if agent.CatalogCommand != "" {
		agent.Command = agent.CatalogCommand
	}
	version := container.AgentVersion(home, agent.Command, "")
	if version == "" {
		// No verified install on this host: keep a stable placeholder so the
		// tag still reflects the recipe. Installing is not a launch decision;
		// the tag cache simply falls back to the recipe-only hash.
		version = "0.0.0-recipe"
	}
	install := container.PlanInstall(agent, version, agent.Path)
	install.NeedsMemory = memoryEnabled && agent.SupportsMemory
	return container.Normalize(stacks, []container.AgentInstall{install}, nil)
}

// addContainerTools adds the trusted auxiliary CLI set to a Docker image.
// semidx is deliberately a tool, not a harness: it is installed on PATH and
// its MCP command is consumed by the selected agent's own configuration.
// Explicit tool catalogs can omit it to opt out of the extra image layer.
func addContainerTools(selection container.Selection, global config.Global, home string) (container.Selection, error) {
	for _, tool := range global.Tools {
		if tool.Command != config.SemidxCommand {
			continue
		}
		if tool.Release == nil {
			return container.Selection{}, fmt.Errorf("container tool %q has no release recipe", tool.Command)
		}
		version := container.AgentVersion(home, tool.Command, "")
		if version == "" {
			version = "0.0.0-recipe"
		}
		selection.Tools = []container.ToolInstall{container.PlanTool(tool, version)}
		return selection, nil
	}
	return selection, nil
}

// generatedGitignore mirrors the private materializedGitignore in
// internal/container/build.go: the CLI needs the content before anything is
// written so the overwrite protection can review it. Keep the two in sync.
const generatedGitignore = "# build context temp\n.context/\n# image cache\n.image-cache/\n# cross-compiled launcher copied into the local build context\nai-launcher\n# operator decision metadata for generated Compose updates\n.compose-approval.json\n# persistent Compose service data\ndata/\n"

// launcherBinaryFileName mirrors the private launcherBinaryName in
// internal/container/constants.go, for the existence check that decides
// whether the cross-compiled launcher must be rebuilt.
const launcherBinaryFileName = "ai-launcher"

func generateContainerArtifacts(local config.Local, agent config.Agent, global config.Global, home, projectDir string, in io.Reader, out io.Writer, choice composeUpdateChoice, interactive bool) error {
	if !local.Options.Docker {
		return errors.New("generate requires container mode; pass --docker-backend or set options.docker: true")
	}
	selection, err := dockerSelectionFromAgent(agent, local.Options.Stacks, home, local.Options.Memory)
	if err != nil {
		return fmt.Errorf("generate container selection: %w", err)
	}
	selection, err = addContainerTools(selection, global, home)
	if err != nil {
		return fmt.Errorf("generate container tools: %w", err)
	}
	launchConfig := launcher.LaunchConfig{
		Agent:                          agent,
		Executable:                     agent.Path,
		HomeDir:                        home,
		UseDocker:                      true,
		Services:                       append([]string(nil), local.Options.Services...),
		ContainerNetworkAllowedDomains: append([]string(nil), local.Options.ContainerNetworkAllowedDomains...),
		ContainerEnvironment:           cloneStringMap(local.Options.ContainerEnvironment),
		ContainerServicePorts:          cloneServicePortMappings(local.Options.ContainerServicePorts),
		ContainerDependencies:          config.MergeDependencySettings(global.ContainerDependencies, local.Options.ContainerDependencies),
		ContainerTmux:                  local.Options.ContainerTmux,
		UseMemory:                      local.Options.Memory,
		Fresh:                          local.Options.Fresh,
		NewWorkstream:                  local.Options.NewWorkstream,
		Workstream:                     local.Options.Workstream,
		ProjectDir:                     projectDir,
		Workspace:                      local.Options.Workspace,
		Project:                        local.Options.Project,
		Yolo:                           local.Options.Yolo,
		ExtraArgs:                      append([]string(nil), local.Options.ExtraArgs...),
		ParamValues:                    cloneStringMap(local.Options.ParamValues),
		Docker: container.RunConfig{
			Selection:       selection,
			Context:         strings.TrimSpace(local.Options.ContainerContext),
			HomeDir:         home,
			ProjectDir:      mustGetwd(),
			UID:             os.Getuid(),
			GID:             os.Getgid(),
			AgentCommands:   []string{agent.Command},
			Env:             composeMemoryEnv(local.Options.Memory, agent, global),
			AddHostGateway:  effectiveContainerHostGateway(local.Options.ContainerHostGateway),
			MemoryLimit:     local.Options.ContainerMemory,
			CPULimit:        local.Options.ContainerCPUs,
			PIDsLimit:       local.Options.ContainerPIDs,
			ExposedPorts:    append([]container.PortMapping(nil), local.Options.ContainerPorts...),
			NetworkName:     local.Options.ContainerNetwork,
			NetworkInternal: config.EffectiveContainerNetworkInternal(local.Options.ContainerNetworkInternal),
		},
		ContainerNetworkInternal: local.Options.ContainerNetworkInternal,
		ContainerHostGateway:     local.Options.ContainerHostGateway,
		Permissions:              copyPermissions(local.Permissions),
	}
	launchConfig.Docker.AddHostGateway = effectiveContainerHostGateway(local.Options.ContainerHostGateway)
	launchConfig = ensureDockerProjectDir(launchConfig)
	launchConfig = finalizeLaunchConfig(launchConfig, global, home, out)
	if choice == composeUpdatePrompt {
		var err error
		choice, err = resolveComposeUpdate(launchConfig, choice, in, out, interactive)
		if err != nil {
			return err
		}
	}
	if choice == composeUpdatePrompt {
		var err error
		choice, err = resolveContainerArtifactsUpdate(launchConfig, global, choice, in, out, interactive)
		if err != nil {
			return err
		}
	}
	return materializeContainerArtifactsWithChoice(launchConfig, global, out, io.Discard, choice)
}

// dockerfileOptions maps the launch permissions to Dockerfile build options:
// the docker CLI layer is only emitted when the launch grants the docker
// permission (which bind-mounts the host daemon socket at run time).
func dockerfileOptions(launchConfig launcher.LaunchConfig) container.DockerfileOptions {
	return container.DockerfileOptions{DockerCLI: launchConfig.Permissions[config.PermissionDocker]}
}

// inspectPlainContainerArtifacts renders the Dockerfile, the install config
// and the .gitignore for the selection and reviews each against the
// project-local copy, so the same approval ledger that protects the Compose
// file also protects these artifacts before anything is written.
func inspectPlainContainerArtifacts(projectDir string, selection container.Selection, options container.DockerfileOptions, global config.Global) ([]artifactReview, error) {
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	dockerfile, err := container.DockerfileWithOptions(selection, options)
	if err != nil {
		return nil, err
	}
	installConfig, err := container.InstallConfigWithTools(global, selection.Agents, selection.Tools)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(projectDir, containerArtifactDir)
	generated := []struct {
		name    string
		content string
	}{
		{"Dockerfile", dockerfile},
		{"install-config.yaml", installConfig},
		{".gitignore", generatedGitignore},
	}
	reviews := make([]artifactReview, 0, len(generated))
	for _, artifact := range generated {
		review, err := inspectGeneratedArtifact(filepath.Join(dir, artifact.name), artifact.name, artifact.content)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, review)
	}
	return reviews, nil
}

// resolveContainerArtifactsUpdate mirrors resolveComposeUpdate for the plain
// generated artifacts: with the prompt policy a local edit either fails with
// the diff (non-interactive) or is confirmed by the operator.
func resolveContainerArtifactsUpdate(launchConfig launcher.LaunchConfig, global config.Global, requested composeUpdateChoice, in io.Reader, out io.Writer, interactive bool) (composeUpdateChoice, error) {
	if requested != composeUpdatePrompt {
		return requested, nil
	}
	reviews, err := inspectPlainContainerArtifacts(mustGetwd(), launchConfig.Docker.Selection, dockerfileOptions(launchConfig), global)
	if err != nil {
		return requested, err
	}
	for _, review := range reviews {
		if !review.Changed {
			continue
		}
		if !interactive {
			return requested, &artifactConflictError{review: review}
		}
		return promptComposeUpdate(review, in, out)
	}
	return requested, nil
}

func materializeContainerArtifactsWithChoice(launchConfig launcher.LaunchConfig, global config.Global, out, errOut io.Writer, choice composeUpdateChoice) error {
	selection := launchConfig.Docker.Selection
	reviews, err := inspectPlainContainerArtifacts(mustGetwd(), selection, dockerfileOptions(launchConfig), global)
	if err != nil {
		return fmt.Errorf("generate container artifacts: %w", err)
	}
	for _, review := range reviews {
		if err := materializePlainArtifactIfNeeded(review, choice, out); err != nil {
			return fmt.Errorf("generate container artifacts: %w", err)
		}
	}
	if selectionNeedsLauncherBinary(selection) {
		source, err := buildLauncherLinux(out, errOut)
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(source) }()
		path, err := container.MaterializeLauncherBinary(mustGetwd(), source)
		if err != nil {
			return fmt.Errorf("materialize launcher binary: %w", err)
		}
		_, _ = fmt.Fprintf(out, "generated %s\n", filepath.Join(containerArtifactDir, filepath.Base(path)))
	}
	if len(launchConfig.Services) > 0 {
		if err := materializeComposeIfNeeded(launchConfig, choice, out); err != nil {
			return fmt.Errorf("generate compose: %w", err)
		}
	}
	return nil
}

func selectionNeedsLauncherBinary(selection container.Selection) bool {
	if container.SelectionNeedsMemory(selection) || container.SelectionNeedsTools(selection) {
		return true
	}
	for _, agent := range selection.Agents {
		if agent.Kind == container.InstallRelease {
			return true
		}
	}
	return false
}

// effectiveContainerHostGateway keeps the historical default while allowing a
// trusted global/profile or explicitly trusted local YAML to turn off host
// network reachability. Disabling it also disables loopback URL rewriting and
// MCP overlays, so a local MCP server fails visibly instead of being pointed
// at an unexpected address. Delegates to config.EffectiveContainerHostGateway,
// which internal/tui also uses (it cannot import this package).
func effectiveContainerHostGateway(value *bool) bool {
	return config.EffectiveContainerHostGateway(value)
}

func ensureComposeArtifacts(launchConfig launcher.LaunchConfig, global config.Global, out, errOut io.Writer, choices ...composeUpdateChoice) error {
	choice := composeUpdatePrompt
	if len(choices) > 0 {
		choice = choices[0]
	}
	project := mustGetwd()
	// Compose is derived from the confirmed launch selection. Refresh all
	// generated inputs on every `compose up`; otherwise an older YAML can keep
	// forwarding a native flag (for example --yolo) that the selected harness
	// no longer accepts, even though the launcher itself has been updated.
	// Locally edited artifacts are never silently replaced: the choice decides.
	reviews, err := inspectPlainContainerArtifacts(project, launchConfig.Docker.Selection, dockerfileOptions(launchConfig), global)
	if err != nil {
		return fmt.Errorf("ensure container artifacts: %w", err)
	}
	for _, review := range reviews {
		if err := materializePlainArtifactIfNeeded(review, choice, out); err != nil {
			return fmt.Errorf("ensure container artifacts: %w", err)
		}
	}
	if selectionNeedsLauncherBinary(launchConfig.Docker.Selection) {
		exists, err := regularArtifactExists(filepath.Join(project, containerArtifactDir, launcherBinaryFileName))
		if err != nil {
			return err
		}
		if !exists {
			source, err := buildLauncherLinux(out, errOut)
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(source) }()
			path, err := container.MaterializeLauncherBinary(project, source)
			if err != nil {
				return fmt.Errorf("materialize launcher binary: %w", err)
			}
			_, _ = fmt.Fprintf(out, "generated %s\n", filepath.Join(containerArtifactDir, filepath.Base(path)))
		}
	}
	if len(launchConfig.Services) > 0 {
		if err := materializeComposeIfNeeded(launchConfig, choice, out); err != nil {
			return fmt.Errorf("ensure compose: %w", err)
		}
	}
	return nil
}

func regularArtifactExists(path string) (bool, error) {
	parent := filepath.Dir(path)
	parentInfo, parentErr := os.Lstat(parent)
	if parentErr == nil {
		if parentInfo.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("container artifact directory %s is a symlink", parent)
		}
		if !parentInfo.IsDir() {
			return false, fmt.Errorf("container artifact parent %s is not a directory", parent)
		}
	} else if !errors.Is(parentErr, os.ErrNotExist) {
		return false, fmt.Errorf("inspect container artifact directory %s: %w", parent, parentErr)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect container artifact %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("container artifact %s is a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("container artifact %s is not a regular file", path)
	}
	return true, nil
}
