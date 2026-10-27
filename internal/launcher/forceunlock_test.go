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

func TestForceUnlockDedupsExtraArgs(t *testing.T) {
	// The duplicate is the only extra arg. A later unrelated token would
	// still leave a single --force-unlock even if the equality check were
	// inverted, so it would not prove the dedup.
	argv := buildMemoryRun(t, hostMemoryLaunch("2.6.0", func(cfg *LaunchConfig) {
		cfg.ForceUnlock = true
		cfg.ExtraArgs = []string{"--force-unlock"}
	}))
	if countArg(argv, "--force-unlock") != 1 {
		t.Fatalf("argv = %#v; --force-unlock must appear once", argv)
	}
}

func TestForceUnlockAppendsBesideOtherExtraArgs(t *testing.T) {
	argv := buildMemoryRun(t, hostMemoryLaunch("2.6.0", func(cfg *LaunchConfig) {
		cfg.ForceUnlock = true
		cfg.ExtraArgs = []string{"--model", "sonnet"}
	}))
	if countArg(argv, "--force-unlock") != 1 {
		t.Fatalf("argv = %#v; --force-unlock must be emitted once next to other extra args", argv)
	}
}

func countArg(argv []string, want string) int {
	count := 0
	for _, token := range argv {
		if token == want {
			count++
		}
	}
	return count
}
