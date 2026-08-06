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
		"/home/lgldsilva/.claude:/home/lgldsilva/.claude:ro",
		"/home/lgldsilva/.claude.json:/home/lgldsilva/.claude.json:ro",
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
	if !strings.Contains(joined, "/usr/local/bin/claude") {
		t.Errorf("Build() missing executable %s", joined)
	}
	if !strings.Contains(joined, "--model sonnet") {
		t.Errorf("Build() missing extra args %s", joined)
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

var errNotFound = errors.New("not found")
