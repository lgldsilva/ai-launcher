package launcher

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
)

func TestBuildComposeSelectsAgentAndInfrastructure(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Services = []string{"postgres", "redis"}
	cfg.ContainerEnvironment = map[string]string{
		"POSTGRES_URL":         "postgres://custom:5432/app",
		"AI_MEMORY_AUTH_TOKEN": "literal-must-not-be-written",
	}
	cfg.UseMemory = true
	cfg.MemoryAuthToken = "configured-token"
	cfg.Docker.ExposedPorts = []container.PortMapping{{Host: 3000, Internal: 3000}}
	cfg.Docker.MemoryLimit = "4g"
	cfg.Docker.CPULimit = "2.0"

	file, err := BuildCompose(cfg)
	if err != nil {
		t.Fatalf("BuildCompose() error = %v", err)
	}
	agent, ok := file.Services["agent"]
	if !ok {
		t.Fatalf("services = %#v; want agent", file.Services)
	}
	if agent.Build != "." || !reflect.DeepEqual(agent.DependsOn, []string{"postgres", "redis"}) {
		t.Fatalf("agent = %#v", agent)
	}
	if !reflect.DeepEqual(agent.Command[:3], []string{"ai-memory", "run", "claude"}) {
		t.Fatalf("memory Compose command = %#v; want ai-memory run claude prefix", agent.Command)
	}
	if agent.Environment["POSTGRES_URL"] != "postgres://custom:5432/app" || agent.Environment["REDIS_URL"] != "redis://redis:6379" {
		t.Fatalf("agent environment = %#v", agent.Environment)
	}
	if agent.Environment["AI_MEMORY_AUTH_TOKEN"] != "${AI_MEMORY_AUTH_TOKEN:-}" {
		t.Fatalf("memory token must remain interpolated: %#v", agent.Environment)
	}
	if !reflect.DeepEqual(agent.Ports, []string{"3000:3000"}) || agent.MemLimit != "4g" || agent.CPUs != "2.0" {
		t.Fatalf("agent resources = %#v", agent)
	}
	for _, id := range []string{"agent", "postgres", "redis"} {
		service, ok := file.Services[id]
		if !ok || !reflect.DeepEqual(service.Networks, []string{"ai-launcher"}) {
			t.Errorf("service %q = %#v; want ai-launcher network", id, service)
		}
	}
	for _, volume := range []string{
		"/home/lgldsilva/work/.ai-launcher/data/postgres:/var/lib/postgresql",
		"/home/lgldsilva/work/.ai-launcher/data/redis:/data",
	} {
		serviceName := "postgres"
		if strings.Contains(volume, "/redis:") {
			serviceName = "redis"
		}
		if !hasComposeVolume(file.Services[serviceName].Volumes, volume) {
			t.Errorf("service %s volumes = %#v; missing %q", serviceName, file.Services[serviceName].Volumes, volume)
		}
	}
	if len(file.Volumes) != 0 {
		t.Fatalf("Compose should not use Docker-managed named data volumes: %#v", file.Volumes)
	}
	rendered, err := container.RenderCompose(file)
	if err != nil {
		t.Fatalf("RenderCompose() error = %v", err)
	}
	if !strings.Contains(rendered, "POSTGRES_URL: postgres://custom:5432/app") {
		t.Fatalf("rendered compose misses custom environment:\n%s", rendered)
	}
}

func TestBuildComposeSetsInternalNetworkWhenConfigured(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Services = []string{"redis"}
	cfg.Docker.NetworkInternal = true

	file, err := BuildCompose(cfg)
	if err != nil {
		t.Fatalf("BuildCompose() error = %v", err)
	}
	network, ok := file.Networks["ai-launcher"]
	if !ok || !network.Internal {
		t.Fatalf("networks = %#v; want ai-launcher internal=true", file.Networks)
	}
	rendered, err := container.RenderCompose(file)
	if err != nil {
		t.Fatalf("RenderCompose() error = %v", err)
	}
	if !strings.Contains(rendered, "internal: true") {
		t.Fatalf("rendered compose missing %q:\n%s", "internal: true", rendered)
	}
}

