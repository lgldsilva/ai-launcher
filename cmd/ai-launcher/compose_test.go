package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

func TestComposeCommandArgv(t *testing.T) {
	tests := []struct {
		name        string
		runtime     container.Runtime
		action      string
		services    []string
		volumes     bool
		interactive bool
		want        []string
	}{
		{
			name:     "docker up detached",
			runtime:  container.DockerRuntime{},
			action:   "up",
			services: []string{"agent"},
			want:     []string{"docker", "compose", "-f", ".ai-launcher/docker-compose.yaml", "up", "--build", "-d", "agent"},
		},
		{
			name:        "podman up interactive",
			runtime:     container.PodmanRuntime{},
			action:      "up",
			interactive: true,
			want:        []string{"podman", "compose", "-f", ".ai-launcher/docker-compose.yaml", "up", "--build"},
		},
		{
			name:    "nerdctl down volumes",
			runtime: container.NerdctlRuntime{},
			action:  "down",
			volumes: true,
			want:    []string{"nerdctl", "compose", "-f", ".ai-launcher/docker-compose.yaml", "down", "--volumes"},
		},
		{
			name:     "logs service",
			runtime:  container.DockerRuntime{},
			action:   "logs",
			services: []string{"postgres"},
			want:     []string{"docker", "compose", "-f", ".ai-launcher/docker-compose.yaml", "logs", "postgres"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := composeCommandArgv(test.runtime, ".ai-launcher/docker-compose.yaml", test.action, test.services, test.volumes, test.interactive)
			if err != nil {
				t.Fatalf("composeCommandArgv() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("composeCommandArgv() = %#v; want %#v", got, test.want)
			}
		})
	}
}

func TestComposeCommandArgvUsesDockerContext(t *testing.T) {
	got, err := composeCommandArgvWithContext(container.DockerRuntime{}, ".ai-launcher/docker-compose.yaml", "ps", nil, false, false, "remote-builder")
	if err != nil {
		t.Fatalf("composeCommandArgvWithContext() error = %v", err)
	}
	want := []string{"docker", "--context", "remote-builder", "compose", "-f", ".ai-launcher/docker-compose.yaml", "ps"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composeCommandArgvWithContext() = %#v; want %#v", got, want)
	}
}

func TestComposeAgentRunArgvOpensInteractiveAgent(t *testing.T) {
	got, err := composeAgentRunArgvWithContext(container.DockerRuntime{}, ".ai-launcher/docker-compose.yaml", "desktop-linux")
	if err != nil {
		t.Fatalf("composeAgentRunArgvWithContext() error = %v", err)
	}
	want := []string{"docker", "--context", "desktop-linux", "compose", "-f", ".ai-launcher/docker-compose.yaml", "run", "--build", "--rm", "--service-ports", "agent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composeAgentRunArgvWithContext() = %#v; want %#v", got, want)
	}
}

func TestComposeDownDryRunDoesNotNeedCurrentServiceSelection(t *testing.T) {
	var out bytes.Buffer
	err := runComposeCommand([]string{"down", "--volumes"}, launcher.LaunchConfig{}, config.Global{}, cliOptions{dryRun: true}, strings.NewReader(""), &out, &out)
	if err != nil {
		t.Fatalf("runComposeCommand(down) error = %v", err)
	}
	if !strings.Contains(out.String(), "docker compose") || !strings.Contains(out.String(), "--volumes") {
		t.Fatalf("preview = %q; want compose down --volumes", out.String())
	}
}

