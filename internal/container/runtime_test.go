package container

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeImplementations(t *testing.T) {
	tests := []struct {
		name    string
		runtime Runtime
		command string
		gateway string
		socket  string
		compose []string
	}{
		{"docker", DockerRuntime{}, "docker", "host.docker.internal", "/var/run/docker.sock", []string{"docker", "compose"}},
		{"podman", PodmanRuntime{}, "podman", "host.containers.internal", "", []string{"podman", "compose"}},
		{"nerdctl", NerdctlRuntime{}, "nerdctl", "host.docker.internal", "/run/containerd/containerd.sock", []string{"nerdctl", "compose"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.runtime.Name() != tt.name || tt.runtime.Command() != tt.command {
				t.Fatalf("runtime identity = %q/%q; want %q/%q", tt.runtime.Name(), tt.runtime.Command(), tt.name, tt.command)
			}
			if tt.runtime.HostGateway() != tt.gateway || tt.runtime.SocketPath() != tt.socket {
				t.Fatalf("runtime host integration = %q/%q; want %q/%q", tt.runtime.HostGateway(), tt.runtime.SocketPath(), tt.gateway, tt.socket)
			}
			if !reflect.DeepEqual(tt.runtime.ComposeCommand(), tt.compose) {
				t.Fatalf("ComposeCommand() = %#v; want %#v", tt.runtime.ComposeCommand(), tt.compose)
			}
		})
	}
}

func TestRuntimeDetect(t *testing.T) {
	lookPath := func(found ...string) func(string) (string, error) {
		available := make(map[string]bool, len(found))
		for _, name := range found {
			available[name] = true
		}
		return func(name string) (string, error) {
			if available[name] {
				return filepath.Join("/bin", name), nil
			}
			return "", os.ErrNotExist
		}
	}

	runtime, err := detectRuntime("auto", lookPath("nerdctl", "podman", "docker"))
	if err != nil || runtime.Name() != "docker" {
		t.Fatalf("DetectRuntime(auto) = %v, %v; want docker", runtime, err)
	}
	runtime, err = detectRuntime("auto", lookPath("nerdctl", "podman"))
	if err != nil || runtime.Name() != "podman" {
		t.Fatalf("DetectRuntime(auto without docker) = %v, %v; want podman", runtime, err)
	}
	runtime, err = detectRuntime("podman", lookPath("docker", "podman"))
	if err != nil || runtime.Name() != "podman" {
		t.Fatalf("DetectRuntime(podman) = %v, %v; want podman", runtime, err)
	}
	if _, err := detectRuntime("podman", lookPath("docker")); err == nil {
		t.Fatal("DetectRuntime(podman) should not fall back to docker")
	}
	if _, err := detectRuntime("lxc", lookPath("docker")); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("DetectRuntime(lxc) error = %v; want unsupported error", err)
	}
	if _, err := detectRuntime("auto", lookPath()); err == nil {
		t.Fatal("DetectRuntime(auto) with no runtimes should error")
	}
}

func TestListRuntimeStatuses(t *testing.T) {
	statuses := listRuntimeStatuses(func(name string) (string, error) {
		if name == "podman" {
			return "/bin/podman", nil
		}
		return "", os.ErrNotExist
	})
	if !reflect.DeepEqual(statuses, []RuntimeStatus{
		{Name: "docker", Available: false},
		{Name: "podman", Available: true},
		{Name: "nerdctl", Available: false},
	}) {
		t.Fatalf("listRuntimeStatuses() = %#v", statuses)
	}
}

func TestDockerContextValidationAndPrefixes(t *testing.T) {
	if err := ValidateContext(DockerRuntime{}, "remote-builder"); err != nil {
		t.Fatalf("ValidateContext() error = %v", err)
	}
	for _, value := range []string{"-bad", "two words"} {
		if err := ValidateContext(DockerRuntime{}, value); err == nil {
			t.Fatalf("ValidateContext(%q) error = nil", value)
		}
	}
	if err := ValidateContext(PodmanRuntime{}, "remote-builder"); err == nil {
		t.Fatal("ValidateContext(podman) error = nil")
	}
	if got := CommandPrefix(DockerRuntime{}, "remote-builder"); !reflect.DeepEqual(got, []string{"docker", "--context", "remote-builder"}) {
		t.Fatalf("CommandPrefix() = %#v", got)
	}
	if got := ComposeCommandFor(DockerRuntime{}, "remote-builder"); !reflect.DeepEqual(got, []string{"docker", "--context", "remote-builder", "compose"}) {
		t.Fatalf("ComposeCommandFor() = %#v", got)
	}
}

