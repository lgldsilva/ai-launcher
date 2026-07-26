package main

import (
	"os"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
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
	b, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, append(b, []byte("# touched after save\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run"); err == nil {
		t.Fatal("run() = nil; a file edited after the launcher saved it must be refused again")
	}
}
