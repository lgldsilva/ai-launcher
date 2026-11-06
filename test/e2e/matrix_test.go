//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestSandboxMatrix(t *testing.T) {
	root := t.TempDir()
	keepWritable(t, root)
	tools := filepath.Join(cacheDir(), "bins")
	if err := installPinnedTools(tools); err != nil {
		t.Fatalf("install pinned CLIs: %v", err)
	}
	launcher := buildLauncher(t, root)
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	stubs := filepath.Join(root, "stubs")
	mustMkdir(t, home, project, filepath.Join(home, ".config"))
	agents := selectedAgents(config.DefaultGlobal().Agents)
	writeStubs(t, stubs, agents)
	request := writeRequestFile(t, root)
	global := writeGlobal(t, root, request)
	local := filepath.Join(root, "missing-local.yaml")
	env := baseEnv(home, project)
	jail2 := filepath.Join(tools, "ai-jail-2.15")
	jail3 := filepath.Join(tools, "ai-jail-3.0.1")
	memory := filepath.Join(tools, "ai-memory-3.4.2")

	var rows []string
	for _, agent := range agents {
		rows = append(rows, runAgent(t, launcher, agent, env, project, global, local, stubs, jail2, jail3, memory)...)
	}
	writeReport(t, rows)
}

func runAgent(t *testing.T, launcher string, agent config.Agent, env []string, project, global, local, stubs, jail2, jail3, memory string) []string {
	t.Helper()
	name := agent.Command
	var rows []string
	rows = append(rows, runCell(t, launcher, directSpec(name, jail2, false), env, project, global, local, stubs, jail2, memory))
	rows = append(rows, runCell(t, launcher, directSpec(name, jail2, true), env, project, global, local, stubs, jail2, memory))
	rows = append(rows, runCell(t, launcher, stateOffSpec(name, jail2), env, project, global, local, stubs, jail2, memory))
	rows = append(rows, runCell(t, launcher, directSpec(name, jail3, false), env, project, global, local, stubs, jail3, memory))
	rows = append(rows, runCell(t, launcher, directSpec(name, jail3, true), env, project, global, local, stubs, jail3, memory))
	rows = append(rows, runCell(t, launcher, stateOffSpec(name, jail3), env, project, global, local, stubs, jail3, memory))
	rows = append(rows, runCell(t, launcher, requestSpec(name, false), env, project, global, local, stubs, jail2, memory))
	rows = append(rows, runCell(t, launcher, requestSpec(name, true), env, project, global, local, stubs, jail3, memory))
	if agent.SupportsMemory {
		rows = append(rows, runCell(t, launcher, memorySpec(agent, jail2), env, project, global, local, stubs, jail2, memory))
		rows = append(rows, runCell(t, launcher, memorySpec(agent, jail3), env, project, global, local, stubs, jail3, memory))
	} else {
		rows = append(rows, name+"\tjail-memory\tnot-testable\tcatalog SupportsMemory is false")
	}
	rows = append(rows, runCell(t, launcher, dockerDrySpec(name), env, project, global, local, stubs, jail3, memory))
	return rows
}

func runCell(t *testing.T, launcher string, spec launchSpec, env []string, project, global, local, stubs, jailBin, memory string) string {
	t.Helper()
	label := spec.agent + "\t" + cellName(spec)
	pathEnv, err := pathWith(filepath.Dir(global), labelSlug(label), jailBin, memory, stubs)
	if err != nil {
		t.Errorf("%s setup: %v", label, err)
		return label + "\tfail\t" + err.Error()
	}
	full := append(append([]string{}, env...), pathEnv...)
	got := runLaunch(context.Background(), launcher, spec, full, project, global, local)
	if err := checkLaunch(spec, got); err != nil {
		t.Errorf("%s: %v", label, err)
		return label + "\tfail\t" + oneLine(err.Error())
	}
	return label + "\tpass"
}

func directSpec(agent, jail string, noNetwork bool) launchSpec {
	spec := launchSpec{
		agent: agent, jailBin: jail, noNetwork: noNetwork, wantCode: 0,
		wantText: "stub-ok " + agent,
		forbids:  []string{"--request-file"},
		wantArgs: []string{"--agent-state"},
	}
	spec.wantArgs = append(spec.wantArgs, networkArgs(agent, jail, noNetwork)...)
	if filepath.Base(jail) == "ai-jail-3.0.1" && acceptsHarness(agent) {
		spec.wantArgs = append(spec.wantArgs, "--harness")
		return spec
	}
	spec.forbids = append(spec.forbids, "--harness")
	return spec
}

func networkArgs(agent, jail string, noNetwork bool) []string {
	if noNetwork {
		return []string{"--no-network"}
	}
	if filepath.Base(jail) == "ai-jail-3.0.1" {
		if hosts := e2eAPIHosts(agent); len(hosts) > 0 {
			args := []string{"--no-network"}
			for _, host := range hosts {
				args = append(args, "--allow-host", host)
			}
			return args
		}
	}
	return []string{"--network"}
}

func e2eAPIHosts(agent string) []string {
	switch agent {
	case "claude":
		return []string{"api.anthropic.com"}
	case "codex":
		return []string{"api.openai.com", "chatgpt.com"}
	case "gemini":
		return []string{"generativelanguage.googleapis.com"}
	case "grok":
		return []string{"api.x.ai"}
	case "devin":
		return []string{"server.codeium.com"}
	default:
		return nil
	}
}

