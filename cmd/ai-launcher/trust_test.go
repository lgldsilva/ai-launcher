package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// A .ai-launch.yaml travels with the repository, so it is attacker-controlled
// for any checkout the operator did not write. It must not be able to pick the
// executed binary: an agent the catalog cannot resolve is refused instead of
// being synthesized into the argv.
func TestLocalConfigCannotChooseAnUnresolvableAgent(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: /bin/sh\noptions:\n  jail: false\n  memory: false\n")
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err == nil {
		t.Fatal("run() = nil; a local config naming an unknown agent must be refused")
	}
	if strings.Contains(stdout, "/bin/sh") {
		t.Fatalf("stdout = %q; the unresolved agent must never reach the argv", stdout)
	}
	if !strings.Contains(err.Error(), "--agent") && !strings.Contains(err.Error(), "--add") {
		t.Errorf("error = %v; want the trusted opt-in path named", err)
	}
}

// The same value typed by the operator stays trusted: the boundary is about
// file-supplied configuration, not about what the user asks for explicitly.
func TestExplicitAgentFlagRemainsTrusted(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: false\n")
	stubToolsOnPath(t, "sh-like")
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--no-jail", "--agent", "sh-like", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; an explicit --agent must stay trusted", err)
	}
	if !strings.Contains(stdout, "sh-like") {
		t.Fatalf("stdout = %q; want the explicitly requested agent", stdout)
	}
}

// Turning the sandbox off is a security decision. A repository file must not
// make it silently, while the operator's own --no-jail still works.
func TestLocalConfigCannotDisableTheJailOnItsOwn(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: false\n")
	if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run"); err == nil {
		t.Fatal("run() = nil; a local config disabling the jail must be refused")
	}
	if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--no-jail", "--dry-run"); err != nil {
		t.Fatalf("run() error = %v; an explicit --no-jail must still work", err)
	}
}

// A mount is a hole in the sandbox. A relative path is ambiguous and a
// filesystem root defeats the boundary entirely, so neither is accepted from a
// repository-supplied file.
func TestLocalConfigRejectsUnsafeMounts(t *testing.T) {
	for _, mount := range []string{"/", "relative/path"} {
		t.Run(mount, func(t *testing.T) {
			globalPath, localPath, _ := writeTestConfigs(t,
				"agent: custom-cli\noptions:\n  jail: true\n  memory: false\nmounts:\n  - path: "+mount+"\n")
			if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run"); err == nil {
				t.Fatalf("run() = nil; mount %q must be refused", mount)
			}
		})
	}
}

// The trust boundary must not fight the launcher's own save flow: a selection
// the operator saved themselves (Ctrl+S in the TUI, or --save) is refused by
// enforceLocalConfigTrust on the very next launch, so reopening the folder
// drops every choice they made. A config the launcher wrote is not the same
// input as one a cloned repository shipped, and the operator's own saved
// jail: false must be honored — today it is not.
func TestLauncherSavedLocalConfigIsHonoredOnReload(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	// What Ctrl+S / --save does: the operator turns the jail off and saves.
	saved := launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseJail:   false,
		UseMemory: false,
	}
	if err := saveLocalSelection(globalPath, true, localPath, local, saved); err != nil {
		t.Fatalf("saveLocalSelection() error = %v", err)
	}
	// Reopen the folder with no flags: the saved selection must come back.
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() refused the launcher's own saved config: %v", err)
	}
	if strings.Contains(stdout, "ai-jail") {
		t.Fatalf("stdout = %q; the saved jail: false was not honored", stdout)
	}
	if !strings.Contains(stdout, "custom-cli") {
		t.Fatalf("stdout = %q; the saved agent selection was lost", stdout)
	}
}

// Provenance covers exactly what the launcher wrote, byte for byte: editing
// the saved file afterwards — by hand or by a git pull — changes its hash and
// the trust boundary applies again.
func TestTamperedSavedConfigIsRefusedAgain(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	saved := launcher.LaunchConfig{Agent: config.Agent{Command: "custom-cli"}, UseJail: false, UseMemory: false}
	if err := saveLocalSelection(globalPath, true, localPath, local, saved); err != nil {
		t.Fatalf("saveLocalSelection() error = %v", err)
	}
	b, err := os.ReadFile(localPath) // #nosec G304 -- test fixture path built by t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, append(b, []byte("# touched after save\n")...), 0o600); err != nil { // #nosec G703 -- test fixture path built by t.TempDir()
		t.Fatal(err)
	}
	if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run"); err == nil {
		t.Fatal("run() = nil; a file edited after the launcher saved it must be refused again")
	}
}

