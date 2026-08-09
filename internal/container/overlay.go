package container

import (
	"os"
	"path/filepath"
	"strings"
)

// OverlayFile describes one config file that must be re-mounted over its host
// original because its content was rewritten for the container. The container
// path equals the host path (same-path design); the rewritten copy lives in a
// launcher-owned temp directory and is cleaned up after the container exits.
type OverlayFile struct {
	// HostPath is the original file on the host (bind-mounted by the launch).
	HostPath string
	// RewrittenPath is the launcher-owned copy with loopback URLs replaced.
	RewrittenPath string
}

// PlanOverlay rewrites a config file for container use when it references
// loopback URLs, returning the overlay to mount over the original. It returns
// nil when the file has no loopback references or cannot be read (a missing
// or unreadable config is left to the original mount, which docker already
// skips when the source does not exist).
//
// This is R7 item 31: the host file is never modified — the rewritten copy is
// mounted over it (file-shadows-dir) only inside the container, so the host
// keeps its localhost URLs for host-side runs while the container reaches the
// host gateway. When RewriteDisabled is set, no overlay is produced.
//
// rewrite is injectable so tests do not need a real regex engine contract;
// the production caller passes RewriteLocalhost.
func PlanOverlay(hostPath, tempDir string, rewrite func(string) (string, bool), disableRewrite bool) *OverlayFile {
	if disableRewrite {
		return nil
	}
	// #nosec G304 -- hostPath comes from overlayCandidates (fixed home-relative
	// config file names), never from user input.
	raw, err := os.ReadFile(hostPath)
	if err != nil {
		return nil
	}
	if !ContainsLoopbackURL(string(raw)) {
		return nil
	}
	rewritten := string(raw)
	changed := false
	if rewrite != nil {
		rewritten, changed = rewrite(rewritten)
	}
	if !changed {
		return nil
	}
	copyPath := filepath.Join(tempDir, "overlay", filepath.Base(hostPath)) // #nosec G703 -- tempDir is os.MkdirTemp, base name of a fixed config path
	if err := os.MkdirAll(filepath.Dir(copyPath), 0o700); err != nil {
		return nil
	}
	// The rewritten copy may carry tokens (MCP configs, claude.json); it is
	// launcher-owned and cleaned up after the container exits (R9.6).
	// #nosec G703 -- copyPath derives from os.MkdirTemp(tempDir) + a fixed
	// config basename; no user-controlled path reaches the write.
	if err := os.WriteFile(copyPath, []byte(rewritten), 0o600); err != nil {
		return nil
	}
	return &OverlayFile{HostPath: hostPath, RewrittenPath: copyPath}
}

// OverlayMountSpec renders the -v pair for an overlay: the rewritten copy is
// mounted read-only over the host path, shadowing the original file inside the
// container without touching it on the host.
func (o OverlayFile) OverlayMountSpec() string {
	return o.RewrittenPath + ":" + o.HostPath + ":ro"
}

// Cleanup removes the launcher-owned rewritten copy after the container exits.
func (o OverlayFile) Cleanup() {
	_ = os.Remove(o.RewrittenPath)
}

// OverlayCandidate reports whether path is a config file worth scanning for
// loopback URLs. It is deliberately narrow: whole-directory config trees (like
// ~/.config/opencode) are bind-mounted and scanned by the caller; this helper
// names the known standalone files that get per-file overlays.
func OverlayCandidate(path string) bool {
	base := filepath.Base(strings.TrimSpace(path))
	switch base {
	case ".claude.json", ".mcp.json", "opencode.json", "settings.json":
		return true
	}
	return strings.HasPrefix(base, ".mcp") || strings.HasSuffix(base, ".mcp.json")
}
