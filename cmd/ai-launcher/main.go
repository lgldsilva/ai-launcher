package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"github.com/lgldsilva/ai-launcher/internal/selfupdate"
)

// Build metadata injected via -ldflags "-X main.version=..." at build time
// (Makefile and .goreleaser.yaml). These identify the BINARY release; they are
// unrelated to config.CurrentVersion, which versions the config file schema.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const (
	// flagNoJail is the CLI flag name for opting out of the sandbox; it is
	// looked up by name after parsing to tell "unset" apart from "false".
	flagNoJail = "no-jail"
	// permissionSystemdUser is the catalog permission id behind the
	// --systemd-user flag.
	permissionSystemdUser = config.PermissionSystemd
	// warningLabel prefixes non-fatal diagnostics on stderr.
	warningLabel = "warning:"
	valueFormat  = "%v"
	// aiMemoryCommand is the upstream memory CLI, resolved on PATH for the
	// read-only surfaces this binary forwards to it.
	aiMemoryCommand = config.AIMemoryCommand
	// memoryWorkstreamIDEnv is the variable ai-memory exports into a managed
	// child so it can address its own workstream. The launcher does not own it
	// (it is absent from ownedMemoryEnvKeys), so it survives into an agent
	// launched from here and is the default for --workstream-search.
	memoryWorkstreamIDEnv = "AI_MEMORY_WORKSTREAM_ID"
)

// workstreamSearchTimeout bounds the ai-memory query so a hung or unreachable
// server cannot wedge the terminal. It is a search, not a session.
const workstreamSearchTimeout = 30 * time.Second

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ai-launcher:", err)
		// Errors carrying a child exit code (compose/agent sessions) propagate
		// it so scripts and CI see the real failure status, not a flat 1.
		var exitErr interface{ ExitCode() int }
		if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

func warnf(errOut io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(errOut, warningLabel+" "+format+"\n", args...)
}

// disableMemoryIfUnsupported turns the ai-memory layer off when the selected
// agent would build a command that ai-memory run cannot execute. This covers
// both agents that declare supports_memory: false and agents whose catalog
// entry is stale/wrong and names a run_harness that ai-memory does not accept
// (for example cursor-agent). Unresolved agents are left alone.
//

// memoryEnvEntries returns the AI_MEMORY_* environment entries the container
// needs when the memory layer is on: the server URL (rewritten to the host
// gateway by BuildRunCommand) and the auth token. Without them the in-image
// ai-memory wrapper cannot reach the host server.
func memoryEnvEntries(agent config.Agent, memoryServerURL, memoryAuthToken string) []string {
	if !agent.SupportsMemory {
		return nil
	}
	var env []string
	if serverURL := strings.TrimSpace(memoryServerURL); serverURL != "" {
		env = append(env, "AI_MEMORY_SERVER_URL="+serverURL)
	}
	if token := strings.TrimSpace(memoryAuthToken); token != "" {
		env = append(env, "AI_MEMORY_AUTH_TOKEN="+token)
	}
	return env
}

func composeMemoryEnv(enabled bool, agent config.Agent, global config.Global) []string {
	if !enabled {
		return nil
	}
	return memoryEnvEntries(agent, global.MemoryServerURL, global.MemoryAuthToken)
}

func disableMemoryIfUnsupported(agent config.Agent, resolved bool, memory *bool, errOut io.Writer) {
	if !resolved || !*memory {
		return
	}
	if !agent.SupportsMemory {
		warnf(errOut, "agent %q does not support ai-memory; disabling memory for this launch", agent.Command)
		*memory = false
		return
	}
	harness := agent.Command
	if agent.Memory != nil {
		if h := strings.TrimSpace(agent.Memory.RunHarness); h != "" {
			harness = h
		}
	}
	if !config.SupportsMemoryRunHarness(harness) {
		warnf(errOut, "ai-memory run does not accept harness %q for agent %q; disabling memory for this launch", harness, agent.Command)
		*memory = false
	}
}

// disableYoloIfUnsupported turns the dangerous-mode flag off when the selected
// agent does not declare support for it. Without this, toggling --yolo for an
// agent like Cursor would append a raw --yolo argument that the agent does not
// understand. Unresolved agents are left alone.
func disableYoloIfUnsupported(agent config.Agent, resolved bool, yolo *bool, errOut io.Writer) {
	if resolved && *yolo && !agent.SupportsYolo {
		warnf(errOut, "agent %q does not support --yolo; disabling it for this launch", agent.Command)
		*yolo = false
	}
}

