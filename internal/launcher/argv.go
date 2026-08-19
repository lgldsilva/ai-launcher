package launcher

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// buildContinue composes "ai-memory run" without a harness, which auto-resumes
// the most recent session of the checkout, jail-wrapped when the jail is on.
// Every wrapper flag `ai-memory run` accepts without a harness is emitted:
// only the harness token and its native arguments are dropped, and the
// validator reports that so nothing disappears silently.
func buildContinue(cfg LaunchConfig) ([]string, error) {
	if !cfg.UseMemory {
		return nil, errors.New("continuing a session requires ai-memory")
	}
	resolve := newPathResolver()
	command := make([]string, 0, 10)
	if cfg.UseJail {
		command = appendJailArgs(command, cfg, resolve)
	}
	command = append(command, memoryCommand(cfg, resolve), "run")
	command = appendMemoryScope(command, cfg)
	command = appendWorkstreamSelection(command, cfg)
	return appendYoloFlag(command, cfg), nil
}

// appendWorkstreamSelection emits the workstream flags in ai-memory's own
// precedence: creating a new one wins over resuming a named one.
func appendWorkstreamSelection(command []string, cfg LaunchConfig) []string {
	if cfg.NewWorkstream != "" {
		return append(command, "--new", cfg.NewWorkstream)
	}
	if cfg.Workstream != "" {
		return append(command, "--workstream", cfg.Workstream)
	}
	return command
}

// appendMemoryScope appends the ai-memory run scoping wrapper flags.
func appendMemoryScope(command []string, cfg LaunchConfig) []string {
	if workspace := strings.TrimSpace(cfg.Workspace); workspace != "" {
		command = append(command, "--workspace", workspace)
	}
	if project := strings.TrimSpace(cfg.Project); project != "" {
		command = append(command, "--project", project)
	}
	return command
}

// appendDeclaredParams appends the catalog-declared harness flags whose names
// have a value in cfg.ParamValues, in declaration order. Undeclared names are
// ignored here and reported by the validator instead.
func appendDeclaredParams(command []string, cfg LaunchConfig) []string {
	for _, param := range cfg.Agent.Params {
		value, ok := cfg.ParamValues[param.Name]
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if !param.TakesValue {
			if flagEnabled(value) {
				command = append(command, param.Flag)
			}
			continue
		}
		command = append(command, param.Flag, value)
	}
	return command
}

// flagEnabled reports whether a param value enables a boolean-style flag
// (any non-empty value except the usual negative spellings).
func flagEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "false", "0", "no", "off":
		return false
	}
	return true
}

// appendYoloFlag appends the resolved dangerous-mode flag. On the host,
// ai-memory owns the wrapper invocation and receives its generic --yolo flag;
// the wrapper translates it for the selected harness. In the Docker backend
// ai-memory is installed and invoked inside the container, so it receives the
// generic wrapper flag and translates it for the selected harness.
// A flag already present in extra_args is never duplicated. Agents that do not
// declare yolo support keep the flag off regardless of the launch option.
func appendYoloFlag(command []string, cfg LaunchConfig) []string {
	if !cfg.Yolo || !cfg.Agent.SupportsYolo {
		return command
	}
	flag := cfg.Agent.YoloFlag
	if cfg.UseMemory || strings.TrimSpace(flag) == "" {
		flag = "--yolo"
	}
	// Some agents (e.g. Devin) declare a yolo flag that is passed as two
	// separate argv entries such as "--permission-mode dangerous". Split on
	// whitespace so the harness receives them as distinct arguments.
	flagParts := strings.Fields(flag)
	if len(flagParts) == 0 {
		flagParts = []string{"--yolo"}
	}
	for _, arg := range cfg.ExtraArgs {
		if arg == flagParts[0] {
			return command
		}
	}
	return append(command, flagParts...)
}

// appendJailArgs prepends the ai-jail wrapper with the programmatic-mode
// flag, the declarative jail capability flags, and the flags derived from the
// enabled permissions and configured mounts.
func appendJailArgs(command []string, cfg LaunchConfig, resolve *pathResolver) []string {
	command = append(command, config.AIJailCommand)
	if cfg.JailExec {
		// --exec is ai-jail's programmatic mode: direct exec without the PTY
		// proxy or status bar. Interactive TUI launches leave the ai-jail
		// defaults in charge of the terminal instead.
		command = append(command, "--exec")
	}
	command = appendDockerDecision(command, cfg)
	command = appendJailFlags(command, cfg.JailFlags)
	command = appendPermissionArgs(command, cfg)
	if cfg.UseMemory {
		command = appendHostDirMount(command, cfg.MemoryExecutable, cfg.Mounts, resolve)
	}
	command = appendExecutableMount(command, cfg, resolve)
	for _, mount := range cfg.Mounts {
		path := strings.TrimSpace(mount.Path)
		if path == "" {
			continue
		}
		if strings.EqualFold(mount.Mode, config.MountReadOnly) || strings.EqualFold(mount.Mode, config.MountReadOnlyLabel) {
			command = append(command, "--map", path)
		} else {
			command = append(command, "--rw-map", path)
		}
	}
	return command
}

