package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

func runComposeCommand(args []string, launchConfig launcher.LaunchConfig, global config.Global, opts cliOptions, in io.Reader, out, errOut io.Writer) error {
	invocation, err := parseComposeInvocation(args, &opts)
	if err != nil {
		return err
	}
	if invocation.action == "up" && !launchConfig.UseDocker {
		return errors.New("compose up requires container mode; pass --docker-backend or set options.docker: true")
	}

	runtime := container.RuntimeOrDefault(launchConfig.Docker.Runtime)
	composePath := filepath.Join(mustGetwd(), containerArtifactDir, "docker-compose.yaml")
	if !opts.dryRun && invocation.action == "up" {
		choice, err := parseComposeUpdateChoice(opts.composeUpdate)
		if err != nil {
			return err
		}
		choice, err = resolveComposeUpdate(launchConfig, choice, in, out, interactiveLaunch(opts))
		if err != nil {
			return err
		}
		if err := ensureComposeArtifacts(launchConfig, global, out, errOut, choice); err != nil {
			return err
		}
	}
	if !opts.dryRun && invocation.action != "up" {
		exists, err := regularArtifactExists(composePath)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("compose file %s does not exist; run ai-launcher generate first", composePath)
		}
	}
	if !opts.dryRun {
		if err := container.RuntimeInfo(runtime, launchConfig.ContainerContext); err != nil {
			return fmt.Errorf("%s runtime is unavailable: %w", runtime.Name(), err)
		}
	}
	argv, err := composeCommandArgvWithContext(runtime, composePath, invocation.action, invocation.services, invocation.removeVolumes, interactiveLaunch(opts), launchConfig.ContainerContext)
	if err != nil {
		return err
	}
	if opts.dryRun {
		if invocation.action == "up" && len(launchConfig.Services) > 0 {
			rendered, err := composePreviewYAML(composePath, launchConfig)
			if err != nil {
				return err
			}
			printComposeYAMLPreview(out, rendered, argv)
		} else {
			printComposePreview(out, container.ComposeFile{}, argv)
		}
		return nil
	}
	return runRuntimeCommand(argv, composeRuntimeEnv(launchConfig), in, out, errOut)
}

type composeInvocation struct {
	action        string
	services      []string
	removeVolumes bool
}

func parseComposeInvocation(args []string, opts *cliOptions) (composeInvocation, error) {
	if len(args) == 0 {
		return composeInvocation{}, errors.New("compose requires one of: up, down, logs, ps")
	}
	invocation := composeInvocation{action: args[0]}
	for _, arg := range args[1:] {
		switch arg {
		case "-v", "--volumes":
			invocation.removeVolumes = true
		case "--dry-run":
			opts.dryRun = true
		default:
			if strings.HasPrefix(arg, "-") {
				return composeInvocation{}, fmt.Errorf("unknown compose flag %q; supported flags are -v/--volumes and --dry-run", arg)
			}
			invocation.services = append(invocation.services, arg)
		}
	}
	switch invocation.action {
	case "up":
		if invocation.removeVolumes {
			return composeInvocation{}, errors.New("compose up does not accept --volumes")
		}
	case "down", "ps":
		if len(invocation.services) > 0 {
			return composeInvocation{}, fmt.Errorf("compose %s does not accept service arguments", invocation.action)
		}
	case "logs":
		if len(invocation.services) > 1 {
			return composeInvocation{}, errors.New("compose logs accepts at most one service")
		}
	default:
		return composeInvocation{}, fmt.Errorf("unsupported compose command %q; use up, down, logs or ps", invocation.action)
	}
	return invocation, nil
}

func composeCommandArgv(runtime container.Runtime, composePath, action string, services []string, removeVolumes, interactive bool) ([]string, error) {
	return composeCommandArgvWithContext(runtime, composePath, action, services, removeVolumes, interactive, "")
}

func composeCommandArgvWithContext(runtime container.Runtime, composePath, action string, services []string, removeVolumes, interactive bool, contextName string) ([]string, error) {
	if runtime == nil {
		runtime = container.DefaultRuntime()
	}
	if err := container.ValidateContext(runtime, contextName); err != nil {
		return nil, err
	}
	argv := append([]string{}, container.ComposeCommandFor(runtime, contextName)...)
	argv = append(argv, "-f", composePath, action)
	switch action {
	case "up":
		if removeVolumes {
			return nil, errors.New("compose up does not accept --volumes")
		}
		argv = append(argv, "--build")
		if !interactive {
			argv = append(argv, "-d")
		}
		argv = append(argv, services...)
	case "down":
		if len(services) > 0 {
			return nil, errors.New("compose down does not accept service arguments")
		}
		if removeVolumes {
			argv = append(argv, "--volumes")
		}
	case "logs":
		if len(services) > 1 {
			return nil, errors.New("compose logs accepts at most one service")
		}
		argv = append(argv, services...)
	case "ps":
		if len(services) > 0 {
			return nil, errors.New("compose ps does not accept service arguments")
		}
	default:
		return nil, fmt.Errorf("unsupported compose command %q", action)
	}
	return argv, nil
}

