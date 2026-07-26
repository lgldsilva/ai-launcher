package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/tui"
)

// Permissions are normalized (dependencies resolved, unknown ids dropped)
// before the CLI flags are merged in, so a dependency pulled in by a flag was
// never resolved. --gpu requires docker, and the argv came out without it —
// while the TUI, which re-normalizes on every toggle, produced the right thing.
func TestCliPermissionFlagsAreNormalized(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--gpu", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stdout, "--docker") {
		t.Fatalf("dry-run = %q; --gpu requires docker, which normalization must pull in", stdout)
	}
}

// --no-memory, --no-jail and --no-yolo are the same kind of flag and must read
// the same way. --no-memory only ever disabled (the false form was inert),
// while its two siblings inverted.
func TestNegativeFlagsShareOneSemantic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--no-memory=false", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stdout, "ai-memory") {
		t.Fatalf("dry-run = %q; --no-memory=false must enable memory, like --no-jail=false enables the jail", stdout)
	}
}

// parseMount splits on every colon, so a directory whose last path element is
// literally "ro" lost it. Mount paths also have to be absolute and cleaned,
// exactly as the TUI already does.
func TestParseMountHandlesPathsEndingInAModeWord(t *testing.T) {
	cases := []struct {
		value string
		want  config.Mount
	}{
		{"/srv/data/ro", config.Mount{Path: "/srv/data/ro", Mode: "rw"}},
		{"/srv/data:ro", config.Mount{Path: "/srv/data", Mode: "ro"}},
		{"/srv/data/../data", config.Mount{Path: "/srv/data", Mode: "rw"}},
	}
	for _, testCase := range cases {
		got, err := parseMount(testCase.value, "rw")
		if err != nil {
			t.Fatalf("parseMount(%q) error = %v", testCase.value, err)
		}
		if got != testCase.want {
			t.Errorf("parseMount(%q) = %#v; want %#v", testCase.value, got, testCase.want)
		}
	}
	if _, err := parseMount("relative/path", "rw"); err == nil {
		t.Error("parseMount() accepted a relative path; a mount must be absolute")
	}
}

// A terminal that cannot be initialized is a real failure. Reporting every TUI
// error as a cancellation exited 0 with no message, which reads as "the UI just
// vanished".
func TestTUIFailureIsNotSilentlyTreatedAsCancellation(t *testing.T) {
	boom := errors.New("open /dev/tty: no such device")
	if err := classifyTUIError(boom); !errors.Is(err, boom) {
		t.Fatalf("classifyTUIError(%v) = %v; a genuine TUI error must propagate", boom, err)
	}
	if err := classifyTUIError(tui.ErrCancelled); err != nil {
		t.Fatalf("classifyTUIError(cancelled) = %v; want nil", err)
	}
}
