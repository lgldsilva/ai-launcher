package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"github.com/lgldsilva/ai-launcher/internal/tui"
)

// launchAction is what run() does with the composed argv.
type launchAction int

const (
	actionExecute launchAction = iota
	actionPrint
)

// decideLaunchAction maps the confirmed launch intent to the post-TUI
// behavior: Enter in the TUI and any CLI invocation execute the composed
// argv; only an explicit dry-run (the --dry-run flag, or Ctrl+D/d inside the
// TUI, which never leaves the TUI) prints it.
func decideLaunchAction(dryRun bool) launchAction {
	if dryRun {
		return actionPrint
	}
	return actionExecute
}

// launchRequest bundles everything launch needs to confirm, validate, and
// execute a selection, keeping the flow readable without a long parameter
// list.
type launchRequest struct {
	args          []string
	opts          cliOptions
	global        config.Global
	local         config.Local
	launchConfig  launcher.LaunchConfig
	in            io.Reader
	out           io.Writer
	errOut        io.Writer
	composeUpdate composeUpdateChoice
}

// launch confirms the configuration (TUI when interactive), optionally saves
// it, and builds, validates, and executes the resulting argv.
//
// In the interactive (TUI) flow a launch failure is recoverable in place:// the TUI is re-opened seeded with the failure so the operator can adjust
// (toggle New workstream / ai-memory off) and retry with r, or quit, without
// restarting ai-launcher. The CLI flow (argv present) stays one-shot and
// exits with the error, since prompts are not an option there.
func launch(req launchRequest) error {
	tuiFlow := len(req.args) == 0
	status := ""
	autoArmedNew := false // true when --new was armed to recover from a 409 (cleared on success)
	for {
		proceed, err := req.confirmSelection(status)
		if err != nil || !proceed {
			return err
		}
		if len(req.launchConfig.Services) > 0 && req.launchConfig.UseDocker {
			return req.launchCompose()
		}
		completed, err := req.launchSingle(tuiFlow, &status, &autoArmedNew)
		if completed || err != nil {
			return err
		}
	}
}

func (req *launchRequest) launchSingle(tuiFlow bool, status *string, autoArmedNew *bool) (bool, error) {
	argv, err := launcher.Build(req.launchConfig)
	if err != nil {
		return false, err
	}
	// Validate before printing: --dry-run is the advertised diagnostic surface,
	// so it must not present a command pre-flight would reject. The argv is
	// still printed when there are issues — seeing what would run is the point.
	printOnly := decideLaunchAction(req.opts.dryRun) == actionPrint
	issues := launcher.NewValidator().WithPermissions(req.global.Permissions).Validate(req.launchConfig)
	fatal := reportPreflight(req.errOut, issues)
	if printOnly {
		printDryRun(req.out, argv, req.launchConfig.Services)
	}
	if fatal {
		return false, errors.New("pre-flight validation failed")
	}
	if printOnly {
		return true, nil
	}
	req.rememberRecentAgent()
	argv, dockerCleanup, err := req.prepareDockerIfNeeded(argv)
	if err != nil {
		return false, err
	}
	execErr := req.executeWithDockerCleanup(argv, dockerCleanup)
	if execErr == nil {
		if *autoArmedNew && req.launchConfig.NewWorkstream != "" {
			req.launchConfig.NewWorkstream = ""
			if err := saveLocalSelection(req.opts.globalPath, true, req.opts.localPath, req.local, req.launchConfig, req.errOut); err != nil {
				warnf(req.errOut, "could not clear the recovery workstream from %s: %v", req.opts.localPath, err)
			}
		}
		return true, nil
	}
	if !tuiFlow {
		return false, execErr
	}
	// TUI flow: surface the failure and re-open the selection screen.
	*status = launchFailureStatus(execErr.Error())
	if isWorkstreamConflict(execErr.Error()) && strings.TrimSpace(req.launchConfig.NewWorkstream) == "" {
		req.launchConfig.NewWorkstream = fmt.Sprintf("recovery-%d", time.Now().Unix())
		*autoArmedNew = true
		*status = "Workstream busy (409) — armed 'New workstream' (" + req.launchConfig.NewWorkstream +
			") for the retry so the next r sidesteps the conflict instead of re-hitting it.\n" +
			"Turn it off if you'd rather wait ~90s, or q to quit.\n" + launchFailureHint(execErr.Error())
	}
	return false, nil
}