func TestEnsureComposeArtifactsRefreshesStaleNativeArguments(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	artifactDir := filepath.Join(dir, ".ai-launcher")
	if err := os.MkdirAll(artifactDir, 0o750); err != nil {
		t.Fatal(err)
	}
	stale := "command:\n- pi\n- --yolo\n"
	if err := os.WriteFile(filepath.Join(artifactDir, "docker-compose.yaml"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	selection := container.Selection{
		Stacks: []string{"go"},
		Agents: []container.AgentInstall{{
			Command: "pi",
			Kind:    container.InstallScript,
			Script:  "curl -fsSL https://example.com/pi.sh | bash",
		}},
	}
	cfg := launcher.LaunchConfig{
		Agent:     config.Agent{Command: "pi", SupportsYolo: true, YoloFlag: "--approve"},
		UseDocker: true,
		Yolo:      true,
		Workspace: dir,
		Services:  []string{"redis"},
		Docker: container.RunConfig{
			Selection:  selection,
			ProjectDir: dir,
		},
	}
	if err := ensureComposeArtifacts(cfg, config.DefaultGlobal(), &bytes.Buffer{}, &bytes.Buffer{}, composeUpdateReplace); err != nil {
		t.Fatalf("ensureComposeArtifacts() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(artifactDir, "docker-compose.yaml")) // #nosec G304 -- artifactDir is a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "--yolo") || !strings.Contains(string(data), "--approve") {
		t.Fatalf("Compose retained stale native argument: %s", data)
	}
}

func TestComposeUpdateReviewPreservesManualChangesAndRemembersDecision(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	cfg := launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseDocker: true,
		Workspace: dir,
		Services:  []string{"redis"},
		Docker: container.RunConfig{Selection: container.Selection{Agents: []container.AgentInstall{{
			Command: "custom-cli",
			Kind:    container.InstallScript,
			Script:  "curl -fsSL https://example.com/custom-cli.sh | bash",
		}}}},
	}
	review, err := inspectComposeArtifact(cfg)
	if err != nil {
		t.Fatalf("inspectComposeArtifact() before materialization: %v", err)
	}
	if review.Exists || review.Changed || review.Generated == "" {
		t.Fatalf("initial review = %#v; want missing artifact", review)
	}
	if err := materializeComposeIfNeeded(cfg, composeUpdateReplace, &bytes.Buffer{}); err != nil {
		t.Fatalf("initial materialization: %v", err)
	}
	review, err = inspectComposeArtifact(cfg)
	if err != nil {
		t.Fatal(err)
	}
	manual := strings.Replace(review.Generated, "6379:6379", "16379:6379", 1)
	if manual == review.Generated {
		t.Fatalf("test fixture did not change the generated Redis port: %s", review.Generated)
	}
	if err := os.WriteFile(review.Path, []byte(manual), 0o600); err != nil { // #nosec G306 -- private test fixture.
		t.Fatal(err)
	}
	review, err = inspectComposeArtifact(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Changed || !strings.Contains(review.Diff, "-    - 16379:6379") || !strings.Contains(review.Diff, "+    - 6379:6379") {
		t.Fatalf("manual Compose review = %#v; want the port diff", review)
	}
	if err := materializeComposeIfNeeded(cfg, composeUpdatePrompt, &bytes.Buffer{}); err == nil {
		t.Fatal("prompt policy silently accepted a manually changed Compose file")
	}
	if err := materializeComposeIfNeeded(cfg, composeUpdateKeep, &bytes.Buffer{}); err != nil {
		t.Fatalf("keep manual Compose: %v", err)
	}
	kept, err := os.ReadFile(review.Path) // #nosec G304 -- private test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != manual {
		t.Fatalf("kept Compose = %q; want manual port", kept)
	}
	if err := materializeComposeIfNeeded(cfg, composeUpdatePrompt, &bytes.Buffer{}); err != nil {
		t.Fatalf("approved manual Compose prompted again: %v", err)
	}
	if err := materializeComposeIfNeeded(cfg, composeUpdateReplace, &bytes.Buffer{}); err != nil {
		t.Fatalf("replace manual Compose: %v", err)
	}
	replaced, err := os.ReadFile(review.Path) // #nosec G304 -- private test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if string(replaced) != review.Generated {
		t.Fatalf("replaced Compose was not regenerated: %s", replaced)
	}
}

func TestComposeCommandArgvRejectsInvalidArguments(t *testing.T) {
	for _, test := range []struct {
		action   string
		services []string
		volumes  bool
	}{
		{action: "up", volumes: true},
		{action: "down", services: []string{"agent"}},
		{action: "logs", services: []string{"agent", "redis"}},
		{action: "ps", services: []string{"agent"}},
		{action: "unknown"},
	} {
		if _, err := composeCommandArgv(container.DockerRuntime{}, "compose.yaml", test.action, test.services, test.volumes, false); err == nil {
			t.Fatalf("composeCommandArgv(%q) error = nil", test.action)
		}
	}
}

// Tokens that look like flags after the compose positional must be rejected,
// not silently adopted as service names.
func TestParseComposeInvocationRejectsUnknownFlags(t *testing.T) {
	for _, test := range []struct {
		args []string
		flag string
	}{
		{[]string{"up", "--follow"}, "--follow"},
		{[]string{"up", "agent", "--remove-orphans"}, "--remove-orphans"},
		{[]string{"logs", "-f", "agent"}, "-f"},
	} {
		opts := cliOptions{}
		_, err := parseComposeInvocation(test.args, &opts)
		if err == nil || !strings.Contains(err.Error(), test.flag) {
			t.Fatalf("parseComposeInvocation(%v) error = %v; want rejection naming %q", test.args, err, test.flag)
		}
	}
	opts := cliOptions{}
	invocation, err := parseComposeInvocation([]string{"up", "agent", "redis"}, &opts)
	if err != nil || !reflect.DeepEqual(invocation.services, []string{"agent", "redis"}) {
		t.Fatalf("parseComposeInvocation(services) = %#v, %v; want both services accepted", invocation, err)
	}
}

// writeExecutableStub writes a shell script the runtime tests execute as the
// child process.
func writeExecutableStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stub.sh")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil { // #nosec G306 -- test stub must be executable.
		t.Fatal(err)
	}
	return path
}

