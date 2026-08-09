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

	launchcmd "github.com/lgldsilva/ai-launcher/internal/cmd"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

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
// config. handled is true when one of them ran.
func runGlobalCommands(opts *cliOptions, global config.Global, home string, out, errOut io.Writer) (bool, error) {
	if opts.install || opts.upgrade {
		return true, launchcmd.InstallConfigured(global, opts.agent, home, opts.upgrade, out, errOut)
	}
	if opts.listProfiles {
		listProfiles(global, out)
		return true, nil
	}
	if opts.deleteProfile != "" {
		return true, deleteProfile(opts.globalPath, global, opts.deleteProfile, out)
	}
	if strings.TrimSpace(opts.workstreamSearch) != "" {
		return true, searchWorkstream(opts, global, home, out, errOut)
	}
	return false, nil
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
	memoryPath, err := exec.LookPath(aiMemoryCommand)
	if err != nil {
		return fmt.Errorf("%s not found in PATH; install it with --install", aiMemoryCommand)
	}
	workstreamID, err := resolveWorkstreamID(opts)
	if err != nil {
		return err
	}
	args := []string{"workstream-search", "--workstream-id", workstreamID}
	if opts.searchLimit > 0 {
		args = append(args, "--limit", strconv.Itoa(opts.searchLimit))
	}
	if opts.searchJSON {
		args = append(args, "--json")
	}
	args = append(args, opts.workstreamSearch)

	ctx, cancel := context.WithTimeout(context.Background(), workstreamSearchTimeout)
	defer cancel()
	// #nosec G204 G702 -- memoryPath is the LookPath result for a fixed tool name;
	// every argument is a launcher-controlled flag or the operator's own query.
	command := exec.CommandContext(ctx, memoryPath, args...)
	command.Env = launcher.Environment(launcher.LaunchConfig{
		UseMemory:       true,
		HomeDir:         home,
		MemoryServerURL: global.MemoryServerURL,
		MemoryAuthToken: global.MemoryAuthToken,
	})
	command.Stdout = out
	command.Stderr = errOut
	if err := command.Run(); err != nil {
		return fmt.Errorf("workstream-search: %w", err)
	}
	return nil
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
