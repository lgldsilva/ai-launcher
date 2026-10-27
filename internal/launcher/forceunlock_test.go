package launcher

import (
	"reflect"
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
	if !reflect.DeepEqual(argv, want) {
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

// Unrelated extra args must not suppress the flag. The scan returns early
// only when a token is exactly --force-unlock; any other token falls through
// and the flag is still appended ahead of extra_args.
func TestForceUnlockAppendsWhenExtraArgsOmitIt(t *testing.T) {
	argv := buildMemoryRun(t, hostMemoryLaunch("2.6.0", func(cfg *LaunchConfig) {
		cfg.ForceUnlock = true
		cfg.MemoryAutowire = true
		cfg.ExtraArgs = []string{"--model", "sonnet"}
	}))
	want := []string{"ai-memory", "run", "claude", "--force-unlock", "--model", "sonnet"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}

// A copy already present in extra_args is the only copy. The slice holds
// only that flag, so the early return is the equality match itself: a
// negated comparison would append a second --force-unlock.
func TestForceUnlockDedupsExtraArgs(t *testing.T) {
	argv := buildMemoryRun(t, hostMemoryLaunch("2.6.0", func(cfg *LaunchConfig) {
		cfg.ForceUnlock = true
		cfg.MemoryAutowire = true
		cfg.ExtraArgs = []string{"--force-unlock"}
	}))
	want := []string{"ai-memory", "run", "claude", "--force-unlock"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}
