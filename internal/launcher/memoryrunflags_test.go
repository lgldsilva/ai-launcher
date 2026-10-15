package launcher

import (
	"os"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func buildMemoryRun(t *testing.T, cfg LaunchConfig) []string {
	t.Helper()
	cfg.Agent = config.Agent{Command: "claude"}
	argv, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return argv
}

func hostMemoryLaunch(version string, modifiers ...func(*LaunchConfig)) LaunchConfig {
	cfg := LaunchConfig{UseMemory: true, MemoryVersion: version}
	for _, modify := range modifiers {
		modify(&cfg)
	}
	return cfg
}

// --profile and --env-file precede the harness on the versions that accept
// them, and ride through a 2.9 jail unchanged — the dry-run spike verified
// ai-jail forwards both.
func TestMemoryRunOptionsPrecedeTheHarness(t *testing.T) {
	argv := buildMemoryRun(t, hostMemoryLaunch("3.0.0", func(cfg *LaunchConfig) {
		cfg.MemoryProfile = "ci"
		cfg.MemoryEnvFile = "/tmp/env"
	}))
	expectTokens(t, argv, "--profile ci --env-file /tmp/env claude")
	profileAt, envAt, harnessAt := -1, -1, -1
	for i, token := range argv {
		switch token {
		case "--profile":
			profileAt = i
		case "--env-file":
			envAt = i
		case "claude":
			harnessAt = i
		}
	}
	if profileAt < 0 || envAt < 0 || harnessAt < 0 || profileAt > harnessAt || envAt > harnessAt {
		t.Fatalf("argv = %#v; profile and env-file must precede the harness", argv)
	}
}

// Below the ai-memory floor — or with an unreadable probe — nothing is
// emitted; pre-flight owns the refusal. 2.5.9 is the boundary witness: env
// file (2.5.0) and ai-memory's own jail (2.5.2) parse there, the profile
// (2.6.0) does not.
func TestMemoryRunOptionsStaySilentBelowTheirFloors(t *testing.T) {
	for _, tc := range []struct {
		version string
	}{
		{"2.5.9"},
		{"2.4.9"},
		{""},
	} {
		argv := buildMemoryRun(t, hostMemoryLaunch(tc.version, func(cfg *LaunchConfig) {
			cfg.MemoryProfile = "ci"
			cfg.MemoryEnvFile = "/tmp/env"
			cfg.MemoryJail = "github,aws"
		}))
		for _, token := range argv {
			if token == "--profile" {
				t.Fatalf("ai-memory %q: argv = %#v; --profile must stay silent below 2.6.0", tc.version, argv)
			}
		}
		if tc.version == "2.5.9" {
			if !containsArg(argv, "--env-file") || !containsArg(argv, "--jail=github,aws") {
				t.Fatalf("ai-memory 2.5.9: argv = %#v; env-file and memory_jail parse there and must emit on a host launch", argv)
			}
			continue
		}
		for _, token := range argv {
			for _, banned := range []string{"--env-file", "--jail", "--no-jail"} {
				if token == banned || strings.HasPrefix(token, "--jail=") {
					t.Fatalf("ai-memory %q: argv = %#v; must stay silent (%s)", tc.version, argv, token)
				}
			}
		}
	}
}

// The pre-harness options also need the ai-jail side: the 1.x
// managed-harness parser bails on unknown dash tokens before the harness.
// Emission and refusal both key on MinPreHarnessAIJailVersion.
func TestMemoryRunOptionsNeedANewJailWhenJailed(t *testing.T) {
	jailed := hostMemoryLaunch("3.0.0", func(cfg *LaunchConfig) {
		cfg.UseJail = true
		cfg.JailVersion = "2.9.0"
		cfg.MemoryProfile = "ci"
		cfg.MemoryEnvFile = "/tmp/env"
	})
	argv := buildMemoryRun(t, jailed)
	expectTokens(t, argv, "--profile ci --env-file /tmp/env claude")
	if !containsArg(argv, "--map") || !containsArg(argv, "/tmp/env") {
		t.Fatalf("argv = %#v; the env file must ride into the jail read-only", argv)
	}

	oldJail := jailed
	oldJail.JailVersion = "2.2.0"
	for _, token := range buildMemoryRun(t, oldJail) {
		for _, banned := range []string{"--profile", "--env-file"} {
			if token == banned {
				t.Fatalf("argv = %#v; a 2.2 jail cannot forward %s", argv, banned)
			}
		}
	}
}

// memory_jail is the host-launch surface for ai-memory's own jailing:
// opt-out spellings, smart defaults, and an exact toggle list.
func TestMemoryJailForms(t *testing.T) {
	for _, tc := range []struct {
		declared string
		want     string
	}{
		{"no", "--no-jail"},
		{"false", "--no-jail"},
		{"none", "--no-jail"},
		{"smart", "--jail"},
		{"true", "--jail"},
		{"github,aws,no-mise", "--jail=github,aws,no-mise"},
	} {
		argv := buildMemoryRun(t, hostMemoryLaunch("3.0.0", func(cfg *LaunchConfig) {
			cfg.MemoryJail = tc.declared
		}))
		if !containsArg(argv, tc.want) {
			t.Fatalf("memory_jail %q: argv = %#v; want %s", tc.declared, argv, tc.want)
		}
	}
	// Jailed and container launches never carry it: ai-memory ignores --jail
	// inside ai-jail and the image has no ai-jail at all. memoryJailFlag is
	// the gate, so it is what the guard asserts (the docker Build path has
	// its own chain and its own fixtures).
	for _, cfg := range []LaunchConfig{
		{UseMemory: true, MemoryVersion: "3.0.0", UseJail: true, JailVersion: "2.9.0"},
		{UseMemory: true, MemoryVersion: "3.0.0", UseDocker: true},
	} {
		cfg.MemoryJail = "github"
		if flag, ok := memoryJailFlag(cfg); ok {
			t.Fatalf("memory_jail must not ride this launch, but the gate returned %q", flag)
		}
	}
}

func TestMemoryRunFlagIssuesRefuseWhatThePairCannotApply(t *testing.T) {
	stat := func(string) (os.FileInfo, error) { return nil, nil }
	validate := func(cfg LaunchConfig) []Issue {
		return memoryRunFlagIssues(cfg, stat)
	}
	if _, ok := issueByCode(validate(hostMemoryLaunch("2.5.9", func(cfg *LaunchConfig) { cfg.MemoryProfile = "ci" })), "memory-profile-requires-ai-memory-2-6"); !ok {
		t.Fatal("preflight accepted memory_profile below ai-memory 2.6.0")
	}
	if _, ok := issueByCode(validate(hostMemoryLaunch("2.4.9", func(cfg *LaunchConfig) { cfg.MemoryEnvFile = "/tmp/env" })), "memory-env-file-requires-ai-memory-2-5"); !ok {
		t.Fatal("preflight accepted memory_env_file below ai-memory 2.5.0")
	}
	if _, ok := issueByCode(validate(hostMemoryLaunch("2.4.9", func(cfg *LaunchConfig) { cfg.MemoryJail = "github" })), "memory-jail-requires-ai-memory-2-5-2"); !ok {
		t.Fatal("preflight accepted memory_jail below ai-memory 2.5.2")
	}
	jailed := hostMemoryLaunch("3.0.0", func(cfg *LaunchConfig) {
		cfg.UseJail = true
		cfg.JailVersion = "2.2.0"
		cfg.MemoryProfile = "ci"
	})
	if _, ok := issueByCode(validate(jailed), "memory-run-flag-requires-ai-jail-2-9"); !ok {
		t.Fatal("preflight accepted a pre-harness option through an old jail")
	}
	missingFile := hostMemoryLaunch("3.0.0", func(cfg *LaunchConfig) { cfg.MemoryEnvFile = "/tmp/nope" })
	if _, ok := issueByCode(missingFileValidate(missingFile), "memory-env-file-not-found"); !ok {
		t.Fatal("preflight accepted a missing env file")
	}
	warn, ok := issueByCode(validate(jailed2("3.0.0", "2.9.0")), "memory-jail-with-launcher-jail")
	if !ok || !warn.Warning {
		t.Fatalf("memory_jail under the launcher jail must be a warning: %#v", warn)
	}
}

func jailed2(memoryVersion, jailVersion string) LaunchConfig {
	return LaunchConfig{
		UseMemory:     true,
		MemoryVersion: memoryVersion,
		MemoryJail:    "github",
		UseJail:       true,
		JailVersion:   jailVersion,
	}
}

// missingFileValidate runs the real os.Stat so the not-found branch fires.
func missingFileValidate(cfg LaunchConfig) []Issue {
	return memoryRunFlagIssues(cfg, os.Stat)
}

// copilot/copilot-cli parse on 3.0.0 and are refused below it, the same
// memory-harness-version gate the 2.3 names carry.
func TestCopilotHarnessesGateOnAIMemory3(t *testing.T) {
	for _, name := range []string{"copilot", "copilot-cli"} {
		if !config.SupportsMemoryRunHarness(name) {
			t.Fatalf("memoryRunHarnesses is missing %s", name)
		}
		if got := config.MemoryHarnessMinVersion(name); got != config.MinCopilotAIMemoryVersion {
			t.Fatalf("MemoryHarnessMinVersion(%s) = %q; want %s", name, got, config.MinCopilotAIMemoryVersion)
		}
		cfg := LaunchConfig{
			Agent:         config.Agent{Command: name},
			UseMemory:     true,
			MemoryVersion: "2.9.9",
		}
		if _, ok := issueByCode(jailVersionValidate(cfg), "memory-harness-version"); !ok {
			t.Fatalf("preflight accepted harness %s below ai-memory 3.0.0", name)
		}
	}
}