func (r *launchRequest) launchCompose() error {
	printOnly := decideLaunchAction(r.opts.dryRun) == actionPrint
	issues := launcher.NewValidator().WithPermissions(r.global.Permissions).Validate(r.launchConfig)
	fatal := reportPreflight(r.errOut, issues)
	runtime := container.RuntimeOrDefault(r.launchConfig.Docker.Runtime)
	composePath := filepath.Join(mustGetwd(), containerArtifactDir, "docker-compose.yaml")
	interactive := interactiveLaunch(r.opts)
	var argv []string
	var err error
	if interactive {
		argv, err = composeAgentRunArgvWithContext(runtime, composePath, r.launchConfig.ContainerContext)
	} else {
		argv, err = composeCommandArgvWithContext(runtime, composePath, "up", nil, false, false, r.launchConfig.ContainerContext)
	}
	if err != nil {
		return err
	}
	if printOnly {
		rendered, err := composePreviewYAML(composePath, r.launchConfig)
		if err != nil {
			return err
		}
		if len(r.launchConfig.Services) > 0 {
			_, _ = fmt.Fprintf(r.out, "services: %s\n", strings.Join(r.launchConfig.Services, " "))
		}
		printComposeYAMLPreview(r.out, rendered, argv)
	}
	if fatal {
		return errors.New("pre-flight validation failed")
	}
	if printOnly {
		return nil
	}
	if r.composeUpdate == composeUpdatePrompt {
		choice, err := resolveComposeUpdate(r.launchConfig, r.composeUpdate, r.in, r.out, interactiveLaunch(r.opts))
		if err != nil {
			return err
		}
		r.composeUpdate = choice
	}
	// The TUI may have changed the agent or service selection since the last
	// generated project artifact. Refresh the automatic Compose launch from
	// the confirmed selection so Dockerfile, install config and compose command
	// cannot drift apart. This replaces generated artifacts; commands that only
	// inspect or stop the stack keep using the existing materialized file.
	if err := materializeContainerArtifactsWithChoice(r.launchConfig, r.global, r.out, r.errOut, r.composeUpdate); err != nil {
		return err
	}
	if err := container.RuntimeInfo(runtime, r.launchConfig.ContainerContext); err != nil {
		return fmt.Errorf("%s runtime is unavailable: %w", runtime.Name(), err)
	}
	if err := container.RequireLocalDaemon(runtime, r.launchConfig.ContainerContext); err != nil {
		return err
	}
	r.rememberRecentAgent()
	return r.runComposeSession(runtime, composePath, argv, interactive)
}

// runComposeSession executes the compose argv and, for the interactive
// one-shot, always stops the dependency stack afterwards — including when the
// agent run failed or was interrupted. runRuntimeCommand returns on SIGINT
// instead of the process dying, so this cleanup cannot be skipped by Ctrl-C.
// The interactive one-shot starts dependencies for the agent, but those
// services must not outlive the launcher session. Keep named volumes/data;
// only stop and remove the Compose containers and network on exit.
func (r *launchRequest) runComposeSession(runtime container.Runtime, composePath string, argv []string, interactive bool) error {
	runErr := runRuntimeCommand(argv, composeRuntimeEnv(r.launchConfig), r.in, r.out, r.errOut)
	if !interactive {
		return runErr
	}
	down, downErr := composeCommandArgvWithContext(runtime, composePath, "down", nil, false, false, r.launchConfig.ContainerContext)
	if downErr == nil {
		downErr = runRuntimeCommand(down, composeRuntimeEnv(r.launchConfig), r.in, r.out, r.errOut)
	}
	if runErr != nil {
		if downErr != nil {
			return fmt.Errorf("agent session failed: %w; compose cleanup failed: %v", runErr, downErr)
		}
		return runErr
	}
	return downErr
}

func printDryRun(out io.Writer, argv, services []string) {
	if len(services) > 0 {
		_, _ = fmt.Fprintf(out, "services: %s\n", strings.Join(services, " "))
	}
	_, _ = fmt.Fprintln(out, shellJoin(argv))
}

// runTUI is the interactive selection loop; a variable so tests can confirm
// a selection without a terminal. The trailing string seeds the initial
// status line (used to surface a launch failure on re-open).
var runTUI = tui.RunWithMessage

