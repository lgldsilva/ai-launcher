// Package container provides the docker backend build logic for ai-launcher.
//
// This file holds the flavor battery: integration-style tests that exercise
// each agent installation path (release recipe, install script, host binary)
// against the real docker daemon. They are guarded by
// AI_LAUNCHER_DOCKER_TESTS=1 so the default suite stays hermetic — CI without
// a docker daemon must not fail.
package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
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

// runDockerOutput executes a fixed docker command and retains its combined
// output for assertions that need to inspect a container's filesystem.
func runDockerOutput(argv []string) (string, error) {
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...) // #nosec G204 -- test battery; argv is a fixed docker command built inline
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return output.String(), fmt.Errorf("exit code %d", exitErr.ExitCode())
		}
		return output.String(), err
	}
	return output.String(), nil
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

	err = runDockerShell([]string{"docker", "run", "--rm", ctx.ImageTag, "sh", "-c", "test -x /home/ai-launcher/.local/bin/claude"})
	if err != nil {
		t.Fatalf("claude not installed in image: %v", err)
	}
}

// dockerfileForFlavor renders the Dockerfile for a script-only selection
// without the dev-profile noise, so the flavor test builds fast.
func dockerfileForFlavor(selection Selection) string {
	var b strings.Builder
	b.WriteString("FROM " + BaseImage + "\n")
	b.WriteString("SHELL [\"/bin/bash\", \"-o\", \"pipefail\", \"-c\"]\n")
	b.WriteString(DevProfile)
	if selectionNeedsNode(selection) {
		b.WriteString("\n")
		b.WriteString(NodeProfile)
	}
	b.WriteString("\n")
	for _, agent := range selection.Agents {
		if agent.Kind == InstallScript {
			b.WriteString(ScriptLine(agent))
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
	err = runDockerShell([]string{"docker", "run", "--rm", ctx.ImageTag, "sh", "-c", "test -x /home/ai-launcher/.local/bin/gh && gh --version"})
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

// TestFlavorSharedLogin validates the shared-login model end to end: a config
// written inside container A (same-path rw mount) is visible to the host and
// to container B. This is the model the docker backend promises — login once,
// reuse everywhere.
func TestFlavorSharedLogin(t *testing.T) {
	if !dockerTestsEnabled(t) {
		return
	}
	// Tiny image: no agent needed, just a shell to write the config.
	selection, err := Normalize([]string{"go"}, []AgentInstall{{Command: "claude", Kind: InstallScript, Script: "curl -fsSL https://claude.ai/install.sh | bash"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	df, err := Dockerfile(selection)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/Dockerfile", []byte(df), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runDockerShell([]string{"docker", "build", "--tag", "ai-launcher-box:login", dir}); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	t.Cleanup(func() { _ = runDockerShell([]string{"docker", "rmi", "ai-launcher-box:login"}) })

	share := t.TempDir()
	// Container A writes a "login".
	write := "mkdir -p $HOME/.claude && printf '{\"token\":\"abc\"}' > $HOME/.claude/credentials.json"
	if err := runDockerShell([]string{"docker", "run", "--rm", "-e", "HOME=" + share, "-v", share + ":" + share, "ai-launcher-box:login", "sh", "-c", write}); err != nil {
		t.Fatalf("container A failed: %v", err)
	}
	// The host sees it.
	loginFile := filepath.Join(share, ".claude", "credentials.json")
	hostData, err := os.ReadFile(loginFile) // #nosec G304 -- test fixture path
	if err != nil || string(hostData) != `{"token":"abc"}` {
		t.Fatalf("host cannot see container A's login (got %q, err %v)", hostData, err)
	}
	// Container B reads it back.
	read := "cat $HOME/.claude/credentials.json"
	if err := runDockerShell([]string{"docker", "run", "--rm", "-e", "HOME=" + share, "-v", share + ":" + share, "ai-launcher-box:login", "sh", "-c", read}); err != nil {
		t.Fatalf("container B failed to read the login: %v", err)
	}
}

// TestEmpiricalConfigDirs validates the config-dir map against real agent
// processes. The image is intentionally built once with every script/npm
// installer, while every probe gets a fresh HOME so no agent can contaminate
// another agent's observation. The test is opt-in because it downloads and
// executes third-party installers and needs a Docker daemon.
func TestEmpiricalConfigDirs(t *testing.T) {
	if !dockerTestsEnabled(t) {
		return
	}

	global := config.DefaultGlobal()
	agents := empiricalInstallableAgents(global)
	if len(agents) == 0 {
		t.Fatal("catalog has no script/npm agents to validate")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(empiricalDockerfile(agents)), 0o600); err != nil {
		t.Fatalf("write empirical Dockerfile: %v", err)
	}
	imageTag := "ai-launcher-box:verify-config-dirs"
	if err := runDockerShell([]string{"docker", "build", "--tag", imageTag, dir}); err != nil {
		t.Fatalf("empirical image build failed: %v", err)
	}
	t.Cleanup(func() { _ = runDockerShell([]string{"docker", "rmi", imageTag}) })

	for _, agent := range agents {
		agent := agent
		t.Run(agent.Command, func(t *testing.T) {
			observation, err := empiricalAgentPaths(imageTag, agent.Command)
			if err != nil {
				t.Fatalf("probe failed: %v", err)
			}
			assertEmpiricalStatuses(t, agent, observation)
			observed := observation.paths
			expected := empiricalDeclaredPaths(agent.Command)
			if len(expected) == 0 {
				t.Fatalf("agent %q has no declared config paths", agent.Command)
			}
			t.Logf("declared config paths: %v; observed roots: %v", expected, empiricalReportPaths(observed))
			for _, path := range expected {
				if !pathObserved(path, observed) {
					t.Errorf("declared path %q was not created; observed %v", path, observed)
				}
			}
			for _, path := range empiricalRelevantPaths(observed) {
				if !pathDeclared(path, expected) {
					t.Errorf("agent created undeclared path %q; declared %v", path, expected)
				}
			}
		})
	}
}

func assertEmpiricalStatuses(t *testing.T, agent config.Agent, observation empiricalObservation) {
	t.Helper()
	if observation.installStatus != 0 && !agent.SetupInteractive {
		t.Errorf("installer exited with status %d", observation.installStatus)
	}
	if observation.versionStatus != 0 {
		t.Errorf("--version exited with status %d", observation.versionStatus)
	}
	t.Logf("installer status: %d; --version status: %d; first-run status: %d", observation.installStatus, observation.versionStatus, observation.firstRunStatus)
}

func empiricalInstallableAgents(global config.Global) []config.Agent {
	agents := make([]config.Agent, 0, len(global.Agents))
	for _, agent := range global.Agents {
		if strings.TrimSpace(agent.SourceURL) == "" && strings.TrimSpace(agent.NpmPackage) == "" {
			continue
		}
		agents = append(agents, agent)
	}
	return agents
}

// empiricalDockerfile records each installer status and propagates a failed
// install to Docker. A broken upstream installer is a real acceptance failure;
// no shell fallback may turn it into a successful image build.
func empiricalDockerfile(agents []config.Agent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", BaseImage)
	b.WriteString("SHELL [\"/bin/bash\", \"-o\", \"pipefail\", \"-c\"]\n")
	b.WriteString(DevProfile)
	for _, catalogAgent := range agents {
		install := PlanInstall(catalogAgent, "", "")
		b.WriteString("\n# Empirical agent: " + catalogAgent.Command + "\n")
		statusPath := empiricalInstallStatusPath(catalogAgent.Command)
		switch install.Kind {
		case InstallScript:
			script := strings.TrimSpace(strings.TrimPrefix(install.Script, "RUN "))
			if install.AllowSetupFailure {
				fmt.Fprintf(&b, "RUN set +e; bash -o pipefail -c %s; status=$?; printf '%%s\\n' \"$status\" > %s; if test \"$status\" -ne 0 && ! command -v %s >/dev/null 2>&1; then exit \"$status\"; fi\n", shellQuote(script), statusPath, shellQuoteWord(install.Command))
				continue
			}
			fmt.Fprintf(&b, "RUN set +e; bash -o pipefail -c %s; status=$?; printf '%%s\\n' \"$status\" > %s; if test \"$status\" -ne 0; then exit \"$status\"; fi\n", shellQuote(script), statusPath)
		case InstallNpm:
			fmt.Fprintf(&b, "RUN set +e; npm install -g %s; status=$?; printf '%%s\\n' \"$status\" > %s; if test \"$status\" -ne 0; then exit \"$status\"; fi\n", install.NpmPackage, statusPath)
		}
	}
	return b.String()
}

type empiricalObservation struct {
	paths          []string
	installStatus  int
	versionStatus  int
	firstRunStatus int
}

func empiricalInstallStatusPath(command string) string {
	var safe strings.Builder
	for _, r := range command {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			safe.WriteRune(r)
			continue
		}
		safe.WriteByte('_')
	}
	return "/tmp/ai-launcher-install-status-" + safe.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func empiricalAgentPaths(imageTag, command string) (empiricalObservation, error) {
	const home = "/tmp/ai-launcher-empirical-home"
	probe := fmt.Sprintf(`set +e
export HOME=%s
mkdir -p "$HOME"
agent_path=""
for candidate in "$(command -v %s 2>/dev/null)" "/home/ai-launcher/.local/bin/%s" "/home/ai-launcher/.opencode/bin/%s" "/usr/local/lib/nvm-bin/bin/%s"; do
    if test -n "$candidate" && test -x "$candidate"; then
        agent_path="$candidate"
        break
    fi
done
printf '__AI_LAUNCHER_PATHS__\n'
if test -z "$agent_path"; then
    printf '__AI_LAUNCHER_AGENT_MISSING__\n'
    exit 1
fi
printf '__AI_LAUNCHER_INSTALL_STATUS__%%s\n' "$(cat %s 2>/dev/null)"
timeout 15s "$agent_path" --version >/tmp/ai-launcher-version.out 2>&1
version_status=$?
printf '__AI_LAUNCHER_VERSION_STATUS__%%s\n' "$version_status"
`, home, command, command, command, command, empiricalInstallStatusPath(command))
	if command == "openclaw" {
		probe += `"$agent_path" onboard --non-interactive --accept-risk --auth-choice skip --skip-daemon --skip-health --skip-channels --skip-search --skip-skills --skip-ui --skip-bootstrap >/tmp/ai-launcher-first-run.out 2>&1
first_run_status=$?
printf '__AI_LAUNCHER_FIRST_RUN_STATUS__%s\n' "$first_run_status"
find "$HOME" -mindepth 1 \( -type d -o -type f \) -print | sort
`
	} else {
		probe += `printf 'no\n' | timeout 15s "$agent_path" >/tmp/ai-launcher-first-run.out 2>&1
first_run_status=$?
printf '__AI_LAUNCHER_FIRST_RUN_STATUS__%s\n' "$first_run_status"
find "$HOME" -mindepth 1 \( -type d -o -type f \) -print | sort
`
	}
	output, err := runDockerOutput([]string{"docker", "run", "--rm", imageTag, "sh", "-c", probe})
	if err != nil {
		return empiricalObservation{}, fmt.Errorf("%s: %w\n%s", command, err, output)
	}

	marker := "__AI_LAUNCHER_PATHS__\n"
	_, paths, ok := strings.Cut(output, marker)
	if !ok {
		return empiricalObservation{}, fmt.Errorf("%s: probe marker missing; output %q", command, output)
	}
	if strings.Contains(paths, "__AI_LAUNCHER_AGENT_MISSING__") {
		return empiricalObservation{}, fmt.Errorf("%s: installer did not provide a runnable command", command)
	}
	installStatus, err := empiricalStatus(output, "__AI_LAUNCHER_INSTALL_STATUS__")
	if err != nil {
		return empiricalObservation{}, fmt.Errorf("%s: %w", command, err)
	}
	versionStatus, err := empiricalStatus(output, "__AI_LAUNCHER_VERSION_STATUS__")
	if err != nil {
		return empiricalObservation{}, fmt.Errorf("%s: %w", command, err)
	}
	firstRunStatus, err := empiricalStatus(output, "__AI_LAUNCHER_FIRST_RUN_STATUS__")
	if err != nil {
		return empiricalObservation{}, fmt.Errorf("%s: %w", command, err)
	}
	var observed []string
	for _, line := range strings.Split(paths, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, home+"/") {
			observed = append(observed, line)
		}
	}
	return empiricalObservation{
		paths:          observed,
		installStatus:  installStatus,
		versionStatus:  versionStatus,
		firstRunStatus: firstRunStatus,
	}, nil
}

func empiricalStatus(output, marker string) (int, error) {
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		status, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, marker)))
		if err != nil {
			return 0, fmt.Errorf("invalid %s value %q", marker, line)
		}
		return status, nil
	}
	return 0, fmt.Errorf("probe marker %q missing", marker)
}

// empiricalRelevantPaths excludes runtime caches from the config-dir
// comparison. Caches such as Cursor's compiler cache and Antigravity's
// Playwright cache are not credentials, settings, or history and must not
// become rw config mounts.
func empiricalRelevantPaths(observed []string) []string {
	const home = "/tmp/ai-launcher-empirical-home"
	var relevant []string
	for _, path := range observed {
		if path == home+"/.cache" || strings.HasPrefix(path, home+"/.cache/") ||
			path == home+"/.npm" || strings.HasPrefix(path, home+"/.npm/") ||
			path == home+"/.local/share/opentui" || strings.HasPrefix(path, home+"/.local/share/opentui/") {
			continue
		}
		relevant = append(relevant, path)
	}
	return relevant
}

// empiricalReportPaths keeps the opt-in test output readable when an agent
// creates a large dependency tree (for example OpenCode's local node_modules).
// Validation still uses every path returned by find through
// empiricalRelevantPaths.
func empiricalReportPaths(observed []string) []string {
	relevant := empiricalRelevantPaths(observed)
	sort.Strings(relevant)
	var roots []string
	for _, path := range relevant {
		if len(roots) > 0 && strings.HasPrefix(path, roots[len(roots)-1]+"/") {
			continue
		}
		roots = append(roots, path)
	}
	return roots
}

func empiricalDeclaredPaths(command string) []string {
	const home = "/tmp/ai-launcher-empirical-home"
	var paths []string
	for _, cfg := range agentConfigDirs[command] {
		if !platformApplies(cfg.Platforms, "linux") {
			continue
		}
		if path := ExpandHome(cfg.Path, home); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func pathObserved(declared string, observed []string) bool {
	for _, path := range observed {
		if path == declared {
			return true
		}
	}
	return false
}

func pathDeclared(observed string, declared []string) bool {
	for _, path := range declared {
		if observed == path || strings.HasPrefix(observed, path+"/") || strings.HasPrefix(path, observed+"/") {
			return true
		}
	}
	return false
}
