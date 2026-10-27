package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	launchcmd "github.com/lgldsilva/ai-launcher/internal/cmd"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// memoryResultLimitFlag is ai-memory's --limit on the ledger side commands
// (workstream-search, workstreams, and resume).
const memoryResultLimitFlag = "--limit"

// userHome returns the operator's home directory, or "" when it cannot be
// determined.
func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// resolveConfigPaths fills in the default config paths for the flags the
// operator left unset.
func resolveConfigPaths(opts *cliOptions, home string) {
	if opts.globalPath == "" && home != "" {
		opts.globalPath = filepath.Join(home, ".config", "ai-launch", "config.yaml")
	}
	if opts.localPath == "" {
		opts.localPath = config.ResolveLocalConfigPath()
	}
}

// runGlobalCommands handles the commands that only need the trusted global
// config. handled is true when one of them ran. in is the process stdin and is
// forwarded only to interactive side commands such as --resume.
func runGlobalCommands(opts *cliOptions, global config.Global, home string, in io.Reader, out, errOut io.Writer) (bool, error) {
	if opts.installSystemDeps && !opts.install && !opts.upgrade {
		return true, fmt.Errorf("--install-system-deps requires --install or --upgrade")
	}
	if err := installInstructionsConflict(opts); err != nil {
		return true, err
	}
	if opts.install || opts.upgrade {
		return true, launchcmd.InstallConfigured(global, opts.agent, home, opts.upgrade, opts.installSystemDeps, out, errOut)
	}
	if opts.installInstructions {
		return true, installMemoryInstructions(opts, global, home, out, errOut)
	}
	if opts.listProfiles {
		listProfiles(global, out)
		return true, nil
	}
	if opts.deleteProfile != "" {
		return true, deleteProfile(opts.globalPath, global, opts.deleteProfile, out)
	}
	if err := validateMemorySideCommandFlags(opts); err != nil {
		return true, err
	}
	if strings.TrimSpace(opts.workstreamSearch) != "" {
		return true, searchWorkstream(opts, global, home, out, errOut)
	}
	if opts.listWorkstreams {
		return true, listMemoryWorkstreams(opts, global, home, out, errOut)
	}
	if opts.resume {
		return true, resumeWorkstream(opts, global, home, in, out, errOut)
	}
	return false, nil
}

// validateMemorySideCommandFlags refuses combinations that would leave the
// operator unsure which ai-memory surface ran. Each of --workstream-search,
// --workstreams and --resume owns the process; they do not compose.
func validateMemorySideCommandFlags(opts *cliOptions) error {
	search := strings.TrimSpace(opts.workstreamSearch) != ""
	count := 0
	if search {
		count++
	}
	if opts.listWorkstreams {
		count++
	}
	if opts.resume {
		count++
	}
	if count > 1 {
		return fmt.Errorf("pick one of --workstream-search, --workstreams, or --resume")
	}
	if opts.resumeAll && !opts.resume {
		return fmt.Errorf("--all requires --resume")
	}
	if strings.TrimSpace(opts.resumeSearch) != "" && !opts.resume {
		return fmt.Errorf("--search requires --resume")
	}
	if opts.searchJSON && !search && !opts.listWorkstreams {
		return fmt.Errorf("--json requires --workstream-search or --workstreams")
	}
	return nil
}

// appendTrimmedFlag appends flag and value when value is non-blank. The
// blank check uses the trimmed form, which is also what is forwarded, so a
// value of spaces does not become an empty ai-memory argument.
func appendTrimmedFlag(args []string, flag, value string) []string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return append(args, flag, trimmed)
	}
	return args
}

// appendEnabledFlag appends flag when the operator turned that switch on.
func appendEnabledFlag(args []string, enabled bool, flag string) []string {
	if enabled {
		return append(args, flag)
	}
	return args
}

// appendOptionalLimit appends --limit only for a positive cap.
func appendOptionalLimit(args []string, limit int) []string {
	if limit > 0 {
		return append(args, memoryResultLimitFlag, strconv.Itoa(limit))
	}
	return args
}

// appendLimitAndJSON appends the result cap and the JSON switch shared by
// the read-only ledger queries.
func appendLimitAndJSON(args []string, limit int, asJSON bool) []string {
	args = appendOptionalLimit(args, limit)
	if asJSON {
		args = append(args, "--json")
	}
	return args
}

