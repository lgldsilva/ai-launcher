package launcher

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/container"
)

const defaultComposeNetwork = "ai-launcher"

// BuildCompose creates the project compose document for a container launch.
// Infrastructure services are deliberately the switch: callers with no
// services continue using Build's docker run path, while this function always
// returns an agent plus the selected catalog services on one bridge network.
func BuildCompose(cfg LaunchConfig) (container.ComposeFile, error) {
	if !cfg.UseDocker {
		return container.ComposeFile{}, fmt.Errorf("compose requires the docker backend")
	}
	if len(cfg.Services) == 0 {
		return container.ComposeFile{}, fmt.Errorf("compose requires at least one infrastructure service")
	}
	if err := validateServicePortOverrides(cfg.ContainerServicePorts); err != nil {
		return container.ComposeFile{}, err
	}
	run, err := prepareDockerRunConfig(cfg)
	if err != nil {
		return container.ComposeFile{}, err
	}
	networkName := strings.TrimSpace(run.NetworkName)
	if networkName == "" {
		networkName = defaultComposeNetwork
	}
	if networkName == "host" {
		return container.ComposeFile{}, fmt.Errorf("compose services require a bridge network; host network is unsupported")
	}

	file := container.NewComposeFile()
	file.Networks[networkName] = container.ComposeNetwork{Driver: "bridge", Internal: run.NetworkInternal}
	dataDir := filepath.Join(run.ProjectDir, ".ai-launcher", "data")
	if err := addComposeServices(&file, cfg.Services, cfg.ContainerServicePorts, networkName, dataDir, run); err != nil {
		return container.ComposeFile{}, err
	}

	command := run.InContainerCommand()
	agent, err := container.ComposeServiceFromRunConfig(run, command, networkName)
	if err != nil {
		return container.ComposeFile{}, err
	}
	agent.DependsOn = append([]string(nil), cfg.Services...)
	if err := addServiceConnectionEnvironment(&agent, cfg.Services, cfg.ContainerServicePorts); err != nil {
		return container.ComposeFile{}, err
	}
	if err := addEgressProxy(&file, &agent, cfg, run, networkName, dataDir); err != nil {
		return container.ComposeFile{}, err
	}
	for key, value := range cfg.ContainerEnvironment {
		if agent.Environment == nil {
			agent.Environment = make(map[string]string)
		}
		if key == "AI_MEMORY_AUTH_TOKEN" {
			value = "${AI_MEMORY_AUTH_TOKEN:-}"
		}
		agent.Environment[key] = value
	}
	file.Services["agent"] = agent
	if err := file.Validate(); err != nil {
		return container.ComposeFile{}, err
	}
	return file, nil
}

// addEgressProxy injects the v2 egress-allowlist proxy when the internal
// network is on and domains are configured — otherwise the agent is left
// with zero egress on an internal network (v1 behavior, unchanged). No-op
// combinations (domains without internal network) are handled entirely by
// validation warnings, not here; this function only acts when both are set.
// Called before the cfg.ContainerEnvironment overlay loop so an operator's
// explicit HTTP_PROXY/HTTPS_PROXY still wins over this default.
func addEgressProxy(file *container.ComposeFile, agent *container.ComposeService, cfg LaunchConfig, run container.RunConfig, networkName, dataDir string) error {
	if !run.NetworkInternal || len(cfg.ContainerNetworkAllowedDomains) == 0 {
		return nil
	}
	egressNetworkName := networkName + "-egress"
	file.Networks[egressNetworkName] = container.ComposeNetwork{Driver: "bridge"}

	configPath := container.EgressProxyConfigPath(dataDir)
	content, err := container.GenerateEgressProxyConfig(cfg.ContainerNetworkAllowedDomains)
	if err != nil {
		return err
	}
	if file.GeneratedFiles == nil {
		file.GeneratedFiles = make(map[string]string)
	}
	file.GeneratedFiles[configPath] = content

	proxy, err := container.EgressProxyComposeService(configPath, networkName, egressNetworkName)
	if err != nil {
		return err
	}
	file.Services[container.EgressProxyServiceID] = proxy

	proxyURL := fmt.Sprintf("http://%s:%d", container.EgressProxyServiceID, container.EgressProxyPort)
	if agent.Environment == nil {
		agent.Environment = make(map[string]string)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		agent.Environment[key] = proxyURL
	}
	agent.DependsOn = append(agent.DependsOn, container.EgressProxyServiceID)
	return nil
}