func TestRuntimeInfo(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"docker", "podman", "nerdctl"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n[ \"$1\" = info ]\n"), 0o700); err != nil { // #nosec G306 -- test helper executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	for _, runtime := range []Runtime{DockerRuntime{}, PodmanRuntime{}, NerdctlRuntime{}} {
		if err := runtime.Info(); err != nil {
			t.Errorf("%s.Info() error = %v", runtime.Name(), err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte("#!/bin/sh\nexit 9\n"), 0o700); err != nil { // #nosec G306 -- test helper executable
		t.Fatal(err)
	}
	if err := (PodmanRuntime{}).Info(); err == nil {
		t.Fatal("PodmanRuntime.Info() should report a non-zero exit")
	}
}

func TestListDockerContexts(t *testing.T) {
	original := listDockerContextsCommand
	listDockerContextsCommand = func() ([]byte, error) {
		return []byte("default\ncolima\ndefault\nremote-builder\n"), nil
	}
	t.Cleanup(func() { listDockerContextsCommand = original })

	got, err := ListDockerContexts()
	if err != nil {
		t.Fatalf("ListDockerContexts() error = %v", err)
	}
	want := []string{"colima", "default", "remote-builder"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListDockerContexts() = %v; want %v", got, want)
	}
}

func TestListDockerContextsReportsCommandFailure(t *testing.T) {
	original := listDockerContextsCommand
	listDockerContextsCommand = func() ([]byte, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { listDockerContextsCommand = original })

	if _, err := ListDockerContexts(); err == nil || !strings.Contains(err.Error(), "docker context ls") {
		t.Fatalf("ListDockerContexts() error = %v; want command context", err)
	}
}

// A context name the CLI prints that fails validation (whitespace, leading
// dash) must surface as an error, not enter the picker list.
func TestListDockerContextsRejectsInvalidNames(t *testing.T) {
	original := listDockerContextsCommand
	listDockerContextsCommand = func() ([]byte, error) { return []byte("default\ntwo words\n"), nil }
	t.Cleanup(func() { listDockerContextsCommand = original })

	if _, err := ListDockerContexts(); err == nil || !strings.Contains(err.Error(), "invalid context") {
		t.Fatalf("ListDockerContexts() error = %v; want invalid-context error", err)
	}
}

// The public wrappers must consult the injected host PATH lookup so the TUI
// picker and the launch preflight agree on what is installed.
func TestListRuntimeStatusesAndDetectRuntimeUseLookPath(t *testing.T) {
	original := runtimeLookPath
	runtimeLookPath = func(name string) (string, error) {
		if name == "docker" {
			return "/usr/bin/docker", nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { runtimeLookPath = original })

	statuses := ListRuntimeStatuses()
	if !reflect.DeepEqual(statuses, []RuntimeStatus{
		{Name: "docker", Available: true},
		{Name: "podman", Available: false},
		{Name: "nerdctl", Available: false},
	}) {
		t.Fatalf("ListRuntimeStatuses() = %#v", statuses)
	}
	runtime, err := DetectRuntime("auto")
	if err != nil || runtime.Name() != "docker" {
		t.Fatalf("DetectRuntime(auto) = %v, %v; want docker", runtime, err)
	}
	if _, err := DetectRuntime("podman"); err == nil {
		t.Fatal("DetectRuntime(podman) should error when podman is not on PATH")
	}
}

// A nil lookup falls back to the real exec.LookPath; both helpers must still
// answer deterministically regardless of what this host has installed.
func TestRuntimeHelpersNilLookPathFallback(t *testing.T) {
	statuses := listRuntimeStatuses(nil)
	if len(statuses) != len(runtimePriority) {
		t.Fatalf("listRuntimeStatuses(nil) = %#v; want one entry per supported runtime", statuses)
	}
	for i, name := range runtimePriority {
		if statuses[i].Name != name {
			t.Fatalf("listRuntimeStatuses(nil)[%d] = %q; want %q", i, statuses[i].Name, name)
		}
	}
	if _, err := detectRuntime("not-a-runtime", nil); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("detectRuntime(unsupported, nil) error = %v; want unsupported error", err)
	}
}

// RuntimeInfo (the package-level preflight) composes context validation, the
// default runtime, and the runtime's info command.
func TestRuntimeInfoPreflight(t *testing.T) {
	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test helper executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if err := RuntimeInfo(DockerRuntime{}, "remote-builder"); err != nil {
		t.Fatalf("RuntimeInfo() with a context error = %v", err)
	}
	if err := RuntimeInfo(nil, ""); err != nil {
		t.Fatalf("RuntimeInfo(nil) should default to docker, got %v", err)
	}
	if err := RuntimeInfo(DockerRuntime{}, "two words"); err == nil {
		t.Fatal("RuntimeInfo() must reject an invalid context before exec")
	}
	if err := RuntimeInfo(PodmanRuntime{}, "ctx"); err == nil {
		t.Fatal("RuntimeInfo(podman, ctx) must reject a docker-only context")
	}

	if err := os.WriteFile(docker, []byte("#!/bin/sh\nexit 9\n"), 0o700); err != nil { // #nosec G306 -- test helper executable
		t.Fatal(err)
	}
	if err := RuntimeInfo(DockerRuntime{}, ""); err == nil || !strings.Contains(err.Error(), "docker info") {
		t.Fatalf("RuntimeInfo() error = %v; want a docker info failure", err)
	}
}

func TestListDockerContextsCommandFailsClearlyWithoutDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := listDockerContextsCommand(); err == nil || !strings.Contains(err.Error(), "docker CLI not found in PATH") {
		t.Fatalf("listDockerContextsCommand() error = %v; want a clear docker-not-found failure", err)
	}
}

