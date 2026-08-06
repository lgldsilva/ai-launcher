package container

import (
	"regexp"
	"strings"
)

// localhostHost is the host-gateway name docker resolves to the host machine
// on every platform: Docker Desktop (macOS/Windows) maps it natively, and on
// Linux the builder emits --add-host=host.docker.internal:host-gateway (R7
// item 29). Rewriting localhost URLs to this name is what lets MCP servers
// and the ai-memory server reach services listening on the host.
const localhostHost = "host.docker.internal"

// localhostPattern matches the URL authority forms a service config uses for
// "this machine": localhost with optional port, or the literal 127.0.0.1
// loopback with optional port. IPv6 [::1] is deliberately not rewritten: it
// is vanishingly rare in MCP configs and the rewrite must stay conservative.
var localhostPattern = regexp.MustCompile(`(?i)(://)(localhost|127\.0\.0\.1)(:\d+)?`)

// RewriteLocalhost replaces localhost / 127.0.0.1 URL authorities with
// host.docker.internal so a service reachable on the host stays reachable
// from inside the container. It returns (rewritten, changed).
//
// This is the single rewrite rule backing R5 item 21 (AI_MEMORY_SERVER_URL)
// and R7 item 31/33 (MCP server URLs). Callers decide whether to apply it to
// environment values in place or to overlay copies of mounted config files —
// the rule itself never touches the filesystem.
func RewriteLocalhost(value string) (string, bool) {
	if !localhostPattern.MatchString(value) {
		return value, false
	}
	return localhostPattern.ReplaceAllString(value, `${1}`+localhostHost+`${3}`), true
}

// ContainsLoopbackURL reports whether a config text references a localhost or
// 127.0.0.1 URL anywhere. It is the cheap pre-filter used by the overlay
// scanner (R7 item 32): only files that pass this get copied, rewritten, and
// re-mounted; untouched files are never copied.
func ContainsLoopbackURL(text string) bool {
	return localhostPattern.MatchString(text)
}

// NoRewriteEnv marks the environment variable that disables URL rewriting
// (R7 item 32, --no-rewrite). Exported as a constant so the CLI flag and the
// builder share one spelling.
const NoRewriteEnv = "AI_LAUNCHER_NO_REWRITE"

// RewriteDisabled reports whether URL rewriting was turned off by the
// --no-rewrite environment switch. Callers should check this before applying
// RewriteLocalhost to configs or environment values.
func RewriteDisabled(environ []string) bool {
	for _, entry := range environ {
		if strings.HasPrefix(entry, NoRewriteEnv+"=") {
			return true
		}
	}
	return false
}
