package launcher

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
)

func dockerLaunchConfig(t *testing.T) LaunchConfig {
	t.Helper()
	stubAllMountSourcesExist(t)
	selection, err := container.Normalize(
		[]string{"go"},
		[]container.AgentInstall{{Command: "claude", Kind: container.InstallRelease, Version: "2.1.0"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	return LaunchConfig{
		Agent:     config.Agent{Command: "claude"},
		UseDocker: true,
		Workspace: "/home/lgldsilva/work",
		HomeDir:   "/home/lgldsilva",
		Docker: container.RunConfig{
			Selection: selection,
		},
	}
}

// stubAllMountSourcesExist forces the docker mount existence probe to answer
// true so tests can assert the credential mounts without touching the real
// filesystem (the probe is a package variable for exactly this).
func stubAllMountSourcesExist(t *testing.T) {
	t.Helper()
	orig := container.ExistsOnHost
	container.ExistsOnHost = func(string) bool { return true }
	t.Cleanup(func() { container.ExistsOnHost = orig })
}

func TestBuildDockerRunBasic(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got[0] != "docker" || got[1] != "run" {
		t.Fatalf("Build() must start with docker run, got %#v", got)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "/home/lgldsilva/work:/home/lgldsilva/work") {
		t.Fatalf("Build() missing same-path project mount: %s", joined)
	}
	if !strings.Contains(joined, "ai-launcher-box:") {
		t.Fatalf("Build() missing image tag: %s", joined)
	}
	if !strings.Contains(joined, "claude") {
		t.Fatalf("Build() missing in-container executable: %s", joined)
	}
}

func TestBuildDockerRunUsesAiMemoryInsideImage(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Agent = config.Agent{Command: "opencode", SupportsMemory: true, SupportsYolo: true, YoloFlag: "--auto"}
	cfg.UseMemory = true
	cfg.Yolo = true
	cfg.ParamValues = map[string]string{"model": "fixture"}
	cfg.Agent.Params = []config.Param{{Name: "model", Flag: "--model", TakesValue: true}}
	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	tail := got[len(got)-6:]
	want := []string{"ai-memory", "run", "opencode", "--model", "fixture", "--yolo"}
	if !reflect.DeepEqual(tail, want) {
		t.Fatalf("memory Docker command tail = %#v; want %#v", tail, want)
	}
}

func TestBuildDockerRunIncludesExternalWorktreeMounts(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Docker.WorktreeMounts = []string{
		"/home/lgldsilva/work",
		"/home/lgldsilva/work/nested",
		"/Volumes/MSD512/other checkout",
		"/Volumes/MSD512/other checkout",
	}
	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "/home/lgldsilva/work/nested:/home/lgldsilva/work/nested") {
		t.Fatalf("Build() duplicated a worktree already covered by the project mount: %s", joined)
	}
	if strings.Count(joined, "/Volumes/MSD512/other checkout:/Volumes/MSD512/other checkout") != 1 {
		t.Fatalf("Build() external worktree mount count = %d; argv = %s", strings.Count(joined, "/Volumes/MSD512/other checkout:/Volumes/MSD512/other checkout"), joined)
	}
}

func TestBuildDockerRunMapsPermissionsToMounts(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Permissions = map[string]bool{
		config.PermissionSSH:    true,
		config.PermissionGitHub: true,
		config.PermissionDocker: true,
	}
	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"/home/lgldsilva/.ssh:/home/lgldsilva/.ssh:ro",
		"/home/lgldsilva/.config/gh:/home/lgldsilva/.config/gh:ro",
		"/var/run/docker.sock:/var/run/docker.sock",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Build() missing %q in %s", want, joined)
		}
	}
}

func TestBuildDockerRunIncludesAgentConfigMounts(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"/home/lgldsilva/.claude:/home/lgldsilva/.claude",
		"/home/lgldsilva/.claude.json:/home/lgldsilva/.claude.json",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Build() missing %q in %s", want, joined)
		}
	}
}

func TestBuildDockerRunWithExtraArgs(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.ExtraArgs = []string{"--model", "sonnet"}
	cfg.Executable = "/usr/local/bin/claude"
	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	joined := strings.Join(got, " ")
	// The selection agent is InstallRelease, so the in-container executable is
	// the command name (installed on PATH), NOT the host path (a macOS binary
	// that does not exist in the linux container).
	if strings.Contains(joined, "/usr/local/bin/claude") {
		t.Errorf("Build() used the host path for an image-installed agent: %s", joined)
	}
	if !strings.Contains(joined, "claude --model sonnet") {
		t.Errorf("Build() missing command + extra args %s", joined)
	}
}