// RequireLocalDaemon must reject ssh:// endpoints: the backend bind-mounts
// host paths same-path, which would silently mount wrong directories on the
// daemon's machine.
func TestRequireLocalDaemonRejectsSSHContext(t *testing.T) {
	stubDaemonSeams(t, "", `[{"Name":"remote-builder","Endpoints":{"docker":{"Host":"ssh://builder@192.0.2.10"}}}]`, nil)
	err := RequireLocalDaemon(DockerRuntime{}, "remote-builder")
	if err == nil {
		t.Fatal("RequireLocalDaemon() must reject an ssh:// context")
	}
	for _, want := range []string{"local Docker daemon", `context "remote-builder"`, "ssh://builder@192.0.2.10"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q; want it to mention %q", err, want)
		}
	}
}

func TestRequireLocalDaemonRejectsRemoteTCP(t *testing.T) {
	stubDaemonSeams(t, "", `[{"Name":"default","Endpoints":{"docker":{"Host":"tcp://192.0.2.20:2375"}}}]`, nil)
	if err := RequireLocalDaemon(DockerRuntime{}, ""); err == nil || !strings.Contains(err.Error(), "tcp://192.0.2.20:2375") {
		t.Fatalf("RequireLocalDaemon() must reject a remote tcp:// endpoint, got %v", err)
	}
}

// Rootless Docker and colima expose the daemon on a loopback TCP socket; that
// is still the local machine, so it must be accepted.
func TestRequireLocalDaemonAcceptsLoopbackTCP(t *testing.T) {
	for _, host := range []string{"tcp://localhost:2375", "tcp://127.0.0.1:2376"} {
		stubDaemonSeams(t, "", `[{"Name":"colima","Endpoints":{"docker":{"Host":"`+host+`"}}}]`, nil)
		if err := RequireLocalDaemon(DockerRuntime{}, "colima"); err != nil {
			t.Fatalf("RequireLocalDaemon() with %s: %v; loopback TCP is local", host, err)
		}
	}
}

func TestRequireLocalDaemonAcceptsUnixSocket(t *testing.T) {
	stubDaemonSeams(t, "", `[{"Name":"default","Endpoints":{"docker":{"Host":"unix:///var/run/docker.sock"}}}]`, nil)
	if err := RequireLocalDaemon(DockerRuntime{}, ""); err != nil {
		t.Fatalf("RequireLocalDaemon() with a unix socket: %v", err)
	}
}

// DOCKER_HOST overrides any context in the Docker CLI, so the guard must
// check it first and never inspect the context.
func TestRequireLocalDaemonDockerHostEnvWins(t *testing.T) {
	inspectCalled := false
	originalInspect := dockerContextInspectCommand
	dockerContextInspectCommand = func(string) ([]byte, error) {
		inspectCalled = true
		return nil, errors.New("must not be called")
	}
	t.Cleanup(func() { dockerContextInspectCommand = originalInspect })
	originalEnv := dockerHostEnvValue
	dockerHostEnvValue = func() (string, bool) { return "ssh://builder@192.0.2.10", true }
	t.Cleanup(func() { dockerHostEnvValue = originalEnv })

	if err := RequireLocalDaemon(DockerRuntime{}, "default"); err == nil || !strings.Contains(err.Error(), "DOCKER_HOST") {
		t.Fatalf("RequireLocalDaemon() must reject a remote DOCKER_HOST, got %v", err)
	}
	if inspectCalled {
		t.Fatal("context inspect must not run when DOCKER_HOST is set")
	}

	dockerHostEnvValue = func() (string, bool) { return "unix:///var/run/docker.sock", true }
	if err := RequireLocalDaemon(DockerRuntime{}, "default"); err != nil {
		t.Fatalf("RequireLocalDaemon() with a local DOCKER_HOST: %v", err)
	}
}

