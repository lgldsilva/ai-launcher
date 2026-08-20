package launcher

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// aiMemoryCommand is the upstream memory CLI invoked as the wrapper layer of
// the composed argv and probed on PATH by the validator.
const aiMemoryCommand = config.AIMemoryCommand

// userHomeDir and isWindows abstract platform lookups used by the managed
// ai-memory native runner path.
var userHomeDir = os.UserHomeDir
var isWindows = func() bool { return runtime.GOOS == config.PlatformWindows }

// runtimeGOOS mirrors runtime.GOOS so tests can simulate macOS behavior.
var runtimeGOOS = func() string { return runtime.GOOS }

// ownedMemoryEnvKeys are the AI_MEMORY_* variables owned by the launcher.
var ownedMemoryEnvKeys = []string{
	"AI_MEMORY_SERVER_URL",
	"AI_MEMORY_AUTH_TOKEN",
	"AI_MEMORY_NATIVE_BIN",
}

// Environment returns the inherited environment with the configured ai-memory
// server URL and auth token applied to child processes. ai-memory reads these
// variables for its runtime wrapper and generated integrations. When the
// launcher-managed native binary exists, AI_MEMORY_NATIVE_BIN is exported too
// so the wrapper skips its own download. The token is a bearer secret: it is
// passed through the environment only and must never be written to logs.
//
// Owned AI_MEMORY_* keys are always removed first. When UseMemory is false the
// child sees none of them (even if the parent shell exported a stale token).
// When UseMemory is true, only the values this launch configures are re-added.
func Environment(cfg LaunchConfig) []string {
	env := append([]string(nil), os.Environ()...)
	for _, key := range ownedMemoryEnvKeys {
		env = upsertEnv(env, key, "")
	}
	// Some agents reach for host credential stores that are inaccessible inside
	// ai-jail (e.g. the macOS login keychain). Default them to a file-backed
	// store unless the operator already configured a different value.
	if cfg.UseJail {
		if key, value := AgentCredentialStoreEnv(cfg.Agent, runtimeGOOS()); key != "" {
			if !envHas(env, key) {
				env = upsertEnv(env, key, value)
			}
		}
	}
	if !cfg.UseMemory {
		return env
	}
	// An empty config value means the operator chose environment-based
	// configuration for the server URL only — restore the stripped inherited
	// value by reading it from the original process environment after a
	// non-empty config override is absent.
	if serverURL := strings.TrimSpace(cfg.MemoryServerURL); serverURL != "" {
		env = upsertEnv(env, "AI_MEMORY_SERVER_URL", serverURL)
	} else if inherited := strings.TrimSpace(os.Getenv("AI_MEMORY_SERVER_URL")); inherited != "" {
		env = upsertEnv(env, "AI_MEMORY_SERVER_URL", inherited)
	}
	env = upsertEnv(env, "AI_MEMORY_AUTH_TOKEN", strings.TrimSpace(cfg.MemoryAuthToken))
	// The ai-memory wrapper skips its own download/refresh logic (fragile
	// inside ai-jail) when AI_MEMORY_NATIVE_BIN points at an executable
	// native binary managed by the launcher's installer. The executable
	// bit is required: a regular file without it would make the wrapper
	// exec a non-runnable path. Windows has no exec bit, so the regular
	// file check is enough there. The check is best-effort — the managed
	// directory is user-writable, so the file can change before the child
	// execs it (accepted TOCTOU; the wrapper fails visibly, not silently).
	if native := managedNativeRunnerPath(cfg.HomeDir); native != "" {
		if info, err := os.Stat(native); err == nil && info.Mode().IsRegular() &&
			(isWindows() || info.Mode().Perm()&0o111 != 0) {
			env = upsertEnv(env, "AI_MEMORY_NATIVE_BIN", native)
		}
	}
	return env
}

// JailEnvPassthrough returns the environment variable names the sandbox must
// receive, in a stable order and filtered to those actually present in env.
//
// ai-jail 1.18 replaced full environment inheritance with a minimal allowlist
// (PATH, HOME, TERM, LANG, SHELL, TMPDIR, COLORTERM, the SSL_CERT_* and proxy
// variables, plus the LC_*, XDG_* and TERM_PROGRAM families). Everything else
// is dropped unless named. For this launcher that silently took out the three
// AI_MEMORY_* variables it owns — invariants 7 and 8 are implemented *through*
// the environment — along with the agent credential-store override and every
// vendor API key.
//
// The list is derived from ownedMemoryEnvKeys and the same catalog the rest of
// the launch reads, rather than being written out a second time, because two
// hand-maintained lists diverge on the first variable somebody adds.
//
// Filtering to what exists keeps --dry-run readable: ai-jail ignores a name
// that is absent from its own environment, but an argv listing thirty
// variables nobody set is noise in the one place an operator reads the
// composed command.
func JailEnvPassthrough(cfg LaunchConfig, env []string) []string {
	names := make([]string, 0, len(ownedMemoryEnvKeys)+len(cfg.Agent.EnvPassthrough)+1)
	if cfg.UseMemory {
		names = append(names, ownedMemoryEnvKeys...)
	}
	if key, _ := AgentCredentialStoreEnv(cfg.Agent, runtimeGOOS()); key != "" {
		names = append(names, key)
	}
	names = append(names, cfg.Agent.EnvPassthrough...)

	seen := make(map[string]struct{}, len(names))
	present := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		if envHas(env, name) {
			present = append(present, name)
		}
	}
	return present
}

// envHas reports whether env already contains a value for key.
func envHas(env []string, key string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

// managedNativeRunnerPath resolves the launcher's managed install location of
// the ai-memory native binary, honoring the same home-dir convention as the
// installer (~/.local/share/ai-launcher/bin, with .exe on Windows). It returns
// "" when no home directory is available.
func managedNativeRunnerPath(home string) string {
	home = strings.TrimSpace(home)
	if home == "" {
		resolved, err := userHomeDir()
		if err != nil {
			return ""
		}
		home = resolved
	}
	path := filepath.Join(home, ".local", "share", config.LauncherName, "bin", aiMemoryCommand)
	if isWindows() {
		path += ".exe"
	}
	return path
}

// upsertEnv replaces or appends a KEY=value entry, and removes the key when
// value is empty. Removal matters: the child inherits the parent environment,
// so returning early on an empty value forwarded whatever the parent happened
// to have — a stale AI_MEMORY_AUTH_TOKEN or an AI_MEMORY_SERVER_URL from
// direnv would silently point the sandboxed agent at another server.
// "Not configured" has to mean "not set".
func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	if value == "" {
		return filtered
	}
	return append(filtered, prefix+value)
}
