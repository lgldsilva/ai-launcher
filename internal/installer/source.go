package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/sourcehealth"
)

// How a source script was installed, recorded in the state file so later runs
// know which idempotence check applies.
const (
	sourceKindWrapper   = "wrapper"
	sourceKindInstaller = "installer"
)

const (
	// probeTimeout bounds one `<command> --version` probe. Probes are the only
	// evidence that a source install produced a working command, so they must
	// never be able to hang an install run; 20s is generous for a wrapper that
	// shells out to a native runner on first use.
	probeTimeout = 20 * time.Second
	// installerTailChars is how much trailing installer output is kept for an
	// error message — enough to name the failure, not a whole download log.
	installerTailChars = 600
)

// runCombinedOutput is the default Run seam: it executes a command, captures
// stdout and stderr together, and never inherits the caller's stdin.
//
// Discarding stdin is load-bearing, not cosmetic. Vendor bootstrappers reach
// for a terminal (interactive login, confirmation prompts, progress bars), and
// an install run that blocks on a TTY nobody can answer turns `ai-launch
// --install` into a hang. Feeding EOF instead makes those steps fail fast, and
// the outcome is judged afterwards by probing the executable they left behind.
func runCombinedOutput(ctx context.Context, name string, args ...string) (string, error) {
	// name is never attacker-controlled text: it is either the interpreter token
	// returned by sourcehealth.Interpreter (a closed set matched out of the
	// shebang: bash|zsh|ksh|dash|sh) or the resolved executable path, and args
	// is a staged file this package wrote. So exec is invoked with no shell and
	// no interpolation of recipe content.
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- closed interpreter set + self-staged script path
	// A nil Stdin makes os/exec connect /dev/null, which is how an installer
	// gets EOF instead of a terminal it would block on.
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// InstallSource installs a command from a trusted HTTPS script URL.
//
// Two different things live behind that field, and conflating them is what
// broke eight agent CLIs at once:
//
//   - a managed wrapper that IS the command (ai-memory's bin/ai-memory): it
//     belongs at the install path and updates itself, so it is stored.
//   - a one-shot vendor bootstrapper (claude, grok, agy, opencode,
//     cursor-agent, devin, kimi, omp): it downloads a release and writes the
//     real executable somewhere else. Storing it at the command path makes the
//     script find its own install target already present — itself — print
//     "already installed", and exit 0. The command never works, the exit code
//     says it does, and the state file caches that verdict as "current".
//
// So the script's kind decides whether it is stored or executed, and the
// behaviour of the resulting command decides whether the install succeeded.
// Neither alone is trustworthy: the text of a script that mentions curl is not
// proof it is an installer, and an exit code of 0 from an installer sitting on
// its own target path is not proof the command runs.
func (i *Installer) InstallSource(ctx context.Context, name, command string, aliases []string, configuredPath, sourceURL string, force bool) (Result, error) {
	if strings.TrimSpace(sourceURL) == "" {
		return Result{Name: name}, errors.New("source URL is empty")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(sourceURL)), "https://") {
		return Result{Name: name}, errors.New("source URL must use HTTPS")
	}
	target, err := i.targetPath(configuredPath, command)
	if err != nil {
		return Result{Name: name}, fmt.Errorf(namedErrorFormat, name, err)
	}
	data, err := i.download(ctx, Asset{Name: sourceURL, BrowserDownloadURL: sourceURL})
	if err != nil {
		return Result{Name: name}, fmt.Errorf("download %s: %w", name, err)
	}
	if len(data) == 0 || !bytes.HasPrefix(data, []byte("#!")) {
		return Result{Name: name}, fmt.Errorf("source does not look like an executable script")
	}

	// Ask what the target already holds before deciding anything; the decision is
	// pure and gated (internal/sourcehealth) because getting it wrong is what lost
	// eight commands their CLIs.
	if !force {
		if result, done, err := i.existingSourceResult(ctx, name, target, data, sourceURL); done {
			return result, err
		}
	}

	if !sourcehealth.LooksLikeInstaller(data) {
		if err := i.installFile(target, data); err != nil {
			return Result{Name: name}, fmt.Errorf("install %s: %w", name, err)
		}
		probe := i.probeCommand(ctx, target)
		if probe.State == sourcehealth.ProbeHealthy || probe.State == sourcehealth.ProbeSilent {
			return i.finishSource(name, target, target, sourceURL, data, sourceKindWrapper, probe, "installed")
		}
		// The wrapper candidate answered like an installer: fall through to the
		// executing path rather than advertising a command that is not there.
		if probe.State != sourcehealth.ProbeInstallerLike {
			return Result{Name: name, Path: target}, fmt.Errorf("%s: source script did not produce a working command (probe verdict %s)", name, probe.State)
		}
	}
	return i.runVendorInstaller(ctx, name, command, aliases, target, sourceURL, data)
}