// resolveAgentSelection picks the agent (the --agent flag wins over the local
// config). An unresolved agent is still synthesized here; enforceLocalConfigTrust
// decides whether it may be used. The returned bool is true when the agent was
// actually found in the catalog (so callers can tell a real catalog entry from
// a synthesized fallback).
func resolveAgentSelection(catalogue catalog.Catalog, flagAgent string, local config.Local) (catalog.AgentStatus, bool) {
	selected := flagAgent
	if selected == "" {
		selected = local.Agent
	}
	status, err := catalogue.Resolve(selected)
	if err != nil {
		return catalog.AgentStatus{Agent: config.Agent{Name: selected, Command: selected}}, false
	}
	if status.ResolvedCommand != "" {
		// Keep the configured catalog name for display, but invoke the alias
		// that was actually found on this machine (for example kilocode).
		// Preserve the original catalog command so launcher internals (state
		// directory mounts, memory harness mapping, etc.) keep working.
		status.Agent.CatalogCommand = status.Agent.Command
		status.Agent.Command = status.ResolvedCommand
	}
	return status, true
}

// localTrustFrom records what the workspace file itself asked for, so the trust
// check can tell an operator's choice from a repository's.
//
// It reads the config as LoadLocal returned it, BEFORE any profile is layered
// on: a profile lives in the global config, which ARCHITECTURE invariant 2b
// lists as trusted alongside the command line. Deriving this from the merged
// selection made a profile's own `jail: false` indistinguishable from a
// repository lowering the sandbox — `--profile` was refused outright, and the
// error blamed a .ai-launch.yaml that had said nothing of the sort.
//
// Only the fields the file still decides are checked. A value a trusted source
// replaced never reaches the argv, so refusing the launch over it would block
// on input that was already discarded: `agent: other-cli` in the workspace file
// is harmless once --agent or a profile picks something else.

// reportPreflight prints every pre-flight issue and reports whether any of them
// is fatal. Warnings are labelled as such; anything else blocks the launch.
func reportPreflight(errOut io.Writer, issues []launcher.Issue) bool {
	fatal := false
	for _, issue := range issues {
		if issue.Warning {
			warnf(errOut, valueFormat, issue)
		} else {
			_, _ = fmt.Fprintln(errOut, "error:", issue)
		}
		if !issue.Warning {
			fatal = true
		}
	}
	return fatal
}

// reportInfoFlags handles the flags that only print information and exit,
// before any configuration is loaded. handled is true when one of them ran.
func reportInfoFlags(opts cliOptions, out io.Writer) (bool, error) {
	switch {
	case opts.showVersion:
		_, _ = fmt.Fprintf(out, "ai-launcher %s (%s, %s)\n", version, commit, date)
		return true, nil
	case opts.doctor:
		return true, reportDoctor(out)
	}
	return false, nil
}

// reportDoctor prints the installed upstream CLI versions against the floor
// this launcher composes with. It is the explicit home of the --version probe:
// pre-flight validation stays hermetic so a launch never pays for extra
// processes, and the TUI event loop never blocks on them.
func reportDoctor(out io.Writer) error {
	_, _ = fmt.Fprintf(out, "ai-launcher %s (%s, %s)\n", version, commit, date)
	var blocked []string
	stale := false
	for _, status := range launcher.UpstreamReport(nil, "") {
		switch {
		case status.Missing:
			blocked = append(blocked, status.Command)
			_, _ = fmt.Fprintf(out, "%-10s not found in PATH (required >= %s)\n", status.Command, status.Minimum)
		case status.Version == "":
			blocked = append(blocked, status.Command)
			_, _ = fmt.Fprintf(out, "%-10s %s (version unreadable; required >= %s)\n", status.Command, status.Path, status.Minimum)
		case status.TooOld:
			stale = true
			blocked = append(blocked, status.Command)
			_, _ = fmt.Fprintf(out, "%-10s %s is older than the required %s; upgrade with --upgrade\n", status.Command, status.Version, status.Minimum)
		default:
			_, _ = fmt.Fprintf(out, "%-10s %s (>= %s)\n", status.Command, status.Version, status.Minimum)
		}
	}
	if stale {
		_, _ = fmt.Fprintln(out, "\nAn older upstream may accept a different flag surface than the one ai-launcher emits.")
	}
	if len(blocked) > 0 {
		return fmt.Errorf("doctor found %d upstream issue(s); see above", len(blocked))
	}
	return nil
}

// upgradeResolveTag and upgradeApply are seams: tests stub them to exercise
// the upgrade wiring without network access or executable replacement.
var upgradeResolveTag = func(ctx context.Context, updater *selfupdate.Updater, wantVersion string) (string, error) {
	if wantVersion != "" {
		return wantVersion, nil
	}
	return updater.LatestTag(ctx)
}

var upgradeApply = func(ctx context.Context, updater *selfupdate.Updater, tag string) error {
	return updater.Apply(ctx, tag)
}