// memorySideSpec is one ai-memory subcommand this launcher runs directly.
// Search and workstreams set timeout; resume sets stdin and leaves timeout
// at zero so the picker can wait on the terminal.
type memorySideSpec struct {
	name    string
	args    []string
	timeout time.Duration
	stdin   io.Reader
}

// lookupAIMemory resolves the ai-memory executable. A missing binary is an
// install hint, not an exec error, and it is reported before any other
// refusal so the operator is not sent to fix a flag for a tool they lack.
func lookupAIMemory() (string, error) {
	memoryPath, err := exec.LookPath(aiMemoryCommand)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH; install it with --install", aiMemoryCommand)
	}
	return memoryPath, nil
}

// runMemorySideCommand resolves ai-memory on PATH and runs spec. The side
// commands share this path so the server URL, the auth token, and the
// decision to skip ai-jail cannot drift between them.
func runMemorySideCommand(global config.Global, home string, spec memorySideSpec, out, errOut io.Writer) error {
	memoryPath, err := lookupAIMemory()
	if err != nil {
		return err
	}
	return execMemorySideCommand(memoryPath, global, home, spec, out, errOut)
}

// execMemorySideCommand runs an already-resolved ai-memory. Search calls it
// after lookupAIMemory so a missing workstream id is only reported once the
// binary is known to be installed.
func execMemorySideCommand(memoryPath string, global config.Global, home string, spec memorySideSpec, out, errOut io.Writer) error {
	var command *exec.Cmd
	if spec.timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), spec.timeout)
		defer cancel()
		// #nosec G204 G702 -- memoryPath is LookPath of a fixed tool name; args are launcher-owned.
		command = exec.CommandContext(ctx, memoryPath, spec.args...)
	} else {
		// #nosec G204 G702 -- memoryPath is LookPath of a fixed tool name; args are launcher-owned.
		command = exec.Command(memoryPath, spec.args...)
	}
	command.Env = launcher.Environment(launcher.LaunchConfig{
		UseMemory:       true,
		HomeDir:         home,
		MemoryServerURL: global.MemoryServerURL,
		MemoryAuthToken: global.MemoryAuthToken,
	})
	command.Stdin = spec.stdin
	command.Stdout = out
	command.Stderr = errOut
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s: %w", spec.name, err)
	}
	return nil
}

// searchWorkstream forwards a query to `ai-memory workstream-search`.
//
// The launcher already creates workstreams (`--new`) and resumes them
// (`--workstream`), so it owned the write side of the ledger and gave no way
// to read it back. The delta ai-memory injects into the next harness is
// size-limited by design; the searchable ledger is where an old decision
// actually lives, and needing a second tool to reach it defeats the point of
// having one command that knows about workstreams.
//
// Deliberately not jail-wrapped, unlike a launch. This is a read-only query
// against the ai-memory server over HTTP, in the same class as --doctor: no
// harness runs, nothing touches the checkout, and paying for a sandbox would
// only mean the operator's terminal cannot see the answer.
//
// The workstream is addressed by id, not by the name --workstream takes:
// upstream's `run --workstream` selects by name while `workstream-search`
// requires --workstream-id, and no subcommand maps one to the other. The id is
// resolved by resolveWorkstreamID and always emitted, so the argv says which
// ledger was read instead of depending on what the child inherits.
func searchWorkstream(opts *cliOptions, global config.Global, home string, out, errOut io.Writer) error {
	memoryPath, err := lookupAIMemory()
	if err != nil {
		return err
	}
	workstreamID, err := resolveWorkstreamID(opts)
	if err != nil {
		return err
	}
	args := []string{"workstream-search", "--workstream-id", workstreamID}
	args = appendLimitAndJSON(args, opts.searchLimit, opts.searchJSON)
	args = append(args, opts.workstreamSearch)
	return execMemorySideCommand(memoryPath, global, home, memorySideSpec{
		name:    "workstream-search",
		args:    args,
		timeout: workstreamSearchTimeout,
	}, out, errOut)
}

