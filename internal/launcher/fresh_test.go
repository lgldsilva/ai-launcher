package launcher

import (
	"os"
	"reflect"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// `ai-memory run --fresh` starts a new native session in the current workstream
// instead of resuming or adopting one. It is the only run-scope flag the
// launcher never exposed.
func TestFreshIsEmittedAsAWrapperFlag(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:     config.Agent{Command: "claude"},
		UseMemory: true,
		Fresh:     true,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	// --fresh goes after the harness: ai-jail 1.18 parses this chain and bails
	// out on any unknown flag before the harness name.
	want := []string{"ai-memory", "run", "claude", "--fresh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

// Without the memory layer there is no wrapper to carry the flag, so it must
// not leak into the harness argv.
func TestFreshIsIgnoredWithoutMemory(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:     config.Agent{Command: "claude"},
		UseMemory: false,
		Fresh:     true,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"claude"}) {
		t.Fatalf("Build() = %#v; --fresh has no meaning without ai-memory", got)
	}
}

// --fresh and --continue ask for opposite things: start a new session versus
// resume the most recent one. Pre-flight says so instead of letting ai-memory
// pick.
func TestFreshConflictsWithContinue(t *testing.T) {
	validator := Validator{
		GOOS:     "linux",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := validator.Validate(LaunchConfig{
		ContinueSession: true,
		UseMemory:       true,
		Fresh:           true,
	})
	found := false
	for _, issue := range issues {
		if issue.Code == "fresh-with-continue" {
			found = true
			if issue.Warning {
				t.Error("asking to both resume and start fresh must fail, not warn")
			}
		}
	}
	if !found {
		t.Fatalf("issues = %#v; want fresh-with-continue", issues)
	}
}

// When the jail is on, --executable points at a host path that must be visible
// inside the sandbox. Nothing mounted it, so the argv named a binary the agent
// could not reach.
func TestExecutableDirectoryIsMountedReadOnly(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:      config.Agent{Command: "claude"},
		Executable: "/opt/tools/bin/claude",
		UseJail:    true,
		UseMemory:  true,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{
		"ai-jail", "--no-docker", "--no-network", "--map", "/opt/tools/bin",
		"ai-memory", "run", "--executable", "/opt/tools/bin/claude", "claude",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want the executable directory mounted read-only:\n%#v", got, want)
	}
}

// A path already covered by a configured mount is not mounted twice.
func TestExecutableInsideAConfiguredMountIsNotRemounted(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:      config.Agent{Command: "claude"},
		Executable: "/work/bin/claude",
		UseJail:    true,
		Mounts:     []config.Mount{{Path: "/work", Mode: "rw"}},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"ai-jail", "--no-docker", "--no-network", "--rw-map", "/work", "/work/bin/claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

// A relative or empty --executable names nothing mountable; the auto-mount
// only applies to absolute host paths.
func TestRelativeExecutableGetsNoAutoMount(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:      config.Agent{Command: "claude"},
		Executable: "bin/claude",
		UseJail:    true,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"ai-jail", "--no-docker", "--no-network", "bin/claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v (no mount for a relative executable)", got, want)
	}
}
