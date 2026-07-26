package launcher

import (
	"errors"
	"strings"
	"testing"
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

func TestCheckUpstreamVersionsWarnsWhenInstalledIsOlder(t *testing.T) {
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "ai-jail version 1.14.2",
		"/bin/ai-memory": "ai-memory 1.18.0",
	}, nil)
	issues := CheckUpstreamVersions(lookPathAll, "linux")
	if len(issues) != 2 {
		t.Fatalf("issues = %#v; want both tools reported", issues)
	}
	for _, issue := range issues {
		if !issue.Warning {
			t.Fatalf("issue = %#v; version mismatches must be warnings, never fatal", issue)
		}
	}
	if issues[0].Code != "ai-jail-version-too-old" || issues[1].Code != "ai-memory-version-too-old" {
		t.Fatalf("codes = %q, %q; want ai-jail-version-too-old, ai-memory-version-too-old", issues[0].Code, issues[1].Code)
	}
	if !strings.Contains(issues[0].Message, "1.14.2") || !strings.Contains(issues[0].Message, "1.15.0") {
		t.Fatalf("ai-jail message = %q; want the detected version and the supported floor", issues[0].Message)
	}
	if !strings.Contains(issues[1].Message, "1.18.0") || !strings.Contains(issues[1].Message, "1.19.0") {
		t.Fatalf("ai-memory message = %q; want the detected version and the supported floor", issues[1].Message)
	}
}

func TestCheckUpstreamVersionsIsSilentWhenCurrentOrNewer(t *testing.T) {
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "ai-jail 1.15.0 (abc123)",
		"/bin/ai-memory": "ai-memory 1.19.0",
	}, nil)
	if issues := CheckUpstreamVersions(lookPathAll, "linux"); len(issues) != 0 {
		t.Fatalf("minimum versions produced issues: %#v", issues)
	}
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "v2.0.0",
		"/bin/ai-memory": "ai-memory 1.20.1",
	}, nil)
	if issues := CheckUpstreamVersions(lookPathAll, "linux"); len(issues) != 0 {
		t.Fatalf("newer versions produced issues: %#v", issues)
	}
}

func TestCheckUpstreamVersionsIsSilentOnProbeFailures(t *testing.T) {
	// LookPath failure: availability is reported by the caller's own checks.
	if issues := CheckUpstreamVersions(func(string) (string, error) {
		return "", errors.New("missing")
	}, "linux"); len(issues) != 0 {
		t.Fatalf("lookPath failure produced issues: %#v", issues)
	}
	// Probe execution failure (hung wrapper, non-zero exit) stays silent.
	stubVersionCommand(t, nil, map[string]error{
		"/bin/ai-jail":   errors.New("exit status 1"),
		"/bin/ai-memory": errors.New("exit status 1"),
	})
	if issues := CheckUpstreamVersions(lookPathAll, "linux"); len(issues) != 0 {
		t.Fatalf("probe failure produced issues: %#v", issues)
	}
	// Unparseable output (no semver token) stays silent.
	stubVersionCommand(t, map[string]string{
		"/bin/ai-jail":   "ai-jail dev-build",
		"/bin/ai-memory": "ai-memory dev-build",
	}, nil)
	if issues := CheckUpstreamVersions(lookPathAll, "linux"); len(issues) != 0 {
		t.Fatalf("unparseable output produced issues: %#v", issues)
	}
}

func TestCheckUpstreamVersionsSkipsAIJailOnWindows(t *testing.T) {
	probed := make([]string, 0)
	original := runVersionCommand
	runVersionCommand = func(path string) ([]byte, error) {
		probed = append(probed, path)
		return []byte("9.9.9"), nil
	}
	t.Cleanup(func() { runVersionCommand = original })
	lookPath := func(command string) (string, error) { return "/bin/" + command, nil }
	if issues := CheckUpstreamVersions(lookPath, "windows"); len(issues) != 0 {
		t.Fatalf("windows issues = %#v; want none", issues)
	}
	if len(probed) != 1 || probed[0] != "/bin/ai-memory" {
		t.Fatalf("probed = %#v; ai-jail must be skipped on Windows", probed)
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
