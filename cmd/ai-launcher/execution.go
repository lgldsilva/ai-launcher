package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// execute runs the composed argv. After the interactive TUI, ai-launcher stays
// the parent (PTY) so a child that exits immediately (ai-memory TLS, jail cwd,
// etc.) is reported as our error instead of looking like the UI just vanished.
// CLI non-TUI launches still Replace into the child for a thin process tree.
// executeWithDockerCleanup runs the composed argv and then invokes the docker
// overlay cleanup (removing temp copies that may carry tokens) whether the
// run succeeded or failed. It is a thin wrapper so the launch loop stays
// readable and the cleanup can never be skipped by an early return.
func (r *launchRequest) executeWithDockerCleanup(argv []string, dockerCleanup func()) error {
	err := r.execute(argv)
	if dockerCleanup != nil {
		dockerCleanup()
	}
	return err
}

func (r *launchRequest) execute(argv []string) error {
	label := r.launchConfig.Agent.Command
	if r.launchConfig.ContinueSession {
		label = "continue session"
	}
	cmdLine := shellJoin(argv)
	// Always announce what is about to run so a TUI close is never silent.
	_, _ = fmt.Fprintf(r.errOut, "ai-launcher: starting %s\n", label)
	_, _ = fmt.Fprintf(r.errOut, "ai-launcher: %s\n", cmdLine)

	useReplace := len(r.args) > 0 && r.in == os.Stdin && r.out == os.Stdout && r.errOut == os.Stderr
	if useReplace {
		if err := launcher.ReplaceWithEnv(argv, launcher.Environment(r.launchConfig)); err != nil {
			return fmt.Errorf("failed to start %s: %w\ncommand: %s", label, err, cmdLine)
		}
		return nil
	}
	// SIGINT/SIGTERM cancel the run instead of killing the launcher outright,
	// so the deferred overlay cleanup in executeWithDockerCleanup still runs
	// and the token-carrying temp copies are removed. In an interactive
	// session the host terminal is in raw mode and Ctrl-C reaches the agent
	// through the PTY; this covers non-terminal stdin and external SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	execErr := (launcher.PTYExecutor{}).RunWithEnv(ctx, argv, launcher.Environment(r.launchConfig), r.in, r.out, r.errOut)
	if execErr != nil {
		return fmt.Errorf("failed to start %s: %w\ncommand: %s\n%s", label, execErr, cmdLine, launchFailureHint(execErr.Error()))
	}
	return nil
}

