package container

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Runtime is the small command/runtime contract shared by the container
// launcher, image builder, and future compose support. Detection chooses the
// CLI; Info is deliberately separate because it talks to the runtime and may
// fail even when the binary is present on PATH.
type Runtime interface {
	// Name returns the stable runtime name used in diagnostics and config.
	Name() string
	// Command returns the CLI binary used to execute container commands.
	Command() string
	// HostGateway returns the hostname that reaches the host from a container.
	HostGateway() string
	// ComposeCommand returns the runtime's compose command prefix.
	ComposeCommand() []string
	// SocketPath returns the default control socket, or empty when none exists.
	SocketPath() string
	// Info checks whether the selected runtime can answer its info command.
	Info() error
}

// DockerRuntime describes the Docker CLI and its host integration points.
type DockerRuntime struct{}

// Name returns Docker's stable runtime name.
func (DockerRuntime) Name() string { return "docker" }

// Command returns Docker's CLI binary.
func (DockerRuntime) Command() string { return "docker" }

// HostGateway returns Docker's host gateway hostname.
func (DockerRuntime) HostGateway() string { return "host.docker.internal" }

// ComposeCommand returns Docker Compose's command prefix.
func (DockerRuntime) ComposeCommand() []string { return []string{"docker", "compose"} }

// SocketPath returns Docker's default control socket.
func (DockerRuntime) SocketPath() string { return "/var/run/docker.sock" }

// Info checks Docker daemon availability.
func (r DockerRuntime) Info() error { return runtimeInfo(r.Command()) }

// PodmanRuntime describes Podman's daemonless/rootless-friendly CLI. A
// rootless Podman setup has no control socket to mount, so SocketPath is empty
// until a future phase adds explicit rootful/rootless policy.
type PodmanRuntime struct{}

// Name returns Podman's stable runtime name.
func (PodmanRuntime) Name() string { return "podman" }

// Command returns Podman's CLI binary.
func (PodmanRuntime) Command() string { return "podman" }

// HostGateway returns Podman's host gateway hostname.
func (PodmanRuntime) HostGateway() string { return "host.containers.internal" }

// ComposeCommand returns Podman Compose's command prefix.
func (PodmanRuntime) ComposeCommand() []string { return []string{"podman", "compose"} }

// SocketPath returns empty for the rootless default.
func (PodmanRuntime) SocketPath() string { return "" }

// Info checks Podman availability.
func (r PodmanRuntime) Info() error { return runtimeInfo(r.Command()) }

// NerdctlRuntime describes nerdctl on a containerd host.
type NerdctlRuntime struct{}

// Name returns nerdctl's stable runtime name.
func (NerdctlRuntime) Name() string { return "nerdctl" }

// Command returns nerdctl's CLI binary.
func (NerdctlRuntime) Command() string { return "nerdctl" }

// HostGateway returns nerdctl's Docker-compatible host gateway hostname.
func (NerdctlRuntime) HostGateway() string { return "host.docker.internal" }

// ComposeCommand returns nerdctl Compose's command prefix.
func (NerdctlRuntime) ComposeCommand() []string { return []string{"nerdctl", "compose"} }

// SocketPath returns containerd's default control socket.
func (NerdctlRuntime) SocketPath() string { return "/run/containerd/containerd.sock" }

// Info checks nerdctl/containerd availability.
func (r NerdctlRuntime) Info() error { return runtimeInfo(r.Command()) }

var runtimeLookPath = exec.LookPath

var runtimeFactories = map[string]func() Runtime{
	"docker":  func() Runtime { return DockerRuntime{} },
	"podman":  func() Runtime { return PodmanRuntime{} },
	"nerdctl": func() Runtime { return NerdctlRuntime{} },
}

var runtimePriority = []string{"docker", "podman", "nerdctl"}

// RuntimeStatus describes whether a supported runtime CLI is discoverable on
// PATH. This is intentionally a lighter check than Runtime.Info: the TUI can
// render the available choices without blocking on a daemon, while launch
// preflight still performs the stronger runtime health check.
type RuntimeStatus struct {
	Name      string
	Available bool
}

// ListRuntimeStatuses reports every supported runtime in selection order.
// "auto" is represented by the caller because it is a policy, not a binary.
func ListRuntimeStatuses() []RuntimeStatus {
	return listRuntimeStatuses(runtimeLookPath)
}

func listRuntimeStatuses(lookPath func(string) (string, error)) []RuntimeStatus {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	statuses := make([]RuntimeStatus, 0, len(runtimePriority))
	for _, name := range runtimePriority {
		_, err := lookPath(name)
		statuses = append(statuses, RuntimeStatus{Name: name, Available: err == nil})
	}
	return statuses
}

