package container

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// RunConfig carries everything the container run argv builder needs beyond the
// image selection: the host paths to share, the agent to invoke, and the
// launch flags that become docker flags. It mirrors the fields of
// launcher.LaunchConfig that matter for container mode, so the launcher can
// fill it without importing this package back.
type RunConfig struct {
	// Runtime is the selected container CLI. A nil value preserves Docker as
	// the compatibility default for callers that construct RunConfig directly.
	Runtime Runtime
	// Context selects a Docker context for this command. Empty uses Docker's
	// current context; other runtimes reject a non-empty value.
	Context string
	// Selection is the image to run (its tag is the image reference).
	Selection Selection
	// HomeDir is the host $HOME, used to resolve credential mounts and the
	// UID/GID user mapping. Empty means mounts are skipped.
	HomeDir string
	// HostGOOS overrides platform resolution for deterministic contract tests;
	// production callers leave it empty so the host platform is detected.
	HostGOOS string
	// UID and GID are the host user ids mapped into the container (R9.5):
	// same-path mounts of ~/.ssh and ~/.gitconfig are only usable when the
	// container runs as the host user.
	UID int
	GID int
	// ProjectDir is the current working directory, mounted read-write at the
	// identical path (same-path design) and set as the container WORKDIR.
	ProjectDir string
	// WorktreeMounts are the existing Git worktree roots discovered by the
	// launcher when the worktree permission is enabled. They are mounted
	// read-write at the same path so an agent can inspect or edit sibling
	// worktrees, including worktrees outside the current project directory.
	WorktreeMounts []string
	// AgentCommands are the agent commands whose credential/history mounts
	// are shared (claude, codex, opencode, muse, ...). A mount is skipped
	// when its host path does not exist.
	AgentCommands []string
	// UseMemory runs the selected harness through the ai-memory wrapper inside
	// the image. The image must therefore contain ai-memory; this is separate
	// from the host-side launcher wrapper used by the jail backend.
	UseMemory bool
	// MemoryHarness is the token accepted by `ai-memory run`. It can differ
	// from AgentExecutable for local wrappers such as oc -> opencode.
	MemoryHarness string
	// MemoryExecutable is an optional in-container executable override for
	// host-binary agents passed through ai-memory's --executable flag.
	MemoryExecutable string
	// GHConfig and SSHConfig are the permission-driven mounts (R8 item 34):
	// they mirror the jail permission→mount mapping but read-only and
	// conditional on the backend instead of the jail. Empty skips the mount.
	GHConfig  string
	SSHConfig string
	// MountDockerSocket mounts the docker control socket (R8 item 34 + R9.1).
	// This is an explicit opt-in: write access to the socket is root on the
	// host, and it requires a DeniedMount exception in the launcher.
	MountDockerSocket bool
	// DockerSocketPath is an explicit host socket override. Empty uses the
	// selected runtime's SocketPath; runtimes without a socket skip the mount.
	DockerSocketPath string
	// DockerSocketGroupID is the host group that owns the Docker socket. When
	// set, the non-root container process receives it as a supplemental group.
	DockerSocketGroupID int
	// DockerSocketGroupSet distinguishes a real group ID of zero from the
	// zero-value RunConfig, where no socket group was resolved.
	DockerSocketGroupSet bool
	// MemoryNativeBin is a legacy optional host runner mount for callers that
	// explicitly provide one. Docker memory launches install and use the Linux
	// runner in the image instead, so the production path leaves this empty.
	MemoryNativeBin string
	// StackCacheMounts are the host toolchain/cache directories shared
	// read-write with the container for the selected stacks (nvm, sdkman,
	// cargo, go-build, npm, m2...). Same-path mounts so downloads are reused
	// on both sides. Resolved by the caller from the selection + home.
	StackCacheMounts []string
	// DependencyMounts are cross-platform host-to-container mounts resolved by
	// the dependency catalog. Unlike StackCacheMounts, their target can differ
	// from the host path (especially on Windows hosts).
	DependencyMounts []DependencyMount
	// DependencyEnv contains environment entries associated with resolved
	// dependency mounts, such as GOMODCACHE, CARGO_HOME and NVM_DIR.
	DependencyEnv []string
	// Overlays are rewritten config copies mounted over the originals inside
	// the container (MCP localhost→host.docker.internal). They must be
	// emitted as -v flags BEFORE the image argument, or docker treats them as
	// the agent's native args (C2).
	Overlays []OverlayFile
	// Env are extra KEY=VALUE entries passed via -e (memory server URL,
	// auth token, ...). Loopback URLs are rewritten to the selected runtime's
	// host gateway
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
	// HostBinaryMounts are the directories holding host binaries that have no
	// install recipe (InstallHostBinary agents). They are mounted read-only at
	// the same path so the in-container executable resolves (R3 item 13).
	HostBinaryMounts []string
	// AgentArgs are the harness's native arguments appended after the
	// executable.
	AgentArgs []string
	// AddHostGateway emits --add-host=<runtime gateway>:host-gateway,
	// required on Linux where Docker Desktop's built-in mapping does not
	// exist (R7 item 29). Defaults to true; the caller can disable it on
	// platforms that map natively.
	AddHostGateway bool
	// MemoryLimit is the optional Docker memory limit (for example, "4g").
	MemoryLimit string
	// CPULimit is the optional Docker CPU quota (for example, "2.0").
	CPULimit string
	// PIDsLimit is the optional maximum number of processes. Zero leaves the
	// Docker default unchanged.
	PIDsLimit int64
	// ExposedPorts are host-to-container port mappings. They are emitted as
	// repeated -p flags before the image.
	ExposedPorts []PortMapping
	// NetworkName selects an optional Docker network, such as "host" or a
	// project-specific bridge network.
	NetworkName string
	// NetworkInternal marks the generated Compose network internal, blocking
	// ALL egress including the agent's own LLM API calls. Compose-only: the
	// plain `docker run` backend (BuildRunCommand) never reads this field —
	// there is no `docker run` equivalent of a Compose-level `internal: true`
	// network attribute.
	NetworkInternal bool
	// Tmux starts the in-container agent in a named interactive tmux session.
	// The launcher enables it only for a real terminal; keeping the flag here
	// lets dry-run and Compose expose the same in-container command.
	Tmux bool
	// TmuxMounts are host tmux config files/directories mounted read-only at
	// their original paths. The list is resolved from trusted YAML settings.
	TmuxMounts []string
	// DockerCLI mirrors the DockerfileOptions.DockerCLI build toggle into the
	// run side: the image tag hashes the build options, so a launch granted
	// the docker permission must reference the tag built WITH the CLI layer.
	DockerCLI bool
}