// existingSourceResult reports the install result when the target already holds
// a command that answers for this recipe, so nothing is downloaded-run or
// re-fetched. done is false when the target must be (re)installed.
func (i *Installer) existingSourceResult(ctx context.Context, name, target string, data []byte, sourceURL string) (Result, bool, error) {
	stored, readErr := os.ReadFile(target) // #nosec G304 -- target is derived from the user's own configuration
	if readErr != nil || !isExecutable(target) {
		return Result{}, false, nil
	}
	entryKind, entryDigest := i.recordedSource(target)
	verdict := sourcehealth.DecideExisting(stored, data, entryKind, entryDigest)
	if verdict != sourcehealth.ExistingCurrentWrapper && verdict != sourcehealth.ExistingCurrentProduced {
		return Result{}, false, nil
	}
	probe := i.probeCommand(ctx, target)
	if probe.State != sourcehealth.ProbeHealthy && probe.State != sourcehealth.ProbeSilent {
		return Result{}, false, nil
	}
	kind := sourceKindInstaller
	if verdict == sourcehealth.ExistingCurrentWrapper {
		kind = sourceKindWrapper
	}
	result, err := i.finishSource(name, target, target, sourceURL, data, kind, probe, "current")
	return result, true, err
}

// recordedSourceKind returns the install state kept for a target: how the
// recipe was installed last time and the digest of the script that was run.
func (i *Installer) recordedSource(target string) (string, string) {
	currentState, err := i.loadState()
	if err != nil {
		return "", ""
	}
	entry, ok := currentState.Installs[target]
	if !ok {
		return "", ""
	}
	return entry.Kind, entry.ScriptDigest
}

// runVendorInstaller executes a one-shot bootstrapper and keeps the executable
// it produced, never the script.
func (i *Installer) runVendorInstaller(ctx context.Context, name, command string, aliases []string, target, sourceURL string, data []byte) (Result, error) {
	if i.goos() == "windows" {
		return Result{Name: name, Path: target}, fmt.Errorf("%s: POSIX installers cannot be executed on Windows; give this recipe a GitHub release asset instead of source_url", name)
	}
	previous, hadPrevious := backupFile(target)
	// A stale copy of the installer at the target path is what triggers the
	// "already installed" deadlock, so the bootstrapper must not see it there.
	if err := removeStaleInstallerScript(target, data); err != nil {
		restoreFile(target, previous, hadPrevious)
		return Result{Name: name, Path: target}, fmt.Errorf("%s: %w", name, err)
	}
	staged, err := i.stageInstallerScript(data)
	if err != nil {
		restoreFile(target, previous, hadPrevious)
		return Result{Name: name}, fmt.Errorf("%s: stage installer: %w", name, err)
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(staged)) }()

	output, runErr := i.run(ctx, sourcehealth.Interpreter(data), staged)
	// The installer's own exit status is not the verdict: several vendors exit
	// non-zero after a successful install when an interactive post-install
	// login is declined in a non-interactive run. What counts is whether an
	// executable that answers to --version now exists.
	resolved, found := i.resolveInstalledCommand(target, command, aliases, data, previous, hadPrevious)
	if !found {
		restoreFile(target, previous, hadPrevious)
		detail := tailForError(output)
		if runErr != nil {
			return Result{Name: name}, fmt.Errorf("%s: installer produced no runnable %q (installer error: %v%s)", name, command, runErr, detail)
		}
		return Result{Name: name}, fmt.Errorf("%s: installer produced no runnable %q%s", name, command, detail)
	}
	probe := i.probeCommand(ctx, resolved)
	if probe.State == sourcehealth.ProbeInstallerLike || probe.State == sourcehealth.ProbeFailed {
		restoreFile(target, previous, hadPrevious)
		return Result{Name: name, Path: resolved}, fmt.Errorf("%s: installed %q but it does not behave like a command (probe verdict %s)", name, resolved, probe.State)
	}
	return i.finishSource(name, target, resolved, sourceURL, data, sourceKindInstaller, probe, "installed")
}

// storedScriptIsInstaller was folded into sourcehealth.DecideExisting, which
// also distinguishes a produced launcher script from a stale recipe.

// removeStaleInstallerScript deletes an installer sitting at the command path.
// Only a script is ever removed: the target may hold the real binary a vendor
// bootstrapper produced, and deleting that would turn a repair into a loss.
func removeStaleInstallerScript(target string, data []byte) error {
	stored, err := os.ReadFile(target) // #nosec G304 -- target is derived from the user's own configuration
	if err != nil || !bytes.HasPrefix(stored, []byte("#!")) {
		return nil // nothing there, or not a script: the bootstrapper decides
	}
	if bytes.Equal(stored, data) || sourcehealth.LooksLikeInstaller(stored) {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove installer script at %s: %w", target, err)
		}
	}
	return nil
}