// runUpgrade implements `ai-launcher upgrade [--check] [--version vX.Y.Z]`,
// the self-update of the running binary from this repository's GitHub
// releases. Checksum verification is strict (a missing checksums.txt is a
// hard error) because the flow overwrites the running executable.
func runUpgrade(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	flags.SetOutput(errOut)
	check := flags.Bool("check", false, "only report whether an update is available")
	wantVersion := flags.String("version", "", "install a specific release tag (default: latest)")
	flags.Usage = func() {
		_, _ = fmt.Fprint(errOut, `usage: ai-launcher upgrade [--check] [--version vX.Y.Z]

Self-update the ai-launcher binary from the project's GitHub releases.
Downloads the archive for this OS/arch, verifies its SHA-256 against the
release checksums.txt (mandatory), and atomically replaces the running
executable.

Environment overrides:
  AI_LAUNCHER_UPDATE_API    release API base (default `+selfupdate.DefaultAPIBaseURL+`)
  AI_LAUNCHER_UPDATE_URL    download base URL (default `+selfupdate.DefaultDownloadBaseURL+`)
  AI_LAUNCHER_UPDATE_TOKEN  GitHub token for private releases (sent as a
                            Bearer header; never printed or logged)
`)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	apiBase := envOr("AI_LAUNCHER_UPDATE_API", selfupdate.DefaultAPIBaseURL)
	downloadBase := envOr("AI_LAUNCHER_UPDATE_URL", selfupdate.DefaultDownloadBaseURL)
	token := os.Getenv("AI_LAUNCHER_UPDATE_TOKEN")
	// Fail closed: the endpoint overrides come from the environment, so a
	// hostile parent process could redirect the update flow to its own host.
	// The Bearer token is only ever sent to the default release host; with an
	// overridden endpoint it is withheld (and said so) rather than exfiltrated.
	if token != "" && (apiBase != selfupdate.DefaultAPIBaseURL || downloadBase != selfupdate.DefaultDownloadBaseURL) {
		warnf(errOut, "AI_LAUNCHER_UPDATE_TOKEN withheld: the update endpoint is overridden and the token is only sent to the default release host")
		token = ""
	}
	updater := &selfupdate.Updater{
		CurrentVersion:  version,
		APIBaseURL:      apiBase,
		DownloadBaseURL: downloadBase,
		Token:           token,
	}
	ctx := context.Background()
	tag, err := upgradeResolveTag(ctx, updater, *wantVersion)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "current: %s\nlatest:  %s\n", version, tag)
	if *check {
		if selfupdate.SameVersion(version, tag) {
			_, _ = fmt.Fprintln(out, "already up to date.")
		} else {
			_, _ = fmt.Fprintln(out, "an update is available — run `ai-launcher upgrade`.")
		}
		return nil
	}
	if *wantVersion == "" && selfupdate.SameVersion(version, tag) {
		_, _ = fmt.Fprintf(out, "already up to date (%s)\n", tag)
		return nil
	}
	if err := upgradeApply(ctx, updater, tag); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "ai-launcher updated: %s → %s\n", version, tag)
	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// isWorkstreamConflict reports whether a launch error is ai-memory's
// "409 workstream is already active" — a busy or stale lease that a parallel
// workstream (--new) sidesteps. The launcher uses it to arm that recovery.
func isWorkstreamConflict(errText string) bool {
	return strings.Contains(errText, "409") && strings.Contains(errText, "workstream is already active")
}

// launchFailureHint maps common child stderr/exit text to a short recovery tip.
func launchFailureHint(errText string) string {
	switch {
	case strings.Contains(errText, "409") && strings.Contains(errText, "workstream is already active"):
		return "hint: another ai-memory run still holds this workstream (or left a stale lease).\n" +
			"  - wait until the lease time in the error, then retry\n" +
			"  - if the owner PID is dead: just retry (the lease expires within 90 seconds)\n" +
			"  - or start a parallel stream: ai-launcher --agent <name> --new my-stream\n" +
			"  - or skip memory: ai-launcher --agent <name> --no-memory"
	case strings.Contains(errText, "401") || strings.Contains(errText, "Unauthorized") || strings.Contains(errText, "auth required"):
		return "hint: ai-memory rejected the token (401). Set memory_auth_token in the global config (~/.config/ai-launch/config.yaml), or use --no-memory"
	case strings.Contains(errText, "certificate") || strings.Contains(errText, "TLS") || strings.Contains(errText, "x509"):
		return "hint: memory server TLS failed — check memory_server_url (expect *.internal.lgldsilva.com.br) or use --no-memory"
	case strings.Contains(errText, "canonicalizing managed run cwd") || strings.Contains(errText, "Operation not permitted"):
		return "hint: ai-memory inside the jail failed on this cwd — try --no-memory (keep Jail) or run from a path under $HOME"
	case strings.Contains(errText, "keychain"):
		return "hint: the agent could not access the macOS keychain inside the jail — try launching from a path under $HOME, or run with --no-jail"
	default:
		return "hint: try --no-memory if the memory server fails, or --no-jail if the project is on an external volume"
	}
}

// launchFailureStatus turns an execute error into a concise status block for
// the TUI recovery screen: a one-line call to action plus the matching hint.
// It is shown when the TUI is re-opened after a launch failure so the operator
// knows what went wrong and how to recover before pressing r again.
func launchFailureStatus(errText string) string {
	return "Last launch failed — adjust and press r to retry, or q to quit:\n" + launchFailureHint(errText)
}
