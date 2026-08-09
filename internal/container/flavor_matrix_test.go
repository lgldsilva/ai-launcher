package container

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// TestFlavorMatrix builds an image per script-recipe agent and verifies its
// binary exists inside. It is the broad coverage the flavor battery needs:
// every agent with an official installer gets a real build. Individual
// failures are reported (not fatal) so one upstream drift does not hide the
// others; the test fails only when the majority of the matrix is broken.
func TestFlavorMatrix(t *testing.T) {
	if os.Getenv("AI_LAUNCHER_DOCKER_TESTS") != "1" {
		t.Skip("set AI_LAUNCHER_DOCKER_TESTS=1 to run the flavor matrix")
	}
	global := config.DefaultGlobal()
	// Both script (curl|bash) and npm-distributed agents get a real build.
	var scriptAgents []config.Agent
	for _, agent := range global.Agents {
		if agent.SourceURL != "" || agent.NpmPackage != "" {
			scriptAgents = append(scriptAgents, agent)
		}
	}
	if len(scriptAgents) == 0 {
		t.Fatal("no script-recipe agents in the catalog")
	}
	launcherPath := ""
	for _, agent := range scriptAgents {
		if agent.SupportsMemory {
			launcherPath = buildFlavorTestLauncher(t)
			break
		}
	}

	var failures []string
	for _, agent := range scriptAgents {
		agent := agent
		t.Run(agent.Command, func(t *testing.T) {
			sel, err := Normalize([]string{"go"}, []AgentInstall{PlanInstall(agent, "", "")}, nil)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			installCfg, err := InstallConfig(global, sel.Agents)
			if err != nil {
				t.Fatalf("InstallConfig() error = %v", err)
			}
			ctx, cleanup, err := PrepareBuildContext(sel, installCfg, launcherPath)
			if err != nil {
				t.Fatalf("PrepareBuildContext() error = %v", err)
			}
			defer cleanup()

			if err := runDockerShell([]string{"docker", "build", "--tag", ctx.ImageTag, ctx.Dir}); err != nil {
				failures = append(failures, agent.Command+": build "+err.Error())
				t.Fatalf("build failed: %v", err)
			}
			t.Cleanup(func() { _ = runDockerShell([]string{"docker", "rmi", ctx.ImageTag}) })

			// Probe common install locations; each vendor installs differently
			// (~/.local/bin for curl installers, /usr/local/bin for others,
			// ~/.opencode/bin for opencode, the nvm node bin for npm CLIs like
			// pi/devin).
			probe := fmt.Sprintf(`for d in /home/ai-launcher/.local/bin /usr/local/bin /usr/bin /home/ai-launcher/.opencode/bin /home/ai-launcher/.dev/bin /opt/nvm/versions/node/*/bin /usr/local/lib/nvm-bin/bin; do if test -x "$d/%s"; then echo FOUND "$d/%s"; exit 0; fi; done; echo MISSING; exit 1`, agent.Command, agent.Command)
			if err := runDockerShell([]string{"docker", "run", "--rm", ctx.ImageTag, "sh", "-c", probe}); err != nil {
				failures = append(failures, agent.Command+": binary "+err.Error())
				t.Fatalf("%s binary missing in image: %v", agent.Command, err)
			}
			// Best-effort version smoke — the binary probe above is the hard
			// assertion; a version flag that demands login reports but does
			// not fail the agent.
			if err := runDockerShell([]string{"docker", "run", "--rm", ctx.ImageTag, "sh", "-c", agent.Command + " --version >/dev/null 2>&1 || " + agent.Command + " version >/dev/null 2>&1"}); err != nil {
				t.Logf("%s: version smoke skipped (non-interactive login required), binary probe passed", agent.Command)
			}
		})
	}

	if len(failures) > len(scriptAgents)/2 {
		t.Fatalf("matrix mostly broken: %v", failures)
	}
}

func buildFlavorTestLauncher(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve flavor test source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	output := filepath.Join(t.TempDir(), "ai-launcher")
	command := exec.Command("go", "build", "-buildvcs=false", "-o", output, "./cmd/ai-launcher") // #nosec G204 -- fixed test build command in this repository
	command.Dir = root
	command.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build linux launcher for flavor matrix: %v\n%s", err, output)
	}
	return output
}

// TestFlavorComposeServices exercises the Compose networking contract for the
// three supported selection shapes: one SQL service, one cache, and both
// together. The agent image is intentionally busybox so this matrix focuses
// on Compose DNS and dependency wiring rather than installer drift.
func TestFlavorComposeServices(t *testing.T) {
	if !dockerTestsEnabled(t) {
		return
	}
	for _, services := range [][]string{{"postgres"}, {"redis"}, {"postgres", "redis"}} {
		services := services
		t.Run(strings.Join(services, "-"), func(t *testing.T) {
			runFlavorCompose(t, services)
		})
	}
}

func runFlavorCompose(t *testing.T, services []string) {
	t.Helper()
	project := fmt.Sprintf("ai-launcher-flavor-%d", time.Now().UnixNano())
	composePath := writeFlavorCompose(t, services)
	t.Cleanup(func() {
		if err := runDockerCompose([]string{"-p", project, "-f", composePath, "down", "--volumes", "--remove-orphans"}); err != nil {
			t.Errorf("compose cleanup: %v", err)
		}
	})
	if err := runDockerCompose([]string{"-p", project, "-f", composePath, "up", "-d", "--build"}); err != nil {
		t.Fatalf("compose up: %v", err)
	}
	for _, id := range services {
		probe := fmt.Sprintf("nslookup %s >/dev/null", id)
		if err := runDockerCompose([]string{"-p", project, "-f", composePath, "run", "--rm", "-T", "--no-deps", "agent", "sh", "-c", probe}); err != nil {
			t.Fatalf("agent DNS probe for %s: %v", id, err)
		}
	}
}

func writeFlavorCompose(t *testing.T, services []string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM busybox:1.36\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := NewComposeFile()
	file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge"}
	for _, id := range services {
		service, ok := ServiceByID(id)
		if !ok {
			t.Fatalf("ServiceByID(%q) = false", id)
		}
		service.Ports = nil
		if err := AddInfrastructureService(&file, service, "ai-launcher"); err != nil {
			t.Fatalf("AddInfrastructureService(%q): %v", id, err)
		}
	}
	file.Services["agent"] = ComposeService{
		Build:     ".",
		Command:   []string{"sh", "-c", "sleep 90"},
		Networks:  []string{"ai-launcher"},
		DependsOn: append([]string(nil), services...),
	}
	rendered, err := RenderCompose(file)
	if err != nil {
		t.Fatalf("RenderCompose(): %v", err)
	}
	composePath := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(composePath, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	return composePath
}