// appendExecutableMount maps the directory holding the resolved harness binary
// read-only, unless a configured mount already covers it. `--executable` names
// a host path that has to exist *inside* the sandbox; nothing mounted it, so
// the argv pointed at a binary the agent could not reach and operators worked
// around it by hand (the .ai-jail in this repo carries /opt/homebrew for
// exactly that reason). Read-only is enough: the agent runs it, never writes it.
// memoryCommand is the argv token for the wrapper. Inside the jail it must
// be the resolved host path so --map and exec agree; without a jail the
// contract stays the bare PATH name.
func memoryCommand(cfg LaunchConfig, resolve *pathResolver) string {
	if cfg.UseJail {
		if resolved := resolve.hostPath(cfg.MemoryExecutable); resolved != "" {
			return resolved
		}
	}
	return aiMemoryCommand
}

// ResolveHostBinaries follows harness and ai-memory symlinks so jail --map
// points at the real directories. CLI finalize and the TUI call this before
// Build so the builder stays I/O-free. Memory is resolved only when both
// jail and memory are on, so a later TUI toggle still fills MemoryExecutable.
func ResolveHostBinaries(cfg LaunchConfig) LaunchConfig {
	if path := strings.TrimSpace(cfg.Executable); path != "" {
		if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != "" {
			cfg.Executable = resolved
		}
	}
	if cfg.UseJail && strings.TrimSpace(cfg.JailVersion) == "" {
		cfg.JailVersion = DetectJailVersion()
	}
	if !cfg.UseJail || !cfg.UseMemory || strings.TrimSpace(cfg.MemoryExecutable) != "" {
		return cfg
	}
	path, err := exec.LookPath(aiMemoryCommand)
	if err != nil {
		return cfg
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != "" {
		path = resolved
	}
	cfg.MemoryExecutable = path
	return cfg
}

// pathResolver memoizes symlink resolution for one Build call. The same host
// paths (harness executable, ai-memory binary) are consulted several times
// while composing the argv; without the cache each consult re-walked every
// symlink chain with lstat syscalls. The cache is per-call on purpose: a TUI
// session may outlive a mount change, so nothing is shared across Builds.
type pathResolver struct {
	resolved map[string]string
}

func newPathResolver() *pathResolver {
	return &pathResolver{resolved: make(map[string]string, 4)}
}

func (r *pathResolver) hostPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if cached, ok := r.resolved[path]; ok {
		return cached
	}
	resolved := path
	if evaluated, err := filepath.EvalSymlinks(path); err == nil && evaluated != "" {
		resolved = evaluated
	}
	r.resolved[path] = resolved
	return resolved
}

func appendExecutableMount(command []string, cfg LaunchConfig, resolve *pathResolver) []string {
	return appendHostDirMount(command, cfg.Executable, cfg.Mounts, resolve)
}

func appendHostDirMount(command []string, path string, mounts []config.Mount, resolve *pathResolver) []string {
	path = resolve.hostPath(path)
	if path == "" || !filepath.IsAbs(path) {
		return command
	}
	dir := filepath.Dir(path)
	if dir == "" || dir == string(filepath.Separator) {
		return command
	}
	if coveredByMounts(dir, mounts) {
		return command
	}
	for i := 0; i < len(command)-1; i++ {
		if command[i] == "--map" && command[i+1] == dir {
			return command
		}
	}
	return append(command, "--map", dir)
}

// appendDockerDecision emits exactly one of --docker / --no-docker, always.
//
// Every other capability the launcher touches may be left unset, which puts
// ai-jail in auto mode: enabled when the resource exists on the host. For the
// Docker socket that default was a sandbox escape. Through ai-jail v1.15.x an
// existing /var/run/docker.sock was bind-mounted read-write with no flag and
// no warning, and write access to that socket is root on the host — the agent
// asks the daemon to mount / into a container and walks past bubblewrap,
// Landlock, seccomp, the tmpfs $HOME and every --mask in one command
// (ai-jail issue #88). v1.16.0 flipped the default to opt-in.
//
// So the launcher stops leaving it to the default. Being explicit costs one
// argv token, says the same thing to 1.15.x and 1.16.x, and makes the README's
// "docker off by default" a property of the command this launcher builds
// rather than a property of whichever ai-jail happens to be installed.
//
// Precedence matches every other capability: an explicit jail_flags.docker is
// the operator's declarative choice and wins, then the permission toggle, then
// the safe default.
func appendDockerDecision(command []string, cfg LaunchConfig) []string {
	if declared := cfg.JailFlags.Docker; declared != nil {
		if *declared {
			return append(command, "--docker")
		}
		return append(command, "--no-docker")
	}
	if cfg.Permissions[config.PermissionDocker] {
		return append(command, "--docker")
	}
	return append(command, "--no-docker")
}
