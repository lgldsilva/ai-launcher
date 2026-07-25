package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func stubWindows(t *testing.T, windows bool) {
	t.Helper()
	original := isWindows
	isWindows = func() bool { return windows }
	t.Cleanup(func() { isWindows = original })
}

func targetCommands(targets []installTarget) []string {
	commands := make([]string, 0, len(targets))
	for _, target := range targets {
		commands = append(commands, target.Command)
	}
	return commands
}

func TestConfiguredInstallTargetsSkipsJailOnWindows(t *testing.T) {
	stubWindows(t, true)
	targets := configuredInstallTargets(config.DefaultGlobal(), "")
	for _, command := range targetCommands(targets) {
		if command == "ai-jail" {
			t.Fatal("ai-jail must not be an install target on Windows")
		}
	}
	if !containsInstallTarget(targets, "ai-memory") {
		t.Fatal("ai-memory must remain an install target on Windows")
	}
}

func TestConfiguredInstallTargetsKeepsJailElsewhere(t *testing.T) {
	stubWindows(t, false)
	targets := configuredInstallTargets(config.DefaultGlobal(), "")
	if !containsInstallTarget(targets, "ai-jail") {
		t.Fatal("ai-jail must be an install target off Windows")
	}
}

func TestConfiguredInstallTargetsSelectsAndAddsMemoryCompanion(t *testing.T) {
	stubWindows(t, false)
	targets := configuredInstallTargets(config.DefaultGlobal(), "claude")
	commands := targetCommands(targets)
	if !reflect.DeepEqual(commands, []string{"claude", "ai-memory"}) {
		t.Fatalf("targets for claude = %#v; want claude plus the ai-memory companion", commands)
	}
	targets = configuredInstallTargets(config.DefaultGlobal(), "ai-jail")
	if commands := targetCommands(targets); !reflect.DeepEqual(commands, []string{"ai-jail"}) {
		t.Fatalf("targets for ai-jail = %#v; want only ai-jail (no memory need)", commands)
	}
	if targets := configuredInstallTargets(config.DefaultGlobal(), "nope"); len(targets) != 0 {
		t.Fatalf("unknown selection = %#v; want no targets", targets)
	}
}

func TestMemoryInstallArgs(t *testing.T) {
	got := memoryInstallArgs("install-mcp", "kimi-code", " https://aimemory.example ", "/tmp/mcp.json")
	want := []string{"install-mcp", "--client", "kimi-code", "--server-url", "https://aimemory.example", "--config-file", "/tmp/mcp.json", "--apply"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("memoryInstallArgs(install-mcp) = %#v; want %#v", got, want)
	}
	got = memoryInstallArgs("install-hooks", "kimi-code", "", "")
	want = []string{"install-hooks", "--agent", "kimi-code", "--apply"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("memoryInstallArgs(install-hooks) = %#v; want %#v", got, want)
	}
	if value := lastArgValue(got, "--agent"); value != "kimi-code" {
		t.Fatalf("lastArgValue(--agent) = %q", value)
	}
	if value := lastArgValue(got, "--missing"); value != "" {
		t.Fatalf("lastArgValue(--missing) = %q; want empty", value)
	}
}

func TestInstallLogNeverContainsTheAuthToken(t *testing.T) {
	home := t.TempDir()
	global := config.DefaultGlobal()
	global.MemoryAuthToken = "s3cret-token"
	// An empty selection fails fast but still exercises the install log path.
	var out, errOut bytes.Buffer
	err := InstallConfigured(config.Global{MemoryAuthToken: global.MemoryAuthToken}, "", home, false, &out, &errOut)
	if err == nil {
		t.Fatal("InstallConfigured with no recipes should fail")
	}
	data, readErr := os.ReadFile(filepath.Join(home, ".config", "ai-launch", "install.log")) // #nosec G304 -- the path is the install log inside the test's own temp home
	if readErr != nil {
		t.Fatalf("install log not written: %v", readErr)
	}
	if strings.Contains(string(data), "s3cret-token") {
		t.Fatalf("auth token leaked into the install log: %s", data)
	}
	if strings.Contains(out.String()+errOut.String(), "s3cret-token") {
		t.Fatal("auth token leaked into install output")
	}
	_ = global
}

func TestAddAgentUpsertsIntoGlobalCatalog(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "custom-cli")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil { // #nosec G306 -- the fixture must be executable to exercise --add
		t.Fatal(err)
	}
	globalPath := filepath.Join(dir, "global.yaml")
	var out bytes.Buffer
	if err := AddAgent(globalPath, "Custom", executable, "", "test agent", &out); err != nil {
		t.Fatalf("AddAgent() error = %v", err)
	}
	if !strings.Contains(out.String(), `agent "Custom" added`) {
		t.Fatalf("output = %q", out.String())
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	countCustom := 0
	for _, agent := range global.Agents {
		if agent.Command == "custom-cli" {
			countCustom++
		}
	}
	if countCustom != 1 {
		t.Fatalf("custom-cli entries = %d; want exactly one", countCustom)
	}
	if err := AddAgent(globalPath, "Custom", executable, "", "", &out); err != nil {
		t.Fatalf("repeat AddAgent() error = %v", err)
	}
	global, _ = config.LoadGlobal(globalPath)
	countCustom = 0
	for _, agent := range global.Agents {
		if agent.Command == "custom-cli" {
			countCustom++
		}
	}
	if countCustom != 1 {
		t.Fatalf("repeat --add duplicated the agent: %d entries", countCustom)
	}
	if err := AddAgent(globalPath, "", executable, "", "", &out); err == nil {
		t.Fatal("AddAgent without a name should fail")
	}
	if err := AddAgent(globalPath, "Custom", filepath.Join(dir, "missing"), "", "", &out); err == nil {
		t.Fatal("AddAgent with a missing path should fail")
	}
	if err := AddAgent(globalPath, "Custom", dir, "", "", &out); err == nil {
		t.Fatal("AddAgent with a directory path should fail")
	}
}

func TestExpandHomePathAndLogicalMemoryConfigFile(t *testing.T) {
	if got := expandHomePath("/home/tester", "~/x.json"); got != "/home/tester/x.json" {
		t.Fatalf("expandHomePath() = %q", got)
	}
	if got := expandHomePath("", "~/x.json"); got != "~/x.json" {
		t.Fatalf("expandHomePath without home = %q", got)
	}
	if got := logicalMemoryConfigFile("/home/tester", "claude-code", true); got != "/home/tester/.claude/settings.json" {
		t.Fatalf("claude hooks file = %q", got)
	}
	if got := logicalMemoryConfigFile("/home/tester", "antigravity-cli", false); got != "/home/tester/.gemini/antigravity-cli/mcp_config.json" {
		t.Fatalf("antigravity MCP file = %q", got)
	}
	if got := logicalMemoryConfigFile("/home/tester", "kimi-code", false); got != "" {
		t.Fatalf("unknown target file = %q; want empty", got)
	}
}