// confirmSelection runs the interactive TUI when the launch came from one, or
// persists the selection for a CLI invocation. proceed is false when the flow
// ends here (a --save run, a TUI cancellation, or a TUI error).
func (r *launchRequest) confirmSelection(initialStatus string) (bool, error) {
	if len(r.args) > 0 {
		if err := saveLocalSelection(r.opts.globalPath, r.opts.save, r.opts.localPath, r.local, r.launchConfig, r.errOut); err != nil {
			return false, err
		}
		return !r.opts.save, nil
	}
	// The last selection a profile load produced, if any. It is what tells an
	// unedited profile launch apart from options the operator chose.
	var profileSelection *launcher.LaunchConfig
	confirmed, err := runTUI(r.global, r.launchConfig, tui.Hooks{
		Save: func(updated launcher.LaunchConfig) error {
			return saveLocalSelection(r.opts.globalPath, true, r.opts.localPath, r.local, updated, r.errOut)
		},
		ProfileLoaded: func(loaded launcher.LaunchConfig) {
			snapshot := loaded
			profileSelection = &snapshot
		},
		SaveProfile: func(name string, updated launcher.LaunchConfig) error {
			if err := config.SetProfile(&r.global, name, profileFromLaunch(updated)); err != nil {
				return err
			}
			return config.SaveGlobal(r.opts.globalPath, r.global)
		},
		ReviewCompose: func(updated launcher.LaunchConfig) (*tui.ComposeUpdateReview, error) {
			if r.composeUpdate != composeUpdatePrompt {
				return nil, nil
			}
			return composeReviewForTUI(updated, &r.composeUpdate)
		},
	}, initialStatus, tui.Options{
		NoColor:       r.opts.noColor,
		HighContrast:  r.opts.highContrast,
		GlobalPath:    r.opts.globalPath,
		LocalPath:     r.opts.localPath,
		NoLocalConfig: r.opts.noLocalConfig,
	})
	if err != nil {
		// A cancellation is a quiet exit; any other failure is reported.
		return false, classifyTUIError(err)
	}
	// The operator can switch from Jail to Docker inside the TUI after the
	// initial CLI-side defaults were resolved. Apply the same cwd fallback here
	// so that selecting Container never leaves Docker without a project mount.
	r.launchConfig = launcher.EnsureDockerProjectDir(confirmed)
	// Autosave: running a selection means wanting it back on the next open —
	// with provenance recorded, or the trust boundary would refuse the file
	// the launcher itself wrote. Best-effort: a failed save never blocks a
	// launch, it warns.
	//
	// One exception: a profile the operator loaded and did not edit. Its
	// options block replaced the workspace file's wholesale, so persisting it
	// would delete every option the file declares and the profile omits —
	// silently, on a launch the operator never asked to save. Ctrl+S and
	// --save still write the full selection.
	keepOptions := profileSelection != nil && sameLocalOptions(*profileSelection, r.launchConfig)
	if err := saveLocalSelectionKeeping(r.opts.globalPath, keepOptions, r.opts.localPath, r.local, r.launchConfig, r.errOut); err != nil {
		warnf(r.errOut, "could not save the selection to %s: %v", r.opts.localPath, err)
	}
	return true, nil
}

// sameLocalOptions reports whether two selections persist to the same options
// block. Comparing the projection rather than the launch configs keeps the
// answer about what would be written: a different agent, mount list or
// permission set is not an options edit.
func sameLocalOptions(a, b launcher.LaunchConfig) bool {
	return reflect.DeepEqual(
		optionsFromLaunch(config.Options{}, a),
		optionsFromLaunch(config.Options{}, b),
	)
}

// rememberRecentAgent records the harness for the TUI MRU list (best-effort;
// never block launch). Only the MRU list is persisted: writing the whole
// merged catalog on every launch froze that release's built-ins into the
// user's config.
func (r *launchRequest) rememberRecentAgent() {
	if r.launchConfig.ContinueSession {
		return
	}
	if cmd := strings.TrimSpace(r.launchConfig.Agent.Command); cmd != "" {
		config.TouchRecentAgent(&r.global, cmd)
		_ = config.SaveRecentAgents(r.opts.globalPath, r.global.RecentAgents)
	}
}