// A host-mounted agent (InstallHostBinary) runs by its resolved host path,
// which is bind-mounted at the same location inside the container.
func TestBuildDockerRunHostBinaryUsesHostPath(t *testing.T) {
	selection, err := container.Normalize(
		[]string{"go"},
		[]container.AgentInstall{{Command: "kiro-cli", Kind: container.InstallHostBinary, HostPath: "/opt/kiro/bin/kiro"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	cfg := dockerLaunchConfig(t)
	cfg.Agent = config.Agent{Command: "kiro-cli"}
	cfg.Executable = "/opt/kiro/bin/kiro"
	cfg.Docker.Selection = selection
	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "/opt/kiro/bin/kiro") {
		t.Errorf("Build() missing host-binary executable %s", joined)
	}
	if !strings.Contains(joined, "/opt/kiro/bin:/opt/kiro/bin:ro") {
		t.Errorf("Build() missing host-binary dir mount %s", joined)
	}
}

func TestBuildDockerRunRequiresWorkspace(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Workspace = ""
	cfg.Project = ""
	if _, err := Build(cfg); err == nil {
		t.Fatal("Build() with docker and no workspace should error")
	}
}

func TestBuildDockerRunValidatesSelection(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Docker.Selection = container.Selection{Stacks: []string{"cobol"}}
	if _, err := Build(cfg); err == nil {
		t.Fatal("Build() with invalid docker selection should error")
	}
}

func TestBuildDockerRunMutuallyExclusiveWithContinue(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.ContinueSession = true
	cfg.UseMemory = true
	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	// ContinueSession wins (existing precedence), no docker run is emitted.
	if len(got) == 0 || got[0] == "docker" {
		t.Fatalf("Build() with ContinueSession must not emit docker run, got %#v", got)
	}
}

func TestAgentDockerCommands(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	got := agentDockerCommands(cfg)
	want := []string{"claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agentDockerCommands() = %v; want %v", got, want)
	}
	cfg.Agent.Command = ""
	if got := agentDockerCommands(cfg); len(got) != 0 {
		t.Fatalf("agentDockerCommands() with empty command = %v; want none", got)
	}
}

func TestAgentDockerCommandsIncludesSelectedSemidxConfig(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Agent.Command = "pi"
	cfg.Docker.Selection.Tools = []container.ToolInstall{{
		Command: config.SemidxCommand,
		Version: "0.44.9",
		Kind:    container.InstallRelease,
		Release: &config.GitHubRelease{Repository: "lgldsilva/semidx"},
	}}
	got := agentDockerCommands(cfg)
	want := []string{"pi", config.SemidxCommand}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agentDockerCommands() = %v; want %v", got, want)
	}
}

func TestPermissionHomeMount(t *testing.T) {
	tests := []struct {
		name         string
		permission   map[string]bool
		permissionID string
		want         string
	}{
		{"off", nil, config.PermissionSSH, ""},
		{"on with home", map[string]bool{config.PermissionSSH: true}, config.PermissionSSH, "/home/u/.ssh"},
		{"on without home", map[string]bool{config.PermissionSSH: true}, config.PermissionSSH, ""},
	}
	for _, tt := range tests {
		cfg := LaunchConfig{HomeDir: "/home/u", Permissions: tt.permission}
		if tt.name == "on without home" {
			cfg.HomeDir = ""
		}
		got := permissionHomeMount(cfg, tt.permissionID, ".ssh")
		if got != tt.want {
			t.Errorf("%s: permissionHomeMount() = %q; want %q", tt.name, got, tt.want)
		}
	}
}

