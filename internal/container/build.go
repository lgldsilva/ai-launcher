package container

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

const materializedGitignore = "# build context temp\n.context/\n# image cache\n.image-cache/\n# cross-compiled launcher copied into the local build context\nai-launcher\n# operator decision metadata for generated Compose updates\n.compose-approval.json\n# persistent Compose service data\ndata/\n"

// BuildContext is a prepared container build context directory: the generated
// Dockerfile, the minimal install config, and the launcher binary the image
// uses to install agents (design C1). The CLI materializes it and runs
// selected runtime's build command, streaming the runtime output.
type BuildContext struct {
	// Dir is the context directory (os.MkdirTemp).
	Dir string
	// DockerfilePath is Dir/Dockerfile.
	DockerfilePath string
	// InstallConfigPath is Dir/install-config.yaml.
	InstallConfigPath string
	// LauncherBinaryPath is Dir/ai-launcher (the linux build of this binary).
	LauncherBinaryPath string
	// ImageTag is the content-hashed image reference the build tags.
	ImageTag string
}

// MaterializedArtifacts describes the inspectable project-local container
// artifacts generated from a selection.
type MaterializedArtifacts struct {
	Dir                string
	DockerfilePath     string
	InstallConfigPath  string
	GitignorePath      string
	ComposeFilePath    string
	LauncherBinaryPath string
}

// MaterializeArtifacts writes the project-local Dockerfile, install config and
// temporary-artifact ignore rules. It deliberately does not probe a runtime,
// build an image or launch an agent. The Dockerfile is derived from the
// selection and may be edited by the operator until the next generate call.
func MaterializeArtifacts(projectDir string, selection Selection, global config.Global) (MaterializedArtifacts, error) {
	return MaterializeArtifactsWithOptions(projectDir, selection, DockerfileOptions{}, global)
}

// MaterializeArtifactsWithOptions is MaterializeArtifacts with explicit
// Dockerfile options (for example the docker CLI layer for launches granted
// the docker permission).
func MaterializeArtifactsWithOptions(projectDir string, selection Selection, options DockerfileOptions, global config.Global) (MaterializedArtifacts, error) {
	if err := selection.Validate(); err != nil {
		return MaterializedArtifacts{}, err
	}
	dockerfile, err := DockerfileWithOptions(selection, options)
	if err != nil {
		return MaterializedArtifacts{}, err
	}
	installConfig, err := InstallConfigWithTools(global, selection.Agents, selection.Tools)
	if err != nil {
		return MaterializedArtifacts{}, err
	}
	if strings.TrimSpace(projectDir) == "" {
		projectDir, err = os.Getwd()
		if err != nil {
			return MaterializedArtifacts{}, fmt.Errorf("resolve project directory: %w", err)
		}
	}
	dir := filepath.Join(projectDir, containerArtifactDirName)
	if err := ensureMaterializedDir(dir); err != nil {
		return MaterializedArtifacts{}, err
	}
	paths := MaterializedArtifacts{
		Dir:                dir,
		DockerfilePath:     filepath.Join(dir, "Dockerfile"),
		InstallConfigPath:  filepath.Join(dir, "install-config.yaml"),
		GitignorePath:      filepath.Join(dir, ".gitignore"),
		ComposeFilePath:    filepath.Join(dir, "docker-compose.yaml"),
		LauncherBinaryPath: filepath.Join(dir, launcherBinaryName),
	}
	files := []struct {
		path string
		data string
	}{
		{paths.DockerfilePath, dockerfile},
		{paths.InstallConfigPath, installConfig},
		{paths.GitignorePath, materializedGitignore},
	}
	for _, file := range files {
		if err := writeMaterializedArtifact(file.path, []byte(file.data)); err != nil {
			return MaterializedArtifacts{}, err
		}
	}
	return paths, nil
}

