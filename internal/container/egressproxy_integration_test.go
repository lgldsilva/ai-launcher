//go:build container_integration

// This file is excluded from the default `go test ./...` run and from the
// coverage gate. It launches real Docker containers to behaviorally verify the
// egress proxy — the squid.conf content tests can only assert that the right
// ACL lines are present, not that squid actually enforces them against a live
// connection. Run it locally where Docker is available:
//
//	go test -tags container_integration -run TestEgressProxyEnforcesAllowlist ./internal/container/
package container

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEgressProxyEnforcesAllowlist is the behavioral test the squid.conf
// content tests cannot be: it starts the real pinned squid image with a
// generated allowlist and asserts an allowed domain is forwarded through the
// proxy while a non-allowlisted domain is refused at the proxy (not silently
// allowed). This is the reproduction of the manual "verified end-to-end" claim
// in the egress-proxy commits, now kept as a runnable regression test.
func TestEgressProxyEnforcesAllowlist(t *testing.T) {
	skipIfNoDocker(t)

	allowedDomains := []string{"example.com"}
	squidConf, err := GenerateEgressProxyConfig(allowedDomains)
	if err != nil {
		t.Fatalf("GenerateEgressProxyConfig() error = %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "squid.conf"), []byte(squidConf), 0o600); err != nil {
		t.Fatalf("write squid.conf: %v", err)
	}
	// The compose document mirrors what BuildCompose produces for the proxy:
	// same pinned digest image, same read-only squid.conf mount, the same
	// hardening baseline, and the dual-homed topology (internal + egress).
	compose := fmt.Sprintf(`services:
  egress-proxy:
    image: %s
    volumes:
      - ./squid.conf:/etc/squid/squid.conf:ro
    networks: [internal, egress]
    cap_drop: [ALL]
    cap_add: [SETGID, SETUID]
    security_opt: [no-new-privileges:true]
    healthcheck:
      test: [CMD-SHELL, squid -k check]
      interval: 2s
      timeout: 3s
      retries: 30
      start_period: 5s
  client:
    image: curlimages/curl:8.10.1
    networks: [internal]
networks:
  internal:
    driver: bridge
    internal: true
  egress:
    driver: bridge
`, EgressProxyImage)
	composePath := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(composePath, []byte(compose), 0o600); err != nil {
		t.Fatalf("write docker-compose.yaml: %v", err)
	}

	project := "aiegtest"
	composeCmd := func(args ...string) *exec.Cmd {
		full := append([]string{"compose", "-p", project, "-f", composePath}, args...)
		return exec.Command("docker", full...)
	}

	t.Cleanup(func() {
		// Best-effort teardown: never let a failed assertion leave containers up.
		_ = composeCmd("down", "--volumes").Run()
	})

	if out, err := composeCmd("up", "-d", "egress-proxy").CombinedOutput(); err != nil {
		t.Fatalf("docker compose up egress-proxy failed: %v\n%s", err, out)
	}
	if err := waitForHealthy(context.Background(), composeCmd, "egress-proxy", 90*time.Second); err != nil {
		// The container is about to be torn down by Cleanup; surface its logs so
		// the failure is diagnosable instead of a bare "unhealthy".
		if out, logsErr := composeCmd("logs", "egress-proxy").CombinedOutput(); logsErr == nil {
			t.Fatalf("egress-proxy never became healthy: %v\n--- logs ---\n%s", err, out)
		}
		t.Fatalf("egress-proxy never became healthy: %v", err)
	}

	// Allowed domain: the proxy must forward it, so the real server answers
	// (any 2xx/3xx status proves the request traveled through the proxy).
	allowed := curlThroughProxy(t, composeCmd, "http://example.com/")
	if len(allowed) == 0 || (allowed[0] != '2' && allowed[0] != '3') {
		t.Fatalf("allowed domain example.com: http_code=%q; want 2xx/3xx (the proxy should forward an allowlisted domain)", allowed)
	}
	t.Logf("allowed domain example.com: http_code=%s (forwarded, as intended)", allowed)

	// Non-allowlisted domain: the proxy must refuse it — a 2xx/3xx here would
	// mean the allowlist is open (the exact regression the bare-TLD bug was).
	blocked := curlThroughProxy(t, composeCmd, "http://example.org/")
	if len(blocked) > 0 && (blocked[0] == '2' || blocked[0] == '3') {
		t.Fatalf("non-allowlisted domain example.org: http_code=%q; the proxy must refuse it, not forward it", blocked)
	}
	t.Logf("non-allowlisted domain example.org: http_code=%q (refused, as intended)", blocked)
}

// TestCatalogServiceStaysHealthyUnderHardening validates that the cap_drop ALL
// + cap_add CHOWN/SETGID/SETUID baseline applied to every catalog service does
// not abort a representative third-party service's startup. redis and postgres
// are the representatives: both start as root, chown their data directory, and
// drop privileges the way the rest of the catalog does, so if cap_drop ALL
// breaks any of those steps this test fails with "unhealthy" exactly the way
// the egress-proxy test did before the right capabilities were handed back.
func TestCatalogServiceStaysHealthyUnderHardening(t *testing.T) {
	skipIfNoDocker(t)
	for _, id := range []string{"redis", "postgres"} {
		t.Run(id, func(t *testing.T) {
			service, ok := ServiceByID(id)
			if !ok {
				t.Fatalf("%q service not in catalog", id)
			}
			dir := t.TempDir()
			file := NewComposeFile()
			file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge"}
			if err := AddInfrastructureServiceWithDataDir(&file, service, "ai-launcher", dir); err != nil {
				t.Fatalf("AddInfrastructureServiceWithDataDir: %v", err)
			}
			rendered, err := RenderCompose(file)
			if err != nil {
				t.Fatalf("RenderCompose: %v", err)
			}
			// The rendered YAML is the real production artifact; assert the
			// hardening is in it before spending container-startup time on the
			// behavioral check.
			if !strings.Contains(rendered, "cap_drop:") || !strings.Contains(rendered, "SETGID") {
				t.Fatalf("rendered catalog compose missing the hardening baseline:\n%s", rendered)
			}
			composePath := filepath.Join(dir, "docker-compose.yaml")
			if err := os.WriteFile(composePath, []byte(rendered), 0o600); err != nil {
				t.Fatalf("write docker-compose.yaml: %v", err)
			}

			project := "aihardtest-" + id
			composeCmd := func(args ...string) *exec.Cmd {
				return exec.Command("docker", append([]string{"compose", "-p", project, "-f", composePath}, args...)...)
			}
			t.Cleanup(func() { _ = composeCmd("down", "--volumes").Run() })

			if out, err := composeCmd("up", "-d").CombinedOutput(); err != nil {
				t.Fatalf("docker compose up %s failed: %v\n%s", id, err, out)
			}
			if err := waitForHealthy(context.Background(), composeCmd, service.ID, 90*time.Second); err != nil {
				if out, logsErr := composeCmd("logs", service.ID).CombinedOutput(); logsErr == nil {
					t.Fatalf("%s never became healthy under hardening: %v\n--- logs ---\n%s", id, err, out)
				}
				t.Fatalf("%s never became healthy under hardening: %v", id, err)
			}
		})
	}
}

// curlThroughProxy runs a one-shot curl inside the client container and returns
// the %{http_code} it printed. curlimages/curl's entrypoint is curl, so the
// args go straight to it. The proxy is set explicitly via --proxy (rather than
// relying on HTTP_PROXY env propagation through `compose run`), and stdout is
// captured separately from stderr so compose's pull/progress lines do not
// contaminate the http_code. A refused/blocked request makes curl exit
// non-zero (and usually emit "000"); that is expected for the blocked-domain
// case, so the error is logged rather than fatal — the http_code is the signal
// callers read.
func curlThroughProxy(t *testing.T, composeCmd func(...string) *exec.Cmd, url string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := composeCmd("run", "--rm", "--no-deps", "client",
		"-sS",
		"--proxy", fmt.Sprintf("http://egress-proxy:%d", EgressProxyPort),
		"-o", "/dev/null", "-w", "%{http_code}", "--max-time", "20", url)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Logf("curl %s exited non-zero (expected for a refused domain): %v\nstderr: %s", url, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// waitForHealthy polls the proxy container's health status until it is healthy
// or the timeout elapses. It resolves the container id through `compose ps -q`
// rather than guessing the <project>-<service>-1 name, so it survives compose's
// container-naming variations.
func waitForHealthy(ctx context.Context, composeCmd func(...string) *exec.Cmd, service string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := composeCmd("ps", "-q", service).Output()
		if err == nil {
			if id := strings.TrimSpace(string(out)); id != "" {
				status, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Health.Status}}", id).Output()
				if err == nil {
					switch strings.TrimSpace(string(status)) {
					case "healthy":
						return nil
					case "unhealthy":
						return fmt.Errorf("container %s is unhealthy", id)
					}
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for %s to become healthy", service)
}

func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH; skipping egress-proxy integration test")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skipf("docker compose plugin not available: %v", err)
	}
}
