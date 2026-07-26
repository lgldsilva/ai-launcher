package main

import (
	"bytes"
	"strings"
	"testing"
)

// runCapture runs the CLI and returns stdout and stderr separately.
func runCapture(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = run(args, strings.NewReader(""), &out, &errOut)
	return out.String(), errOut.String(), err
}

// --dry-run is the diagnostic surface the README advertises, so it must not
// print a command that pre-flight would reject. The argv is still printed —
// seeing what would run is the whole point — but a fatal issue is reported and
// makes the run fail.
func TestDryRunReportsPreflightIssues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: false\n")
	stdout, stderr, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--ssh", "--dry-run")
	if err == nil {
		t.Fatal("run() = nil; a fatal pre-flight issue must fail the dry-run")
	}
	if !strings.Contains(stderr, "permission-without-jail") {
		t.Errorf("stderr = %q; want the pre-flight issue reported", stderr)
	}
	if !strings.Contains(stdout, "custom-cli") {
		t.Errorf("stdout = %q; the argv must still be printed for diagnosis", stdout)
	}
}

// A warning is reported but never suppresses the argv nor fails the run.
func TestDryRunSucceedsWithWarningsOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; a valid configuration must dry-run cleanly", err)
	}
	if !strings.Contains(stdout, "ai-jail") {
		t.Errorf("stdout = %q; want the jail chain", stdout)
	}
}
