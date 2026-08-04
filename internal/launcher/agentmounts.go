package launcher

import (
	"os"
	"path/filepath"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// AgentRequiredMounts returns read-write mounts an agent needs to function
// inside ai-jail that are not covered by the generic home-symlink auto-detection.
// These are agent-specific state directories (e.g. Cursor's ~/.cursor). The
// launcher creates missing directories so a first run inside the jail does not
// fail with EPERM, and skips paths that cannot be created.
func AgentRequiredMounts(agent config.Agent, home string, goos string) []config.Mount {
	if goos == "windows" {
		return nil
	}
	dirs := agentStateDirs(agent, home)
	mounts := make([]config.Mount, 0, len(dirs))
	for _, d := range dirs {
		if d == "" {
			continue
		}
		// Resolve tilde to the supplied home so tests can use a temp dir.
		if d == "~" || len(d) > 0 && d[0] == '~' && (len(d) == 1 || d[1] == filepath.Separator) {
			d = filepath.Join(home, d[1:])
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		if err := ensureDir(abs); err != nil {
			continue
		}
		mounts = append(mounts, config.Mount{Path: abs, Mode: config.MountReadWrite})
	}
	return mounts
}

// agentStateDirs maps an agent to the list of host directories it uses for
// configuration, credentials, session history and caches. Relative paths are
// interpreted against $HOME; when a well-known environment variable overrides
// the default location, it is read from the current process environment.
func agentStateDirs(agent config.Agent, home string) []string {
	switch agent.Command {
	case "claude":
		return []string{envOrHome(home, "CLAUDE_CONFIG_DIR", ".claude")}
	case "codex":
		return []string{envOrHome(home, "CODEX_HOME", ".codex")}
	case "opencode", "oc":
		return xdgDirs(home, "opencode")
	case "kimi":
		return []string{envOrHome(home, "KIMI_CODE_HOME", ".kimi-code")}
	case "kilo":
		return []string{
			envOrHome(home, "XDG_CONFIG_HOME", filepath.Join(".config", "kilo")),
			filepath.Join(home, ".kilo"),
		}
	case "mimo":
		if root := os.Getenv("MIMOCODE_HOME"); root != "" {
			return []string{root}
		}
		return xdgDirs(home, "mimocode")
	case "agy":
		return []string{
			filepath.Join(home, ".gemini", "antigravity-cli"),
			filepath.Join(home, ".antigravity"),
		}
	case "pi", "omp":
		if agent.Command == "omp" {
			return []string{envOrHome(home, "PI_CODING_AGENT_DIR", filepath.Join(".omp", "agent"))}
		}
		return []string{envOrHome(home, "PI_CODING_AGENT_DIR", filepath.Join(".pi", "agent"))}
	case "cursor-agent":
		return []string{filepath.Join(home, ".cursor")}
	case "grok":
		return []string{envOrHome(home, "GROK_HOME", ".grok")}
	case "devin":
		return []string{xdgConfigDir(home, "devin")}
	case "gemini":
		return []string{envOrHome(home, "GEMINI_CLI_HOME", ".gemini")}
	case "qwen":
		return []string{envOrHome(home, "QWEN_HOME", ".qwen")}
	case "aider":
		return []string{filepath.Join(home, ".aider")}
	case "goose":
		return []string{
			xdgConfigDir(home, "goose"),
			xdgDataDir(home, "goose"),
		}
	case "kiro-cli":
		return []string{envOrHome(home, "KIRO_HOME", ".kiro")}
	case "openclaw":
		if root := os.Getenv("OPENCLAW_STATE_DIR"); root != "" {
			return []string{root}
		}
		return []string{filepath.Join(home, ".openclaw")}
	case "hermes":
		return []string{envOrHome(home, "HERMES_HOME", ".hermes")}
	case "cline":
		if root := os.Getenv("CLINE_DATA_DIR"); root != "" {
			return []string{root}
		}
		return []string{filepath.Join(home, ".cline", "data")}
	default:
		return nil
	}
}

// ensureDir creates dir if it does not exist, preserving an existing directory.
func ensureDir(dir string) error {
	info, err := os.Stat(dir)
	if err == nil && info.IsDir() {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(dir, 0o750)
}

// envOrHome returns value of key if it is set and non-empty, otherwise
// home/subPath. The caller supplies home so tests stay isolated.
func envOrHome(home, key, subPath string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if filepath.IsAbs(subPath) {
		return subPath
	}
	return filepath.Join(home, subPath)
}

// xdgDirs returns the XDG config/data/state/cache directories for appName.
// When the corresponding XDG_* variable is set, it is used as the base and
// appName is appended underneath it.
func xdgDirs(home, appName string) []string {
	return []string{
		xdgConfigDir(home, appName),
		xdgDataDir(home, appName),
		xdgStateDir(home, appName),
		xdgCacheDir(home, appName),
	}
}

func xdgConfigDir(home, appName string) string {
	return xdgDir(home, "XDG_CONFIG_HOME", filepath.Join(".config", appName), appName)
}

func xdgDataDir(home, appName string) string {
	return xdgDir(home, "XDG_DATA_HOME", filepath.Join(".local", "share", appName), appName)
}

func xdgStateDir(home, appName string) string {
	return xdgDir(home, "XDG_STATE_HOME", filepath.Join(".local", "state", appName), appName)
}

func xdgCacheDir(home, appName string) string {
	return xdgDir(home, "XDG_CACHE_HOME", filepath.Join(".cache", appName), appName)
}

func xdgDir(home, envKey, defaultSubPath, appName string) string {
	if v := os.Getenv(envKey); v != "" {
		return filepath.Join(v, appName)
	}
	return filepath.Join(home, defaultSubPath)
}

// AgentCredentialStoreEnv returns an environment override an agent needs to
// avoid host credentials that are inaccessible inside the jail. When the
// returned key is non-empty and the operator has not set it explicitly, the
// launcher exports it with the returned value.
func AgentCredentialStoreEnv(agent config.Agent, goos string) (key, value string) {
	if goos != "darwin" {
		return "", ""
	}
	if agent.Command == "cursor-agent" {
		return "AGENT_CLI_CREDENTIAL_STORE", "file"
	}
	return "", ""
}
