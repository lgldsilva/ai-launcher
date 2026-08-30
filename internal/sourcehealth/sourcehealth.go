// Package sourcehealth classifies the two questions a source_url install has
// to answer: what kind of script did we just download, and what did a version
// probe actually prove about the installed command.
//
// It is pure on purpose — no filesystem, no exec, no network. The I/O packages
// that call it (internal/installer, internal/cmd) are excluded from the logic
// coverage boundary, so a classification that decides whether a vendor
// installer is stored as a command or executed would otherwise ship untested.
// Keeping the decision here puts it under unit and mutation testing instead.
//
// The failure this guards against is concrete: `curl … /install.sh | bash`
// scripts saved at the command's own PATH entry find their own install target
// already present (themselves), print "already installed", and exit 0. The
// command never works, the exit code says it does, and the state file caches
// that verdict so later runs keep reporting "current".
package sourcehealth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// ScriptKind is what a downloaded script is, judging its content.
type ScriptKind int

const (
	// KindLauncher is a versionless wrapper that IS the command: it stays at
	// the install path and manages the real binary itself (ai-memory's
	// bin/ai-memory wrapper is the reference case).
	KindLauncher ScriptKind = iota
	// KindInstaller is a one-shot bootstrapper that downloads a release and
	// writes the real executable somewhere else. Storing it at the command
	// path breaks the command; it must be executed instead.
	KindInstaller
)

// installerSignatures are the markers that only make sense in a script meant
// to be run once. They are deliberately narrow: a script that merely fetches
// something (curl -fsSL appears in managed wrappers too) is not an installer,
// and misclassifying a wrapper as an installer would delete a working command.
var (
	// installTargetAssignment covers the variable names vendors use to hold
	// where the real binary goes.
	installTargetAssignment = regexp.MustCompile(`(?i)\b(?:INSTALL_DIR|INSTALL_PREFIX|LOCAL_BIN_DIR|BIN_DIR|TARGET_DIR|DEST_DIR|DOWNLOAD_DIR|DOWNLOAD_BASE_URL|DOWNLOAD_URL|VERSIONS_DIR)\s*=`)
	// preExistenceNotice is the self-detect deadlock: "already installed"
	// combined with the destructive `rm "<binary>"` suggestion that follows it.
	preExistenceNotice   = regexp.MustCompile(`(?is)already installed.{0,400}?(?:rm "|delete the binary|fresh installation)`)
	installScriptName    = regexp.MustCompile(`(?i)\binstall\.sh\b`)
	releaseFetchLanguage = regexp.MustCompile(`(?i)(?:extracting binary|downloading release package|release package\.\.\.|checksum verified)`)
)

// ClassifyScript reports whether a downloaded script is a one-shot installer.
//
// Only the head is scanned. A source_url recipe can point at anything a vendor
// publishes, and reading a 180 MB native binary into a string to look for shell
// assignments would be both slow and meaningless — a Mach-O is not a script.
func ClassifyScript(script []byte) ScriptKind {
	text := string(headOf(script, sniffLimit))
	switch {
	case installTargetAssignment.MatchString(text):
		return KindInstaller
	case preExistenceNotice.MatchString(text):
		return KindInstaller
	case installScriptName.MatchString(text):
		return KindInstaller
	case releaseFetchLanguage.MatchString(text):
		return KindInstaller
	default:
		return KindLauncher
	}
}

// sniffLimit bounds how much of a script is scanned for installer signatures.
// Every real bootstrapper in the corpus declares its install directory inside
// the first few kilobytes; the limit keeps a mistaken binary from being scanned
// whole without changing any verdict.
const sniffLimit = 64 << 10

func headOf(data []byte, limit int) []byte {
	if len(data) <= limit {
		return data
	}
	return data[:limit]
}

// LooksLikeInstaller reports whether the script must be executed rather than
// stored at the command path.
func LooksLikeInstaller(script []byte) bool { return ClassifyScript(script) == KindInstaller }

// interpreterPattern reads a shebang line to pick the interpreter an installer
// expects; vendors differ (bash-only versus POSIX sh) and running a bash script
// under sh fails in ways that look like a broken download.
var interpreterPattern = regexp.MustCompile(`^#!.*\b(bash|zsh|ksh|dash|sh)\b`)

// Interpreter returns the interpreter to run script with, defaulting to sh.
func Interpreter(script []byte) string {
	head := script
	if len(head) > 256 {
		head = head[:256] // the shebang is the first line; never scan a large file
	}
	match := interpreterPattern.FindStringSubmatch(string(head))
	if match == nil {
		return "sh"
	}
	return match[1]
}

// ExistingVerdict is what the file already sitting at the install target tells
// us about a source recipe, before deciding to download-run anything.
type ExistingVerdict int

