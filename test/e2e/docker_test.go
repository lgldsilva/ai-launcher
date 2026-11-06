//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestDockerGenerationAndRun(t *testing.T) {
	root := t.TempDir()
	keepWritable(t, root)
	tools := filepath.Join(cacheDir(), "bins")
	if err := installPinnedTools(tools); err != nil {
		t.Fatalf("install pinned CLIs: %v", err)
	}
	launcher := buildLauncher(t, root)
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	stubs := filepath.Join(root, "stubs")
	mustMkdir(t, home, project, filepath.Join(home, ".config"), stubs)
	writeStubs(t, stubs, []config.Agent{{Command: "goose"}, {Command: "claude"}, {Command: "gemini"}, {Command: "kilo"}})
	global := writeGlobal(t, root, writeRequestFile(t, root))
	local := filepath.Join(root, "missing-local.yaml")
	env := baseEnv(home, project)
	jail := filepath.Join(tools, "ai-jail-3.0.1")
	memory := filepath.Join(tools, "ai-memory-3.4.2")

	for _, agent := range []string{"claude", "gemini", "kilo", "goose"} {
		spec := dockerDrySpec(agent)
		row := runCell(t, launcher, spec, env, project, global, local, stubs, jail, memory)
		if !strings.HasSuffix(row, "\tpass") {
			t.Errorf("dry-run %s: %s", agent, row)
		}
	}
	generateKinds(t, launcher, global, project, home, stubs)
	if os.Getenv("E2E_SKIP_DOCKER_BUILD") == "1" {
		t.Log("docker build skipped: E2E_SKIP_DOCKER_BUILD=1")
		return
	}
	if !dockerReady() {
		t.Log("docker build not-testable: daemon is not reachable")
		return
	}
	runHostBinaryContainer(t, launcher, env, project, global, local, stubs, jail, memory)
}

func generateKinds(t *testing.T, launcher, global, project, home, stubs string) {
	t.Helper()
	cases := []struct {
		agent  string
		marker string
	}{
		{"goose", "bind-mounted from host"},
		{"claude", "curl -fsSL"},
		{"gemini", "npm install -g"},
		{"kilo", "pinned release"},
	}
	for _, tc := range cases {
		dir := filepath.Join(project, "gen-"+tc.agent)
		mustMkdir(t, dir)
		argv := []string{"generate", "--config", global, "--agent", tc.agent, "--docker-backend", "--stack", "python", "--no-memory"}
		cmd := exec.Command(launcher, argv...) // #nosec G204 -- fixed e2e argv
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate %s: %v\n%s", tc.agent, err, out)
		}
		body, err := os.ReadFile(filepath.Join(dir, ".ai-launcher", "Dockerfile"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), tc.marker) {
			t.Errorf("Dockerfile for %s missing %q", tc.agent, tc.marker)
		}
	}
}

func dockerReady() bool {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func runHostBinaryContainer(t *testing.T, launcher string, env []string, project, global, local, stubs, jail, memory string) {
	t.Helper()
	spec := dockerDrySpec("goose")
	spec.dryRun = false
	spec.timeout = 20 * time.Minute
	spec.wantText = "stub-ok goose"
	row := runCell(t, launcher, spec, env, project, global, local, stubs, jail, memory)
	if !strings.HasSuffix(row, "\tpass") {
		t.Fatalf("docker run goose: %s", row)
	}
}