// A local config enabling docker permission must not silently grant it; the
// operator has to pass --docker on the command line or save the selection.
func TestLocalConfigPermissionsRequireExplicitFlags(t *testing.T) {
	t.Run("docker:true blocks without flag", func(t *testing.T) {
		globalPath, localPath, _ := writeTestConfigs(t,
			"agent: custom-cli\noptions:\n  jail: true\n  memory: false\npermissions:\n  docker: true\n")
		_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
		if err == nil {
			t.Fatal("run() = nil; permissions.docker:true must be refused without --docker")
		}
		if !strings.Contains(err.Error(), "docker") || !strings.Contains(err.Error(), "--docker") {
			t.Errorf("error = %v; want it mentions both 'docker' and the --docker flag", err)
		}
	})
	t.Run("gh:true requires --gh", func(t *testing.T) {
		globalPath, localPath, _ := writeTestConfigs(t,
			"agent: custom-cli\noptions:\n  jail: true\n  memory: false\npermissions:\n  gh: true\n")
		if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run"); err == nil {
			t.Fatal("run() = nil; gh:true needs --gh")
		}
	})
	t.Run("ssh:true requires --ssh", func(t *testing.T) {
		globalPath, localPath, _ := writeTestConfigs(t,
			"agent: custom-cli\noptions:\n  jail: true\n  memory: false\npermissions:\n  ssh: true\n")
		if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run"); err == nil {
			t.Fatal("run() = nil; ssh:true needs --ssh")
		}
	})
	t.Run("gpu:true requires --gpu", func(t *testing.T) {
		globalPath, localPath, _ := writeTestConfigs(t,
			"agent: custom-cli\noptions:\n  jail: true\n  memory: false\npermissions:\n  gpu: true\n")
		if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run"); err == nil {
			t.Fatal("run() = nil; gpu:true needs --gpu")
		}
	})
}

// Matching CLI flags make enabled permissions through.
func TestPermissionFlagsOverrideTrustRefusals(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: true\n  memory: false\npermissions:\n  docker: true\n  ssh: true\n")
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--docker", "--ssh", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; matching flags must permit file-supplied permissions", err)
	}
	if !strings.Contains(stdout, "custom-cli") {
		t.Fatalf("stdout = %q; wanted a successful dry-run", stdout)
	}
}

func TestSavedLocalConfigHonorsPermissions(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	saved := launcher.LaunchConfig{
		Agent:       config.Agent{Command: "custom-cli"},
		UseJail:     true,
		UseMemory:   false,
		Permissions: map[string]bool{"docker": true},
	}
	if err := saveLocalSelection(globalPath, true, localPath, local, saved); err != nil {
		t.Fatalf("saveLocalSelection() error = %v", err)
	}
	// Reopen with no flags — the saved file should be trusted by hash.
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() refused the launcher's own saved config: %v", err)
	}
	if !strings.Contains(stdout, "custom-cli") {
		t.Fatalf("stdout = %q; saved permissions were lost", stdout)
	}
}

// Local config mounting a sensitive path is refused.
func TestLocalConfigRejectsSensitiveMounts(t *testing.T) {
	sensitivePaths := []string{"/etc", "/etc/passwd", "/var/run/docker.sock", "/home", "/Users", "/Volumes"}
	for _, p := range sensitivePaths {
		t.Run(p, func(t *testing.T) {
			globalPath, localPath, _ := writeTestConfigs(t,
				"agent: custom-cli\noptions:\n  jail: true\n  memory: false\nmounts:\n  - path: "+p+"\n")
			if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run"); err == nil {
				t.Fatalf("run() = nil; mount %q must be refused as sensitive path", p)
			}
		})
	}
}