// dockerRunner runs docker argv as a child process with the launcher's own
// stdin/stdout/stderr wired through, so `docker build` progress streams and
// interactive containers behave like the CLI the operator expects. It is a
// container.Runner so EnsureImage can call it.
func (r *launchRequest) dockerRunner(argv []string) (int, error) {
	// argv is composed internally by launcher.Build (docker run from the
	// container backend); it is never raw user input. The image tag and mount
	// paths come from the selection the operator saved. The interruptible
	// command lets an interrupted build return here — so EnsureImage removes
	// its temp build context — instead of the launcher dying on the signal.
	cmd, stop := interruptibleCommand(argv)
	defer stop()
	cmd.Stdin = r.in
	cmd.Stdout = r.out
	cmd.Stderr = r.errOut
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// prepareDockerIfNeeded ensures the tagged image exists (building it when
// missing) and applies MCP config overlays to the docker run argv. It is a
// no-op when the docker backend is off, keeping the hot launch path free of
// the branch. It returns the argv to execute (the original argv is not
// mutated) plus a cleanup that removes the overlay temp copies after the
// container exits (R9.6 — they may carry tokens). A dry-run never reaches
// here — the image check happens only for a real launch.
func (r *launchRequest) prepareDockerIfNeeded(argv []string) ([]string, func(), error) {
	noop := func() {
		// Docker is disabled or the image preparation failed before a cleanup
		// resource was allocated; the launch caller still receives a safe hook.
	}
	if !r.launchConfig.UseDocker {
		return argv, noop, nil
	}
	sel := r.launchConfig.Docker.Selection
	if sel.Validate() != nil {
		return argv, noop, errors.New("docker backend has an invalid image selection")
	}
	runtime := container.RuntimeOrDefault(r.launchConfig.Docker.Runtime)
	if err := container.RuntimeInfo(runtime, r.launchConfig.ContainerContext); err != nil {
		return argv, noop, fmt.Errorf("%s runtime is unavailable: %w", runtime.Name(), err)
	}

	// The image needs the launcher binary for release agents and for the
	// explicit in-image ai-memory installation; script/npm/host-only agents
	// without memory build without it.
	needLauncher := container.SelectionNeedsMemory(sel) || container.SelectionNeedsTools(sel)
	for _, agent := range sel.Agents {
		if agent.Kind == container.InstallRelease {
			needLauncher = true
			break
		}
	}
	var launcherLinux string
	if needLauncher {
		var err error
		launcherLinux, err = buildLauncherLinux(r.out, r.errOut)
		if err != nil {
			return argv, noop, err
		}
		defer func() { _ = os.Remove(launcherLinux) }()
	}

	installConfigYAML, err := container.InstallConfigWithTools(r.global, sel.Agents, sel.Tools)
	if err != nil {
		return argv, noop, fmt.Errorf("docker backend: %w", err)
	}
	_, cleanup, err := container.EnsureImageWithContextOptions(runtime, sel, dockerfileOptions(r.launchConfig), installConfigYAML, launcherLinux, r.launchConfig.ContainerContext, r.dockerRunner)
	if err != nil {
		return argv, noop, err
	}
	// The image build context is a temp dir; once the image is built it can go.
	cleanup()

	// Apply MCP config overlays: rewrite loopback URLs in known config files
	// into temp copies mounted over the originals (R7 item 31). The overlays
	// go into the RunConfig so BuildRunCommand emits them as -v flags BEFORE
	// the image argument — appended after the image, docker would treat them
	// as the agent's native args (C2).
	runConfig := r.launchConfig.Docker
	runConfig.Overlays = nil
	var overlayDir string
	if runConfig.AddHostGateway {
		var err error
		overlayDir, err = os.MkdirTemp("", "ai-launcher-overlay-")
		if err != nil {
			return argv, noop, err
		}
		disableRewrite := container.RewriteDisabled(os.Environ())
		rewrite := func(value string) (string, bool) {
			return container.RewriteLocalhost(value, runtime.HostGateway())
		}
		for _, mount := range overlayCandidates(r.launchConfig.HomeDir) {
			overlay := container.PlanOverlay(mount, overlayDir, rewrite, disableRewrite)
			if overlay == nil {
				continue
			}
			runConfig.Overlays = append(runConfig.Overlays, *overlay)
		}
	}
	// Rebuild the argv with the overlays in place (the launcher build is pure
	// and deterministic, so this reproduces the same command plus the mounts).
	launch := r.launchConfig
	launch.Docker = runConfig
	argv, err = launcher.Build(launch)
	if err != nil {
		_ = os.RemoveAll(overlayDir)
		return argv, noop, err
	}
	// The overlay copies may carry tokens (claude.json, MCP configs): remove
	// the whole temp dir after the container exits, success or failure.
	return argv, func() { _ = os.RemoveAll(overlayDir) }, nil
}

// overlayCandidates returns the known config files worth scanning for
// loopback URLs. The agent config dirs are bind-mounted by the launcher; the
// loose files that store MCP server URLs get per-file overlays.
func overlayCandidates(home string) []string {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, configDirectoryName, "opencode", "opencode.json"),
		filepath.Join(home, configDirectoryName, "semidx", "config.yaml"),
		filepath.Join(home, configDirectoryName, "semidx", "semidx.env"),
	}
}
