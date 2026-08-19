package launcher

import (
	"errors"
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
