package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestEffectiveContainerRuntimeDefaultsToAuto(t *testing.T) {
	for _, value := range []string{"", "  ", "AUTO"} {
		if got := EffectiveContainerRuntime(value); got != ContainerRuntimeAuto {
			t.Errorf("EffectiveContainerRuntime(%q) = %q; want auto", value, got)
		}
	}
	if got := EffectiveContainerRuntime(" PodMan "); got != "podman" {
		t.Fatalf("EffectiveContainerRuntime(podman) = %q; want podman", got)
	}
}

func TestOptionsContainerRuntimeRoundTrip(t *testing.T) {
	var options Options
	if err := yaml.Unmarshal([]byte("container_runtime: podman\ncontainer_context: remote-builder\ndocker: true\nextra_args: --model sonnet\n"), &options); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if options.ContainerRuntime != "podman" || options.ContainerContext != "remote-builder" || !options.Docker || len(options.ExtraArgs) != 2 || options.ExtraArgs[0] != "--model" {
		t.Fatalf("Options = %#v; want podman + docker", options)
	}

	path := filepath.Join(t.TempDir(), "local.yaml")
	if err := SaveLocal(path, Local{Agent: "claude", Options: options}); err != nil {
		t.Fatalf("SaveLocal() error = %v", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is a test-owned temp file
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "container_runtime: podman") {
		t.Fatalf("saved config = %q; want persisted container_runtime", data)
	}
	if !strings.Contains(string(data), "container_context: remote-builder") {
		t.Fatalf("saved config = %q; want persisted container_context", data)
	}
	loaded, err := LoadLocal(path)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if loaded.Options.ContainerRuntime != "podman" {
		t.Fatalf("loaded runtime = %q; want podman", loaded.Options.ContainerRuntime)
	}
	if loaded.Options.ContainerContext != "remote-builder" {
		t.Fatalf("loaded context = %q; want remote-builder", loaded.Options.ContainerContext)
	}
}
