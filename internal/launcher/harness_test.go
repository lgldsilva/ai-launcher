package launcher

import (
	"os"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func harnessValidator() Validator {
	return Validator{
		GOOS:     "linux",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
}

// `ai-memory run` accepts a fixed set of harnesses and rejects everything else
// with an opaque clap error, raised inside the jail where the user cannot see
// it. Pre-flight has to catch it first, and name the escape hatch.
func TestValidateRejectsHarnessUnknownToAiMemory(t *testing.T) {
	for _, harness := range config.MemoryRunHarnesses() {
		issues := harnessValidator().Validate(LaunchConfig{
			Agent:     config.Agent{Command: harness},
			UseMemory: true,
		})
		for _, issue := range issues {
			if issue.Code == "memory-harness-unsupported" {
				t.Errorf("harness %q rejected: %v", harness, issue)
			}
		}
	}

	for _, harness := range []string{"gemini", "cursor-agent", "aider"} {
		issues := harnessValidator().Validate(LaunchConfig{
			Agent:     config.Agent{Command: harness},
			UseMemory: true,
		})
		found := false
		for _, issue := range issues {
			if issue.Code != "memory-harness-unsupported" {
				continue
			}
			found = true
			if issue.Warning {
				t.Errorf("harness %q: issue is a warning; an argv ai-memory rejects must be fatal", harness)
			}
			if !strings.Contains(issue.Message, "--no-memory") {
				t.Errorf("harness %q message = %q; want the --no-memory escape named", harness, issue.Message)
			}
		}
		if !found {
			t.Errorf("harness %q accepted; ai-memory run rejects it", harness)
		}
	}
}

// An explicit run_harness remaps a wrapper onto a supported harness, so the
// wrapper name itself never reaches ai-memory.
func TestValidateAcceptsWrapperWithDeclaredRunHarness(t *testing.T) {
	issues := harnessValidator().Validate(LaunchConfig{
		Agent:     config.Agent{Command: "oc", Memory: &config.MemoryIntegration{RunHarness: "opencode"}},
		UseMemory: true,
	})
	for _, issue := range issues {
		if issue.Code == "memory-harness-unsupported" {
			t.Fatalf("declared run_harness rejected: %v", issue)
		}
	}
}

// Disabling memory removes the constraint entirely: any binary can run under
// the jail alone.
func TestUnsupportedHarnessIsFineWithoutMemory(t *testing.T) {
	issues := harnessValidator().Validate(LaunchConfig{
		Agent:     config.Agent{Command: "gemini"},
		UseMemory: false,
	})
	for _, issue := range issues {
		if issue.Code == "memory-harness-unsupported" {
			t.Fatalf("--no-memory must lift the harness constraint: %v", issue)
		}
	}
}

// Every catalog agent must be honest about memory support: either it maps onto
// a harness ai-memory accepts, or it declares supports_memory false.
func TestCatalogAgentsDeclareMemorySupportHonestly(t *testing.T) {
	for _, agent := range config.DefaultGlobal().Agents {
		if !agent.SupportsMemory {
			continue
		}
		harness := agent.Command
		if agent.Memory != nil && strings.TrimSpace(agent.Memory.RunHarness) != "" {
			harness = strings.TrimSpace(agent.Memory.RunHarness)
		}
		if !config.SupportsMemoryRunHarness(harness) {
			t.Errorf("agent %q claims supports_memory but harness %q is not accepted by ai-memory run", agent.Command, harness)
		}
	}
}