// imageName returns the docker image reference (the content-hashed tag).
// The Dockerfile build options are part of the hash, so the run side must
// tag with the same options the build side used, or it would reference an
// image that was never built.
func (c RunConfig) imageName() (string, error) {
	return ImageTagWithOptions(c.Selection, DockerfileOptions{DockerCLI: c.DockerCLI})
}

// ValidateProjectDir rejects a project directory the container backend cannot
// mount. Absolute is not a formality: the argv builds "-v <dir>:<dir>", and
// Docker reads a relative source as a *named volume* with a relative
// destination, which it then refuses. The operator sees an opaque daemon error
// about an invalid mount instead of being told the directory is wrong, and the
// project is never mounted.
func ValidateProjectDir(dir string) error {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return fmt.Errorf("project directory is required")
	}
	if !filepath.IsAbs(trimmed) {
		return fmt.Errorf("project directory %q must be an absolute path", trimmed)
	}
	return nil
}

// BuildRunCommand returns the selected runtime's run argv for RunConfig, in a fixed
// order: flags, then mounts, then image, then the in-container command. It is
// pure — no I/O, no docker calls — so dry-run, table tests, and the Gherkin
// contract can all exercise it.
func BuildRunCommand(cfg RunConfig) ([]string, error) {
	if err := ValidateContext(cfg.Runtime, cfg.Context); err != nil {
		return nil, err
	}
	if err := ValidateRunResources(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Selection.Validate(); err != nil {
		return nil, err
	}
	if err := ValidateProjectDir(cfg.ProjectDir); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.AgentExecutable) == "" {
		return nil, fmt.Errorf("agent executable is required")
	}

	image, err := cfg.imageName()
	if err != nil {
		return nil, err
	}

	runtime := RuntimeOrDefault(cfg.Runtime)
	argv := append(CommandPrefix(runtime, cfg.Context), "run", "--rm")
	if cfg.Interactive {
		argv = append(argv, "-it")
	} else {
		argv = append(argv, "-i")
	}
	argv = appendResourceFlags(argv, cfg)
	// Same hardening baseline every Compose service carries, as docker run
	// flags: cap_drop ALL strips Docker's default capability set and
	// no-new-privileges blocks setuid escalation. The agent runs as a non-root
	// --user, so it needs no cap_add — it never starts as root, so it has no
	// privileges to drop. This closes the gap where only the Compose path was
	// hardened and a plain `docker run` launch (no services) kept every default
	// capability.
	argv = appendSecurityFlags(argv)
	if cfg.UID > 0 || cfg.GID > 0 {
		argv = append(argv, "--user", fmt.Sprintf("%d:%d", cfg.UID, cfg.GID))
	}
	if cfg.MountDockerSocket && cfg.DockerSocketGroupSet {
		argv = append(argv, "--group-add", fmt.Sprintf("%d", cfg.DockerSocketGroupID))
	}
	// HOME must be explicit: docker run does not inherit the parent HOME, and
	// with --user UID:GID (no passwd entry) the container defaults HOME to /
	// or /root. Every same-path credential/cache mount resolves under $HOME,
	// so without -e HOME the agent reads the wrong location and the mounts
	// are unreachable (C1).
	if strings.TrimSpace(cfg.HomeDir) != "" {
		argv = append(argv, "-e", "HOME="+cfg.HomeDir)
	}
	argv = append(argv, "-w", cfg.ProjectDir)

	// Same-path project mount (R2 item 6): rw, host path = container path.
	argv = append(argv, "-v", cfg.ProjectDir+":"+cfg.ProjectDir)

	argv = append(argv, runMountSpecs(cfg, runtime)...)

	// Environment: rewrite loopback URLs to the host gateway (R5/R7).
	argv = appendRunEnvironment(argv, cfg, runtime)

	if cfg.AddHostGateway {
		argv = append(argv, "--add-host="+runtime.HostGateway()+":host-gateway")
	}

	// Overlay mounts (rewritten MCP configs) shadow the originals: they must
	// precede the image argument so docker parses them as mount flags, not as
	// the agent's native arguments (C2).
	for _, overlay := range cfg.Overlays {
		argv = append(argv, "-v", overlay.OverlayMountSpec())
	}

	argv = append(argv, image)
	argv = append(argv, cfg.InContainerCommand()...)
	return argv, nil
}