func TestRunRuntimeCommandPropagatesChildExitCode(t *testing.T) {
	stub := writeExecutableStub(t, "#!/bin/sh\nexit 42\n")
	err := runRuntimeCommand([]string{stub}, os.Environ(), strings.NewReader(""), io.Discard, io.Discard)
	var exitErr *exitStatusError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 {
		t.Fatalf("runRuntimeCommand() error = %v; want exit status 42", err)
	}
}

// SIGINT must stop the child and return an error (non-zero exit) instead of
// killing the launcher mid-flight and skipping the callers' cleanup.
func TestRunRuntimeCommandInterruptStopsChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGINT delivery differs on Windows")
	}
	stub := writeExecutableStub(t, "#!/bin/sh\nexec sleep 30\n")
	done := make(chan error, 1)
	go func() {
		done <- runRuntimeCommand([]string{stub}, os.Environ(), strings.NewReader(""), io.Discard, io.Discard)
	}()
	// Let the child exec before interrupting.
	time.Sleep(300 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	select {
	case err := <-done:
		var exitErr *exitStatusError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != interruptedExitCode {
			t.Fatalf("runRuntimeCommand() error = %v; want exit status %d", err, interruptedExitCode)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runRuntimeCommand() did not return after SIGINT")
	}
}

// The interactive one-shot must always run compose down — including when the
// agent run failed — and must surface the agent's real exit code.
func TestRunComposeSessionCleansUpAndPropagatesExitCode(t *testing.T) {
	log := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("COMPOSE_STUB_LOG", log)
	stub := writeExecutableStub(t, "#!/bin/sh\necho \"$*\" >> \"$COMPOSE_STUB_LOG\"\ncase \" $* \" in\n  *\" run \"*) exit 5;;\nesac\nexit 0\n")
	composePath := filepath.Join(t.TempDir(), "docker-compose.yaml")
	stubRuntime := composeTestRuntime{command: stub}
	argv, err := composeAgentRunArgvWithContext(stubRuntime, composePath, "")
	if err != nil {
		t.Fatal(err)
	}
	req := &launchRequest{
		in:     strings.NewReader(""),
		out:    io.Discard,
		errOut: io.Discard,
	}
	err = req.runComposeSession(stubRuntime, composePath, argv, true)
	var exitErr *exitStatusError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 5 {
		t.Fatalf("runComposeSession() error = %v; want exit status 5", err)
	}
	data, err := os.ReadFile(log) // #nosec G304 -- test-owned log file.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), " run ") || !strings.Contains(string(data), " down") {
		t.Fatalf("runtime invocations = %q; want the agent run and the down cleanup", data)
	}
}

func TestRunComposeSessionSuccessStopsStack(t *testing.T) {
	log := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("COMPOSE_STUB_LOG", log)
	stub := writeExecutableStub(t, "#!/bin/sh\necho \"$*\" >> \"$COMPOSE_STUB_LOG\"\nexit 0\n")
	composePath := filepath.Join(t.TempDir(), "docker-compose.yaml")
	stubRuntime := composeTestRuntime{command: stub}
	argv, err := composeAgentRunArgvWithContext(stubRuntime, composePath, "")
	if err != nil {
		t.Fatal(err)
	}
	req := &launchRequest{
		in:     strings.NewReader(""),
		out:    io.Discard,
		errOut: io.Discard,
	}
	if err := req.runComposeSession(stubRuntime, composePath, argv, true); err != nil {
		t.Fatalf("runComposeSession() error = %v", err)
	}
	data, err := os.ReadFile(log) // #nosec G304 -- test-owned log file.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), " down") {
		t.Fatalf("runtime invocations = %q; want the down cleanup after a clean run", data)
	}
}

