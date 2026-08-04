package launcher

import (
	"os"
	"reflect"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// `ai-memory run` accepts --workspace, --project, --new, --workstream and
// --yolo with no harness, so a continue session has no reason to drop them.
// Dropping them silently made "--continue --new sprint-3 --yolo" resume the
// previous workstream without yolo — the opposite of what was asked.
func TestContinueKeepsEveryWrapperFlag(t *testing.T) {
	got, err := Build(LaunchConfig{
		ContinueSession: true,
		Agent:           config.Agent{Command: "claude", SupportsYolo: true},
		UseMemory:       true,
		Workspace:       "acme",
		Project:         "billing",
		NewWorkstream:   "sprint-3",
		Yolo:            true,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"ai-memory", "run", "--workspace", "acme", "--project", "billing", "--new", "sprint-3", "--yolo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

// --workstream is the resume form; --new wins when both are set, exactly as in
// a normal launch.
func TestContinueResumesANamedWorkstream(t *testing.T) {
	got, err := Build(LaunchConfig{
		ContinueSession: true,
		UseMemory:       true,
		Workstream:      "release-1",
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"ai-memory", "run", "--workstream", "release-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

// Harness-specific input genuinely cannot apply without a harness. Dropping it
// is correct; dropping it silently is not.
func TestContinueWarnsAboutHarnessOnlyInput(t *testing.T) {
	validator := Validator{
		GOOS:     "linux",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := validator.Validate(LaunchConfig{
		ContinueSession: true,
		UseMemory:       true,
		ExtraArgs:       []string{"--resume", "latest"},
		ParamValues:     map[string]string{"model": "sonnet"},
	})
	found := false
	for _, issue := range issues {
		if issue.Code != "continue-ignores-harness-input" {
			continue
		}
		found = true
		if !issue.Warning {
			t.Error("dropping harness-only input must warn, not fail")
		}
	}
	if !found {
		t.Fatalf("issues = %#v; want continue-ignores-harness-input", issues)
	}
}

// --exec is set for every non-TUI launch, so including it in the "jail options
// without a jail" condition fired the warning on plain --no-jail runs where the
// user had set no jail option at all.
func TestNoJailWarningWithoutAnyJailOption(t *testing.T) {
	validator := Validator{
		GOOS:     "linux",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := validator.Validate(LaunchConfig{
		Agent:    config.Agent{Command: "claude"},
		UseJail:  false,
		JailExec: true,
	})
	for _, issue := range issues {
		if issue.Code == "jail-options-without-jail" {
			t.Fatalf("issues = %#v; --exec alone is not a jail option the user set", issues)
		}
	}

	// A real jail option still warns.
	disabled := false
	issues = validator.Validate(LaunchConfig{
		Agent:     config.Agent{Command: "claude"},
		UseJail:   false,
		JailExec:  true,
		JailFlags: config.JailFlags{Seccomp: &disabled},
	})
	found := false
	for _, issue := range issues {
		if issue.Code == "jail-options-without-jail" {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %#v; want jail-options-without-jail for a configured jail flag", issues)
	}
}

// "Not configured" has to mean "not set". Returning early on an empty value let
// an AI_MEMORY_AUTH_TOKEN or AI_MEMORY_SERVER_URL inherited from the parent
// process (direnv, .envrc, a wrapper script) reach the sandboxed agent and
// point it at somebody else's server.
func TestEmptyValueRemovesTheInheritedVariable(t *testing.T) {
	env := []string{"PATH=/usr/bin", "AI_MEMORY_AUTH_TOKEN=inherited", "HOME=/home/tester"}
	got := upsertEnv(env, "AI_MEMORY_AUTH_TOKEN", "")
	for _, entry := range got {
		if entry == "AI_MEMORY_AUTH_TOKEN=inherited" {
			t.Fatalf("env = %#v; an unconfigured AI_MEMORY_* must be removed, not forwarded", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("env = %#v; want the other entries untouched", got)
	}
}