// InContainerCommand returns the command that follows the image reference.
// Memory-enabled launches use the wrapper installed in the image and pass
// native arguments after its harness token; plain launches invoke the harness
// directly.
func (c RunConfig) InContainerCommand() []string {
	var command []string
	if !c.UseMemory {
		command = append([]string{c.AgentExecutable}, c.AgentArgs...)
	} else {
		harness := strings.TrimSpace(c.MemoryHarness)
		if harness == "" {
			harness = c.AgentExecutable
		}
		// --executable before the harness, matching the host chain. The
		// container has no ai-jail parsing this argv, so nothing here depends
		// on the order — but one command should have one spelling, or the two
		// drift and the next person has to discover which is authoritative.
		command = []string{"ai-memory", "run"}
		if executable := strings.TrimSpace(c.MemoryExecutable); executable != "" {
			command = append(command, "--executable", executable)
		}
		command = append(command, harness)
		command = append(command, c.AgentArgs...)
	}
	if c.Tmux {
		return TmuxCommand(command)
	}
	return command
}

func runMountSpecs(cfg RunConfig, runtime Runtime) []string {
	var specs []string
	for _, mount := range AgentMounts(cfg.HomeDir, cfg.AgentCommands, ExistsOnHost) {
		// Shared-login model: every agent config dir is read-write so a login
		// made inside a container persists to the host and other containers.
		specs = append(specs, "-v", mountSpec(mount.HostPath, false))
	}
	for _, path := range []string{cfg.SSHConfig, cfg.GHConfig} {
		if path != "" && ExistsOnHost(path) {
			specs = append(specs, "-v", path+":"+path+":ro")
		}
	}
	specs = append(specs, worktreeMountSpecs(cfg)...)
	if cfg.MountDockerSocket {
		socket := cfg.DockerSocketPath
		if socket == "" {
			socket = runtime.SocketPath()
		}
		if socket != "" {
			specs = append(specs, "-v", socket+":"+socket)
		}
	}
	if cfg.MemoryNativeBin != "" {
		specs = append(specs, "-v", cfg.MemoryNativeBin+":"+cfg.MemoryNativeBin+":ro")
	}
	specs = append(specs, hostBinaryMountSpecs(cfg.HostBinaryMounts)...)
	specs = append(specs, stackCacheMountSpecs(cfg.StackCacheMounts)...)
	for _, path := range cfg.TmuxMounts {
		if strings.TrimSpace(path) != "" && ExistsOnHost(path) {
			specs = append(specs, "-v", mountSpec(path, true))
		}
	}
	for _, mount := range cfg.DependencyMounts {
		if strings.TrimSpace(mount.HostPath) == "" || strings.TrimSpace(mount.ContainerPath) == "" {
			continue
		}
		mode := strings.TrimSpace(mount.Mode)
		if mode == "" {
			mode = config.MountReadWrite
		}
		specs = append(specs, "-v", mount.HostPath+":"+mount.ContainerPath+":"+mode)
	}
	return specs
}