// Fail-closed: when the endpoint cannot be determined, the launch is refused
// and the error says how to override, because silently wrong mounts are the
// failure mode this guard exists to prevent.
func TestRequireLocalDaemonFailsClosedOnInspectFailure(t *testing.T) {
	stubDaemonSeams(t, "", "", errors.New("context does not exist"))
	err := RequireLocalDaemon(DockerRuntime{}, "gone")
	if err == nil {
		t.Fatal("RequireLocalDaemon() must fail when context inspect fails")
	}
	for _, want := range []string{"could not be determined", `context "gone"`, "--container-context"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q; want it to mention %q", err, want)
		}
	}
}

func TestRequireLocalDaemonFailsClosedOnUndeterminedEndpoint(t *testing.T) {
	for name, out := range map[string]string{
		"invalid JSON":       "not-json",
		"no entries":         `[]`,
		"no docker endpoint": `[{"Name":"aci","Endpoints":{"kubernetes":{"Host":"https://example"}}}]`,
	} {
		stubDaemonSeams(t, "", out, nil)
		if err := RequireLocalDaemon(DockerRuntime{}, ""); err == nil {
			t.Fatalf("RequireLocalDaemon() with %s must fail closed", name)
		}
	}
}

// The guard is docker-specific; other runtimes use different connection
// mechanisms and must not be blocked by it.
func TestRequireLocalDaemonIgnoresNonDockerRuntimes(t *testing.T) {
	stubDaemonSeams(t, "ssh://builder@192.0.2.10", "", errors.New("must not be called"))
	for _, runtime := range []Runtime{PodmanRuntime{}, NerdctlRuntime{}} {
		if err := RequireLocalDaemon(runtime, ""); err != nil {
			t.Fatalf("RequireLocalDaemon(%s): %v; non-docker runtimes are out of scope", runtime.Name(), err)
		}
	}
}

func stubDaemonSeams(t *testing.T, dockerHost string, inspectOut string, inspectErr error) {
	t.Helper()
	originalEnv := dockerHostEnvValue
	dockerHostEnvValue = func() (string, bool) { return dockerHost, dockerHost != "" }
	t.Cleanup(func() { dockerHostEnvValue = originalEnv })
	originalInspect := dockerContextInspectCommand
	dockerContextInspectCommand = func(string) ([]byte, error) { return []byte(inspectOut), inspectErr }
	t.Cleanup(func() { dockerContextInspectCommand = originalInspect })
}

func TestDockerContextInspectCommandFailsClearlyWithoutDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := dockerContextInspectCommand(""); err == nil || !strings.Contains(err.Error(), "docker CLI not found in PATH") {
		t.Fatalf("dockerContextInspectCommand() error = %v; want a clear docker-not-found failure", err)
	}
}

// The real seam must place the context name after `context inspect` so an
// explicit selection is inspected instead of the current context.
func TestDockerContextInspectCommandPassesContextName(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	docker := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + log + "\"\nprintf '[{\"Endpoints\":{\"docker\":{\"Host\":\"unix:///x.sock\"}}}]'\n"
	if err := os.WriteFile(docker, []byte(script), 0o700); err != nil { // #nosec G306 -- test helper executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	out, err := dockerContextInspectCommand("colima")
	if err != nil {
		t.Fatalf("dockerContextInspectCommand() error = %v", err)
	}
	argv, err := os.ReadFile(log) // #nosec G304 -- log is a path this test just created under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(argv) != "context\ninspect\ncolima\n" {
		t.Fatalf("argv = %q; want context inspect colima", argv)
	}
	host, err := parseContextDockerHost(out)
	if err != nil || host != "unix:///x.sock" {
		t.Fatalf("parseContextDockerHost() = %q, %v", host, err)
	}
}

// Endpoint classification in isolation: the shapes a daemon endpoint can take
// beyond the ones the RequireLocalDaemon tests exercise end to end.
func TestIsLocalDockerHostClassifiesEndpoints(t *testing.T) {
	cases := []struct {
		host  string
		local bool
	}{
		{"", true},
		{"/var/run/docker.sock", true},
		{"unix:///var/run/docker.sock", true},
		{`npipe:////./pipe/docker_engine`, true},
		{"UNIX:///var/run/docker.sock", true},
		{"tcp://[::1]:2375", true},
		{"tcp://192.0.2.20:2375", false},
		{"tcp://docker.example.com:2376", false},
		{"tcp://%zz:2375", false},
		{"ssh://builder@192.0.2.10", false},
		{"http://192.0.2.20:2375", false},
		{"var/run/docker.sock", false},
	}
	for _, tt := range cases {
		if got := isLocalDockerHost(tt.host); got != tt.local {
			t.Errorf("isLocalDockerHost(%q) = %v; want %v", tt.host, got, tt.local)
		}
	}
}
