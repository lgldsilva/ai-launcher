package launcher

import (
	"errors"
	"sync"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// stubVersionCommand replaces the upstream --version probe seam with canned
// outputs keyed by the resolved binary path.
func stubVersionCommand(t *testing.T, outputs map[string]string, errs map[string]error) {
	t.Helper()
	original := runVersionCommand
	runVersionCommand = func(path string) ([]byte, error) {
		if err, ok := errs[path]; ok {
			return nil, err
		}
		if output, ok := outputs[path]; ok {
			return []byte(output), nil
		}
		return nil, errors.New("unexpected probe of " + path)
	}
	t.Cleanup(func() { runVersionCommand = original })
	// Neutralize the managed-runner probe by default. Left alone it reads the
	// developer's own ~/.local/share/ai-launcher/bin, which is the machine
	// dependence the lookPathCommand seam was introduced to remove: a report
	// would carry a third row on one box and two on CI.
	stubManagedRunner(t, "")
}

// stubExecutableStat replaces the exec-bit check with a fixture set, so a test
// can describe which paths exist without touching the filesystem.
func stubExecutableStat(t *testing.T, executable map[string]bool) {
	t.Helper()
	original := statExecutable
	statExecutable = func(path string) error {
		if executable[path] {
			return nil
		}
		return errors.New("no such file: " + path)
	}
	t.Cleanup(func() { statExecutable = original })
}

// stubManagedRunner points the managed native runner probe at path, or
// disables it when path is empty.
func stubManagedRunner(t *testing.T, path string) {
	t.Helper()
	original := managedRunnerProbePath
	managedRunnerProbePath = func() string { return path }
	t.Cleanup(func() { managedRunnerProbePath = original })
}

func lookPathAll(command string) (string, error) { return "/bin/" + command, nil }

// These exercise UpstreamReport rather than a wrapper around it: --doctor is
// the only caller, and it formats the report itself. An earlier
// CheckUpstreamVersions turned the same data into []Issue for a pre-flight
// warning that was never wired up, so it was tested code nothing ran.

func TestUpstreamReportFlagsInstallsBelowTheSupportedFloor(t *testing.T) {
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "ai-jail version 1.14.2",
		"/bin/ai-memory": "ai-memory 1.18.0",
	}, nil)
	report := UpstreamReport(lookPathAll, "linux")
	if len(report) != 2 {
		t.Fatalf("report = %#v; want both tools probed", report)
	}
	for _, status := range report {
		if !status.TooOld {
			t.Errorf("%s TooOld = false; %s is below the floor %s", status.Command, status.Version, status.Minimum)
		}
	}
	if report[0].Code != "ai-jail-version-too-old" || report[1].Code != "ai-memory-version-too-old" {
		t.Fatalf("codes = %q, %q", report[0].Code, report[1].Code)
	}
	// --doctor prints the detected version against the floor, so both have to
	// survive the probe.
	if report[0].Version != "1.14.2" || report[0].Minimum != config.MinAIJailVersion {
		t.Errorf("ai-jail status = %#v; want the detected version and the supported floor", report[0])
	}
	if report[1].Version != "1.18.0" || report[1].Minimum != config.MinAIMemoryVersion {
		t.Errorf("ai-memory status = %#v; want the detected version and the supported floor", report[1])
	}
}

func TestUpstreamReportAcceptsCurrentOrNewerInstalls(t *testing.T) {
	for _, outputs := range []map[string]string{
		// Exactly at the floor, and comfortably above it.
		{"/bin/ai-jail": "ai-jail " + config.MinAIJailVersion + " (abc123)", "/bin/ai-memory": "ai-memory " + config.MinAIMemoryVersion},
		{"/bin/ai-jail": "v2.0.0", "/bin/ai-memory": "ai-memory 1.28.1"},
	} {
		stubVersionCommand(t, outputs, nil)
		for _, status := range UpstreamReport(lookPathAll, "linux") {
			if status.TooOld {
				t.Errorf("%s TooOld = true for %q; the floor is met", status.Command, status.Version)
			}
		}
	}
}

// Two-segment semver outputs such as "2.0" must be detected, not discarded as
// unparseable.
func TestUpstreamReportDetectsTwoSegmentSemver(t *testing.T) {
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "ai-jail version 2.0",
		"/bin/ai-memory": "ai-memory 2.0",
	}, nil)
	for _, status := range UpstreamReport(lookPathAll, "linux") {
		if status.Version != "2.0" {
			t.Errorf("%s Version = %q; want \"2.0\"", status.Command, status.Version)
		}
		if status.TooOld {
			t.Errorf("%s TooOld = true for %q; the floor is met", status.Command, status.Version)
		}
	}
}

