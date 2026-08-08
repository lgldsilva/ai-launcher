package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestOptionsRoundTripPreservesServices(t *testing.T) {
	input := []byte("jail: false\nmemory: false\ndocker: true\nservices: [redis, mongo]\n")
	var options Options
	if err := yaml.Unmarshal(input, &options); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if want := []string{"redis", "mongo"}; !reflect.DeepEqual(options.Services, want) {
		t.Fatalf("Options.Services = %#v; want %#v", options.Services, want)
	}

	encoded, err := yaml.Marshal(options)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	var decoded Options
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round-trip unmarshal error = %v", err)
	}
	if !reflect.DeepEqual(decoded.Services, options.Services) {
		t.Fatalf("round-trip services = %#v; want %#v", decoded.Services, options.Services)
	}
}

func TestOptionsScalarFormPreservesServices(t *testing.T) {
	var options Options
	if err := yaml.Unmarshal([]byte("jail: false\nmemory: false\nservices: [postgres]\nextra_args: --model sonnet\n"), &options); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(options.Services, []string{"postgres"}) {
		t.Fatalf("scalar services = %#v; want [postgres]", options.Services)
	}
}

func TestOptionsRoundTripPreservesContainerResources(t *testing.T) {
	input := []byte(`docker: true
container_memory: "4g"
container_cpus: "2.0"
container_pids: 512
container_ports:
  - host: 3000
    internal: 3000
  - host: 5353
    internal: 53
    protocol: udp
container_network: host
container_environment:
  POSTGRES_URL: postgres://db.internal:5432/app
container_service_ports:
  wiremock:
    - host: 18080
      internal: 8080
`)
	var options Options
	if err := yaml.Unmarshal(input, &options); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if options.ContainerMemory != "4g" || options.ContainerCPUs != "2.0" || options.ContainerPIDs != 512 || options.ContainerNetwork != "host" || options.ContainerEnvironment["POSTGRES_URL"] != "postgres://db.internal:5432/app" {
		t.Fatalf("resource options = %#v", options)
	}
	wantPorts := []PortMapping{{Host: 3000, Internal: 3000}, {Host: 5353, Internal: 53, Protocol: "udp"}}
	if !reflect.DeepEqual(options.ContainerPorts, wantPorts) {
		t.Fatalf("container ports = %#v; want %#v", options.ContainerPorts, wantPorts)
	}

	encoded, err := yaml.Marshal(options)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	var decoded Options
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round-trip unmarshal error = %v", err)
	}
	if !reflect.DeepEqual(decoded.ContainerPorts, options.ContainerPorts) || decoded.ContainerNetwork != options.ContainerNetwork || !reflect.DeepEqual(decoded.ContainerEnvironment, options.ContainerEnvironment) {
		t.Fatalf("round-trip resources = %#v; want %#v", decoded, options)
	}
	if got := options.ContainerServicePorts["wiremock"]; !reflect.DeepEqual(got, []PortMapping{{Host: 18080, Internal: 8080}}) {
		t.Fatalf("service port override = %#v", got)
	}
	if !reflect.DeepEqual(decoded.ContainerServicePorts, options.ContainerServicePorts) {
		t.Fatalf("round-trip service port overrides = %#v; want %#v", decoded.ContainerServicePorts, options.ContainerServicePorts)
	}
}

func TestOptionsRoundTripPreservesContainerTmuxSettings(t *testing.T) {
	input := []byte(`docker: true
container_tmux:
  enabled: true
  config: ~/.config/tmux/tmux.conf
  local_config: ~/.tmux.conf.local
  oh_my_tmux_dir: ~/.tmux
  additional_paths:
    - ~/.tmux/plugins
`)
	var options Options
	if err := yaml.Unmarshal(input, &options); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	want := TmuxSettings{
		Enabled:         true,
		Config:          "~/.config/tmux/tmux.conf",
		LocalConfig:     "~/.tmux.conf.local",
		OhMyTmuxDir:     "~/.tmux",
		AdditionalPaths: []string{"~/.tmux/plugins"},
	}
	if !reflect.DeepEqual(options.ContainerTmux, want) {
		t.Fatalf("container tmux = %#v; want %#v", options.ContainerTmux, want)
	}
	encoded, err := yaml.Marshal(options)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	var decoded Options
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round-trip unmarshal error = %v", err)
	}
	if !reflect.DeepEqual(decoded.ContainerTmux, want) {
		t.Fatalf("round-trip container tmux = %#v; want %#v", decoded.ContainerTmux, want)
	}
}

func TestSaveLocalPreservesContainerResources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.yaml")
	want := Local{Agent: "claude", Options: Options{
		Docker:           true,
		ContainerMemory:  "4g",
		ContainerCPUs:    "2.0",
		ContainerPIDs:    512,
		ContainerPorts:   []PortMapping{{Host: 3000, Internal: 3000}},
		ContainerNetwork: "bridge",
	}}
	if err := SaveLocal(path, want); err != nil {
		t.Fatalf("SaveLocal() error = %v", err)
	}
	got, err := LoadLocal(path)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if got.Options.ContainerMemory != want.Options.ContainerMemory || got.Options.ContainerCPUs != want.Options.ContainerCPUs || got.Options.ContainerPIDs != want.Options.ContainerPIDs || got.Options.ContainerNetwork != want.Options.ContainerNetwork || !reflect.DeepEqual(got.Options.ContainerPorts, want.Options.ContainerPorts) {
		t.Fatalf("loaded resources = %#v; want %#v", got.Options, want.Options)
	}
}
