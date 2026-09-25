package launcher

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// TestLiveUpstreamScenarios execs the real ai-jail 2.x and ai-memory 2.x
// binaries for the scenarios the Gherkin contract only assembles. The contract
// locks argv shape; this is the run that checks the installed CLIs accept it.
// CI and the mutation container have no upstream install, so they skip.
func TestLiveUpstreamScenarios(t *testing.T) {
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" || os.Getenv("MUTATION_BASE") != "" {
		t.Skip("live upstream exec is host-only")
	}
	jailPath := requireRealCLI(t, "ai-jail", "ai-jail")
	memoryPath := requireRealCLI(t, "ai-memory", "ai-memory")
	jailVersion := liveVersion(t, jailPath)
	memoryVersion := liveVersion(t, memoryPath)
	if compareVersions(jailVersion, config.MinAllowHostAIJailVersion) < 0 {
		t.Skipf("ai-jail %s is below %s", jailVersion, config.MinAllowHostAIJailVersion)
	}
	if compareVersions(memoryVersion, config.MinNoAutowireAIMemoryVersion) < 0 {
		t.Skipf("ai-memory %s is below %s", memoryVersion, config.MinNoAutowireAIMemoryVersion)
	}

	t.Run("allow-host replaces open network", func(t *testing.T) {
		liveAllowHostReplacesOpenNetwork(t, jailVersion)
	})

	bin := buildLauncher(t)

	t.Run("preflight refuses allow_hosts below ai-jail 2", func(t *testing.T) {
		livePreflightRefusesOldJail(t, bin)
	})

	t.Run("preflight refuses allow_hosts with network true", func(t *testing.T) {
		livePreflightRefusesNetworkConflict(t, bin)
	})

	t.Run("ai-memory accepts no-autowire and the 2.4 harness names", func(t *testing.T) {
		liveMemoryAcceptsNoAutowire(t, memoryPath)
	})

	t.Run("doctor reports a managed runner behind PATH", func(t *testing.T) {
		liveDoctorReportsManagedRunnerBehind(t, bin, jailPath, memoryPath, memoryVersion)
	})
}

func liveAllowHostReplacesOpenNetwork(t *testing.T, jailVersion string) {
	t.Helper()
	argv, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "true"},
		Executable:  "/usr/bin/true",
		UseJail:     true,
		JailExec:    true,
		JailVersion: jailVersion,
		Permissions: map[string]bool{config.PermissionJail: true, config.PermissionNetwork: true},
		JailFlags:   config.JailFlags{AllowHosts: []string{"api.anthropic.com", "github.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--no-network") || strings.Contains(joined, " --network ") {
		t.Fatalf("argv = %s; allow_hosts must replace unrestricted network", joined)
	}
	for _, want := range []string{"--allow-host", "api.anthropic.com", "github.com"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv = %s; missing %s", joined, want)
		}
	}
	out, err := runLive(t, argv, 30*time.Second)
	if err != nil {
		t.Fatalf("ai-jail rejected allow-host: %v\n%s\nargv=%s", err, out, joined)
	}
}