func stateOffSpec(agent, jail string) launchSpec {
	spec := directSpec(agent, jail, false)
	spec.profile = "state-off"
	spec.wantArgs = swapArg(spec.wantArgs, "--agent-state", "--no-agent-state")
	return spec
}

func requestSpec(agent string, jail3 bool) launchSpec {
	if !jail3 {
		return launchSpec{
			agent: agent, jailBin: "ai-jail-2.15", profile: "request",
			wantCode: 1, wantText: "request-file-unsupported",
		}
	}
	// ai-jail 3.0.1 enforces an unapproved request (exit 3) for harnesses it
	// knows. An unknown command still starts; the flag is present either way.
	if acceptsHarness(agent) {
		return launchSpec{
			agent: agent, jailBin: "ai-jail-3.0.1", profile: "request",
			wantCode: 3, wantText: "AJ-REQ-002",
		}
	}
	spec := directSpec(agent, "ai-jail-3.0.1", false)
	spec.profile = "request"
	spec.forbids = dropArg(spec.forbids, "--request-file")
	spec.wantArgs = append(spec.wantArgs, "--request-file")
	return spec
}

func dropArg(args []string, gone string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != gone {
			out = append(out, arg)
		}
	}
	return out
}

func memorySpec(agent config.Agent, jail string) launchSpec {
	spec := directSpec(agent.Command, jail, false)
	spec.memory = true
	spec.profile = "memory"
	spec.timeout = 60 * time.Second
	if filepath.Base(jail) != "ai-jail-3.0.1" {
		return spec
	}
	spec.forbids = dropArg(spec.forbids, "--harness")
	spec.wantArgs = dropArg(spec.wantArgs, "--harness")
	if acceptsHarness(memoryHarnessName(agent)) {
		spec.wantArgs = append(spec.wantArgs, "--harness")
		return spec
	}
	spec.forbids = append(spec.forbids, "--harness")
	return spec
}

func memoryHarnessName(agent config.Agent) string {
	if agent.Memory != nil && strings.TrimSpace(agent.Memory.RunHarness) != "" {
		return agent.Memory.RunHarness
	}
	return agent.Command
}

func dockerDrySpec(agent string) launchSpec {
	return launchSpec{
		agent: agent, dryRun: true, docker: true, wantCode: 0,
		wantArgs: []string{"docker run"},
		forbids:  []string{"ai-jail"},
	}
}

func swapArg(args []string, from, to string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == from {
			out = append(out, to)
			continue
		}
		out = append(out, arg)
	}
	return out
}

func cellName(spec launchSpec) string {
	name := "jail"
	if spec.docker {
		name = "docker-dry-run"
	}
	if spec.memory {
		name += "-memory"
	}
	if spec.profile != "" {
		name += "-" + spec.profile
	}
	if spec.noNetwork {
		name += "-no-network"
	}
	return name + "-" + filepath.Base(spec.jailBin)
}

func selectedAgents(agents []config.Agent) []config.Agent {
	only := strings.TrimSpace(os.Getenv("E2E_ONLY"))
	if only == "" {
		return agents
	}
	var out []config.Agent
	for _, agent := range agents {
		if agent.Command == only {
			out = append(out, agent)
		}
	}
	return out
}

func acceptsHarness(name string) bool {
	switch name {
	case "claude", "claude-code", "codex", "opencode", "open-code", "pi", "omp", "oh-my-pi",
		"agy", "antigravity", "antigravity-cli", "gemini", "crush", "grok", "devin", "aider", "kiro-cli":
		return true
	default:
		return strings.HasPrefix(name, "kimi")
	}
}

func writeStubs(t *testing.T, dir string, agents []config.Agent) {
	t.Helper()
	mustMkdir(t, dir)
	// --version prints a semver first. ai-memory 3.4.2 reads `opencode --version`
	// and refuses a launch that does not look like OpenCode 1.x or 2.x.
	script := "#!/bin/sh\nbase=$(basename \"$0\")\nif [ \"$1\" = \"--version\" ]; then\n  echo \"1.2.3\"\nfi\necho \"stub-ok $base\"\nexit 0\n"
	for _, agent := range agents {
		path := filepath.Join(dir, agent.Command)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil { // #nosec G306 -- stub must be executable
			t.Fatal(err)
		}
	}
}

func writeRequestFile(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "requests.json")
	body := "{\"version\":1,\"requester\":\"ai-launcher\",\"requests\":[{\"kind\":\"host\",\"value\":\"example.com\",\"reason\":\"e2e\"}]}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeGlobal(t *testing.T, root, request string) string {
	t.Helper()
	path := filepath.Join(root, "global.yaml")
	body := fmt.Sprintf(`version: "2.1"
memory_require_server: false
tools:
  - name: noop
    command: noop
profiles:
  state-off:
    agent: claude
    options:
      jail_flags:
        agent_state: false
  request:
    agent: claude
    options:
      jail_flags:
        request_file: %q
  memory:
    agent: claude
    options:
      memory: true
      memory_exit_prompt: false
`, request)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func buildLauncher(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(root, "ai-launcher")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/ai-launcher")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build launcher: %v\n%s", err, out)
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(dir, "..", ".."))
}

func mustMkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
}

func writeReport(t *testing.T, rows []string) {
	t.Helper()
	text := strings.Join(rows, "\n") + "\n"
	path := filepath.Join(os.TempDir(), "ai-launcher-e2e-matrix.tsv")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("matrix written to %s\n%s", path, text)
}

func oneLine(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 180 {
		return text[:180]
	}
	return text
}

func labelSlug(label string) string {
	return strings.NewReplacer("\t", "-", " ", "-", "/", "-").Replace(label)
}
