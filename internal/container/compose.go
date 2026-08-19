package container

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// ComposeFile is the deterministic in-memory representation of the
// docker-compose.yaml generated for a container launch. Map keys are encoded
// in sorted order by goccy/go-yaml, so the same selection produces identical
// bytes regardless of Go map iteration order.
type ComposeFile struct {
	Services map[string]ComposeService `yaml:"services"`
	Networks map[string]ComposeNetwork `yaml:"networks,omitempty"`
	Volumes  map[string]ComposeVolume  `yaml:"volumes,omitempty"`
	// GeneratedFiles maps an absolute host path to content that
	// MaterializeCompose must write alongside docker-compose.yaml (for
	// example the egress proxy's squid.conf). yaml:"-" is required: this
	// must never leak into the rendered Compose document. Builders
	// (BuildCompose) only ever populate this map — they do no I/O
	// themselves; MaterializeCompose is the single place these bytes reach
	// disk.
	GeneratedFiles map[string]string `yaml:"-"`
}

// ComposeService is one agent or infrastructure service in the generated
// compose file. Build is used by the agent; Image is used by catalog services.
type ComposeService struct {
	Build       string            `yaml:"build,omitempty"`
	Image       string            `yaml:"image,omitempty"`
	Command     []string          `yaml:"command,omitempty"`
	WorkingDir  string            `yaml:"working_dir,omitempty"`
	User        string            `yaml:"user,omitempty"`
	GroupAdd    []string          `yaml:"group_add,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Networks    []string          `yaml:"networks,omitempty"`
	DependsOn   []string          `yaml:"depends_on,omitempty"`
	ExtraHosts  []string          `yaml:"extra_hosts,omitempty"`
	MemLimit    string            `yaml:"mem_limit,omitempty"`
	CPUs        string            `yaml:"cpus,omitempty"`
	PIDsLimit   int64             `yaml:"pids_limit,omitempty"`
	StdinOpen   bool              `yaml:"stdin_open,omitempty"`
	TTY         bool              `yaml:"tty,omitempty"`
	Restart     string            `yaml:"restart,omitempty"`
	Healthcheck map[string]any    `yaml:"healthcheck,omitempty"`
}

// ComposeNetwork describes a named compose network. Internal, when true, is
// the Compose Spec's own network-level flag: Docker adds no gateway/route to
// the host's outbound interface for it, so every service attached only to
// this network — including the agent's own LLM API calls — loses all
// internet egress. Compose networks have no domain/IP allowlist primitive;
// selective access needs a companion dual-homed proxy service (see
// docs/design-decisions.md).
type ComposeNetwork struct {
	Driver   string `yaml:"driver,omitempty"`
	Internal bool   `yaml:"internal,omitempty"`
}

// ComposeVolume describes a named compose volume.
type ComposeVolume struct {
	Driver string `yaml:"driver,omitempty"`
}

// NewComposeFile returns an empty Compose Specification document with
// initialized maps ready for service insertion. The obsolete top-level
// version key is intentionally omitted.
func NewComposeFile() ComposeFile {
	return ComposeFile{
		Services: make(map[string]ComposeService),
		Networks: make(map[string]ComposeNetwork),
		Volumes:  make(map[string]ComposeVolume),
	}
}

// RenderCompose validates and serializes a compose document. The renderer is
// deliberately pure: materialization and runtime execution are separate so
// tests can inspect the exact YAML without a Docker daemon.
func RenderCompose(file ComposeFile) (string, error) {
	if err := file.Validate(); err != nil {
		return "", err
	}
	data, err := yaml.Marshal(file)
	if err != nil {
		return "", fmt.Errorf("render compose YAML: %w", err)
	}
	return string(data), nil
}

// Validate checks the structural invariants that make the rendered document
// useful to a runtime. A service must be either buildable or image-backed, and
// a compose document must contain at least one service.
func (file ComposeFile) Validate() error {
	if len(file.Services) == 0 {
		return fmt.Errorf("compose requires at least one service")
	}
	if err := validateComposeDeclarations(file); err != nil {
		return err
	}
	published := make(map[string]string)
	for name, service := range file.Services {
		if err := validateComposeService(file, name, service, published); err != nil {
			return err
		}
	}
	return nil
}

func validateComposeDeclarations(file ComposeFile) error {
	for name := range file.Networks {
		if !validComposeIdentifier(name) {
			return fmt.Errorf("invalid compose network name %q", name)
		}
	}
	for name := range file.Volumes {
		if !validComposeIdentifier(name) {
			return fmt.Errorf("invalid compose volume name %q", name)
		}
	}
	return nil
}

func validateComposeService(file ComposeFile, name string, service ComposeService, published map[string]string) error {
	if !validComposeIdentifier(name) {
		return fmt.Errorf("compose service name cannot be empty")
	}
	if strings.TrimSpace(service.Build) == "" && strings.TrimSpace(service.Image) == "" {
		return fmt.Errorf("compose service %q requires build or image", name)
	}
	if strings.TrimSpace(service.Build) != "" && strings.TrimSpace(service.Image) != "" {
		return fmt.Errorf("compose service %q cannot define both build and image", name)
	}
	for _, network := range service.Networks {
		if _, ok := file.Networks[network]; !ok {
			return fmt.Errorf("compose service %q references undeclared network %q", name, network)
		}
	}
	for _, dependency := range service.DependsOn {
		if _, ok := file.Services[dependency]; !ok {
			return fmt.Errorf("compose service %q references unknown dependency %q", name, dependency)
		}
	}
	for index, port := range service.Ports {
		mapping, err := ParsePortMapping(port)
		if err != nil {
			return fmt.Errorf("compose service %q port %d: %w", name, index+1, err)
		}
		protocol := strings.ToLower(strings.TrimSpace(mapping.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		key := fmt.Sprintf("%d/%s", mapping.HostPort(), protocol)
		if previous, exists := published[key]; exists {
			return fmt.Errorf("published port %s is used by both %q and %q", key, previous, name)
		}
		published[key] = name
	}
	for _, volume := range service.Volumes {
		if err := validateComposeVolume(file, name, volume); err != nil {
			return err
		}
	}
	return validateComposeHealthcheck(name, service.Healthcheck)
}

func validComposeIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validateComposeVolume(file ComposeFile, serviceName, value string) error {
	source, destination, ok := composeVolumeParts(value)
	if !ok {
		return fmt.Errorf("compose service %q has invalid volume mapping %q", serviceName, value)
	}
	if !composeAbsolutePath(destination) {
		return fmt.Errorf("compose service %q volume %q has a non-absolute destination", serviceName, value)
	}
	if strings.Contains(destination, ":") && !composeWindowsPath(destination) {
		return fmt.Errorf("compose service %q volume %q has an invalid mode", serviceName, value)
	}
	if composeRootPath(source) {
		return fmt.Errorf("compose service %q volume %q cannot bind the filesystem root", serviceName, value)
	}
	if strings.HasPrefix(source, "/") || composeWindowsPath(source) {
		return nil
	}
	if _, declared := file.Volumes[source]; !declared {
		return fmt.Errorf("compose service %q volume %q references undeclared named volume %q", serviceName, value, source)
	}
	return nil
}

func composeVolumeParts(value string) (source, destination string, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	for _, mode := range []string{":ro", ":rw", ":z", ":Z"} {
		value = strings.TrimSuffix(value, mode)
	}
	if len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' {
		separator := strings.Index(value[2:], ":")
		if separator < 0 {
			return "", "", false
		}
		separator += 2
		return value[:separator], value[separator+1:], true
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), parts[0] != "" && parts[1] != ""
}

func composeAbsolutePath(value string) bool {
	return strings.HasPrefix(value, "/") || composeWindowsPath(value)
}

func composeWindowsPath(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func composeRootPath(value string) bool {
	if value == "/" {
		return true
	}
	return composeWindowsPath(value) && strings.Trim(value[3:], "\\/") == ""
}

func validateComposeHealthcheck(serviceName string, healthcheck map[string]any) error {
	if len(healthcheck) == 0 {
		return nil
	}
	test, ok := healthcheck["test"]
	if !ok {
		return fmt.Errorf("compose service %q healthcheck requires test", serviceName)
	}
	var command string
	switch values := test.(type) {
	case []string:
		if len(values) == 1 && strings.EqualFold(values[0], "NONE") {
			return nil
		}
		if len(values) < 2 || (values[0] != "CMD" && values[0] != composeHealthcheckShell) {
			return fmt.Errorf("compose service %q healthcheck test has invalid form", serviceName)
		}
		command = values[len(values)-1]
	case []any:
		if len(values) < 2 {
			return fmt.Errorf("compose service %q healthcheck test has invalid form", serviceName)
		}
		kind, kindOK := values[0].(string)
		command, _ = values[len(values)-1].(string)
		if !kindOK || (kind != "CMD" && kind != composeHealthcheckShell) {
			return fmt.Errorf("compose service %q healthcheck test has invalid form", serviceName)
		}
	default:
		return fmt.Errorf("compose service %q healthcheck test must be a list", serviceName)
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("compose service %q healthcheck command is empty", serviceName)
	}
	if containsUnconditionalSuccess(command) {
		return fmt.Errorf("compose service %q healthcheck cannot unconditionally succeed", serviceName)
	}
	return nil
}

func containsUnconditionalSuccess(command string) bool {
	lower := strings.ToLower(command)
	for _, marker := range []string{"|| true", "; true", "&& true"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ComposeServiceFromCatalog converts one Phase 4 service into the compose
// representation used by the generated infrastructure stack.
func ComposeServiceFromCatalog(service Service, networkName string) (ComposeService, error) {
	if strings.TrimSpace(service.ID) == "" {
		return ComposeService{}, fmt.Errorf("service ID is required")
	}
	if strings.TrimSpace(service.Image) == "" {
		return ComposeService{}, fmt.Errorf("service %q has no image", service.ID)
	}
	if strings.TrimSpace(networkName) == "" {
		networkName = "ai-launcher"
	}
	for index, mapping := range service.Ports {
		if err := ValidatePortMapping(mapping); err != nil {
			return ComposeService{}, fmt.Errorf("service %q port %d: %w", service.ID, index+1, err)
		}
	}
	compose := ComposeService{
		Image:       service.Image,
		Command:     append([]string(nil), service.Command...),
		WorkingDir:  service.WorkingDir,
		Ports:       composeServicePorts(service.Ports),
		Volumes:     append([]string(nil), service.Volumes...),
		Environment: cloneStringMap(service.Environment),
		Networks:    []string{networkName},
		DependsOn:   append([]string(nil), service.DependsOn...),
		Restart:     "no",
	}
	if command := strings.TrimSpace(service.Healthcheck); command != "" {
		compose.Healthcheck = map[string]any{
			"test": []string{composeHealthcheckShell, command},
		}
	}
	return compose, nil
}

// ComposeServiceFromRunConfig converts the already-resolved docker run inputs
// into the agent service representation. The same-path mounts intentionally
// follow BuildRunCommand: project, agent state and toolchain caches keep their
// host paths inside the container, so a compose launch has the same persistence
// behavior as a single-container launch. The TTY bit is always set: attaching
// interactively is the agent service's purpose, and keying it to the
// generator's own terminal would make a non-interactively rendered file
// conflict with the next interactive run, forcing spurious keep/replace
// prompts.
func ComposeServiceFromRunConfig(cfg RunConfig, command []string, networkName string) (ComposeService, error) {
	if err := ValidateRunResources(cfg); err != nil {
		return ComposeService{}, err
	}
	if err := ValidateProjectDir(cfg.ProjectDir); err != nil {
		return ComposeService{}, err
	}
	if strings.TrimSpace(cfg.AgentExecutable) == "" {
		return ComposeService{}, fmt.Errorf("agent executable is required")
	}
	if strings.TrimSpace(networkName) == "" {
		networkName = "ai-launcher"
	}
	compose := ComposeService{
		Build:      ".",
		Command:    append([]string(nil), command...),
		WorkingDir: cfg.ProjectDir,
		Ports:      composeServicePorts(cfg.ExposedPorts),
		Networks:   []string{networkName},
		Restart:    "no",
		MemLimit:   strings.TrimSpace(cfg.MemoryLimit),
		CPUs:       strings.TrimSpace(cfg.CPULimit),
		PIDsLimit:  cfg.PIDsLimit,
		StdinOpen:  true,
		TTY:        true,
	}
	if cfg.UID > 0 || cfg.GID > 0 {
		compose.User = fmt.Sprintf("%d:%d", cfg.UID, cfg.GID)
	}
	if cfg.MountDockerSocket && cfg.DockerSocketGroupSet {
		compose.GroupAdd = []string{fmt.Sprintf("%d", cfg.DockerSocketGroupID)}
	}
	runtime := RuntimeOrDefault(cfg.Runtime)
	compose.Volumes = composeRunVolumes(cfg, runtime)
	compose.Environment = composeRunEnvironment(cfg, runtime)
	compose.ExtraHosts = composeRunExtraHosts(cfg, runtime)
	return compose, nil
}

func composeRunVolumes(cfg RunConfig, runtime Runtime) []string {
	volumes := []string{mountSpec(cfg.ProjectDir, false)}
	for _, mount := range AgentMounts(cfg.HomeDir, cfg.AgentCommands, ExistsOnHost) {
		volumes = append(volumes, mountSpec(mount.HostPath, false))
	}
	for _, path := range []string{cfg.SSHConfig, cfg.GHConfig} {
		if path != "" && ExistsOnHost(path) {
			volumes = append(volumes, mountSpec(path, true))
		}
	}
	for _, path := range worktreeMountPaths(cfg) {
		if ExistsOnHost(path) {
			volumes = append(volumes, mountSpec(path, false))
		}
	}
	if cfg.MountDockerSocket {
		socket := cfg.DockerSocketPath
		if socket == "" {
			socket = runtime.SocketPath()
		}
		if socket != "" {
			volumes = append(volumes, mountSpec(socket, false))
		}
	}
	if cfg.MemoryNativeBin != "" && ExistsOnHost(cfg.MemoryNativeBin) {
		volumes = append(volumes, mountSpec(cfg.MemoryNativeBin, true))
	}
	for _, dir := range cfg.HostBinaryMounts {
		if strings.TrimSpace(dir) != "" && ExistsOnHost(dir) {
			volumes = append(volumes, mountSpec(dir, true))
		}
	}
	volumes = appendExistingComposeVolumes(volumes, cfg.StackCacheMounts, false)
	volumes = appendExistingComposeVolumes(volumes, cfg.TmuxMounts, true)
	for _, mount := range cfg.DependencyMounts {
		if strings.TrimSpace(mount.HostPath) == "" || strings.TrimSpace(mount.ContainerPath) == "" {
			continue
		}
		mode := strings.TrimSpace(mount.Mode)
		if mode == "" {
			mode = "rw"
		}
		volumes = append(volumes, mount.HostPath+":"+mount.ContainerPath+":"+mode)
	}
	for _, overlay := range cfg.Overlays {
		volumes = append(volumes, overlay.OverlayMountSpec())
	}
	return volumes
}

func appendExistingComposeVolumes(volumes, paths []string, readOnly bool) []string {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" || !ExistsOnHost(path) {
			continue
		}
		volumes = append(volumes, mountSpec(path, readOnly))
	}
	return volumes
}

func composeRunEnvironment(cfg RunConfig, runtime Runtime) map[string]string {
	var environment map[string]string
	if strings.TrimSpace(cfg.HomeDir) != "" {
		environment = map[string]string{"HOME": cfg.HomeDir}
	}
	for _, entry := range cfg.DependencyEnv {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		if environment == nil {
			environment = make(map[string]string)
		}
		environment[key] = value
	}
	for _, entry := range cfg.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		if cfg.AddHostGateway {
			value, _ = RewriteLocalhost(value, runtime.HostGateway())
		}
		if key == "AI_MEMORY_AUTH_TOKEN" {
			value = "${AI_MEMORY_AUTH_TOKEN:-}"
		}
		if environment == nil {
			environment = make(map[string]string)
		}
		environment[key] = value
	}
	return environment
}

func composeRunExtraHosts(cfg RunConfig, runtime Runtime) []string {
	if !cfg.AddHostGateway {
		return nil
	}
	host := runtime.HostGateway()
	if host == "" {
		host = "host.docker.internal"
	}
	return []string{host + ":host-gateway"}
}

func composeServicePorts(mappings []PortMapping) []string {
	ports := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		ports = append(ports, mapping.DockerFlag())
	}
	return ports
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

// AddInfrastructureService inserts one catalog service and declares its named
// volumes. It keeps service insertion and volume derivation together so a
// generated file cannot contain a volume mount without its top-level volume.
func AddInfrastructureService(file *ComposeFile, service Service, networkName string) error {
	return AddInfrastructureServiceWithDataDir(file, service, networkName, "")
}

// AddInfrastructureServiceWithDataDir inserts a catalog service and maps its
// declared container data paths to a project-local directory. An empty data
// directory preserves the legacy named-volume representation for callers that
// only need an in-memory Compose model; the launcher always supplies the
// project .ai-launcher/data directory.
func AddInfrastructureServiceWithDataDir(file *ComposeFile, service Service, networkName, dataDir string) error {
	if file == nil {
		return fmt.Errorf("compose file is required")
	}
	if file.Services == nil {
		file.Services = make(map[string]ComposeService)
	}
	if file.Volumes == nil {
		file.Volumes = make(map[string]ComposeVolume)
	}
	compose, err := ComposeServiceFromCatalog(service, networkName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(dataDir) != "" {
		compose.Volumes, err = persistentServiceVolumes(service, dataDir)
		if err != nil {
			return err
		}
	}
	for index, mapping := range service.Ports {
		if err := ValidatePortMapping(mapping); err != nil {
			return fmt.Errorf("service %q port %d: %w", service.ID, index+1, err)
		}
	}
	if err := validateComposeHealthcheck(service.ID, compose.Healthcheck); err != nil {
		return err
	}
	volumeNames := make([]string, 0, len(service.Volumes))
	for _, volume := range service.Volumes {
		name, _, ok := strings.Cut(volume, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return fmt.Errorf("service %q has invalid volume mapping %q", service.ID, volume)
		}
		volumeNames = append(volumeNames, name)
	}
	file.Services[service.ID] = compose
	if strings.TrimSpace(dataDir) == "" {
		for _, name := range volumeNames {
			file.Volumes[name] = ComposeVolume{}
		}
	}
	return nil
}

// persistentServiceVolumes preserves each catalog volume's container target
// while replacing the Compose named-volume source with a host directory under
// .ai-launcher/data/<service>. A service with multiple data paths gets one
// additional directory per catalog volume, so its state remains separated and
// recoverable without guessing from the container path.
func persistentServiceVolumes(service Service, dataDir string) ([]string, error) {
	dataDir = filepath.Clean(strings.TrimSpace(dataDir))
	if !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("service data directory must be absolute: %q", dataDir)
	}
	if !validComposeIdentifier(service.ID) {
		return nil, fmt.Errorf("service %q has invalid persistent data identifier", service.ID)
	}
	volumes := make([]string, 0, len(service.Volumes))
	for index, raw := range service.Volumes {
		source, destination, ok := composeVolumeParts(raw)
		if !ok || !validComposeIdentifier(source) || !filepath.IsAbs(destination) {
			return nil, fmt.Errorf("service %q has invalid volume mapping %q", service.ID, raw)
		}
		path := filepath.Join(dataDir, service.ID)
		if len(service.Volumes) > 1 {
			path = filepath.Join(path, source)
		}
		if filepath.Clean(path) == filepath.Clean(dataDir) || !pathWithin(dataDir, path) {
			return nil, fmt.Errorf("service %q volume %d escapes data directory", service.ID, index)
		}
		volumes = append(volumes, path+":"+destination+composeVolumeMode(raw))
	}
	return volumes, nil
}

func composeVolumeMode(value string) string {
	value = strings.TrimSpace(value)
	for _, mode := range []string{":ro", ":rw", ":z", ":Z"} {
		if strings.HasSuffix(value, mode) {
			return mode
		}
	}
	return ""
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