// stageInstallerScript writes the script outside every PATH directory so it can
// be executed without ever being mistaken for the command again.
func (i *Installer) stageInstallerScript(data []byte) (string, error) {
	root := i.stagingRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "source-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "install.sh")
	// 0600, not 0700: the script is always launched through its interpreter
	// (`bash <path>`), never by exec'ing the path directly, so it needs no
	// execute bit — and nothing else in that private directory can read it.
	if err := os.WriteFile(path, data, 0o600); err != nil { // #nosec G306 -- private staging dir, read only by the interpreter we invoke
		_ = os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

// stagingRoot is where source bootstrappers run from: the launcher's own
// state directory, never a directory on PATH.
func (i *Installer) stagingRoot() string {
	home := i.HomeDir
	if strings.TrimSpace(home) == "" {
		home = mustCurrentDir()
	}
	return filepath.Join(home, ".local", "share", "ai-launcher", "staging")
}

// resolveInstalledCommand finds the executable a vendor installer produced: the
// configured target if it now holds something other than the script and other
// than what was already there, then the command name and its aliases on PATH
// (vendors differ — Cursor Agent installs both `agent` and `cursor-agent`, Kimi
// Code installs under its own home).
//
// The "other than what was already there" rule matters: a pre-existing working
// command at the target must never be mistaken for this install's product, or a
// bootstrapper that silently did nothing would be reported as a success.
func (i *Installer) resolveInstalledCommand(target, command string, aliases []string, data, previous []byte, hadPrevious bool) (string, bool) {
	if isExecutable(target) && !sameBytes(target, data) && (!hadPrevious || !sameBytes(target, previous)) {
		return target, true
	}
	lookPath := i.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	for _, candidate := range append([]string{command}, aliases...) {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if found, err := lookPath(candidate); err == nil && found != "" && isExecutable(found) && !sameBytes(found, data) && (!hadPrevious || found != target) {
			return found, true
		}
	}
	return "", false
}

// probeCommand asks a command for its version and classifies the answer.
func (i *Installer) probeCommand(ctx context.Context, path string) sourcehealth.Probe {
	if strings.TrimSpace(path) == "" {
		return sourcehealth.Probe{State: sourcehealth.ProbeFailed}
	}
	output, err := i.run(ctx, path, "--version")
	return sourcehealth.ClassifyProbe(output, err == nil)
}

// run invokes the exec seam, falling back to the real one when a caller built
// an Installer literal without it.
func (i *Installer) run(ctx context.Context, name string, args ...string) (string, error) {
	runFn := i.Run
	if runFn == nil {
		runFn = runCombinedOutput
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return runFn(pctx, name, args...)
}

// finishSource records the install and reports it, carrying the probed version.
func (i *Installer) finishSource(name, key, resolved, sourceURL string, data []byte, kind string, probe sourcehealth.Probe, status string) (Result, error) {
	currentState, err := i.loadState()
	if err != nil {
		return Result{Name: name, Path: resolved}, err
	}
	currentState.Installs[key] = stateEntry{
		Repository:   sourceURL,
		Tag:          "source",
		Asset:        sourceURL,
		Path:         resolved,
		Kind:         kind,
		ScriptDigest: digestHex(data),
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if err := i.saveState(currentState); err != nil {
		return Result{Name: name, Path: resolved}, err
	}
	return Result{Name: name, Version: probe.Version, Path: resolved, Status: status}, nil
}

// digestHex is the short-form sha256 used to notice an upstream script change.
// It delegates to the pure package so the recipe digest recorded in state and
// the digest compared on disk can never drift apart.
func digestHex(data []byte) string { return sourcehealth.Digest(data) }

// sameBytes compares a file's contents against expected bytes, missing file = no.
func sameBytes(path string, want []byte) bool {
	got, err := os.ReadFile(path) // #nosec G304 -- path is a candidate install location derived from configuration
	return err == nil && bytes.Equal(got, want)
}

// backupFile keeps the current target so a failed install can leave the machine
// exactly as it found it instead of one step worse.
func backupFile(path string) ([]byte, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived from the user's own configuration
	if err != nil {
		return nil, false
	}
	return data, true
}

// restoreFile puts a backed-up target back, or removes the path when there was
// nothing there before.
func restoreFile(path string, data []byte, existed bool) {
	if !existed {
		_ = os.Remove(path)
		return
	}
	_ = os.WriteFile(path, data, 0o755) // #nosec G306 -- restoring the user's own previous executable
}

// tailForError trims installer output to the part that names the failure.
func tailForError(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) > installerTailChars {
		trimmed = trimmed[len(trimmed)-installerTailChars:]
	}
	return " (installer output: " + strings.ReplaceAll(trimmed, "\n", " | ") + ")"
}
