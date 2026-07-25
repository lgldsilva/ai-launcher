package main

import (
	"strings"
	"testing"
)

func TestDecideLaunchAction(t *testing.T) {
	if decideLaunchAction(true) != actionPrint {
		t.Fatal("dry-run must print instead of executing")
	}
	if decideLaunchAction(false) != actionExecute {
		t.Fatal("a confirmed launch (Enter in the TUI or any CLI call) must execute")
	}
}

func TestContinueFlagBuildsHarnessLessAiMemoryRun(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--continue", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.TrimSpace(out) != "ai-memory run" {
		t.Fatalf("--continue dry-run = %q; want %q", out, "ai-memory run")
	}
}

func TestWorkspaceProjectAndWorkstreamForwarding(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath,
		"--workspace", "acme", "--project", "billing", "--workstream", "release-1", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-memory run --workspace acme --project billing --workstream release-1 custom-cli"
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q", out, want)
	}
}

func TestNewWorkstreamStillCreates(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--new", "fresh", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.TrimSpace(out) != "ai-memory run --new fresh custom-cli" {
		t.Fatalf("--new dry-run = %q", out)
	}
}

func TestCliLaunchUsesJailExecProgrammaticMode(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.TrimSpace(out) != "ai-jail --exec custom-cli" {
		t.Fatalf("CLI dry-run = %q; want ai-jail --exec programmatic mode", out)
	}
}

func TestJailFlagsFromLocalConfigMapToAiJail(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: true\n  memory: false\n  jail_flags:\n    lockdown: true\n    status_bar: false\n    browser: soft\n    mask: [/etc/secrets]\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "ai-jail --exec --lockdown --no-status-bar --mask /etc/secrets --browser=soft custom-cli"
	if strings.TrimSpace(out) != want {
		t.Fatalf("dry-run = %q; want %q", out, want)
	}
}
