package container

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

const tmuxSessionName = "ai-launcher"

// ResolveTmuxMounts returns the small, read-only set of host tmux files that
// should be visible to an interactive container. Explicit settings win; when
// omitted, the conventional tmux and oh-my-tmux locations are tried. The
// result contains host paths because the Docker backend deliberately mounts
// them at the same path inside the container, preserving ~ and include lines.
func ResolveTmuxMounts(home string, settings config.TmuxSettings, exists func(string) bool) []string {
	if strings.TrimSpace(home) == "" || !settings.Enabled {
		return nil
	}
	if exists == nil {
		exists = ExistsOnHost
	}

	paths := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	add := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" || !exists(path) {
			return
		}
		if _, duplicate := seen[path]; duplicate {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	add(firstExistingTmuxPath(home, settings.Config, []string{
		"~/.tmux.conf",
		"$XDG_CONFIG_HOME/tmux/tmux.conf",
		"~/.config/tmux/tmux.conf",
	}, exists))
	add(firstExistingTmuxPath(home, settings.LocalConfig, []string{
		"~/.tmux.conf.local",
		"$XDG_CONFIG_HOME/tmux/tmux.conf.local",
		"~/.config/tmux/tmux.conf.local",
	}, exists))
	add(firstExistingTmuxPath(home, settings.OhMyTmuxDir, []string{
		"~/.tmux",
		"$XDG_CONFIG_HOME/oh-my-tmux",
		"~/.config/oh-my-tmux",
	}, exists))
	for _, path := range settings.AdditionalPaths {
		add(ExpandTmuxPath(path, home))
	}
	if len(paths) == 0 {
		return nil
	}
	return paths
}

func firstExistingTmuxPath(home, configured string, defaults []string, exists func(string) bool) string {
	if strings.TrimSpace(configured) != "" {
		return ExpandTmuxPath(configured, home)
	}
	for _, candidate := range defaults {
		path := ExpandTmuxPath(candidate, home)
		if exists(path) {
			return path
		}
	}
	return ""
}

// ExpandTmuxPath expands the supported home and XDG placeholders without
// invoking a shell. It intentionally leaves unrelated environment variables
// untouched so a YAML path cannot turn into an opaque command substitution.
func ExpandTmuxPath(raw, home string) string {
	raw = strings.TrimSpace(raw)
	if raw == "$XDG_CONFIG_HOME" || strings.HasPrefix(raw, "$XDG_CONFIG_HOME/") {
		xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		if raw == "$XDG_CONFIG_HOME" {
			return filepath.Clean(xdg)
		}
		return filepath.Clean(filepath.Join(xdg, strings.TrimPrefix(raw, "$XDG_CONFIG_HOME/")))
	}
	return ExpandHome(raw, home)
}

// TmuxCommand wraps an agent command in a named interactive session. The
// first window is the agent itself; users can create additional windows and
// panes with their normal host key bindings after the container starts.
func TmuxCommand(command []string) []string {
	if len(command) == 0 {
		return nil
	}
	wrapped := []string{"tmux", "new-session", "-A", "-s", tmuxSessionName}
	return append(wrapped, command...)
}