// Local options.jail_flags are refused because they weaken sandbox posture.
func TestLocalConfigJailFlagsRequireSaveOrProfile(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantStr string
	}{
		{"seccomp off", "options:\n  jail: true\n  memory: false\n  jail_flags:\n    seccomp: false\n", "jail_flags"},
		{"landlock off", "options:\n  jail: true\n  memory: false\n  jail_flags:\n    landlock: false\n", "jail_flags"},
		{"private_home true", "options:\n  jail: true\n  memory: false\n  jail_flags:\n    private_home: true\n", "jail_flags"},
		{"browser set", "options:\n  jail: true\n  memory: false\n  jail_flags:\n    browser: hard\n", "jail_flags"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			globalPath, localPath, _ := writeTestConfigs(t, tt.yaml)
			_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
			if err == nil {
				t.Fatalf("run() = nil; jail_flags must be refused without save/profile")
			}
			if !strings.Contains(err.Error(), tt.wantStr) {
				t.Errorf("error = %v; want it mentioning %q", err, tt.wantStr)
			}
		})
	}
}

// yolo from local config is refused unless --yolo.
func TestLocalConfigYoloRequiresFlag(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: true\n  memory: false\n  yolo: true\n")
	if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run"); err == nil {
		t.Fatal("run() = nil; yolo:true must be refused without --yolo")
	}
	// With --yolo, the launch succeeds (yolo flag overrides trust check).
	stubToolsOnPath(t, "sh-like")
	if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--no-jail", "--yolo", "--agent", "sh-like", "--dry-run"); err != nil {
		t.Fatalf("run() error = %v; --yolo must accept file yolo setting", err)
	}
}

// extra_args from local config is refused unless --args/--extra-args.
func TestLocalConfigExtraArgsRequiresFlag(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: true\n  memory: false\n  extra_args:\n    - --foo\n")
	_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err == nil {
		t.Fatal("run() = nil; extra_args in local config must be refused without --args")
	}

	// The key point: refusal due to extra_args goes away when --extra-args is passed.
	_, _, err2 := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--agent", "codex", "--no-jail", "--no-memory", "--extra-args", "--bar", "--dry-run")
	if err2 != nil && strings.Contains(err2.Error(), "extra_args") {
		t.Fatalf("extra_args refusal persisted despite flag: %v", err2)
	}
}

// A checkout-controlled .ai-jail symlink changes what ai-jail reads and writes
// when config masking is disabled. The launcher detects it and refuses the
// launch unless the operator explicitly bypasses the local file.
func TestLocalConfigSymlinkedProjectJailRequiresExplicitConsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	target := filepath.Join(dir, "ai-jail.toml")
	if err := os.WriteFile(target, []byte("# jail config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".ai-jail")); err != nil {
		t.Fatal(err)
	}
	restore := chdir(t, dir)
	defer restore()

	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err == nil {
		t.Fatal("run() = nil; symlinked .ai-jail must be refused without explicit consent")
	}
	if !strings.Contains(err.Error(), ".ai-jail") || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %v; want refusal naming .ai-jail and symlink", err)
	}

	// --no-local-config is the explicit opt-out that bypasses the checkout file.
	// --agent is required because DefaultLocal() picks the built-in "claude"
	// agent when no workspace file owns the selection, and "claude" is not in
	// the test PATH.
	_, _, err = runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--no-local-config", "--no-jail", "--agent", "custom-cli", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; --no-local-config must bypass symlink refusal", err)
	}
}

// A broken .ai-jail symlink also disables masking and must be refused rather
// than launching into an undefined config path.
func TestLocalConfigBrokenProjectJailSymlinkRequiresExplicitConsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, ".ai-jail")); err != nil {
		t.Fatal(err)
	}
	restore := chdir(t, dir)
	defer restore()

	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err == nil {
		t.Fatal("run() = nil; broken .ai-jail symlink must be refused")
	}
	if !strings.Contains(err.Error(), "broken symlink") {
		t.Errorf("error = %v; want broken symlink refusal", err)
	}
}