// MaterializeLauncherBinary copies the Linux launcher used by release-agent
// Dockerfile layers into the project-local build context. The file is ignored
// by the generated .gitignore and is replaced atomically like the YAML
// artifacts, so compose build never relies on the temporary EnsureImage
// context used by docker run.
func MaterializeLauncherBinary(projectDir, source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("launcher binary source is required")
	}
	if strings.TrimSpace(projectDir) == "" {
		var err error
		projectDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve project directory: %w", err)
		}
	}
	dir := filepath.Join(projectDir, containerArtifactDirName)
	if err := ensureMaterializedDir(dir); err != nil {
		return "", err
	}
	data, err := os.ReadFile(source) // #nosec G304 -- source is the launcher temp artifact created by the CLI.
	if err != nil {
		return "", fmt.Errorf("read launcher binary: %w", err)
	}
	path := filepath.Join(dir, launcherBinaryName)
	if err := writeMaterializedArtifact(path, data); err != nil {
		return "", err
	}
	// The artifact stays 0600 on disk; the Dockerfile grants the exec bit at
	// build time via COPY --chmod=0755, so the in-image mode has a single
	// source of truth and no permissive file ever sits in the project.
	return path, nil
}

// MaterializeCompose writes the project-local compose document after the
// regular container artifacts have been materialized. Compose is intentionally
// a separate operation so callers without infrastructure services keep the
// existing Dockerfile-only behavior.
func MaterializeCompose(projectDir string, file ComposeFile) (string, error) {
	var err error
	if strings.TrimSpace(projectDir) == "" {
		projectDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve project directory: %w", err)
		}
	}
	file, err = ResolveComposeSecrets(projectDir, file)
	if err != nil {
		return "", err
	}
	content, err := RenderCompose(file)
	if err != nil {
		return "", err
	}
	if err := ensureComposeDataDirectories(projectDir, file); err != nil {
		return "", err
	}
	if err := writeComposeGeneratedFiles(projectDir, file); err != nil {
		return "", err
	}
	dir := filepath.Join(projectDir, containerArtifactDirName)
	if err := ensureMaterializedDir(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "docker-compose.yaml")
	if err := writeMaterializedArtifact(path, []byte(content)); err != nil {
		return "", err
	}
	return path, nil
}

// writeComposeGeneratedFiles writes any content BuildCompose staged on
// ComposeFile.GeneratedFiles (for example the egress proxy's squid.conf).
// BuildCompose stays pure — it only computes content and a path; this is the
// single place those bytes reach disk, alongside every other Compose
// artifact, with the same symlink-refusal and atomic-write guarantees.
// Domain-agnostic: works for any future generated file, not just the proxy.
func writeComposeGeneratedFiles(projectDir string, file ComposeFile) error {
	dataDir := filepath.Join(projectDir, containerArtifactDirName, "data")
	for path, content := range file.GeneratedFiles {
		if !pathWithin(dataDir, path) {
			return fmt.Errorf("generated compose file %s escapes the data directory", path)
		}
		if err := ensureMaterializedDir(filepath.Dir(path)); err != nil {
			return err
		}
		if err := writeMaterializedArtifact(path, []byte(content)); err != nil {
			return err
		}
	}
	return nil
}

// ensureComposeDataDirectories creates only the service data directories
// generated by BuildCompose. Arbitrary user bind mounts are never created as
// a side effect of materializing Compose.
func ensureComposeDataDirectories(projectDir string, file ComposeFile) error {
	dataDir := filepath.Join(projectDir, containerArtifactDirName, "data")
	found := false
	for _, service := range file.Services {
		for _, volume := range service.Volumes {
			source, _, ok := composeVolumeParts(volume)
			if !ok || !pathWithin(dataDir, source) {
				continue
			}
			if !found {
				if err := ensureMaterializedDir(dataDir); err != nil {
					return err
				}
				found = true
			}
			if err := ensureMaterializedDir(source); err != nil {
				return fmt.Errorf("create Compose service data directory %s: %w", source, err)
			}
		}
	}
	return nil
}

func ensureMaterializedDir(dir string) error {
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("container artifact directory %s is a symlink", dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("container artifact path %s is not a directory", dir)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect container artifact directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create container artifact directory: %w", err)
	}
	return nil
}

