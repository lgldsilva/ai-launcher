package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/installer"
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

// --add used to hardcode supports_memory: true. `ai-memory run` accepts a
// fixed harness list, so any agent whose command is not on it failed pre-flight
// with memory-harness-unsupported the first time it was launched — the operator
// registered a working binary and got an error that named neither --add nor the
// list. The flag now follows what ai-memory actually accepts.
func TestAddAgentDerivesMemorySupportFromTheHarnessList(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{command: "opencode", want: true}, // on ai-memory's list
		{command: "definitely-not-a-harness", want: false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			executable := filepath.Join(dir, tc.command)
			if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil { // #nosec G306 -- the fixture must be executable to exercise --add
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := AddAgent(globalPath, "Agent "+tc.command, executable, tc.command, "", &out); err != nil {
				t.Fatalf("AddAgent() error = %v", err)
			}
			global, err := config.LoadGlobal(globalPath)
			if err != nil {
				t.Fatal(err)
			}
			var found *config.Agent
			for i := range global.Agents {
				if global.Agents[i].Command == tc.command {
					found = &global.Agents[i]
				}
			}
			if found == nil {
				t.Fatalf("agent %q not in the catalog", tc.command)
			}
			if found.SupportsMemory != tc.want {
				t.Errorf("SupportsMemory = %v; want %v for a command ai-memory %s accept",
					found.SupportsMemory, tc.want, map[bool]string{true: "does", false: "does not"}[tc.want])
			}
		})
	}
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
	countCustom := countCatalogEntries(t, global, "custom-cli")
	if countCustom != 1 {
		t.Fatalf("custom-cli entries = %d; want exactly one", countCustom)
	}
	if err := AddAgent(globalPath, "Custom", executable, "", "", &out); err != nil {
		t.Fatalf("repeat AddAgent() error = %v", err)
	}
	global, _ = config.LoadGlobal(globalPath)
	countCustom = countCatalogEntries(t, global, "custom-cli")
	if countCustom != 1 {
		t.Fatalf("repeat --add duplicated the agent: %d entries", countCustom)
	}
	expectAddAgentError(t, AddAgent(globalPath, "", executable, "", "", &out), "AddAgent without a name should fail")
	expectAddAgentError(t, AddAgent(globalPath, "Custom", filepath.Join(dir, "missing"), "", "", &out), "AddAgent with a missing path should fail")
	expectAddAgentError(t, AddAgent(globalPath, "Custom", dir, "", "", &out), "AddAgent with a directory path should fail")
}

// countCatalogEntries counts how many agents in the catalog use the command.
func countCatalogEntries(t *testing.T, global config.Global, command string) int {
	t.Helper()
	count := 0
	for _, agent := range global.Agents {
		if agent.Command == command {
			count++
		}
	}
	return count
}

// expectAddAgentError asserts that an AddAgent call rejected the input.
func expectAddAgentError(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil {
		t.Fatal(message)
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
	if got := logicalMemoryConfigFile("/home/tester", "antigravity", false); got != "/home/tester/.gemini/antigravity-cli/mcp_config.json" {
		t.Fatalf("antigravity (harness) MCP file = %q", got)
	}
	if got := logicalMemoryConfigFile("/home/tester", "kimi-code", false); got != "" {
		t.Fatalf("unknown target file = %q; want empty", got)
	}
}

func discardTrace() *installLog {
	return &installLog{logger: log.New(io.Discard, "", 0)}
}

func TestInstallConfiguredInstallsNativeRunnerOnlyForAIMemory(t *testing.T) {
	stubWindows(t, false)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ai-memory", "other-tool"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil { // #nosec G306 -- the fixtures must be executable to be discovered in PATH
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var nativeCalls []string
	original := installNativeRunner
	installNativeRunner = func(_ *installer.Installer, target installTarget, _ bool, _ *installLog) (installer.Result, error) {
		nativeCalls = append(nativeCalls, target.Command)
		return installer.Result{Name: target.Name, Status: "installed", Path: filepath.Join(dir, "managed", target.Command)}, nil
	}
	t.Cleanup(func() { installNativeRunner = original })
	global := config.Global{Tools: []config.Tool{
		{Name: "ai-memory", Command: "ai-memory"},
		{Name: "Other", Command: "other-tool"},
	}}
	var out, errOut bytes.Buffer
	if err := InstallConfigured(global, "", filepath.Join(dir, "home"), false, &out, &errOut); err != nil {
		t.Fatalf("InstallConfigured() error = %v", err)
	}
	if !reflect.DeepEqual(nativeCalls, []string{"ai-memory"}) {
		t.Fatalf("native runner installs = %#v; want only ai-memory", nativeCalls)
	}
	if !strings.Contains(out.String(), "native runner") {
		t.Fatalf("output does not report the native runner install: %q", out.String())
	}
}

func TestInstallNativeMemoryRunnerSkipsPlatformsWithoutAsset(t *testing.T) {
	client := installer.New(t.TempDir())
	client.GOOS = "plan9"
	client.GOARCH = "amd64"
	target := installTarget{Name: "ai-memory", Command: "ai-memory", Release: &config.GitHubRelease{
		Repository: "acme/ai-memory",
		Assets:     map[string]string{"linux-amd64": "ai-memory-linux-x86_64.tar.gz"},
		Binary:     "ai-memory",
	}}
	result, err := installNativeRunner(client, target, false, discardTrace())
	if err != nil || result.Status != "" || result.Path != "" {
		t.Fatalf("skip result = %#v, err=%v; want an empty result and no error", result, err)
	}
}

func TestInstallNativeMemoryRunnerInstallsToManagedPath(t *testing.T) {
	script := []byte("#!/bin/sh\necho native\n")
	archive := tarGzFixture(t, "ai-memory", script)
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/acme/ai-memory/releases/latest":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"tag_name": "v1.0.0",
				"assets": []map[string]string{
					{"name": "ai-memory-linux-x86_64.tar.gz", "browser_download_url": "http://" + request.Host + "/download/archive"},
					{"name": "ai-memory-linux-x86_64.tar.gz.sha256", "browser_download_url": "http://" + request.Host + "/download/checksum"},
				},
			})
		case "/download/archive":
			_, _ = response.Write(archive)
		case "/download/checksum":
			_, _ = response.Write([]byte(hex.EncodeToString(digest[:]) + "  ai-memory-linux-x86_64.tar.gz\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	client := installer.New(home)
	client.APIBaseURL = server.URL
	client.GOOS = "linux"
	client.GOARCH = "amd64"
	target := installTarget{Name: "ai-memory", Command: "ai-memory", Release: &config.GitHubRelease{
		Repository: "acme/ai-memory",
		Assets:     map[string]string{"linux-amd64": "ai-memory-linux-x86_64.tar.gz"},
		Binary:     "ai-memory",
	}}
	result, err := installNativeRunner(client, target, false, discardTrace())
	want := filepath.Join(home, ".local", "share", "ai-launcher", "bin", "ai-memory")
	if err != nil || result.Status != "installed" || result.Path != want {
		t.Fatalf("native runner result = %#v, err=%v; want installed at %q", result, err, want)
	}
	contents, err := os.ReadFile(want) // #nosec G304 -- the path is the managed install created by the test itself
	if err != nil || !bytes.Equal(contents, script) {
		t.Fatalf("managed native binary = %q, err=%v", contents, err)
	}
}

func tarGzFixture(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
