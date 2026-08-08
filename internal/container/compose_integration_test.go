package container

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestComposeEndToEnd validates the user-facing generate → compose lifecycle
// with the real Docker daemon. It is opt-in because it builds the selected
// agent image and publishes the catalog's PostgreSQL/Redis ports.
func TestComposeEndToEnd(t *testing.T) {
	if !dockerTestsEnabled(t) {
		return
	}
	if !tcpPortAvailable(5432) || !tcpPortAvailable(6379) {
		t.Skip("ports 5432 and 6379 must be available for the Compose acceptance probe")
	}
	root := repositoryRoot(t)
	project := t.TempDir()
	home := filepath.Join(project, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	launcherPath := filepath.Join(project, "ai-launcher")
	build := exec.Command("go", "build", "-o", launcherPath, "./cmd/ai-launcher") // #nosec G204 -- fixed repository build command
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ai-launcher: %v\n%s", err, output)
	}

	projectName := fmt.Sprintf("ai-launcher-e2e-%d", time.Now().UnixNano())
	composePath := filepath.Join(project, ".ai-launcher", "docker-compose.yaml")
	cleanup := func() {
		if _, err := os.Stat(composePath); os.IsNotExist(err) {
			return
		}
		if err := runDockerCompose([]string{"-p", projectName, "-f", composePath, "down", "--volumes", "--remove-orphans"}); err != nil {
			t.Errorf("compose cleanup: %v", err)
		}
	}
	t.Cleanup(cleanup)

	generate := exec.Command(launcherPath, "--no-local-config", "--no-memory", "--docker-backend", "--agent", "claude", "--stack", "go", "--service", "postgres", "--service", "redis", "generate") // #nosec G204 -- test fixture invokes the built launcher with fixed arguments
	generate.Dir = project
	generate.Env = append(os.Environ(), "HOME="+home)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("ai-launcher generate: %v\n%s", err, output)
	}
	data, err := os.ReadFile(composePath) // #nosec G304 -- compose path is the test's own temporary project
	if err != nil {
		t.Fatalf("read generated compose: %v", err)
	}
	for _, fragment := range []string{"postgres:", "redis:", ".ai-launcher/data/postgres", ".ai-launcher/data/redis", "ai-launcher:"} {
		if !strings.Contains(string(data), fragment) {
			t.Fatalf("generated compose missing %q:\n%s", fragment, data)
		}
	}
	for _, legacy := range []string{"pg-data:", "redis-data:"} {
		if strings.Contains(string(data), legacy) {
			t.Fatalf("generated compose still uses named service volume %q:\n%s", legacy, data)
		}
	}

	if err := runDockerCompose([]string{"-p", projectName, "-f", composePath, "up", "-d", "--build"}); err != nil {
		t.Fatalf("compose up: %v", err)
	}
	waitForTCP(t, 5432)
	waitForTCP(t, 6379)
	probe := "getent hosts postgres >/dev/null && getent hosts redis >/dev/null"
	if err := runDockerCompose([]string{"-p", projectName, "-f", composePath, "run", "--rm", "-T", "--no-deps", "agent", "sh", "-c", probe}); err != nil {
		t.Fatalf("agent DNS probe: %v", err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func tcpPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func waitForTCP(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("TCP port %d did not become reachable before timeout", port)
}

// TestCompose exercises the real Compose network path. It is opt-in because it
// pulls database images and requires a running Docker daemon.
func TestCompose(t *testing.T) {
	if !dockerTestsEnabled(t) {
		return
	}
	project := fmt.Sprintf("ai-launcher-compose-%d", time.Now().UnixNano())
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM busybox:1.36\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	file := NewComposeFile()
	file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge"}
	for _, id := range []string{"postgres", "redis"} {
		service, ok := ServiceByID(id)
		if !ok {
			t.Fatalf("ServiceByID(%q) = false", id)
		}
		// DNS is the acceptance target here; removing host publication keeps
		// the opt-in probe isolated from a developer's local 5432/6379 users.
		service.Ports = nil
		if err := AddInfrastructureService(&file, service, "ai-launcher"); err != nil {
			t.Fatalf("AddInfrastructureService(%q): %v", id, err)
		}
	}
	file.Services["agent"] = ComposeService{
		Build:     ".",
		Command:   []string{"sh", "-c", "sleep 60"},
		Networks:  []string{"ai-launcher"},
		DependsOn: []string{"postgres", "redis"},
		StdinOpen: true,
	}
	rendered, err := RenderCompose(file)
	if err != nil {
		t.Fatalf("RenderCompose(): %v", err)
	}
	if err := os.WriteFile(composePath, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = runDockerCompose([]string{"-p", project, "-f", composePath, "down", "--volumes", "--remove-orphans"})
		_ = runDockerShell([]string{"docker", "image", "rm", project + "-agent"})
	})
	if err := runDockerCompose([]string{"-p", project, "-f", composePath, "up", "-d", "--build"}); err != nil {
		t.Fatalf("compose up: %v", err)
	}
	probe := "i=0; while [ \"$i\" -lt 30 ]; do if nslookup postgres >/dev/null 2>&1; then postgres_status=0; else postgres_status=$?; fi; if nslookup redis >/dev/null 2>&1; then redis_status=0; else redis_status=$?; fi; if [ \"$postgres_status\" -eq 0 ] && [ \"$redis_status\" -eq 0 ]; then exit 0; fi; i=$((i+1)); sleep 1; done; exit 1"
	if err := runDockerCompose([]string{"-p", project, "-f", composePath, "exec", "-T", "agent", "sh", "-c", probe}); err != nil {
		t.Fatalf("agent DNS probe: %v", err)
	}
}

func runDockerCompose(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...) // #nosec G204 -- test-only docker argv
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("docker compose timeout: %w", ctx.Err())
		}
		return err
	}
	return nil
}