func addComposeServices(file *container.ComposeFile, ids []string, overrides map[string][]container.PortMapping, networkName, dataDir string, run container.RunConfig) error {
	for _, id := range ids {
		service, ok := container.ServiceByID(id)
		if !ok {
			return fmt.Errorf("unknown compose service %q", id)
		}
		service, err := container.ServiceWithPortOverride(service, overrides)
		if err != nil {
			return err
		}
		if err := addComposeInfrastructure(file, service, networkName, dataDir, run); err != nil {
			return err
		}
	}
	return nil
}

func addServiceConnectionEnvironment(agent *container.ComposeService, ids []string, overrides map[string][]container.PortMapping) error {
	for _, id := range ids {
		service, ok := container.ServiceByID(id)
		if !ok {
			return fmt.Errorf("unknown compose service %q", id)
		}
		service, err := container.ServiceWithPortOverride(service, overrides)
		if err != nil {
			return err
		}
		for key, value := range serviceConnectionEnvironment(service) {
			if agent.Environment == nil {
				agent.Environment = make(map[string]string)
			}
			if _, exists := agent.Environment[key]; !exists {
				agent.Environment[key] = value
			}
		}
	}
	return nil
}

func validateServicePortOverrides(overrides map[string][]container.PortMapping) error {
	for id := range overrides {
		service, ok := container.ServiceByID(id)
		if !ok {
			return fmt.Errorf("unknown compose service port override %q", id)
		}
		if _, err := container.ServiceWithPortOverride(service, overrides); err != nil {
			return err
		}
	}
	return nil
}

func addComposeInfrastructure(file *container.ComposeFile, service container.Service, networkName, dataDir string, run container.RunConfig) error {
	if err := container.AddInfrastructureServiceWithDataDir(file, service, networkName, dataDir); err != nil {
		return err
	}
	if !service.MountProject {
		return nil
	}
	composeService := file.Services[service.ID]
	composeService.Volumes = append(composeService.Volumes, run.ProjectDir+":/home/coder/project:rw")
	if run.UID > 0 || run.GID > 0 {
		composeService.User = fmt.Sprintf("%d:%d", run.UID, run.GID)
	}
	file.Services[service.ID] = composeService
	return nil
}

// serviceConnectionEnvironment returns conservative DNS-based defaults. The
// host-published port remains available for host tools, but agent-to-service
// traffic always uses the compose service name and internal port.
func serviceConnectionEnvironment(service container.Service) map[string]string {
	if len(service.Ports) == 0 || strings.TrimSpace(service.ID) == "" {
		return nil
	}
	port := service.Ports[0].Internal
	if port <= 0 {
		return nil
	}
	id := strings.ToLower(strings.TrimSpace(service.ID))
	keyID := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(id))
	scheme := "http"
	switch id {
	case "postgres", "pgvector", "timescaledb", "cockroachdb":
		scheme = "postgres"
	case "mysql", "mariadb":
		scheme = "mysql"
	case "mongo", "mongodb":
		scheme = "mongodb"
	case "redis", "valkey", "dragonfly":
		scheme = "redis"
	}
	value := fmt.Sprintf("%s://%s:%d", scheme, id, port)
	if scheme == "postgres" {
		value += "/dev"
	}
	return map[string]string{keyID + "_URL": value}
}