// workspace/project from local config are forwarded verbatim to ai-memory run,
// so an unsaved file must not redirect an authenticated token to another scope.
func TestLocalConfigMemoryScopeRequiresExplicitFlags(t *testing.T) {
	t.Run("workspace requires --workspace", func(t *testing.T) {
		globalPath, localPath, _ := writeTestConfigs(t,
			"agent: custom-cli\noptions:\n  jail: false\n  memory: true\n  workspace: acme\n")
		_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--no-jail", "--dry-run")
		if err == nil {
			t.Fatal("run() = nil; workspace in local config must be refused without --workspace")
		}
		if !strings.Contains(err.Error(), "workspace") || !strings.Contains(err.Error(), "--workspace") {
			t.Errorf("error = %v; want refusal naming workspace and --workspace", err)
		}
	})
	t.Run("project requires --project", func(t *testing.T) {
		globalPath, localPath, _ := writeTestConfigs(t,
			"agent: custom-cli\noptions:\n  jail: false\n  memory: true\n  project: billing\n")
		_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--no-jail", "--dry-run")
		if err == nil {
			t.Fatal("run() = nil; project in local config must be refused without --project")
		}
		if !strings.Contains(err.Error(), "project") || !strings.Contains(err.Error(), "--project") {
			t.Errorf("error = %v; want refusal naming project and --project", err)
		}
	})
}

// Matching CLI flags make memory scope values from the file pass the trust gate.
func TestMemoryScopeFlagsOverrideTrustRefusals(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: false\n  memory: true\n  workspace: acme\n  project: billing\n")
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--no-jail", "--workspace", "acme", "--project", "billing", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; matching flags must permit file-supplied scope", err)
	}
	if !strings.Contains(stdout, "--workspace acme") || !strings.Contains(stdout, "--project billing") {
		t.Fatalf("stdout = %q; want scope forwarded", stdout)
	}
}

// A saved local config is trusted operator input, so workspace/project may be
// honored without repeating the CLI flags.
func TestSavedLocalConfigHonorsMemoryScope(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	saved := launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseJail:   false,
		UseMemory: true,
		Workspace: "acme",
		Project:   "billing",
	}
	if err := saveLocalSelection(globalPath, true, localPath, local, saved); err != nil {
		t.Fatalf("saveLocalSelection() error = %v", err)
	}
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() refused the launcher's own saved config: %v", err)
	}
	if !strings.Contains(stdout, "--workspace acme") || !strings.Contains(stdout, "--project billing") {
		t.Fatalf("stdout = %q; saved scope was lost", stdout)
	}
}

// The docker container backend replaces the sandbox (jail → container), which
// is a security-relevant change. A repository file must not switch the
// sandbox silently; the operator's own --docker-backend still works.
func TestLocalConfigCannotEnableDockerBackendOnItsOwn(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: true\n  memory: false\n  docker: true\n  stacks: [go]\n")
	stubToolsOnPath(t, "docker")
	if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--workspace", "/w", "--dry-run"); err == nil {
		t.Fatal("run() = nil; a local config enabling the docker backend must be refused")
	}
	if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--docker-backend", "--workspace", "/w", "--dry-run"); err != nil {
		t.Fatalf("run() error = %v; an explicit --docker-backend must still work", err)
	}
}

// The same docker choice saved by the operator becomes trusted input, exactly
// like jail: false is honored after --save.
func TestSavedLocalConfigHonorsDockerBackend(t *testing.T) {
	globalPath, localPath, _ := writeTestConfigs(t,
		"agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	stubToolsOnPath(t, "docker")
	local, err := config.LoadLocal(localPath)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	saved := launcher.LaunchConfig{
		Agent:     config.Agent{Command: "custom-cli"},
		UseDocker: true,
		UseMemory: false,
		Workspace: "/w",
		Docker: container.RunConfig{
			Selection: selectionFromTest("go"),
		},
	}
	if err := saveLocalSelection(globalPath, true, localPath, local, saved); err != nil {
		t.Fatalf("saveLocalSelection() error = %v", err)
	}
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--workspace", "/w", "--dry-run")
	if err != nil {
		t.Fatalf("run() refused the launcher's own saved docker config: %v", err)
	}
	if !strings.Contains(stdout, "docker run") {
		t.Fatalf("stdout = %q; want the docker run argv", stdout)
	}
}

// selectionFromTest builds a minimal valid docker selection for trust tests.
func selectionFromTest(stacks ...string) container.Selection {
	selection, err := container.Normalize(stacks, []container.AgentInstall{{
		Command: "custom-cli",
		Kind:    container.InstallRelease,
		Version: "0.0.0-test",
	}}, nil)
	if err != nil {
		panic(err)
	}
	return selection
}