// A probe that cannot answer must never be reported as too old: availability is
// the LookPath-based pre-flight check's job, and an unreadable version is not
// evidence of an outdated one.
func TestUpstreamReportNeverGuessesFromAFailedProbe(t *testing.T) {
	t.Run("binary missing", func(t *testing.T) {
		stubManagedRunner(t, "")
		report := UpstreamReport(func(string) (string, error) {
			return "", errors.New("missing")
		}, "linux")
		for _, status := range report {
			if !status.Missing || status.TooOld {
				t.Errorf("status = %#v; want Missing without TooOld", status)
			}
		}
	})

	t.Run("probe exits non-zero", func(t *testing.T) {
		stubVersionCommand(t, nil, map[string]error{
			"/bin/ai-jail":   errors.New("exit status 1"),
			"/bin/ai-memory": errors.New("exit status 1"),
		})
		for _, status := range UpstreamReport(lookPathAll, "linux") {
			if status.TooOld || status.Version != "" {
				t.Errorf("status = %#v; a failed probe reports no version", status)
			}
		}
	})

	t.Run("output carries no semver", func(t *testing.T) {
		stubVersionCommand(t, map[string]string{
			"/bin/ai-jail":   "ai-jail dev-build",
			"/bin/ai-memory": "ai-memory dev-build",
		}, nil)
		for _, status := range UpstreamReport(lookPathAll, "linux") {
			if status.TooOld || status.Version != "" {
				t.Errorf("status = %#v; unparseable output reports no version", status)
			}
		}
	})
}

func TestUpstreamReportSkipsAIJailOnWindows(t *testing.T) {
	stubManagedRunner(t, "")
	probed := make([]string, 0)
	original := runVersionCommand
	runVersionCommand = func(path string) ([]byte, error) {
		probed = append(probed, path)
		return []byte("9.9.9"), nil
	}
	t.Cleanup(func() { runVersionCommand = original })
	report := UpstreamReport(lookPathAll, "windows")
	if len(report) != 1 || report[0].Command != "ai-memory" {
		t.Fatalf("report = %#v; ai-jail has no Windows build and must be skipped", report)
	}
	if len(probed) != 1 || probed[0] != "/bin/ai-memory" {
		t.Fatalf("probed = %#v; ai-jail must not even be executed on Windows", probed)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign of the comparison: -1 older, 0 equal, 1 newer
	}{
		{"1.15.0", "1.15.0", 0},
		{"1.14.9", "1.15.0", -1},
		{"1.15.1", "1.15.0", 1},
		{"2.0.0", "1.99.9", 1},
		{"1.15", "1.15.0", 0},
		{"1.15.0", "1.15", 0},
		{"v1.15.0", "1.15.0", 0},
		{"1.15.0-rc1", "1.15.0", 0},
	}
	for _, tc := range cases {
		got := compareVersions(tc.a, tc.b)
		sign := 0
		if got < 0 {
			sign = -1
		} else if got > 0 {
			sign = 1
		}
		if sign != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d; want sign %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// The floor only ever looked down, so an upstream that keeps every flag while
// changing what they mean — ai-jail 1.18.0 — read as healthy in --doctor at the
// exact moment the composed argv stopped working. The ceiling is the report for
// that class; it warns and never blocks, because a newer upstream is not
// automatically broken.
func TestUpstreamReportFlagsInstallsAboveTheTestedCeiling(t *testing.T) {
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "ai-jail " + config.UntestedAIJailVersion,
		"/bin/ai-memory": "ai-memory " + config.UntestedAIMemoryVersion,
	}, nil)
	report := UpstreamReport(lookPathAll, "linux")
	if len(report) != 2 {
		t.Fatalf("report = %#v; want both tools", report)
	}
	for _, status := range report {
		if !status.TooNew {
			t.Errorf("%s TooNew = false; %s is at the untested bound %s", status.Command, status.Version, status.Untested)
		}
		if status.TooOld {
			t.Errorf("%s TooOld = true; a newer install is not stale", status.Command)
		}
	}
	if report[0].UntestedCode != "ai-jail-version-untested" || report[1].UntestedCode != "ai-memory-version-untested" {
		t.Fatalf("untested codes = %q, %q", report[0].UntestedCode, report[1].UntestedCode)
	}
}

// The bound is exclusive, so the last validated version is still clean. Without
// this the report would cry wolf on every install the launcher does support.
func TestUpstreamReportAcceptsTheVersionJustBelowTheCeiling(t *testing.T) {
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "ai-jail 1.20.9",
		"/bin/ai-memory": "ai-memory 1.34.0",
	}, nil)
	for _, status := range UpstreamReport(lookPathAll, "linux") {
		if status.TooNew {
			t.Errorf("%s TooNew = true for %q; the ceiling %s is exclusive", status.Command, status.Version, status.Untested)
		}
		if status.TooOld {
			t.Errorf("%s TooOld = true for %q; it is within range", status.Command, status.Version)
		}
	}
}

// An unreadable probe stays silent in both directions: the launcher reports
// what it measured, and it measured nothing.
func TestUpstreamReportLeavesAnUnreadableVersionUnjudged(t *testing.T) {
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "no version here",
		"/bin/ai-memory": "no version here",
	}, nil)
	for _, status := range UpstreamReport(lookPathAll, "linux") {
		if status.TooOld || status.TooNew {
			t.Errorf("%s judged %#v with no readable version", status.Command, status)
		}
	}
}

