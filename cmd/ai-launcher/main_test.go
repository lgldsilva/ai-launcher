package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/catalog"
	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestRunSupportsLegacyAliasesAndQuotedArguments(t *testing.T) {
	var out, errOut bytes.Buffer
	configDir := t.TempDir()
	t.Setenv("PATH", configDir)
	args := []string{
		"--agent", "codex",
		"--sandbox",
		"--no-memory",
		"--no-yolo",
		"--map", "/read only",
		"--rw-map", "/workspace",
		"--args", `--model "gpt 5"`,
		"--config", configDir + "/global.yaml",
		"--local-config", configDir + "/local.yaml",
		"--dry-run",
		"--", "--resume", "latest",
	}

	if err := run(args, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("run() error = %v; stderr = %q", err, errOut.String())
	}

	want := "ai-jail --map '/read only' --rw-map /workspace codex --model 'gpt 5' --resume latest\n"
	if out.String() != want {
		t.Fatalf("dry-run output = %q; want %q", out.String(), want)
	}
}

func TestRunAcceptsNamedNewWorkstream(t *testing.T) {
	var out, errOut bytes.Buffer
	configDir := t.TempDir()
	t.Setenv("PATH", configDir)
	args := []string{
		"--agent", "codex",
		"--no-jail",
		"--new", "release-check",
		"--config", configDir + "/global.yaml",
		"--local-config", configDir + "/local.yaml",
		"--dry-run",
	}

	if err := run(args, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("run() error = %v; stderr = %q", err, errOut.String())
	}

	want := "ai-memory run --new release-check codex\n"
	if out.String() != want {
		t.Fatalf("dry-run output = %q; want %q", out.String(), want)
	}
}

func TestRunMapAliasWorksWithoutRWMap(t *testing.T) {
	var out, errOut bytes.Buffer
	configDir := t.TempDir()
	t.Setenv("PATH", configDir)
	args := []string{
		"--agent", "codex",
		"--no-memory",
		"--map", "/reference",
		"--config", configDir + "/global.yaml",
		"--local-config", configDir + "/local.yaml",
		"--dry-run",
	}

	if err := run(args, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("run() error = %v; stderr = %q", err, errOut.String())
	}

	want := "ai-jail --map /reference codex\n"
	if out.String() != want {
		t.Fatalf("dry-run output = %q; want %q", out.String(), want)
	}
}

func TestRunAddPersistsAgentWithoutLaunchingIt(t *testing.T) {
	var out, errOut bytes.Buffer
	root := t.TempDir()
	executable := filepath.Join(root, "xpto")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fixture must be executable
		t.Fatal(err)
	}
	globalPath := filepath.Join(root, "config", "config.yaml")
	args := []string{"--add", "Xpto", "--path", executable, "--config", globalPath}
	if err := run(args, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("run() error = %v; stderr = %q", err, errOut.String())
	}
	if !strings.Contains(out.String(), `agent "Xpto" added`) {
		t.Fatalf("add output = %q", out.String())
	}

	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	status, err := catalog.New(global).Resolve("Xpto")
	if err != nil || status.Agent.Command != "xpto" || status.Agent.Path != executable {
		t.Fatalf("saved custom agent = %#v, err=%v", status.Agent, err)
	}

	var dryRunOut, dryRunErr bytes.Buffer
	if err := run([]string{"--agent", "Xpto", "--no-jail", "--no-memory", "--dry-run", "--config", globalPath, "--local-config", filepath.Join(root, "local.yaml")}, strings.NewReader(""), &dryRunOut, &dryRunErr); err != nil {
		t.Fatalf("custom agent dry-run error = %v; stderr = %q", err, dryRunErr.String())
	}
	if got, want := dryRunOut.String(), executable+"\n"; got != want {
		t.Fatalf("custom agent dry-run = %q; want %q", got, want)
	}
}