func writeMaterializedArtifact(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("container artifact %s is a symlink", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect container artifact %s: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ai-launcher-artifact-*")
	if err != nil {
		return fmt.Errorf("create temporary container artifact %s: %w", path, err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary container artifact %s: %w", path, err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect container artifact %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary container artifact %s: %w", path, err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace container artifact %s: %w", path, err)
	}
	return nil
}

// PrepareBuildContext materializes the docker build context for a canonical
// Selection: the Dockerfile (dev profile + stacks + agent install steps), the
// minimal install config naming the release agents, and a launcher binary
// copied from launcherLinuxPath (the CLI cross-compiles it first). The image
// tag is derived from the same canonical selection, so an identical selection
// reuses the cached image without rebuilding (R1 item 2).
//
// Returns the context and a cleanup function that removes the temp dir.
func PrepareBuildContext(selection Selection, globalConfigYAML string, launcherLinuxPath string) (*BuildContext, func(), error) {
	return PrepareBuildContextWithOptions(selection, DockerfileOptions{}, globalConfigYAML, launcherLinuxPath)
}

// PrepareBuildContextWithOptions is PrepareBuildContext with explicit
// Dockerfile options (for example the docker CLI layer for launches granted
// the docker permission).
func PrepareBuildContextWithOptions(selection Selection, options DockerfileOptions, globalConfigYAML string, launcherLinuxPath string) (*BuildContext, func(), error) {
	if err := selection.Validate(); err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "ai-launcher-box-")
	if err != nil {
		return nil, nil, fmt.Errorf("create build context: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	tag, err := ImageTagWithOptions(selection, options)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	dockerfile, err := DockerfileWithOptions(selection, options)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	dfPath := filepath.Join(dir, "Dockerfile")
	// 0600 is fine for docker's build context reader and satisfies the strict
	// file-permission rule; the Dockerfile contains no secrets, but the
	// install-config.yaml next to it may, so the whole context is private.
	if err := os.WriteFile(dfPath, []byte(dockerfile), 0o600); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("write Dockerfile: %w", err)
	}
	cfgPath := filepath.Join(dir, "install-config.yaml")
	if strings.TrimSpace(globalConfigYAML) != "" {
		if err := os.WriteFile(cfgPath, []byte(globalConfigYAML), 0o600); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("write install config: %w", err)
		}
	}
	// The launcher binary is needed for release agents and for the explicit
	// in-image ai-memory install. Script/host-only agents without memory do
	// not COPY it.
	needLauncher := SelectionNeedsMemory(selection) || SelectionNeedsTools(selection)
	for _, agent := range selection.Agents {
		if agent.Kind == InstallRelease {
			needLauncher = true
			break
		}
	}
	if needLauncher {
		if strings.TrimSpace(launcherLinuxPath) == "" {
			cleanup()
			return nil, nil, fmt.Errorf("launcher binary is required to install release agents, ai-memory, or auxiliary tools in the image")
		}
		if strings.TrimSpace(globalConfigYAML) == "" {
			cleanup()
			return nil, nil, fmt.Errorf("install config is required to install release agents, ai-memory, or auxiliary tools in the image")
		}
		dest := filepath.Join(dir, launcherBinaryName) // #nosec G703 -- dir is os.MkdirTemp, launcherLinuxPath is our own temp binary
		// #nosec G304 -- launcherLinuxPath is produced by buildLauncherLinux
		// (an os.CreateTemp path) or by the caller; it is never user input.
		data, err := os.ReadFile(launcherLinuxPath)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("read launcher binary: %w", err)
		}
		// The copy stays 0600 in the build context: the Dockerfile grants the
		// exec bit at build time via COPY --chmod=0755.
		if err := os.WriteFile(dest, data, 0o600); err != nil { // #nosec G703 -- dest is os.MkdirTemp-derived
			cleanup()
			return nil, nil, fmt.Errorf("copy launcher binary: %w", err)
		}
	}

	return &BuildContext{
		Dir:                dir,
		DockerfilePath:     dfPath,
		InstallConfigPath:  cfgPath,
		LauncherBinaryPath: filepath.Join(dir, launcherBinaryName),
		ImageTag:           tag,
	}, cleanup, nil
}