func TestLaunchModeSelection(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  docker: false\n  memory: false\n")

	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--agent", "custom-cli", "--no-memory", "--no-jail", "--docker-backend", "--dry-run")
	if err != nil {
		t.Fatalf("docker run dry-run error = %v", err)
	}
	if !strings.Contains(stdout, "docker run") || strings.Contains(stdout, "docker compose") {
		t.Fatalf("without services = %q; want docker run only", stdout)
	}

	stdout, _, err = runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--agent", "custom-cli", "--no-memory", "--service", "redis", "--dry-run")
	if err != nil {
		t.Fatalf("compose dry-run error = %v", err)
	}
	if !strings.Contains(stdout, "docker compose") || !strings.Contains(stdout, "redis:") {
		t.Fatalf("with services = %q; want compose redis stack", stdout)
	}
}

func TestComposePreviewUsesMaterializedFileAsSourceOfTruth(t *testing.T) {
	dir := t.TempDir()
	artifactDir := filepath.Join(dir, ".ai-launcher")
	if err := os.MkdirAll(artifactDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(artifactDir, "docker-compose.yaml")
	want := "services:\n  edited:\n    image: busybox:1.36\n"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil { // #nosec G306 -- test fixture is private.
		t.Fatal(err)
	}
	got, err := composePreviewYAML(path, launcher.LaunchConfig{})
	if err != nil {
		t.Fatalf("composePreviewYAML() error = %v", err)
	}
	if got != want {
		t.Fatalf("composePreviewYAML() = %q; want materialized YAML %q", got, want)
	}
}

type composeTestRuntime struct {
	command string
}

func (r composeTestRuntime) Name() string             { return "test-runtime" }
func (r composeTestRuntime) Command() string          { return r.command }
func (r composeTestRuntime) HostGateway() string      { return "host.test" }
func (r composeTestRuntime) ComposeCommand() []string { return []string{r.command} }
func (r composeTestRuntime) SocketPath() string       { return "" }
func (r composeTestRuntime) Info() error              { return nil }

func TestLaunchComposeRefreshesArtifactsForCurrentSelection(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	globalPath, _, _ := writeTestConfigs(t, "")
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := filepath.Join(dir, ".ai-launcher")
	if err := os.MkdirAll(artifactDir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Dockerfile", "install-config.yaml", "docker-compose.yaml"} {
		if err := os.WriteFile(filepath.Join(artifactDir, name), []byte("stale-agent"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runtimePath := filepath.Join(t.TempDir(), "compose-runtime")
	if err := os.WriteFile(runtimePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test runtime must be executable.
		t.Fatal(err)
	}
	selection := container.Selection{
		Stacks: []string{"go"},
		Agents: []container.AgentInstall{{
			Command: "custom-cli",
			Kind:    container.InstallScript,
			Script:  "curl -fsSL https://example.com/custom-cli.sh | bash",
		}},
	}
	req := &launchRequest{
		global:        global,
		composeUpdate: composeUpdateReplace,
		launchConfig: launcher.LaunchConfig{
			Agent:     config.Agent{Command: "custom-cli"},
			UseDocker: true,
			Workspace: dir,
			Services:  []string{"redis"},
			Docker: container.RunConfig{
				Runtime:    composeTestRuntime{command: runtimePath},
				Selection:  selection,
				ProjectDir: dir,
			},
		},
		in:     strings.NewReader(""),
		out:    &bytes.Buffer{},
		errOut: &bytes.Buffer{},
	}
	if err := req.launchCompose(); err != nil {
		t.Fatalf("launchCompose() error = %v", err)
	}
	dockerfile, err := os.ReadFile(filepath.Join(artifactDir, "Dockerfile")) // #nosec G304 -- artifactDir is a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "# Agent: custom-cli") || strings.Contains(string(dockerfile), "stale-agent") {
		t.Fatalf("Dockerfile was not refreshed: %q", dockerfile)
	}
	compose, err := os.ReadFile(filepath.Join(artifactDir, "docker-compose.yaml")) // #nosec G304 -- artifactDir is a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "custom-cli") || strings.Contains(string(compose), "stale-agent") {
		t.Fatalf("Compose file was not refreshed: %q", compose)
	}
}