func TestDockerIssues(t *testing.T) {
	tests := []struct {
		name     string
		cfg      LaunchConfig
		lookPath func(string) (string, error)
		wantCode string
	}{
		{"docker missing", dockerLaunchConfig(t), func(string) (string, error) { return "", errNotFound }, "docker-not-found"},
		{"invalid selection", func() LaunchConfig {
			cfg := dockerLaunchConfig(t)
			cfg.Docker.Selection = container.Selection{Stacks: []string{"cobol"}}
			return cfg
		}(), func(s string) (string, error) { return "/bin/" + s, nil }, "docker-selection-invalid"},
		{"no agent", func() LaunchConfig {
			cfg := dockerLaunchConfig(t)
			selection, _ := container.Normalize([]string{"go"}, nil, nil)
			cfg.Docker.Selection = container.Selection{Stacks: selection.Stacks}
			return cfg
		}(), func(s string) (string, error) { return "/bin/" + s, nil }, "docker-no-agent"},
		{"ok", dockerLaunchConfig(t), func(s string) (string, error) { return "/bin/" + s, nil }, ""},
	}
	for _, tt := range tests {
		issues := dockerIssues(tt.cfg, tt.lookPath)
		if tt.wantCode == "" {
			if len(issues) != 0 {
				t.Errorf("%s: expected no issues, got %#v", tt.name, issues)
			}
			continue
		}
		found := false
		for _, issue := range issues {
			if issue.Code == tt.wantCode {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected issue %q, got %#v", tt.name, tt.wantCode, issues)
		}
	}
}

func TestDockerIssuesWarnsWhenPodmanSocketIsUnavailable(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Docker.Runtime = container.PodmanRuntime{}
	cfg.Permissions = map[string]bool{config.PermissionDocker: true}
	issues := dockerIssues(cfg, func(string) (string, error) { return "/bin/podman", nil })
	for _, issue := range issues {
		if issue.Code == "container-socket-unavailable" && issue.Warning {
			return
		}
	}
	t.Fatalf("dockerIssues() = %#v; want a warning for Podman without a socket path", issues)
}

var errNotFound = errors.New("not found")

// C3: declared params and the yolo intent must reach the Docker memory chain
// (ai-memory receives generic --yolo and translates it for the harness).
func TestBuildDockerRunIncludesParamsAndYolo(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Agent = config.Agent{
		Command:      "claude",
		SupportsYolo: true,
		YoloFlag:     "--dangerously-skip-permissions",
		Params:       []config.Param{{Name: "model", Flag: "--model", TakesValue: true}},
	}
	cfg.ParamValues = map[string]string{"model": "sonnet"}
	cfg.UseMemory = true
	cfg.Yolo = true
	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"--model", "sonnet", "ai-memory", "run", "claude", "--yolo"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Build() missing %q in %s", want, joined)
		}
	}
}

func TestBuildDockerRunUsesAiMemoryYoloFlagWhenMemoryIsEnabled(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Agent = config.Agent{
		Command:      "opencode",
		SupportsYolo: true,
		YoloFlag:     "--auto",
	}
	cfg.UseMemory = true
	cfg.Yolo = true

	got, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !reflect.DeepEqual(got[len(got)-4:], []string{"ai-memory", "run", "opencode", "--yolo"}) {
		t.Fatalf("Build() = %#v; docker memory must invoke ai-memory with generic yolo", got)
	}
	if strings.Contains(strings.Join(got, "\x00"), "--auto") {
		t.Fatalf("Build() = %#v; native opencode flag must be translated by ai-memory", got)
	}
}

// The docker permission switches the image to the CLI-bearing build, and the
// image tag is part of the run argv: build and run must reference one tag or
// the launch dies with "Unable to find image".
func TestBuildDockerRunTagsImageWithDockerCLIOption(t *testing.T) {
	plainCfg := dockerLaunchConfig(t)
	plainArgv, err := Build(plainCfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	cliCfg := dockerLaunchConfig(t)
	cliCfg.Permissions = map[string]bool{config.PermissionDocker: true}
	cliArgv, err := Build(cliCfg)
	if err != nil {
		t.Fatalf("Build() with docker permission error = %v", err)
	}
	plainTag, err := container.ImageTag(plainCfg.Docker.Selection)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	cliTag, err := container.ImageTagWithOptions(cliCfg.Docker.Selection, container.DockerfileOptions{DockerCLI: true})
	if err != nil {
		t.Fatalf("ImageTagWithOptions() error = %v", err)
	}
	plainJoined := strings.Join(plainArgv, " ")
	if !strings.Contains(plainJoined, plainTag) || strings.Contains(plainJoined, cliTag) {
		t.Errorf("run without the docker permission must reference the minimal tag %s: %s", plainTag, plainJoined)
	}
	cliJoined := strings.Join(cliArgv, " ")
	if !strings.Contains(cliJoined, cliTag) || strings.Contains(cliJoined, plainTag) {
		t.Errorf("run with the docker permission must reference the CLI tag %s: %s", cliTag, cliJoined)
	}
}
