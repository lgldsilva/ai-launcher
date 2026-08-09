package launcher

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
)

// TestCatalogHarnessArgumentsAcrossBackends is the executable contract for
// every built-in harness. It exercises every declared parameter in declaration
// order and checks the three launch boundaries where arguments can be lost or
// translated incorrectly: bare native execution, host-side ai-memory, and the
// in-container command used by both docker run and Compose.
func TestCatalogHarnessArgumentsAcrossBackends(t *testing.T) {
	for _, agent := range config.DefaultGlobal().Agents {
		agent := agent
		t.Run(agent.Command, func(t *testing.T) {
			values := make(map[string]string, len(agent.Params))
			for _, param := range agent.Params {
				value := "true"
				if param.TakesValue {
					value = "fixture-" + param.Name
				}
				values[param.Name] = value
			}
			native := expectedNativeHarnessArgs(agent, values, agent.SupportsYolo, agent.YoloFlag)
			assertBareHarnessArgs(t, agent, values, native)
			assertDockerHarnessArgs(t, agent, values, native)
			assertMemoryHarnessArgs(t, agent, values)
		})
	}
}

func assertBareHarnessArgs(t *testing.T, agent config.Agent, values map[string]string, native []string) {
	t.Helper()
	got, err := Build(LaunchConfig{Agent: agent, Yolo: agent.SupportsYolo, ParamValues: values})
	if err != nil {
		t.Fatalf("bare Build() error = %v", err)
	}
	want := append([]string{agent.Command}, native...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bare Build() = %#v; want %#v", got, want)
	}
}

func assertDockerHarnessArgs(t *testing.T, agent config.Agent, values map[string]string, native []string) {
	t.Helper()
	docker := dockerLaunchConfig(t)
	docker.Agent = agent
	docker.UseMemory = agent.SupportsMemory && config.SupportsMemoryRunHarness(memoryRunHarness(agent))
	docker.Yolo = agent.SupportsYolo
	docker.ParamValues = values
	selection, err := container.Normalize([]string{"go"}, []container.AgentInstall{{
		Command:    agent.Command,
		Kind:       container.InstallNpm,
		NpmPackage: "fixture-" + agent.Command,
	}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	docker.Docker.Selection = selection
	run, err := prepareDockerRunConfig(docker)
	if err != nil {
		t.Fatalf("prepareDockerRunConfig() error = %v", err)
	}
	wantNative := native
	if docker.UseMemory && agent.SupportsYolo {
		wantNative = append(nativeParams(values, agent), "--yolo")
	}
	if !reflect.DeepEqual(run.AgentArgs, wantNative) {
		t.Fatalf("docker native args = %#v; want %#v", run.AgentArgs, wantNative)
	}
	if !docker.UseMemory && agent.YoloFlag != "--yolo" && containsArg(run.AgentArgs, "--yolo") {
		t.Fatalf("docker native args = %#v; generic --yolo must not reach %s", run.AgentArgs, agent.Command)
	}

	docker.Services = []string{"redis"}
	compose, err := BuildCompose(docker)
	if err != nil {
		t.Fatalf("BuildCompose() error = %v", err)
	}
	agentService := compose.Services["agent"]
	want := append([]string{agent.Command}, native...)
	if docker.UseMemory {
		want = append([]string{"ai-memory", "run", memoryRunHarness(agent)}, nativeParams(values, agent)...)
		if agent.SupportsYolo {
			want = append(want, "--yolo")
		}
	}
	if !reflect.DeepEqual(agentService.Command, want) {
		t.Fatalf("compose agent command = %#v; want %#v", agentService.Command, want)
	}
}

func assertMemoryHarnessArgs(t *testing.T, agent config.Agent, values map[string]string) {
	t.Helper()
	if !agent.SupportsMemory || !config.SupportsMemoryRunHarness(memoryRunHarness(agent)) {
		return
	}
	got, err := Build(LaunchConfig{Agent: agent, UseMemory: true, Yolo: agent.SupportsYolo, ParamValues: values})
	if err != nil {
		t.Fatalf("ai-memory Build() error = %v", err)
	}
	want := append([]string{aiMemoryCommand, "run", memoryRunHarness(agent)}, nativeParams(values, agent)...)
	if agent.SupportsYolo {
		want = append(want, "--yolo")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ai-memory Build() = %#v; want %#v", got, want)
	}
}

func expectedNativeHarnessArgs(agent config.Agent, values map[string]string, includeYolo bool, yoloFlag string) []string {
	args := nativeParams(values, agent)
	if includeYolo {
		args = append(args, strings.Fields(yoloFlag)...)
	}
	return args
}

func nativeParams(values map[string]string, agent config.Agent) []string {
	var args []string
	for _, param := range agent.Params {
		value := values[param.Name]
		if param.TakesValue {
			args = append(args, param.Flag, value)
			continue
		}
		args = append(args, param.Flag)
	}
	return args
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
