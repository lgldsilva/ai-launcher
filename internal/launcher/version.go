// Package launcher builds and validates the argv used to start an AI agent.
package launcher

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// versionProbeTimeout caps each upstream --version call so a hung wrapper
// never stalls pre-flight validation.
const versionProbeTimeout = 5 * time.Second

// runVersionCommand executes "<path> --version" and returns its combined
// output. It is a seam so tests can stub upstream CLIs without spawning
// processes.
var runVersionCommand = func(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, path, "--version").CombinedOutput() // #nosec G204 -- path comes from LookPath of a fixed tool name
}

var semverPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// UpstreamStatus is what the doctor surface found for one upstream dependency.
// Version is empty when the binary is missing or its output carries no version
// token.
type UpstreamStatus struct {
	Command string
	Path    string
	Version string
	Minimum string
	// Untested is the first version the launcher's argv was not validated
	// against (exclusive). TooNew reports the installed version reaching it.
	Untested string
	Code     string
	// UntestedCode is the stable code for the TooNew report, kept separate
	// from Code so a caller can tell "too old to run" from "newer than what
	// was validated" without parsing the message.
	UntestedCode string
	Missing      bool
	TooOld       bool
	TooNew       bool
}

// UpstreamReport probes the upstream CLIs this launcher composes with and
// reports what is installed. It execs one child process per tool, so it belongs
// to explicit diagnostics (--doctor) and never to pre-flight validation, which
// runs on every launch and inside the TUI event loop. ai-jail is skipped on
// Windows, where no upstream build exists.
func UpstreamReport(lookPath func(string) (string, error), goos string) []UpstreamStatus {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	report := make([]UpstreamStatus, 0, 2)
	if goos != config.PlatformWindows {
		report = append(report, probeUpstream(lookPath, config.AIJailCommand,
			config.MinAIJailVersion, config.UntestedAIJailVersion,
			"ai-jail-version-too-old", "ai-jail-version-untested"))
	}
	report = append(report, probeUpstream(lookPath, config.AIMemoryCommand,
		config.MinAIMemoryVersion, config.UntestedAIMemoryVersion,
		"ai-memory-version-too-old", "ai-memory-version-untested"))
	return report
}

// jailVersionCache memoizes the ai-jail probe for the life of the process.
// The cache is not an optimization detail, it is what keeps the probe out of
// the TUI event loop: the TUI rebuilds its argv preview through
// ResolveHostBinaries on every keystroke and discards the result, so an
// un-memoized probe would exec ai-jail once per rendered frame. An operator's
// ai-jail does not change mid-session, so one probe per process is also the
// correct answer, not merely the cheap one.
var jailVersionCache struct {
	once  sync.Once
	value string
}

// DetectJailVersion reads the installed ai-jail version, or returns "" when
// ai-jail is absent or its output carries no version token. The probe runs at
// most once per process and never from the validator, which stays hermetic.
//
// An unreadable probe returns "" on purpose. "Version unknown" has to stay
// distinguishable from "version too old", or a host where the probe merely
// failed would be refused a launch it can perfectly well perform.
func DetectJailVersion() string {
	jailVersionCache.once.Do(func() { jailVersionCache.value = probeJailVersion() })
	return jailVersionCache.value
}

func probeJailVersion() string {
	path, err := exec.LookPath(config.AIJailCommand)
	if err != nil {
		return ""
	}
	output, err := runVersionCommand(path)
	if err != nil {
		return ""
	}
	return semverPattern.FindString(string(output))
}

// probeUpstream resolves one tool and reads its --version output. untested is
// an exclusive bound: an install at or above it is flagged TooNew.
func probeUpstream(lookPath func(string) (string, error), command, minimum, untested, code, untestedCode string) UpstreamStatus {
	status := UpstreamStatus{
		Command: command, Minimum: minimum, Untested: untested,
		Code: code, UntestedCode: untestedCode,
	}
	path, err := lookPath(command)
	if err != nil {
		status.Missing = true
		return status
	}
	status.Path = path
	output, err := runVersionCommand(path)
	if err != nil {
		return status
	}
	status.Version = semverPattern.FindString(string(output))
	if status.Version == "" {
		return status
	}
	status.TooOld = compareVersions(status.Version, minimum) < 0
	status.TooNew = untested != "" && compareVersions(status.Version, untested) >= 0
	return status
}

// compareVersions compares two dotted numeric versions, returning a negative
// number, zero, or a positive number when a is older, equal, or newer than b.
// Missing segments count as zero and non-numeric input compares as empty.
func compareVersions(a, b string) int {
	as := versionSegments(a)
	bs := versionSegments(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

// versionSegments splits a dotted version into numeric segments, dropping
// anything that is not a number (for example a "v" prefix or pre-release tag).
func versionSegments(version string) []int {
	parts := strings.Split(strings.TrimSpace(version), ".")
	segments := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(strings.TrimPrefix(part, "v"))
		if err != nil {
			break
		}
		segments = append(segments, value)
	}
	return segments
}
