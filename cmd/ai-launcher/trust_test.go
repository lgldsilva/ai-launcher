package main

import (
	"strings"
	"testing"
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
