package launcher

import (
	"errors"
	"os"
	"reflect"
	"strings"
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

func TestBuildRemapsOcWrapperToOpencodeHarness(t *testing.T) {
	// Catalog command is "oc" (preset selector); ai-memory only accepts
	// "opencode". The remap comes from the catalog entry, not from a hardcoded
	// case in the builder: with agents merged per entry, run_harness always
	// reaches this point.
	var oc config.Agent
	for _, agent := range config.DefaultGlobal().Agents {
		if agent.Command == "oc" {
			oc = agent
		}
	}
	if oc.Memory == nil || oc.Memory.RunHarness != "opencode" {
		t.Fatalf("catalog entry for oc = %#v; want run_harness opencode", oc)
	}
	got, err := Build(LaunchConfig{
		Agent:      oc,
		Executable: "/Users/me/.local/bin/oc",
		UseJail:    false,
		UseMemory:  true,
	})
	want := []string{"ai-memory", "run", "opencode", "--executable", "/Users/me/.local/bin/oc"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build(oc) = %#v, %v; want %#v", got, err, want)
	}
	// Without memory, still invoke the wrapper binary directly.
	got, err = Build(LaunchConfig{Agent: oc, Executable: "/bin/oc", UseMemory: false})
	if err != nil || !reflect.DeepEqual(got, []string{"/bin/oc"}) {
		t.Fatalf("Build(oc no-memory) = %#v, %v", got, err)
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
	want := []string{"ai-jail", "--ssh", "--rw-map", "/home/lgldsilva/.config/gh", "--docker", "--gpu", "--map", "/usr/bin", "--map", "/data", "--rw-map", "/work", "ai-memory", "run", "--new", "ws-test", "claude", "--executable", "/usr/bin/claude", "--yolo", "--model", "sonnet"}
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
		Getwd: func() (string, error) { return "/home/dev/project", nil },
		GOOS:  "linux",
	}
	issues := v.Validate(LaunchConfig{Agent: config.Agent{Command: "claude"}, UseJail: false, UseMemory: true, Permissions: map[string]bool{"gpu": true}, Mounts: []config.Mount{{Path: "/missing"}}})
	if len(issues) != 4 {
		t.Fatalf("got %d issues: %#v", len(issues), issues)
	}
}

func TestExternalVolumeCwdByPlatform(t *testing.T) {
	cases := []struct {
		cwd, goos string
		want      bool
	}{
		{"/Volumes/Data/proj", "darwin", true},
		{"/Users/dev/proj", "darwin", false},
		{"/media/usb/proj", "linux", true},
		{"/mnt/disk/proj", "linux", true},
		{"/run/media/u/disk", "linux", true},
		{"/home/dev/proj", "linux", false},
		{"/Volumes/Data", "windows", false},
		{"C:\\Users\\dev", "windows", false},
	}
	for _, tc := range cases {
		if got := externalVolumeCwd(tc.cwd, tc.goos); got != tc.want {
			t.Errorf("externalVolumeCwd(%q, %q) = %v; want %v", tc.cwd, tc.goos, got, tc.want)
		}
	}
}

func TestJailMemoryVolumeIssuesBranches(t *testing.T) {
	// getwd error / empty cwd → no issue
	if issues := jailMemoryVolumeIssues(LaunchConfig{UseJail: true, UseMemory: true}, func() (string, error) {
		return "", errors.New("no cwd")
	}, "darwin"); len(issues) != 0 {
		t.Fatalf("getwd error: %#v", issues)
	}
	if issues := jailMemoryVolumeIssues(LaunchConfig{UseJail: true, UseMemory: true}, func() (string, error) {
		return "  ", nil
	}, "darwin"); len(issues) != 0 {
		t.Fatalf("empty cwd: %#v", issues)
	}
	// non-external → no issue
	if issues := jailMemoryVolumeIssues(LaunchConfig{UseJail: true, UseMemory: true}, func() (string, error) {
		return "/Users/dev/proj", nil
	}, "darwin"); len(issues) != 0 {
		t.Fatalf("home path: %#v", issues)
	}
}

func TestValidatorWarnsJailMemoryOnExternalVolumeCwd(t *testing.T) {
	v := Validator{
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(path string) (os.FileInfo, error) { return nil, nil },
		Getwd:    func() (string, error) { return "/Volumes/MSD512/Projetos/app", nil },
		GOOS:     "darwin",
	}
	// Jail stays allowed on macOS /Volumes; only a warning when memory is also on.
	issues := v.Validate(LaunchConfig{Agent: config.Agent{Command: "pi"}, UseJail: true, UseMemory: true})
	found := false
	for _, issue := range issues {
		if issue.Code == "jail-memory-external-volume" && issue.Warning {
			found = true
		}
		if issue.Code == "jail-memory-external-volume" && !issue.Warning {
			t.Fatalf("jail-memory-external-volume must be a warning, not fatal: %#v", issue)
		}
	}
	if !found {
		t.Fatalf("issues = %#v; want warning jail-memory-external-volume", issues)
	}
	// Jail alone (no memory) — no volume advisory.
	issues = v.Validate(LaunchConfig{Agent: config.Agent{Command: "pi"}, UseJail: true, UseMemory: false})
	for _, issue := range issues {
		if issue.Code == "jail-memory-external-volume" {
			t.Fatalf("jail without memory should not warn about volume: %#v", issues)
		}
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

func TestBuildMapsV115PassthroughPermissions(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:   config.Agent{Command: "claude"},
		UseJail: true,
		Permissions: map[string]bool{
			"display": true, "pictures": true, "tailscale": true,
			"systemd-user": true, "mise": true, "worktree": true,
		},
	})
	want := []string{"ai-jail", "--display", "--pictures", "--tailscale", "--systemd-user", "--mise", "--worktree", "claude"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
	}
}

func TestValidatorWarnsForPermissionUnsupportedOnPlatform(t *testing.T) {
	v := Validator{
		GOOS:     "darwin",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := v.Validate(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		Permissions: map[string]bool{"systemd-user": true},
	})
	if len(issues) != 1 {
		t.Fatalf("issues = %#v; want a single unsupported-platform warning", issues)
	}
	issue := issues[0]
	if issue.Code != "unsupported-platform" || !issue.Warning {
		t.Fatalf("issue = %#v; want an unsupported-platform warning", issue)
	}
	if !strings.Contains(issue.Message, "systemd-user") || !strings.Contains(issue.Message, "darwin") {
		t.Fatalf("message = %q; want the permission id and the platform", issue.Message)
	}
	// The same permission is supported on Linux: no warning there.
	v.GOOS = "linux"
	if issues := v.Validate(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		Permissions: map[string]bool{"systemd-user": true},
	}); len(issues) != 0 {
		t.Fatalf("linux issues = %#v; want none", issues)
	}
}

func TestValidatorIgnoresUnsupportedPlatformForDisabledOrUnknownPermissions(t *testing.T) {
	v := Validator{
		GOOS:     "darwin",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := v.Validate(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		Permissions: map[string]bool{"systemd-user": false, "custom-unknown": true},
	})
	if len(issues) != 0 {
		t.Fatalf("issues = %#v; disabled and unknown permissions must not warn", issues)
	}
}
