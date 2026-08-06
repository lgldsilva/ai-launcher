package container

import (
	"fmt"
	"os"
	"testing"

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
			ctx, cleanup, err := PrepareBuildContext(sel, installCfg, "")
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
			probe := fmt.Sprintf(`for d in /root/.local/bin /usr/local/bin /usr/bin /root/.opencode/bin /root/.dev/bin /root/.nvm/versions/node/*/bin; do if test -x "$d/%s"; then echo FOUND "$d/%s"; exit 0; fi; done; echo MISSING; exit 1`, agent.Command, agent.Command)
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
