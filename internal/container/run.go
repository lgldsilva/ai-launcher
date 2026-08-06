package container

import (
	"fmt"
	"strings"
)

// RunConfig carries everything the docker run argv builder needs beyond the
// image selection: the host paths to share, the agent to invoke, and the
// launch flags that become docker flags. It mirrors the fields of
// launcher.LaunchConfig that matter for container mode, so the launcher can
// fill it without importing this package back.
type RunConfig struct {
	// Selection is the image to run (its tag is the image reference).
	Selection Selection
	// HomeDir is the host $HOME, used to resolve credential mounts and the
	// UID/GID user mapping. Empty means mounts are skipped.
	HomeDir string
	// UID and GID are the host user ids mapped into the container (R9.5):
	// same-path mounts of ~/.ssh and ~/.gitconfig are only usable when the
	// container runs as the host user.
	UID int
	GID int
	// ProjectDir is the current working directory, mounted read-write at the
	// identical path (same-path design) and set as the container WORKDIR.
	ProjectDir string
	// AgentCommands are the agent commands whose credential/history mounts
	// are shared (claude, codex, opencode, muse, ...). A mount is skipped
	// when its host path does not exist.
	AgentCommands []string
	// GHConfig and SSHConfig are the permission-driven mounts (R8 item 34):
	// they mirror the jail permission→mount mapping but read-only and
	// conditional on the backend instead of the jail. Empty skips the mount.
	GHConfig  string
	SSHConfig string
	// MountDockerSocket mounts the docker control socket (R8 item 34 + R9.1).
	// This is an explicit opt-in: write access to the socket is root on the
	// host, and it requires a DeniedMount exception in the launcher.
	MountDockerSocket bool
	// DockerSocketPath is the host socket; defaults to /var/run/docker.sock.
	DockerSocketPath string
	// MemoryNativeBin is the managed ai-memory binary to mount read-only and
	// export as AI_MEMORY_NATIVE_BIN (R5 item 20).
	MemoryNativeBin string
	// Env are extra KEY=VALUE entries passed via -e (memory server URL,
	// auth token, ...). Loopback URLs are rewritten to host.docker.internal
	// unless the value already references it (R5 item 21, R7 item 33).
	Env []string
	// Interactive selects -it (TTY) vs -i (stdin only). The caller decides
	// from stdin/stdout TTY-ness so non-interactive invocations (muse
	// --version, CI) stay clean.
	Interactive bool
	// AgentExecutable is the argv[0] inside the container: either the
	// installed binary name ("claude") or the resolved path for host-mounted
	// agents (InstallHostBinary).
	AgentExecutable string
	// AgentArgs are the harness's native arguments appended after the
	// executable.
	AgentArgs []string
	// AddHostGateway emits --add-host=host.docker.internal:host-gateway,
	// required on Linux where Docker Desktop's built-in mapping does not
	// exist (R7 item 29). Defaults to true; the caller can disable it on
	// platforms that map natively.
	AddHostGateway bool
}

// imageName returns the docker image reference (the content-hashed tag).
func (c RunConfig) imageName() (string, error) {
	return ImageTag(c.Selection)
}

// hostGatewayArg is the Linux-only flag that makes host.docker.internal
// resolve to the host network gateway (R7 item 29).
const hostGatewayArg = "--add-host=host.docker.internal:host-gateway"

// BuildRunCommand returns the docker run argv for RunConfig, in a fixed
// order: flags, then mounts, then image, then the in-container command. It is
// pure — no I/O, no docker calls — so dry-run, table tests, and the Gherkin
// contract can all exercise it.
func BuildRunCommand(cfg RunConfig) ([]string, error) {
	if err := cfg.Selection.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.ProjectDir) == "" {
		return nil, fmt.Errorf("project directory is required")
	}
	if strings.TrimSpace(cfg.AgentExecutable) == "" {
		return nil, fmt.Errorf("agent executable is required")
	}

	image, err := cfg.imageName()
	if err != nil {
		return nil, err
	}

	argv := []string{"docker", "run", "--rm"}
	if cfg.Interactive {
		argv = append(argv, "-it")
	} else {
		argv = append(argv, "-i")
	}
	if cfg.UID > 0 || cfg.GID > 0 {
		argv = append(argv, "--user", fmt.Sprintf("%d:%d", cfg.UID, cfg.GID))
	}
	argv = append(argv, "-w", cfg.ProjectDir)

	// Same-path project mount (R2 item 6): rw, host path = container path.
	argv = append(argv, "-v", cfg.ProjectDir+":"+cfg.ProjectDir)

	// Credential/history mounts, read-only by default (R9.2), same-path.
	// Only paths that exist on the host are mounted — docker refuses a -v
	// source that does not exist (M5): a fresh machine with no ~/.codex yet
	// must not fail the run.
	for _, mount := range AgentMounts(cfg.HomeDir, cfg.AgentCommands, ExistsOnHost) {
		argv = append(argv, "-v", mountSpec(mount.HostPath, mount.ReadOnly))
	}
	if cfg.SSHConfig != "" && ExistsOnHost(cfg.SSHConfig) {
		argv = append(argv, "-v", cfg.SSHConfig+":"+cfg.SSHConfig+":ro")
	}
	if cfg.GHConfig != "" && ExistsOnHost(cfg.GHConfig) {
		argv = append(argv, "-v", cfg.GHConfig+":"+cfg.GHConfig+":ro")
	}
	if cfg.MountDockerSocket {
		socket := cfg.DockerSocketPath
		if socket == "" {
			socket = "/var/run/docker.sock"
		}
		argv = append(argv, "-v", socket+":"+socket)
	}
	if cfg.MemoryNativeBin != "" {
		argv = append(argv, "-v", cfg.MemoryNativeBin+":"+cfg.MemoryNativeBin+":ro")
	}

	// Environment: rewrite loopback URLs to the host gateway (R5/R7).
	for _, entry := range cfg.Env {
		key, value, hasEquals := strings.Cut(entry, "=")
		if !hasEquals || value == "" {
			continue
		}
		value, changed := RewriteLocalhost(value)
		_ = changed // the flag value is always emitted rewritten; callers that
		// need to report the rewrite do so via the dry-run preview.
		argv = append(argv, "-e", key+"="+value)
	}

	if cfg.AddHostGateway {
		argv = append(argv, hostGatewayArg)
	}

	argv = append(argv, image)
	argv = append(argv, cfg.AgentExecutable)
	argv = append(argv, cfg.AgentArgs...)
	return argv, nil
}

// mountSpec renders a -v spec: same-path with the requested mode. The
// container path equals the host path by design (same-path decision), so a
// config that stores absolute paths keeps working inside the container.
func mountSpec(hostPath string, readOnly bool) string {
	if readOnly {
		return hostPath + ":" + hostPath + ":ro"
	}
	return hostPath + ":" + hostPath
}
