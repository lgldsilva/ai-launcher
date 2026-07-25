package launcher

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestBuildMinimalWithMemory(t *testing.T) {
	got, err := Build(LaunchConfig{Agent: config.Agent{Command: "claude"}, UseJail: true, UseMemory: true, Permissions: map[string]bool{"jail": true}})
	want := []string{"ai-jail", "ai-memory", "run", "claude"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
	}
}

func TestBuildAllOptions(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:         config.Agent{Command: "claude"},
		Executable:    "/usr/bin/claude",
		HomeDir:       "/home/lgldsilva",
		UseJail:       true,
		UseMemory:     true,
		NewWorkstream: "ws-test",
		Permissions:   map[string]bool{"ssh": true, "gh": true, "docker": true, "gpu": true},
		Mounts:        []config.Mount{{Path: "/data", Mode: "ro"}, {Path: "/work", Mode: "rw"}},
		Yolo:          true,
		ExtraArgs:     []string{"--model", "sonnet"},
	})
	want := []string{"ai-jail", "--ssh", "--rw-map", "/home/lgldsilva/.config/gh", "--docker", "--gpu", "--map", "/data", "--rw-map", "/work", "ai-memory", "run", "--new", "ws-test", "claude", "--executable", "/usr/bin/claude", "--yolo", "--model", "sonnet"}
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

func TestBuildWithoutJailRunsAgentDirectly(t *testing.T) {
	got, err := Build(LaunchConfig{Agent: config.Agent{Command: "codex"}, UseMemory: false, ExtraArgs: []string{"--foo"}})
	if err != nil || !reflect.DeepEqual(got, []string{"codex", "--foo"}) {
		t.Fatalf("Build() = %#v, %v", got, err)
	}
}

func TestBuildUsesConfiguredExecutablePathWithoutMemory(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:      config.Agent{Command: "xpto"},
		Executable: "/opt/xpto/bin/xpto",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/xpto/bin/xpto"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

func TestBuildRejectsMissingAgent(t *testing.T) {
	if _, err := Build(LaunchConfig{}); err == nil {
		t.Fatal("empty agent should fail")
	}
}

func TestEnvironmentPropagatesMemoryServerURL(t *testing.T) {
	t.Setenv("AI_MEMORY_SERVER_URL", "https://old.example")
	env := Environment(LaunchConfig{
		UseMemory:       true,
		MemoryServerURL: " https://aimemory.example ",
	})

	count := 0
	for _, entry := range env {
		if entry == "AI_MEMORY_SERVER_URL=https://aimemory.example" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("AI_MEMORY_SERVER_URL entries = %d; want exactly one configured value", count)
	}
	for _, entry := range env {
		if entry == "AI_MEMORY_SERVER_URL=https://old.example" {
			t.Fatal("stale AI_MEMORY_SERVER_URL was not replaced")
		}
	}
}

func TestValidatorFindsIssues(t *testing.T) {
	v := Validator{
		LookPath: func(command string) (string, error) {
			if command == "claude" {
				return "/bin/claude", nil
			}
			return "", errors.New("missing")
		},
		Stat: func(path string) (os.FileInfo, error) {
			return nil, errors.New("missing")
		},
	}
	issues := v.Validate(LaunchConfig{Agent: config.Agent{Command: "claude"}, UseJail: false, UseMemory: true, Permissions: map[string]bool{"gpu": true}, Mounts: []config.Mount{{Path: "/missing"}}})
	if len(issues) != 4 {
		t.Fatalf("got %d issues: %#v", len(issues), issues)
	}
}

func TestBuildAppendsDeclaredParamsInDeclarationOrder(t *testing.T) {
	kimi := config.Agent{Command: "kimi", Params: []config.Param{
		{Name: "model", Flag: "--model", TakesValue: true},
		{Name: "query", Flag: "--query", TakesValue: true},
	}}
	got, err := Build(LaunchConfig{
		Agent:       kimi,
		ParamValues: map[string]string{"query": "explain this", "model": "k2"},
	})
	want := []string{"kimi", "--model", "k2", "--query", "explain this"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
	}
}

func TestBuildAppendsBooleanFlagParamOnlyWhenEnabled(t *testing.T) {
	agent := config.Agent{Command: "xpto", Params: []config.Param{{Name: "fast", Flag: "--fast"}}}
	got, err := Build(LaunchConfig{Agent: agent, ParamValues: map[string]string{"fast": "true"}})
	if err != nil || !reflect.DeepEqual(got, []string{"xpto", "--fast"}) {
		t.Fatalf("Build(enabled) = %#v, %v", got, err)
	}
	for _, disabled := range []string{"false", "0", "off", "no", "  "} {
		got, err = Build(LaunchConfig{Agent: agent, ParamValues: map[string]string{"fast": disabled}})
		if err != nil || !reflect.DeepEqual(got, []string{"xpto"}) {
			t.Fatalf("Build(%q) = %#v, %v; want flag omitted", disabled, got, err)
		}
	}
}

func TestBuildIgnoresUndeclaredParamValues(t *testing.T) {
	agent := config.Agent{Command: "goose"}
	got, err := Build(LaunchConfig{
		Agent:       agent,
		ParamValues: map[string]string{"query": "ignored"},
		ExtraArgs:   []string{"--verbose"},
	})
	want := []string{"goose", "--verbose"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
	}
}

func TestBuildUsesAgentYoloFlagWithoutMemory(t *testing.T) {
	claude := config.Agent{Command: "claude", YoloFlag: "--dangerously-skip-permissions"}
	got, err := Build(LaunchConfig{Agent: claude, Yolo: true})
	want := []string{"claude", "--dangerously-skip-permissions"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
	}
}

func TestBuildYoloWithoutDeclaredFlagFallsBackToDashYolo(t *testing.T) {
	got, err := Build(LaunchConfig{Agent: config.Agent{Command: "aider"}, Yolo: true})
	want := []string{"aider", "--yolo"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
	}
}

func TestBuildYoloWithMemoryKeepsDashYoloForAiMemory(t *testing.T) {
	claude := config.Agent{Command: "claude", YoloFlag: "--dangerously-skip-permissions"}
	got, err := Build(LaunchConfig{Agent: claude, UseMemory: true, Yolo: true})
	want := []string{"ai-memory", "run", "claude", "--yolo"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
	}
}

func TestBuildYoloDoesNotDuplicateFlagPresentInExtraArgs(t *testing.T) {
	claude := config.Agent{Command: "claude", YoloFlag: "--dangerously-skip-permissions"}
	got, err := Build(LaunchConfig{Agent: claude, Yolo: true, ExtraArgs: []string{"--dangerously-skip-permissions", "--verbose"}})
	want := []string{"claude", "--dangerously-skip-permissions", "--verbose"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
	}
}