// composeAgentRunArgvWithContext starts only the agent as an interactive
// one-shot while Compose brings up its declared dependencies. Unlike
// `compose up`, this attaches stdin/stdout to the agent instead of multiplexing
// dependency logs into the user's terminal.
func composeAgentRunArgvWithContext(runtime container.Runtime, composePath, contextName string) ([]string, error) {
	if runtime == nil {
		runtime = container.DefaultRuntime()
	}
	if err := container.ValidateContext(runtime, contextName); err != nil {
		return nil, err
	}
	argv := append([]string{}, container.ComposeCommandFor(runtime, contextName)...)
	return append(argv, "-f", composePath, "run", "--build", "--rm", "--service-ports", "agent"), nil
}

func printComposePreview(out io.Writer, compose container.ComposeFile, argv []string) {
	if len(compose.Services) > 0 {
		if rendered, err := container.RenderCompose(compose); err == nil {
			printComposeYAMLPreview(out, rendered, argv)
			return
		}
	}
	_, _ = fmt.Fprintln(out, shellJoin(argv))
}

func printComposeYAMLPreview(out io.Writer, rendered string, argv []string) {
	if strings.TrimSpace(rendered) != "" {
		_, _ = fmt.Fprintln(out, rendered)
	}
	_, _ = fmt.Fprintln(out, shellJoin(argv))
}

func composePreviewYAML(composePath string, launchConfig launcher.LaunchConfig) (string, error) {
	exists, err := regularArtifactExists(composePath)
	if err != nil {
		return "", err
	}
	if exists {
		data, err := os.ReadFile(composePath) // #nosec G304 -- path is the fixed project-local Compose artifact.
		if err != nil {
			return "", fmt.Errorf("read compose file %s: %w", composePath, err)
		}
		if strings.TrimSpace(string(data)) == "" {
			return "", fmt.Errorf("compose file %s is empty", composePath)
		}
		return string(data), nil
	}
	compose, err := launcher.BuildCompose(launchConfig)
	if err != nil {
		return "", fmt.Errorf("build compose: %w", err)
	}
	rendered, err := container.RenderCompose(container.MaskComposeSecrets(compose))
	if err != nil {
		return "", fmt.Errorf("render compose: %w", err)
	}
	return rendered, nil
}

// interruptedExitCode is the conventional shell status for a SIGINT kill
// (128+2). exec reports -1 for a signal-terminated child, which cannot serve
// as a process exit status.
const interruptedExitCode = 130

// interruptGrace bounds the wait for a child to honor the forwarded SIGINT
// before the cancelled context kills it outright.
const interruptGrace = 10 * time.Second

// exitStatusError preserves the child's exit status so the compose path can
// mirror it as the process exit code instead of the flattened 1 the
// error-only channel forces.
type exitStatusError struct{ code int }

func (e *exitStatusError) Error() string {
	return fmt.Sprintf("compose command exited with status %d", e.code)
}

// ExitCode returns the child exit status for the process-exit mapping.
func (e *exitStatusError) ExitCode() int { return e.code }

// interruptibleCommand builds a child that is stopped on SIGINT/SIGTERM. The
// signal is forwarded to the child instead of the default SIGKILL so the
// runtime can stop containers gracefully, and Run returns — letting the
// launcher's own cleanup (compose down, overlay removal) run — instead of the
// process dying on the signal. stop releases the signal handler.
func interruptibleCommand(argv []string) (cmd *exec.Cmd, stop context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// #nosec G204 -- argv is assembled from the fixed Runtime contract and
	// validated compose subcommands; no shell is involved.
	cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		return nil
	}
	cmd.WaitDelay = interruptGrace
	return cmd, stop
}

func runRuntimeCommand(argv, env []string, in io.Reader, out, errOut io.Writer) error {
	if len(argv) == 0 {
		return errors.New("compose command is empty")
	}
	command, stop := interruptibleCommand(argv)
	defer stop()
	command.Stdin = in
	command.Stdout = out
	command.Stderr = errOut
	command.Env = env
	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			if code < 0 {
				// A signal-killed child reports -1; map it to the
				// conventional status so an interruption exits non-zero.
				code = interruptedExitCode
			}
			return &exitStatusError{code: code}
		}
		if errors.Is(err, context.Canceled) {
			// The interrupt tore the run down before an exit status was
			// produced (WaitDelay kill); report the SIGINT status.
			return &exitStatusError{code: interruptedExitCode}
		}
		return fmt.Errorf("run compose command: %w", err)
	}
	return nil
}

func composeRuntimeEnv(launchConfig launcher.LaunchConfig) []string {
	env := append([]string(nil), os.Environ()...)
	value := ""
	if launchConfig.UseMemory {
		value = strings.TrimSpace(launchConfig.MemoryAuthToken)
	}
	filtered := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, "AI_MEMORY_AUTH_TOKEN=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	if value != "" {
		filtered = append(filtered, "AI_MEMORY_AUTH_TOKEN="+value)
	}
	return filtered
}
