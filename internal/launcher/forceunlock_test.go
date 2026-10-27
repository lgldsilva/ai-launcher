package launcher

import (
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestForceUnlockEmitsAfterHarness(t *testing.T) {
	argv := buildMemoryRun(t, hostMemoryLaunch("2.6.0", func(cfg *LaunchConfig) {
		cfg.ForceUnlock = true
		cfg.MemoryAutowire = true
		cfg.Workstream = "release-1"
	}))
	want := []string{"ai-memory", "run", "--workstream", "release-1", "claude", "--force-unlock"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}

func TestForceUnlockSilentBelowFloor(t *testing.T) {
	argv := buildMemoryRun(t, hostMemoryLaunch("2.5.9", func(cfg *LaunchConfig) {
		cfg.ForceUnlock = true
	}))
	if containsArg(argv, "--force-unlock") {
		t.Fatalf("argv = %#v; --force-unlock must stay silent below 2.6.0", argv)
	}
}

func TestForceUnlockRefusedBelowFloor(t *testing.T) {
	issues := (&Validator{}).Validate(LaunchConfig{
		Agent:         config.Agent{Command: "claude"},
		UseMemory:     true,
		MemoryVersion: "2.5.9",
		ForceUnlock:   true,
	})
	if _, ok := issueByCode(issues, "memory-force-unlock-requires-ai-memory-2-6"); !ok {
		t.Fatalf("preflight = %#v; want force_unlock floor refusal", issues)
	}
}

func TestForceUnlockEmittedOnContinue(t *testing.T) {
	argv, err := Build(LaunchConfig{
		Agent:           config.Agent{Command: "claude"},
		UseMemory:       true,
		MemoryVersion:   "2.6.0",
		ContinueSession: true,
		ForceUnlock:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsArg(argv, "--force-unlock") {
		t.Fatalf("continue argv = %#v; want --force-unlock", argv)
	}
}

// The duplicate is the only extra arg. A later unrelated token would still
// leave a single --force-unlock if the equality check were inverted, so it
// would not prove the dedup. The flag from extra_args stays after
// --no-autowire; an inverted check would also prepend a second copy.
func TestForceUnlockDedupsExtraArgs(t *testing.T) {
	argv := buildMemoryRun(t, hostMemoryLaunch("2.6.0", func(cfg *LaunchConfig) {
		cfg.ForceUnlock = true
		cfg.ExtraArgs = []string{"--force-unlock"}
	}))
	want := []string{"ai-memory", "run", "claude", "--no-autowire", "--force-unlock"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}

// Unrelated extra args must not suppress the flag. An inverted equality
// returns on the first other token and drops --force-unlock.
func TestForceUnlockAppendsBesideOtherExtraArgs(t *testing.T) {
	argv := buildMemoryRun(t, hostMemoryLaunch("2.6.0", func(cfg *LaunchConfig) {
		cfg.ForceUnlock = true
		cfg.ExtraArgs = []string{"--model", "sonnet"}
	}))
	want := []string{"ai-memory", "run", "claude", "--force-unlock", "--no-autowire", "--model", "sonnet"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}
