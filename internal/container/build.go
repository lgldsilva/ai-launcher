package container

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildContext is a prepared docker build context directory: the generated
// Dockerfile, the minimal install config, and the launcher binary the image
// uses to install agents (design C1). The CLI materializes it and runs
// `docker build`, streaming the daemon output.
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

// PrepareBuildContext materializes the docker build context for a canonical
// Selection: the Dockerfile (dev profile + stacks + agent install steps), the
// minimal install config naming the release agents, and a launcher binary
// copied from launcherLinuxPath (the CLI cross-compiles it first). The image
// tag is derived from the same canonical selection, so an identical selection
// reuses the cached image without rebuilding (R1 item 2).
//
// Returns the context and a cleanup function that removes the temp dir.
func PrepareBuildContext(selection Selection, globalConfigYAML string, launcherLinuxPath string) (*BuildContext, func(), error) {
	if err := selection.Validate(); err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "ai-launcher-box-")
	if err != nil {
		return nil, nil, fmt.Errorf("create build context: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	tag, err := ImageTag(selection)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	dockerfile, err := Dockerfile(selection)
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
	// The launcher binary is only needed when the selection installs agents
	// by release recipe; script and host-binary agents do not COPY it.
	needLauncher := false
	for _, agent := range selection.Agents {
		if agent.Kind == InstallRelease {
			needLauncher = true
			break
		}
	}
	if needLauncher {
		if strings.TrimSpace(launcherLinuxPath) == "" {
			cleanup()
			return nil, nil, fmt.Errorf("launcher binary is required to install release agents in the image")
		}
		dest := filepath.Join(dir, "ai-launcher") // #nosec G703 -- dir is os.MkdirTemp, launcherLinuxPath is our own temp binary
		// #nosec G304 -- launcherLinuxPath is produced by buildLauncherLinux
		// (an os.CreateTemp path) or by the caller; it is never user input.
		data, err := os.ReadFile(launcherLinuxPath)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("read launcher binary: %w", err)
		}
		// #nosec G306 -- the copied launcher binary must keep its exec bit for
		// the in-image installer (COPY in the Dockerfile preserves it); the
		// source is our own cross-compiled temp binary, not user input.
		if err := os.WriteFile(dest, data, 0o755); err != nil { // #nosec G306 G703 -- exec bit required; dest is os.MkdirTemp-derived
			cleanup()
			return nil, nil, fmt.Errorf("copy launcher binary: %w", err)
		}
	}

	return &BuildContext{
		Dir:                dir,
		DockerfilePath:     dfPath,
		InstallConfigPath:  cfgPath,
		LauncherBinaryPath: filepath.Join(dir, "ai-launcher"),
		ImageTag:           tag,
	}, cleanup, nil
}

// BuildCommand returns the `docker build` argv for a prepared context,
// tagging the content-hashed image. The context is passed as the build
// context path; --pull and the cache are docker defaults.
func BuildCommand(ctx *BuildContext) []string {
	return []string{
		"docker", "build",
		"--tag", ctx.ImageTag,
		ctx.Dir,
	}
}

// Runner executes a command line and returns its exit code and error. The
// CLI passes a docker runner (exec + stream); tests pass fakes. Exit code 0
// means the command succeeded.
type Runner func(argv []string) (int, error)

// EnsureImage checks whether the tagged image exists locally and, when it
// does not, materializes the build context and runs `docker build` with the
// runner. It returns the tag and a cleanup that removes the temp context.
// A missing docker CLI or daemon surfaces as a runner error, not here.
func EnsureImage(selection Selection, globalConfigYAML, launcherLinuxPath string, runner Runner) (string, func(), error) {
	tag, err := ImageTag(selection)
	if err != nil {
		return "", nil, err
	}
	code, err := runner([]string{"docker", "image", "inspect", tag})
	if err == nil && code == 0 {
		return tag, func() {}, nil
	}
	ctx, cleanup, err := PrepareBuildContext(selection, globalConfigYAML, launcherLinuxPath)
	if err != nil {
		return "", nil, err
	}
	code, err = runner(BuildCommand(ctx))
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker build failed: %w", err)
	}
	if code != 0 {
		cleanup()
		return "", nil, fmt.Errorf("docker build exited with code %d", code)
	}
	return tag, cleanup, nil
}