// stubJailProbe replaces both halves of the ai-jail probe: the PATH lookup and
// the exec. Stubbing only the exec made these tests pass or fail according to
// whether the machine had ai-jail installed.
func stubJailProbe(t *testing.T, run func(string) ([]byte, error)) func() {
	t.Helper()
	originalLook, originalRun := lookPathCommand, runVersionCommand
	lookPathCommand = func(command string) (string, error) { return "/bin/" + command, nil }
	runVersionCommand = run
	return func() { lookPathCommand, runVersionCommand = originalLook, originalRun }
}

// resetJailVersionCache clears the memoized probe so a test can drive it.
func resetJailVersionCache(t *testing.T) {
	t.Helper()
	jailVersionCache.once = sync.Once{}
	jailVersionCache.value = ""
	t.Cleanup(func() {
		jailVersionCache.once = sync.Once{}
		jailVersionCache.value = ""
	})
}

// The TUI rebuilds its argv preview through ResolveHostBinaries on every
// keystroke and throws the result away, so an un-memoized probe would exec
// ai-jail once per rendered frame — the cost ARCHITECTURE keeps out of the
// event loop on purpose.
func TestDetectJailVersionProbesAtMostOncePerProcess(t *testing.T) {
	resetJailVersionCache(t)
	calls := 0
	restore := stubJailProbe(t, func(string) ([]byte, error) {
		calls++
		return []byte("ai-jail 1.16.0"), nil
	})
	defer restore()

	for i := 0; i < 5; i++ {
		if got := DetectJailVersion(); got != "1.16.0" {
			t.Fatalf("DetectJailVersion() = %q; want 1.16.0", got)
		}
	}
	if calls > 1 {
		t.Errorf("probed %d times; the result is memoized for the process", calls)
	}
}

// A failed probe is remembered as "unknown", not retried on every frame, and
// never mistaken for an old version.
func TestDetectJailVersionCachesAFailedProbe(t *testing.T) {
	resetJailVersionCache(t)
	calls := 0
	restore := stubJailProbe(t, func(string) ([]byte, error) {
		calls++
		return nil, errors.New("boom")
	})
	defer restore()

	for i := 0; i < 3; i++ {
		if got := DetectJailVersion(); got != "" {
			t.Fatalf("DetectJailVersion() = %q; want empty for a failed probe", got)
		}
	}
	if calls > 1 {
		t.Errorf("probed %d times after a failure; want the outcome memoized", calls)
	}
}

// The managed native runner is a second ai-memory install with its own
// lifecycle: PATH's copy moves when Homebrew or cargo says so, this one only
// when `--install` / `--upgrade` runs. Reporting only the first lets a current
// install vouch for a stale one, and the stale one is what Environment()
// exports as AI_MEMORY_NATIVE_BIN.
func TestUpstreamReportJudgesTheManagedRunnerSeparately(t *testing.T) {
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":       "ai-jail " + config.MinAIJailVersion,
		"/bin/ai-memory":     "ai-memory 1.32.2",
		"/managed/ai-memory": "ai-memory 1.24.0",
	}, nil)
	stubManagedRunner(t, "/managed/ai-memory")
	stubExecutableStat(t, map[string]bool{"/managed/ai-memory": true})

	report := UpstreamReport(lookPathAll, "linux")
	if len(report) != 3 {
		t.Fatalf("report = %#v; want a third row for the managed runner", report)
	}
	managed := report[2]
	if managed.Command != ManagedRunnerCommand {
		t.Fatalf("third row = %q; want the managed runner", managed.Command)
	}
	if !managed.TooOld {
		t.Errorf("managed TooOld = false for %q against floor %s", managed.Version, config.MinAIMemoryVersion)
	}
	if report[1].TooOld {
		t.Errorf("PATH ai-memory TooOld = true for %q; only the managed copy is stale", report[1].Version)
	}
	if managed.Code != "ai-memory-native-too-old" {
		t.Errorf("managed code = %q; want a code distinct from the PATH install's", managed.Code)
	}
}

// Most operators never run --install, and the wrapper only consults
// AI_MEMORY_NATIVE_BIN under its Docker shell. A file nobody asked for must not
// add a row, because a Missing row fails the doctor's exit code.
func TestUpstreamReportOmitsAnAbsentManagedRunner(t *testing.T) {
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "ai-jail " + config.MinAIJailVersion,
		"/bin/ai-memory": "ai-memory " + config.MinAIMemoryVersion,
	}, nil)
	stubManagedRunner(t, "/managed/ai-memory")
	stubExecutableStat(t, map[string]bool{})

	if report := UpstreamReport(lookPathAll, "linux"); len(report) != 2 {
		t.Fatalf("report = %#v; an absent managed runner is not a finding", report)
	}
}