// listDockerContextsCommand is a seam for the TUI picker and its tests. The
// command only returns context names; it never changes Docker's active
// context, so selecting a name remains an explicit launch-time decision.
var listDockerContextsCommand = func() ([]byte, error) {
	return exec.Command("docker", "context", "ls", "--format", "{{.Name}}").Output() // #nosec G204 -- fixed Docker context-list command
}

// ListDockerContexts returns the names available to the local Docker CLI.
// Empty output is valid (the caller can still type a context name manually),
// while command failures are surfaced so the TUI can explain why the picker
// is unavailable instead of silently presenting an empty list.
func ListDockerContexts() ([]string, error) {
	out, err := listDockerContextsCommand()
	if err != nil {
		return nil, fmt.Errorf("docker context ls: %w", err)
	}
	seen := make(map[string]struct{})
	contexts := make([]string, 0)
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if err := ValidateContext(DockerRuntime{}, name); err != nil {
			return nil, fmt.Errorf("docker context ls returned invalid context %q: %w", name, err)
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		contexts = append(contexts, name)
	}
	sort.Strings(contexts)
	return contexts, nil
}

// DetectRuntime returns the preferred available runtime. An empty preference
// and "auto" use docker → podman → nerdctl; an explicit preference checks
// only that runtime and never silently falls back to another CLI.
func DetectRuntime(preference string) (Runtime, error) {
	return detectRuntime(preference, runtimeLookPath)
}

func detectRuntime(preference string, lookPath func(string) (string, error)) (Runtime, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	preference = strings.ToLower(strings.TrimSpace(preference))
	if preference == "" || preference == "auto" {
		for _, name := range runtimePriority {
			if _, err := lookPath(name); err == nil {
				return runtimeFactories[name](), nil
			}
		}
		return nil, fmt.Errorf("no container runtime found in PATH (tried %s)", strings.Join(runtimePriority, ", "))
	}
	factory, ok := runtimeFactories[preference]
	if !ok {
		return nil, fmt.Errorf("unsupported container runtime %q (supported: auto, %s)", preference, strings.Join(runtimePriority, ", "))
	}
	if _, err := lookPath(preference); err != nil {
		return nil, fmt.Errorf("container runtime %q is not available in PATH: %w", preference, err)
	}
	return factory(), nil
}

// DefaultRuntime preserves the historical Docker behavior for callers that
// construct a zero RunConfig directly (including older integrations/tests).
func DefaultRuntime() Runtime { return DockerRuntime{} }

// RuntimeOrDefault returns rt when configured, otherwise Docker.
func RuntimeOrDefault(rt Runtime) Runtime {
	if rt == nil {
		return DefaultRuntime()
	}
	return rt
}

// ValidateContext checks the explicit Docker context selector. Docker context
// names are passed as one argv element, but rejecting whitespace and leading
// dashes keeps malformed configuration from becoming an ambiguous CLI flag.
// Other runtimes deliberately reject it because their connection/context
// mechanisms are different and are not silently guessed here.
func ValidateContext(runtime Runtime, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	runtime = RuntimeOrDefault(runtime)
	if runtime.Name() != "docker" {
		return fmt.Errorf("container context %q is only supported by the docker runtime", value)
	}
	if strings.HasPrefix(value, "-") || strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("invalid docker context %q: use a context name without whitespace or a leading dash", value)
	}
	return nil
}

// CommandPrefix returns the runtime CLI prefix with an optional Docker
// context. Keeping the context in argv makes each launch deterministic and
// prevents a remote selection from leaking into unrelated Docker commands.
func CommandPrefix(runtime Runtime, contextName string) []string {
	runtime = RuntimeOrDefault(runtime)
	prefix := []string{runtime.Command()}
	if contextName = strings.TrimSpace(contextName); contextName != "" && runtime.Name() == "docker" {
		prefix = append(prefix, "--context", contextName)
	}
	return prefix
}

// ComposeCommandFor returns the selected runtime's compose prefix with an
// optional Docker context.
func ComposeCommandFor(runtime Runtime, contextName string) []string {
	prefix := CommandPrefix(runtime, contextName)
	return append(prefix, "compose")
}

// RuntimeInfo checks the selected runtime and Docker context.
func RuntimeInfo(runtime Runtime, contextName string) error {
	if err := ValidateContext(runtime, contextName); err != nil {
		return err
	}
	runtime = RuntimeOrDefault(runtime)
	prefix := CommandPrefix(runtime, contextName)
	if err := exec.Command(prefix[0], append(prefix[1:], "info")...).Run(); err != nil { // #nosec G204 -- command is a fixed runtime CLI and context is one argv value.
		return fmt.Errorf("%s info: %w", runtime.Name(), err)
	}
	return nil
}

func runtimeInfo(command string) error {
	if err := exec.Command(command, "info").Run(); err != nil { // #nosec G204 -- command is one of the fixed runtime CLIs above
		return fmt.Errorf("%s info: %w", command, err)
	}
	return nil
}