func livePreflightRefusesOldJail(t *testing.T, bin string) {
	t.Helper()
	stdout, stderr, err := runLauncher(t, bin, trustedAllowHostConfig(t, false), fakeJailPATH(t, "1.20.1"))
	if err == nil {
		t.Fatalf("launcher accepted allow_hosts on a 1.20.1 jail\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "allow-host-requires-ai-jail-2") {
		t.Fatalf("stderr = %s; want allow-host-requires-ai-jail-2", stderr)
	}
}

func livePreflightRefusesNetworkConflict(t *testing.T, bin string) {
	t.Helper()
	stdout, stderr, err := runLauncher(t, bin, trustedAllowHostConfig(t, true), nil)
	if err == nil {
		t.Fatalf("launcher accepted allow_hosts with network: true\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "allow-host-conflicts-with-network") {
		t.Fatalf("stderr = %s; want allow-host-conflicts-with-network", stderr)
	}
}

// Clap lists canonical names only. Aliases are still accepted, which is the
// check that matters: the error for an unknown name must name opencode2 and
// must not call --no-autowire unexpected, and each alias must get past
// parsing. A dead server stops the run before any agent starts, so this does
// not open a workstream.
func liveMemoryAcceptsNoAutowire(t *testing.T, memoryPath string) {
	t.Helper()
	text := runMemory(t, memoryPath, "not-a-harness", "--no-autowire")
	if strings.Contains(text, "unexpected argument") || strings.Contains(text, "unexpected value") {
		t.Fatalf("ai-memory rejected --no-autowire after the harness:\n%s", text)
	}
	if !strings.Contains(text, "opencode2") {
		t.Fatalf("unknown-harness error missing opencode2:\n%s", text)
	}
	for _, name := range []string{"grok-build", "claude-code", "oh-my-pi", "kimi-code", "open-code", "opencode2", "opencode-v2"} {
		got := runMemory(t, memoryPath, name, "--fresh", "--no-autowire", "--", "--version")
		if strings.Contains(got, "invalid value") || strings.Contains(got, "unexpected argument") {
			t.Fatalf("ai-memory rejected harness %s with --no-autowire:\n%s", name, got)
		}
		if !strings.Contains(got, "the agent was not started") {
			t.Fatalf("harness %s did not stop before the agent:\n%s", name, got)
		}
	}
}

func liveDoctorReportsManagedRunnerBehind(t *testing.T, bin, jailPath, memoryPath, memoryVersion string) {
	t.Helper()
	home := t.TempDir()
	managed := filepath.Join(home, ".local", "share", "ai-launcher", "bin")
	if err := os.MkdirAll(managed, 0o750); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho 'ai-memory 1.34.0'\n"
	if err := os.WriteFile(filepath.Join(managed, "ai-memory"), []byte(script), 0o700); err != nil { // #nosec G306 -- the doctor execs this fixture
		t.Fatal(err)
	}
	binDir := t.TempDir()
	writeExecWrapper(t, filepath.Join(binDir, "ai-jail"), jailPath)
	writeExecWrapper(t, filepath.Join(binDir, "ai-memory"), memoryPath)
	stdout, stderr, err := runLauncher(t, bin, nil, []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	combined := stdout + stderr
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, combined)
	}
	if !strings.Contains(combined, "ai-memory-native-behind-path") || !strings.Contains(combined, memoryVersion) {
		t.Fatalf("doctor = %s; want the managed 1.34.0 runner behind PATH %s", combined, memoryVersion)
	}
}

// runMemory execs `ai-memory run` against a data dir and server that do not
// exist, so a parsed harness fails while opening the workstream instead of
// starting an agent or writing to the operator's store.
func runMemory(t *testing.T, memoryPath string, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	argv := append([]string{"run"}, args...)
	cmd := exec.CommandContext(ctx, memoryPath, argv...) // #nosec G204 -- path is LookPath of ai-memory; args are fixed harness names
	cmd.Env = mergeEnv(os.Environ(), []string{
		"HOME=" + dir,
		"USERPROFILE=" + dir,
		"AI_MEMORY_DATA_DIR=" + filepath.Join(dir, "data"),
		"AI_MEMORY_SERVER_URL=http://127.0.0.1:9",
	})
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func liveVersion(t *testing.T, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput() // #nosec G204 -- path is LookPath of a fixed tool name
	if err != nil {
		t.Fatalf("%s --version: %v\n%s", path, err, out)
	}
	version := semverPattern.FindString(string(out))
	if version == "" {
		t.Fatalf("%s --version = %q; no version token", path, out)
	}
	return version
}

func buildLauncher(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "ai-launcher")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/ai-launcher") // #nosec G204 -- fixed build of this module
	cmd.Dir = filepath.Join("..", "..")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("build launcher: %v\n%s", err, buf.String())
	}
	return out
}

// trustedAllowHostConfig writes a launcher-trusted local config. networkForced
// selects the conflict scenario; otherwise the list is the only jail flag.
func trustedAllowHostConfig(t *testing.T, networkForced bool) []string {
	t.Helper()
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	localPath := filepath.Join(dir, "local.yaml")
	if err := config.SaveGlobal(globalPath, config.DefaultGlobal()); err != nil {
		t.Fatal(err)
	}
	flags := config.JailFlags{AllowHosts: []string{"api.anthropic.com"}}
	if networkForced {
		on := true
		flags.Network = &on
	}
	if err := config.SaveLocal(localPath, config.Local{
		Version: config.CurrentVersion,
		Agent:   "claude",
		Options: config.Options{Jail: true, Memory: false, JailFlags: flags},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.RecordTrustedLocalConfig(globalPath, localPath); err != nil {
		t.Fatal(err)
	}
	return []string{
		"--config", globalPath,
		"--local-config", localPath,
		"--agent", "claude",
		"--dry-run",
	}
}

func fakeJailPATH(t *testing.T, version string) []string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'ai-jail " + version + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "ai-jail"), []byte(script), 0o700); err != nil { // #nosec G306 -- PATH fixture the launcher execs for --version
		t.Fatal(err)
	}
	return []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}
}

func writeExecWrapper(t *testing.T, path, target string) {
	t.Helper()
	script := "#!/bin/sh\nexec " + shellQuote(target) + " \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { // #nosec G306 -- PATH fixture that execs a fixed absolute binary
		t.Fatal(err)
	}
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func runLauncher(t *testing.T, bin string, args, extraEnv []string) (string, string, error) {
	t.Helper()
	if args == nil {
		args = []string{"--doctor"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- bin is the test-built launcher; args are fixed flags plus temp config paths
	cmd.Dir = t.TempDir()
	if extraEnv != nil {
		cmd.Env = mergeEnv(os.Environ(), extraEnv)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// mergeEnv applies overrides in place of any earlier value of the same key.
// Appending to os.Environ is not enough: getenv returns the first match.
func mergeEnv(base, overrides []string) []string {
	replace := make(map[string]string, len(overrides))
	for _, entry := range overrides {
		key, value, _ := strings.Cut(entry, "=")
		replace[key] = value
	}
	out := make([]string, 0, len(base)+len(overrides))
	seen := make(map[string]bool, len(replace))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if value, ok := replace[key]; ok {
			if !seen[key] {
				out = append(out, key+"="+value)
				seen[key] = true
			}
			continue
		}
		out = append(out, entry)
	}
	for key, value := range replace {
		if !seen[key] {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func runLive(t *testing.T, argv []string, timeout time.Duration) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- argv comes from Build in this test
	dir := t.TempDir()
	cmd.Dir = dir
	cmd.Env = mergeEnv(os.Environ(), []string{"HOME=" + dir, "USERPROFILE=" + dir})
	out, err := cmd.CombinedOutput()
	return string(out), err
}
