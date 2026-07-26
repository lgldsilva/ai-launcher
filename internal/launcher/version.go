// Package launcher builds and validates the argv used to start an AI agent.
package launcher

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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

var semverPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

// UpstreamStatus is what the doctor surface found for one upstream dependency.
// Version is empty when the binary is missing or its output carries no version
// token.
type UpstreamStatus struct {
	Command string
	Path    string
	Version string
	Minimum string
	Code    string
	Missing bool
	TooOld  bool
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
	if goos != "windows" {
		report = append(report, probeUpstream(lookPath, "ai-jail", config.MinAIJailVersion, "ai-jail-version-too-old"))
	}
	report = append(report, probeUpstream(lookPath, "ai-memory", config.MinAIMemoryVersion, "ai-memory-version-too-old"))
	return report
}

// CheckUpstreamVersions reports advisory warnings when an installed upstream
// CLI is older than the minimum pinned in the config package. Any probe failure
// (missing binary, timeout, unparseable output) is silent: availability
// problems are already reported by the LookPath-based pre-flight checks, and an
// old version must never block a launch.
func CheckUpstreamVersions(lookPath func(string) (string, error), goos string) []Issue {
	issues := make([]Issue, 0)
	for _, status := range UpstreamReport(lookPath, goos) {
		if !status.TooOld {
			continue
		}
		issues = append(issues, Issue{
			Code:    status.Code,
			Message: fmt.Sprintf("%s %s is older than the required %s; upgrade with --upgrade", status.Command, status.Version, status.Minimum),
			Warning: true,
		})
	}
	return issues
}

// probeUpstream resolves one tool and reads its --version output.
func probeUpstream(lookPath func(string) (string, error), command, minimum, code string) UpstreamStatus {
	status := UpstreamStatus{Command: command, Minimum: minimum, Code: code}
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
	status.TooOld = status.Version != "" && compareVersions(status.Version, minimum) < 0
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