func TestRunAddRequiresExecutablePath(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"--add", "Xpto", "--config", filepath.Join(t.TempDir(), "config.yaml")}, strings.NewReader(""), &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "--add requires --path") {
		t.Fatalf("error = %v; want missing path error", err)
	}
}

func TestConfiguredInstallTargetsAcceptsAliasAndAddsMemoryDependency(t *testing.T) {
	targets := configuredInstallTargets(config.DefaultGlobal(), "kilocode")
	var kilo, memory bool
	for _, target := range targets {
		if target.Command == "kilo" {
			kilo = containsString(target.Aliases, "kilocode")
		}
		if target.Command == "ai-memory" {
			memory = true
		}
	}
	if !kilo || !memory {
		t.Fatalf("install targets for alias = %#v; want kilo and ai-memory", targets)
	}
}

func TestWireMemoryRejectsIncompleteRecipe(t *testing.T) {
	err := wireMemory(context.TODO(), "ai-memory", "", "", &config.MemoryIntegration{InstallMCP: true}, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err == nil || !strings.Contains(err.Error(), "MCP client is empty") {
		t.Fatalf("wireMemory() error = %v; want incomplete recipe error", err)
	}
}

func TestMemoryInstallArgsUseHTTPSAndHarnessSpecificConfig(t *testing.T) {
	args := memoryInstallArgs("install-mcp", "antigravity-cli", "https://aimemory.raspberrypi.lan", "/storage/gemini/antigravity-cli/mcp_config.json")
	joined := strings.Join(args, " ")
	for _, wanted := range []string{"--client antigravity-cli", "--server-url https://aimemory.raspberrypi.lan", "--config-file /storage/gemini/antigravity-cli/mcp_config.json", "--apply"} {
		if !strings.Contains(joined, wanted) {
			t.Fatalf("memory install args = %q; missing %q", joined, wanted)
		}
	}
}

func TestConfigFileForResolvesHarnessSymlinkParent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "claude-target")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".claude")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got := configFileFor(root, filepath.Join("~", ".claude", "settings.json"), "claude-code", true)
	if got != filepath.Join(root, ".claude", "settings.json") {
		t.Fatalf("configured path = %q; want explicit path", got)
	}
	got = configFileFor(root, "", "claude-code", true)
	if got != filepath.Join(target, "settings.json") {
		t.Fatalf("resolved Claude path = %q; want %q", got, filepath.Join(target, "settings.json"))
	}
}

func TestInstallLogIsPrivateAndPersistent(t *testing.T) {
	root := t.TempDir()
	trace, err := newInstallLog(root)
	if err != nil {
		t.Fatal(err)
	}
	trace.Printf("test event")
	path := trace.path
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path) // #nosec G304 -- path is the install log created by the test itself
	if err != nil || !strings.Contains(string(contents), "test event") {
		t.Fatalf("install log = %q, err=%v", contents, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("install log mode = %v, err=%v; want 0600", info, err)
	}
}

func TestSplitArgsPreservesShellLikeQuoting(t *testing.T) {
	got, err := splitArgs(`--model "gpt 5" 'quoted value' escaped\ value ""`)
	if err != nil {
		t.Fatalf("splitArgs() error = %v", err)
	}
	want := []string{"--model", "gpt 5", "quoted value", "escaped value", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitArgs() = %#v; want %#v", got, want)
	}

	for _, input := range []string{`"unterminated`, `trailing\`} {
		if _, err := splitArgs(input); err == nil {
			t.Errorf("splitArgs(%q) = nil error; want malformed input error", input)
		}
	}
}

func TestShellJoinQuotesUnsafeArguments(t *testing.T) {
	got := shellJoin([]string{"codex", "gpt 5", "owner's prompt"})
	want := "codex 'gpt 5' 'owner'\"'\"'s prompt'"
	if got != want {
		t.Fatalf("shellJoin() = %q; want %q", got, want)
	}
}