func TestBuildComposeInjectsEgressProxyWhenDomainsConfigured(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Services = []string{"redis"}
	cfg.Docker.NetworkInternal = true
	cfg.ContainerNetworkAllowedDomains = []string{"api.anthropic.com"}

	file, err := BuildCompose(cfg)
	if err != nil {
		t.Fatalf("BuildCompose() error = %v", err)
	}

	egressNetwork, ok := file.Networks["ai-launcher-egress"]
	if !ok || egressNetwork.Internal {
		t.Fatalf("networks = %#v; want ai-launcher-egress present and not internal", file.Networks)
	}

	proxy, ok := file.Services[container.EgressProxyServiceID]
	if !ok {
		t.Fatalf("services = %v; want %q present", mapKeys(file.Services), container.EgressProxyServiceID)
	}
	wantNetworks := []string{"ai-launcher", "ai-launcher-egress"}
	if len(proxy.Networks) != len(wantNetworks) || proxy.Networks[0] != wantNetworks[0] || proxy.Networks[1] != wantNetworks[1] {
		t.Fatalf("proxy.Networks = %v; want %v", proxy.Networks, wantNetworks)
	}

	agent, ok := file.Services["agent"]
	if !ok {
		t.Fatal("services missing \"agent\"")
	}
	wantProxyURL := fmt.Sprintf("http://%s:%d", container.EgressProxyServiceID, container.EgressProxyPort)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if agent.Environment[key] != wantProxyURL {
			t.Fatalf("agent.Environment[%q] = %q; want %q", key, agent.Environment[key], wantProxyURL)
		}
	}
	if !slices.Contains(agent.DependsOn, container.EgressProxyServiceID) {
		t.Fatalf("agent.DependsOn = %v; want it to include %q", agent.DependsOn, container.EgressProxyServiceID)
	}

	var configContent string
	for path, content := range file.GeneratedFiles {
		if strings.Contains(path, "egress-proxy") {
			configContent = content
		}
	}
	if !strings.Contains(configContent, "api.anthropic.com") {
		t.Fatalf("generated egress-proxy config missing the configured domain:\n%s", configContent)
	}
}

func TestBuildComposeIgnoresAllowedDomainsWithoutInternalNetwork(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Services = []string{"redis"}
	cfg.ContainerNetworkAllowedDomains = []string{"api.anthropic.com"}
	// cfg.Docker.NetworkInternal left false.

	file, err := BuildCompose(cfg)
	if err != nil {
		t.Fatalf("BuildCompose() error = %v", err)
	}
	if _, ok := file.Networks["ai-launcher-egress"]; ok {
		t.Fatalf("networks = %#v; want no ai-launcher-egress network", file.Networks)
	}
	if _, ok := file.Services[container.EgressProxyServiceID]; ok {
		t.Fatalf("services = %v; want no %q service", mapKeys(file.Services), container.EgressProxyServiceID)
	}
}

func mapKeys(m map[string]container.ComposeService) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func hasComposeVolume(volumes []string, want string) bool {
	for _, volume := range volumes {
		if volume == want {
			return true
		}
	}
	return false
}

func TestBuildComposeRejectsMissingServices(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	if _, err := BuildCompose(cfg); err == nil {
		t.Fatal("BuildCompose() error = nil; want missing-services error")
	}
}

func TestBuildComposeRejectsPublishedPortCollision(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Services = []string{"postgres", "pgvector"}
	if _, err := BuildCompose(cfg); err == nil || !strings.Contains(err.Error(), "published port") {
		t.Fatalf("BuildCompose() error = %v; want published-port collision", err)
	}
}

func TestBuildComposeCodeServerMountsProjectAndUsesHostIdentity(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Services = []string{"code-server"}
	cfg.Docker.UID = 501
	cfg.Docker.GID = 20

	file, err := BuildCompose(cfg)
	if err != nil {
		t.Fatalf("BuildCompose(code-server) error = %v", err)
	}
	service := file.Services["code-server"]
	if service.User != "501:20" {
		t.Fatalf("code-server user = %q; want 501:20", service.User)
	}
	if !hasComposeVolume(service.Volumes, "/home/lgldsilva/work:/home/coder/project:rw") {
		t.Fatalf("code-server volumes = %#v; want project mount", service.Volumes)
	}
	if service.WorkingDir != "/home/coder/project" || !reflect.DeepEqual(service.Command, []string{"--bind-addr", "0.0.0.0:8080", "/home/coder/project"}) {
		t.Fatalf("code-server runtime = %#v", service)
	}
}

func TestBuildComposeAppliesServicePortOverrides(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.Services = []string{"wiremock"}
	cfg.ContainerServicePorts = map[string][]config.PortMapping{
		"wiremock": {{Host: 18080, Internal: 8080}},
	}

	file, err := BuildCompose(cfg)
	if err != nil {
		t.Fatalf("BuildCompose() error = %v", err)
	}
	if got := file.Services["wiremock"].Ports; !reflect.DeepEqual(got, []string{"18080:8080"}) {
		t.Fatalf("wiremock published ports = %#v; want [18080:8080]", got)
	}
}

func TestServiceConnectionEnvironment(t *testing.T) {
	tests := []struct {
		id, want string
	}{
		{"postgres", "POSTGRES_URL=postgres://postgres:5432/dev"},
		{"redis", "REDIS_URL=redis://redis:6379"},
		{"mongo", "MONGO_URL=mongodb://mongo:27017"},
		{"nginx", "NGINX_URL=http://nginx:80"},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			service, ok := container.ServiceByID(test.id)
			if !ok {
				t.Fatalf("ServiceByID(%q) = false", test.id)
			}
			parts := strings.SplitN(test.want, "=", 2)
			if got := serviceConnectionEnvironment(service)[parts[0]]; got != parts[1] {
				t.Fatalf("serviceConnectionEnvironment(%q) = %q; want %q", test.id, got, parts[1])
			}
		})
	}
}