// resolveWorkstreamID answers which workstream a search reads, preferring the
// explicit flag over the inherited environment.
//
// Without it the launcher forwarded a query with no --workstream-id at all and
// upstream's own argument parser refused the command, so the feature failed
// outside a managed session — which is the session it exists to serve. Falling
// back to the environment keeps the zero-argument form working for an agent
// this launcher started; the flag is what makes it usable from a bare shell.
//
// --workstream is deliberately not consulted: it carries the workstream *name*
// that `ai-memory run --workstream` selects by, and upstream offers no way to
// turn a name into the id this subcommand wants. Guessing they are the same
// would send a search to a workstream the operator did not ask for.
func resolveWorkstreamID(opts *cliOptions) (string, error) {
	if id := strings.TrimSpace(opts.workstreamID); id != "" {
		return id, nil
	}
	if id := strings.TrimSpace(os.Getenv(memoryWorkstreamIDEnv)); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("--workstream-search needs a workstream id: pass --workstream-id, or run it from an agent this launcher started, where %s is set", memoryWorkstreamIDEnv)
}

// listMemoryWorkstreams forwards to `ai-memory workstreams`. Like
// workstream-search it is a read-only ledger query: no jail, no harness, and
// the operator's terminal must see the answer.
func listMemoryWorkstreams(opts *cliOptions, global config.Global, home string, out, errOut io.Writer) error {
	args := []string{"workstreams"}
	args = appendTrimmedFlag(args, "--workspace", opts.workspace)
	args = appendTrimmedFlag(args, "--project", opts.project)
	args = appendLimitAndJSON(args, opts.searchLimit, opts.searchJSON)
	return runMemorySideCommand(global, home, memorySideSpec{
		name:    "workstreams",
		args:    args,
		timeout: workstreamSearchTimeout,
	}, out, errOut)
}

// resumeWorkstream forwards to `ai-memory resume`, the interactive picker.
// Unlike workstream-search it launches a harness after the operator picks a
// row, so stdin must reach ai-memory and there is no short timeout — the
// picker waits on the terminal. Still not jail-wrapped: the eventual `run`
// that resume starts is ai-memory's own composition, not this launcher's.
func resumeWorkstream(opts *cliOptions, global config.Global, home string, in io.Reader, out, errOut io.Writer) error {
	args := []string{"resume"}
	args = appendTrimmedFlag(args, "--workspace", opts.workspace)
	args = appendEnabledFlag(args, opts.resumeAll, "--all")
	args = appendOptionalLimit(args, opts.searchLimit)
	args = appendTrimmedFlag(args, "--search", opts.resumeSearch)
	args = appendEnabledFlag(args, opts.yolo, "--yolo")
	args = appendEnabledFlag(args, opts.fresh, "--fresh")
	return runMemorySideCommand(global, home, memorySideSpec{
		name:  "resume",
		args:  args,
		stdin: in,
	}, out, errOut)
}

// installInstructionsConflict refuses folding the markdown writer into the
// binary installer. Those commands answer different questions, and the mix
// is checked before either one runs.
func installInstructionsConflict(opts *cliOptions) error {
	if opts.installInstructions && (opts.install || opts.upgrade) {
		return fmt.Errorf("--install-instructions cannot combine with --install/--upgrade; run it on its own")
	}
	return nil
}

// installMemoryInstructions forwards to `ai-memory install-instructions`.
//
// It is deliberately a separate flag from --install: that command provisions
// binaries globally, while install-instructions mutates a markdown file in the
// current checkout. Folding the two would surprise anyone who only asked for
// binaries. The target file must be named with --target (or --print for a
// no-write preview) so the operator answers "which file may it touch" before
// anything is written.
func installMemoryInstructions(opts *cliOptions, global config.Global, home string, out, errOut io.Writer) error {
	target := strings.TrimSpace(opts.instructionsTarget)
	if target == "" && !opts.instructionsPrint {
		return fmt.Errorf("--install-instructions needs --target <file> (for example CLAUDE.md or AGENTS.md), or --print to preview without writing")
	}
	args := []string{"install-instructions"}
	if opts.instructionsPrint {
		args = append(args, "--print")
	}
	if target != "" {
		args = append(args, "--target", target)
	}
	if opts.instructionsNoSkills {
		args = append(args, "--no-skills")
	}
	if opts.instructionsCompact {
		args = append(args, "--compact")
	}
	return runMemorySideCommand(global, home, memorySideSpec{
		name:    "install-instructions",
		args:    args,
		timeout: workstreamSearchTimeout,
	}, out, errOut)
}