// BuildCommand returns the selected runtime's build argv for a prepared
// context, tagging the content-hashed image. The context is passed as the
// build context path; --pull and the cache are runtime defaults.
func BuildCommand(ctx *BuildContext, runtime Runtime) []string {
	return BuildCommandWithContext(ctx, runtime, "")
}

// BuildCommandWithContext returns the selected runtime's build argv with an
// optional Docker context. The context is placed before the subcommand, as
// required by the Docker CLI.
func BuildCommandWithContext(ctx *BuildContext, runtime Runtime, contextName string) []string {
	runtime = RuntimeOrDefault(runtime)
	argv := append(CommandPrefix(runtime, contextName), "build",
		"--tag", ctx.ImageTag,
		ctx.Dir,
	)
	return argv
}

// Runner executes a command line and returns its exit code and error. The
// CLI passes a runtime runner (exec + stream); tests pass fakes. Exit code 0
// means the command succeeded.
type Runner func(argv []string) (int, error)

// EnsureImage checks whether the tagged image exists locally and, when it
// does not, materializes the build context and runs the selected runtime's
// build command with the runner. It returns the tag and a cleanup that removes
// the temp context. A missing runtime CLI or daemon surfaces as a runner
// error, not here.
func EnsureImage(runtime Runtime, selection Selection, globalConfigYAML, launcherLinuxPath string, runner Runner) (string, func(), error) {
	return EnsureImageWithContext(runtime, selection, globalConfigYAML, launcherLinuxPath, "", runner)
}

// EnsureImageWithOptions is EnsureImage with explicit Dockerfile options (for
// example the docker CLI layer for launches granted the docker permission).
func EnsureImageWithOptions(runtime Runtime, selection Selection, options DockerfileOptions, globalConfigYAML, launcherLinuxPath string, runner Runner) (string, func(), error) {
	return ensureImage(runtime, selection, options, globalConfigYAML, launcherLinuxPath, "", runner)
}

// EnsureImageWithContext is EnsureImage with an explicit Docker context. The
// inspect and build are sent to the same context, avoiding a local image
// cache hit followed by a remote run (or the inverse).
func EnsureImageWithContext(runtime Runtime, selection Selection, globalConfigYAML, launcherLinuxPath, contextName string, runner Runner) (string, func(), error) {
	return ensureImage(runtime, selection, DockerfileOptions{}, globalConfigYAML, launcherLinuxPath, contextName, runner)
}

// EnsureImageWithContextOptions is EnsureImageWithContext with explicit
// Dockerfile options (for example the docker CLI layer for launches granted
// the docker permission).
func EnsureImageWithContextOptions(runtime Runtime, selection Selection, options DockerfileOptions, globalConfigYAML, launcherLinuxPath, contextName string, runner Runner) (string, func(), error) {
	return ensureImage(runtime, selection, options, globalConfigYAML, launcherLinuxPath, contextName, runner)
}

func ensureImage(runtime Runtime, selection Selection, options DockerfileOptions, globalConfigYAML, launcherLinuxPath, contextName string, runner Runner) (string, func(), error) {
	runtime = RuntimeOrDefault(runtime)
	if err := ValidateContext(runtime, contextName); err != nil {
		return "", nil, err
	}
	tag, err := ImageTagWithOptions(selection, options)
	if err != nil {
		return "", nil, err
	}
	code, err := runner(append(CommandPrefix(runtime, contextName), "image", "inspect", tag))
	if err == nil && code == 0 {
		return tag, func() {
			// A cached image owns no temporary build context or overlay here.
		}, nil
	}
	ctx, cleanup, err := PrepareBuildContextWithOptions(selection, options, globalConfigYAML, launcherLinuxPath)
	if err != nil {
		return "", nil, err
	}
	code, err = runner(BuildCommandWithContext(ctx, runtime, contextName))
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("%s build failed: %w", runtime.Name(), err)
	}
	if code != 0 {
		cleanup()
		return "", nil, fmt.Errorf("%s build exited with code %d", runtime.Name(), code)
	}
	return tag, cleanup, nil
}