const (
	// ExistingNothing means there is no usable file at the install target.
	ExistingNothing ExistingVerdict = iota
	// ExistingCurrentWrapper means the target holds this exact recipe script and
	// the recipe is a managed wrapper, so it is the command and it is up to date.
	ExistingCurrentWrapper
	// ExistingCurrentProduced means the target holds an executable produced by an
	// earlier run of this same installer recipe — a native binary, or a vendor
	// launcher script (Cursor Agent links one). Re-running the bootstrapper here
	// would re-fetch a whole release, and a bootstrapper with a pre-existence
	// check would refuse anyway.
	ExistingCurrentProduced
	// ExistingResidue means the target holds an installer script — the failure
	// state itself, not a command.
	ExistingResidue
	// ExistingChanged means the recipe's own bytes changed upstream, so the
	// install has to be redone; calling that current is how an update is lost.
	ExistingChanged
)

// DecideExisting judges what to do with a file already at the install target.
//
// recordedKind and recordedDigest come from the launcher's own install state:
// kind is "installer" when an earlier run executed this recipe rather than
// storing it, and digest is the sha256 of the recipe script that was run. They
// are what separates two cases that look identical on disk — a vendor launcher
// script the installer produced, and a stale wrapper whose recipe has moved.
func DecideExisting(stored, recipe []byte, recordedKind, recordedDigest string) ExistingVerdict {
	if len(stored) == 0 {
		return ExistingNothing
	}
	if LooksLikeInstaller(stored) {
		return ExistingResidue
	}
	if bytes.Equal(stored, recipe) {
		if LooksLikeInstaller(recipe) {
			return ExistingResidue
		}
		return ExistingCurrentWrapper
	}
	if recordedKind == "installer" && recordedDigest != "" && recordedDigest == Digest(recipe) {
		return ExistingCurrentProduced
	}
	if !IsScript(stored) {
		// A native binary at the path: something produced it, and it is not this
		// script. Treat it as current only when the recipe is an installer, which
		// is the case where the target legitimately holds a different file.
		if LooksLikeInstaller(recipe) {
			return ExistingCurrentProduced
		}
	}
	return ExistingChanged
}

// IsScript reports whether content begins with a shebang, i.e. it is a script
// rather than a compiled executable.
func IsScript(content []byte) bool {
	return bytes.HasPrefix(content, []byte("#!"))
}

// Digest is the hex sha256 used to notice an upstream recipe change.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// String names the verdict for logs.
func (v ExistingVerdict) String() string {
	switch v {
	case ExistingCurrentWrapper:
		return "current-wrapper"
	case ExistingCurrentProduced:
		return "current-produced"
	case ExistingResidue:
		return "installer-residue"
	case ExistingChanged:
		return "recipe-changed"
	default:
		return "nothing"
	}
}

// ProbeState is the verdict of asking an installed command for its version.
type ProbeState int

const (
	// ProbeHealthy means the command answered with a version: it works.
	ProbeHealthy ProbeState = iota
	// ProbeInstallerLike means the answer was installer usage text or an
	// "already installed" notice, which proves the file at that path is an
	// installer rather than the command it claims to be.
	ProbeInstallerLike
	// ProbeSilent means the command ran but said nothing usable. It is not
	// proof of health, and not proof of the installer bug either.
	ProbeSilent
	// ProbeFailed means the command exited non-zero with output that does not
	// match the installer signature.
	ProbeFailed
)

// installerProbeMarkers are the strings these vendors print when a one-shot
// installer is invoked with `--version` instead of being piped to a shell.
var installerProbeMarkers = []string{
	"usage: install.sh",
	"already installed at",
	"invalid version format",
	"requires a version argument",
	"unknown parameter",
	"agent installer",
	"cli installer",
}

// versionToken accepts the shapes real CLIs print: `1.2.3`, `grok 1.0.5 (…)`,
// `kimi, version 1.41.0`, `omp/18.0.11`, `codex-cli 0.151.0`.
var versionToken = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// Probe is the classified result of one version probe.
type Probe struct {
	State   ProbeState
	Version string
}

// ClassifyProbe judges a version probe from its combined output and whether
// the command exited cleanly. output may hold text captured even on failure.
func ClassifyProbe(output string, exitOK bool) Probe {
	lower := strings.ToLower(output)
	if hasInstallerMarker(lower) {
		return Probe{State: ProbeInstallerLike}
	}
	version := versionToken.FindString(output)
	if version != "" {
		// Some launchers print a version and exit non-zero behind a warning.
		return Probe{State: ProbeHealthy, Version: version}
	}
	if strings.Contains(lower, "usage:") {
		// Usage text with no version anywhere means the file identifies itself
		// as a script, not as a versioned CLI. That is how every installer in
		// the incident answered --version, whatever the exit code claimed.
		return Probe{State: ProbeInstallerLike}
	}
	if exitOK {
		return Probe{State: ProbeSilent}
	}
	return Probe{State: ProbeFailed}
}

// hasInstallerMarker reports the exact strings these vendors emit when a
// one-shot installer is invoked with `--version` instead of piped to a shell.
func hasInstallerMarker(lower string) bool {
	for _, marker := range installerProbeMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// String names the verdict for logs and error messages.
func (p ProbeState) String() string {
	switch p {
	case ProbeHealthy:
		return "healthy"
	case ProbeInstallerLike:
		return "installer-in-path"
	case ProbeSilent:
		return "unresponsive"
	default:
		return "failed"
	}
}
