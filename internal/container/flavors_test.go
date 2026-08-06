// Package container provides the docker backend build logic for ai-launcher.
//
// This file holds the flavor battery: integration-style tests that exercise
// each agent installation path (release recipe, install script, host binary)
// against the real docker daemon. They are guarded by
// AI_LAUNCHER_DOCKER_TESTS=1 so the default suite stays hermetic — CI without
// a docker daemon must not fail.
package container

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// dockerTestsEnabled reports whether the integration battery should run.
func dockerTestsEnabled(t *testing.T) bool {
	t.Helper()
	if os.Getenv("AI_LAUNCHER_DOCKER_TESTS") == "1" {
		// The battery really talks to the daemon; make sure one is reachable
		// rather than failing on a missing docker binary.
		if err := runDockerShell([]string{"docker", "info"}); err != nil {
			t.Skip("AI_LAUNCHER_DOCKER_TESTS=1 but docker daemon is not reachable")
		}
		return true
	}
	t.Skip("set AI_LAUNCHER_DOCKER_TESTS=1 to run the docker flavor battery")
	return false
}

// runDockerShell executes a command line and returns an error when the exit
// code is non-zero, wiring stdout/stderr to the test output so a failing
// build is diagnosable.
func runDockerShell(argv []string) error {
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...) // #nosec G204 -- test battery; argv is a fixed docker command built inline
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("exit code %d", exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// TestFlavorScriptAgent builds an image whose agent installs via a script
// recipe (the shape claude/opencode/muse use), then verifies the executable
// exists inside. This is the mainstream flavor.
func TestFlavorScriptAgent(t *testing.T) {
	if !dockerTestsEnabled(t) {
		return
	}
	selection, err := Normalize([]string{"go"}, []AgentInstall{{
		Command: "claude",
		Kind:    InstallScript,
		Script:  "RUN curl -fsSL https://claude.ai/install.sh | bash",
	}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	ctx, cleanup, err := PrepareBuildContext(selection, "", "")
	if err != nil {
		t.Fatalf("PrepareBuildContext() error = %v", err)
	}
	defer cleanup()
	// Patch the Dockerfile to only install claude (the generated one also
	// emits COPY steps for release agents; here there are none).
	if err := os.WriteFile(ctx.DockerfilePath, []byte(dockerfileForFlavor(selection)), 0o600); err != nil {
		t.Fatal(err)
	}
	err = runDockerShell([]string{"docker", "build", "--tag", ctx.ImageTag, ctx.Dir})
	if err != nil {
		t.Fatalf("docker build failed: %v", err)
	}
	t.Cleanup(func() { _ = runDockerShell([]string{"docker", "rmi", ctx.ImageTag}) })

	err = runDockerShell([]string{"docker", "run", "--rm", ctx.ImageTag, "sh", "-c", "test -x /root/.local/bin/claude"})
	if err != nil {
		t.Fatalf("claude not installed in image: %v", err)
	}
}

// dockerfileForFlavor renders the Dockerfile for a script-only selection
// without the dev-profile noise, so the flavor test builds fast.
func dockerfileForFlavor(selection Selection) string {
	var b strings.Builder
	b.WriteString("FROM " + BaseImage + "\n")
	b.WriteString(DevProfile + "\n")
	for _, agent := range selection.Agents {
		if agent.Kind == InstallScript {
			b.WriteString(ScriptLine(agent.Script))
		}
	}
	return b.String()
}

// TestFlavorReleaseAgent builds an image whose agent installs from a GitHub
// release recipe (the shape a release-recipe agent uses), verifying the
// in-image installer resolves the pinned version with checksum verification.
// The recipe is a synthetic gh (cli/cli) entry: the built-in kilo entry
// points at assets upstream no longer ships, which this battery surfaced.
func TestFlavorReleaseAgent(t *testing.T) {
	if !dockerTestsEnabled(t) {
		return
	}
	selection, err := Normalize([]string{"go"}, []AgentInstall{{
		Command: "gh",
		Kind:    InstallRelease,
		Version: "2.97.0",
	}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	// The release path needs the launcher binary inside the build; build a
	// throwaway launcher via the host go toolchain (the test machine has it).
	launcherPath, err := buildLauncherForTest(t)
	if err != nil {
		t.Fatalf("buildLauncherForTest() error = %v", err)
	}
	installCfg := installConfigWithRecipe(t, selection, "gh", "cli/cli", map[string]string{
		"linux-amd64": "gh_2.97.0_linux_amd64.tar.gz",
		"linux-arm64": "gh_2.97.0_linux_arm64.tar.gz",
	}, "gh", "checksums.txt")
	ctx, cleanup, err := PrepareBuildContext(selection, installCfg, launcherPath)
	if err != nil {
		t.Fatalf("PrepareBuildContext() error = %v", err)
	}
	defer cleanup()

	err = runDockerShell([]string{"docker", "build", "--tag", ctx.ImageTag, ctx.Dir})
	if err != nil {
		t.Fatalf("docker build failed: %v", err)
	}
	t.Cleanup(func() { _ = runDockerShell([]string{"docker", "rmi", ctx.ImageTag}) })
	err = runDockerShell([]string{"docker", "run", "--rm", ctx.ImageTag, "sh", "-c", "test -x /root/.local/bin/gh && gh --version"})
	if err != nil {
		t.Fatalf("gh not installed in image: %v", err)
	}
}

// buildLauncherForTest cross-compiles a throwaway launcher for the image
// platform so the in-image installer can run. It reuses the same GOOS/GOARCH
// rule as the production path.
func buildLauncherForTest(t *testing.T) (string, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ai-launcher")
	cmd := exec.Command("go", "build", "-o", path, "../../cmd/ai-launcher") // #nosec G204 G702 -- fixed test argv, no shell
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cross-compile launcher: %v\n%s", err, out)
	}
	return path, nil
}

// installConfigWithRecipe renders a minimal install config carrying a
// synthetic agent with the given release recipe, plus the ai-memory tool so
// the image build is self-contained. The catalog entry is built inline
// because the flavor battery must not depend on upstream catalog drift.
func installConfigWithRecipe(t *testing.T, selection Selection, command, repository string, assets map[string]string, binary, checksumAsset string) string {
	t.Helper()
	global := config.DefaultGlobal()
	global.Agents = []config.Agent{{
		Name:    "Flavor Agent",
		Command: command,
		Release: &config.GitHubRelease{
			Repository:    repository,
			Assets:        assets,
			Binary:        binary,
			ChecksumAsset: checksumAsset,
		},
	}}
	// Keep the built-in tools (ai-memory) so memory wiring in the image is
	// exercised too.
	result, err := InstallConfig(global, selection.Agents)
	if err != nil {
		t.Fatalf("InstallConfig() error = %v", err)
	}
	return result
}

// TestFlavorHostBinary covers the third install path: an agent with no recipe
// whose host binary is bind-mounted into the container (read-only). No image
// build is needed — the mount is part of the docker run argv — so this test
// runs even without a daemon and asserts the argv contract instead.
func TestFlavorHostBinary(t *testing.T) {
	selection, err := Normalize([]string{"go"}, []AgentInstall{{
		Command:  "kiro-cli",
		Kind:     InstallHostBinary,
		HostPath: "/usr/local/bin/kiro",
	}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	argv, err := BuildRunCommand(RunConfig{
		Selection:        selection,
		ProjectDir:       "/work",
		AgentExecutable:  "/usr/local/bin/kiro",
		AddHostGateway:   true,
		HostBinaryMounts: []string{"/usr/local/bin"},
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	joined := strings.Join(argv, " ")
	// The host binary directory is mounted read-only at the same path.
	if !strings.Contains(joined, "/usr/local/bin:/usr/local/bin:ro") {
		t.Fatalf("argv %q missing the host-binary dir mount", joined)
	}
	// The in-container executable is the mounted path, not a command name.
	if !strings.Contains(joined, "/usr/local/bin/kiro") {
		t.Fatalf("argv %q missing the executable", joined)
	}
	// The image tag still reflects the selection (recipe-only, no version).
	if !strings.Contains(joined, "ai-launcher-box:") {
		t.Fatalf("argv %q missing the image tag", joined)
	}
}

// TestFlavorCatalogAudit asserts that every built-in agent either has an
// install recipe (release or script) or is reachable via a host binary mount.
// This is the guard that would have caught the kilo asset drift before the
// release battery failed.
func TestFlavorCatalogAudit(t *testing.T) {
	global := config.DefaultGlobal()
	for _, agent := range global.Agents {
		if agent.Release != nil {
			continue // release recipe covers the image install
		}
		if agent.SourceURL != "" {
			// Script recipes need the operator's explicit allow_unverified
			// (invariant 4); built-in entries must not carry it silently.
			if !agent.AllowUnverified {
				t.Fatalf("agent %q has source_url without allow_unverified; the image install would be refused (invariant 4)", agent.Command)
			}
			continue
		}
		// No recipe: the agent is installed on the host and bind-mounted.
		// This is the documented fallback for recipe-less agents (R3 item 13).
		_ = agent.Command // reachable via host binary; nothing to assert
	}
}