func worktreeMountSpecs(cfg RunConfig) []string {
	paths := worktreeMountPaths(cfg)
	specs := make([]string, 0, len(paths)*2)
	for _, path := range paths {
		if !ExistsOnHost(path) {
			continue
		}
		specs = append(specs, "-v", path+":"+path)
	}
	return specs
}

// worktreeMountPaths removes duplicate or nested roots. The current project
// mount is already emitted separately, so a worktree beneath that directory
// would only create a duplicate bind mount. A broader registered worktree is
// retained because it can contain Git metadata or sibling worktrees needed by
// the agent.
func worktreeMountPaths(cfg RunConfig) []string {
	paths := append([]string(nil), cfg.WorktreeMounts...)
	sort.Slice(paths, func(i, j int) bool {
		left, right := filepath.Clean(strings.TrimSpace(paths[i])), filepath.Clean(strings.TrimSpace(paths[j]))
		if len(left) != len(right) {
			return len(left) < len(right)
		}
		return left < right
	})
	roots := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" || pathCoveredBy(path, cfg.ProjectDir) {
			continue
		}
		covered := false
		for _, root := range roots {
			if pathCoveredBy(path, root) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		roots = append(roots, path)
	}
	return roots
}

func pathCoveredBy(path, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if path == "" || root == "" || root == "." {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func appendRunEnvironment(argv []string, cfg RunConfig, runtime Runtime) []string {
	for _, entry := range cfg.DependencyEnv {
		if key, value, ok := strings.Cut(entry, "="); ok && strings.TrimSpace(key) != "" {
			argv = append(argv, "-e", key+"="+value)
		}
	}
	for _, entry := range cfg.Env {
		key, value, hasEquals := strings.Cut(entry, "=")
		if !hasEquals || value == "" {
			continue
		}
		if !cfg.AddHostGateway {
			argv = append(argv, "-e", key+"="+value)
			continue
		}
		value, changed := RewriteLocalhost(value, runtime.HostGateway())
		_ = changed // the flag value is always emitted rewritten; callers that
		// need to report the rewrite do so via the dry-run preview.
		argv = append(argv, "-e", key+"="+value)
	}
	return argv
}

func appendResourceFlags(argv []string, cfg RunConfig) []string {
	if value := strings.TrimSpace(cfg.MemoryLimit); value != "" {
		argv = append(argv, "--memory", value)
	}
	if value := strings.TrimSpace(cfg.CPULimit); value != "" {
		argv = append(argv, "--cpus", value)
	}
	if cfg.PIDsLimit > 0 {
		argv = append(argv, "--pids-limit", fmt.Sprintf("%d", cfg.PIDsLimit))
	}
	for _, mapping := range cfg.ExposedPorts {
		argv = append(argv, "-p", mapping.DockerFlag())
	}
	if value := strings.TrimSpace(cfg.NetworkName); value != "" {
		argv = append(argv, "--network", value)
	}
	return argv
}

// appendSecurityFlags applies the hardening baseline every Compose service also
// gets, as docker run flags. cap_drop ALL removes Docker's default Linux
// capability set and security_opt no-new-privileges:true blocks setuid-based
// privilege escalation. The agent runs as a non-root --user, so unlike the
// egress proxy and catalog services (which start as root and drop) it needs no
// cap_add here. See hardenedServiceSecurity for the Compose-side twin.
func appendSecurityFlags(argv []string) []string {
	return append(argv,
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges:true",
	)
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

// hostBinaryMountSpecs renders the -v pair for each unique, existing host
// binary directory: read-only at the same path (R3 item 13).
func hostBinaryMountSpecs(dirs []string) []string {
	var specs []string
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || !ExistsOnHost(dir) {
			continue
		}
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		specs = append(specs, "-v", dir+":"+dir+":ro")
	}
	return specs
}

// stackCacheMountSpecs renders the -v pair for each unique, existing toolchain
// cache dir: read-write at the same path so downloads are shared with the
// host (nvm, sdkman, cargo, go-build, m2, gradle, pip...).
func stackCacheMountSpecs(dirs []string) []string {
	var specs []string
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || !ExistsOnHost(dir) {
			continue
		}
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		specs = append(specs, "-v", dir+":"+dir)
	}
	return specs
}
