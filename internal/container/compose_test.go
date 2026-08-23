package container

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestRenderCompose(t *testing.T) {
	file := NewComposeFile()
	file.Services["agent"] = ComposeService{
		Build:    ".",
		Networks: []string{"ai-launcher"},
		DependsOn: []string{
			"postgres",
			"redis",
		},
	}
	if err := AddInfrastructureService(&file, ServiceByIDMust(t, "postgres"), "ai-launcher"); err != nil {
		t.Fatalf("AddInfrastructureService(postgres) error = %v", err)
	}
	if err := AddInfrastructureService(&file, ServiceByIDMust(t, "redis"), "ai-launcher"); err != nil {
		t.Fatalf("AddInfrastructureService(redis) error = %v", err)
	}
	file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge"}

	first, err := RenderCompose(file)
	if err != nil {
		t.Fatalf("RenderCompose() error = %v", err)
	}
	second, err := RenderCompose(file)
	if err != nil {
		t.Fatalf("RenderCompose() second error = %v", err)
	}
	if first != second {
		t.Fatalf("RenderCompose() is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(first), &document); err != nil {
		t.Fatalf("rendered YAML is invalid: %v\n%s", err, first)
	}
	if _, ok := document["version"]; ok {
		t.Fatalf("rendered compose must omit obsolete version key: %#v", document["version"])
	}
	for _, key := range []string{"services:", "networks:", "volumes:", "agent:", "postgres:", "redis:", "5432:5432", "6379:6379"} {
		if !strings.Contains(first, key) {
			t.Errorf("rendered compose missing %q:\n%s", key, first)
		}
	}
	if strings.Index(first, "agent:") > strings.Index(first, "postgres:") {
		t.Fatalf("services are not sorted in rendered YAML:\n%s", first)
	}
}

func TestComposeNetworkInternalRendersWhenTrue(t *testing.T) {
	file := NewComposeFile()
	file.Services["agent"] = ComposeService{Build: ".", Networks: []string{"ai-launcher"}}
	file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge", Internal: true}

	rendered, err := RenderCompose(file)
	if err != nil {
		t.Fatalf("RenderCompose() error = %v", err)
	}
	if !strings.Contains(rendered, "internal: true") {
		t.Fatalf("rendered compose missing %q:\n%s", "internal: true", rendered)
	}
}

func TestComposeNetworkInternalOmittedWhenFalse(t *testing.T) {
	file := NewComposeFile()
	file.Services["agent"] = ComposeService{Build: ".", Networks: []string{"ai-launcher"}}
	file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge"}

	rendered, err := RenderCompose(file)
	if err != nil {
		t.Fatalf("RenderCompose() error = %v", err)
	}
	if strings.Contains(rendered, "internal:") {
		t.Fatalf("rendered compose must omit internal key when false:\n%s", rendered)
	}
}

func TestComposeServiceFromCatalog(t *testing.T) {
	service := ServiceByIDMust(t, "postgres")
	got, err := ComposeServiceFromCatalog(service, "shared")
	if err != nil {
		t.Fatalf("ComposeServiceFromCatalog() error = %v", err)
	}
	if got.Image != service.Image || !reflect.DeepEqual(got.Networks, []string{"shared"}) || !reflect.DeepEqual(got.Ports, []string{"5432:5432"}) {
		t.Fatalf("compose service = %#v", got)
	}
	if got.Healthcheck == nil || got.Healthcheck["test"] == nil {
		t.Fatalf("compose healthcheck = %#v; want test", got.Healthcheck)
	}
	if got.Environment["POSTGRES_PASSWORD"] != "dev" || got.Environment["POSTGRES_DB"] != "dev" {
		t.Fatalf("compose environment = %#v; want catalog defaults", got.Environment)
	}
	if !reflect.DeepEqual(got.Healthcheck["test"], []string{"CMD-SHELL", service.Healthcheck}) {
		t.Fatalf("healthcheck test = %#v; want CMD-SHELL + catalog command", got.Healthcheck["test"])
	}
	if got.Restart != "no" {
		t.Fatalf("restart = %q; want no", got.Restart)
	}
}

func TestComposeServiceFromCatalogPreservesServiceRuntime(t *testing.T) {
	for _, id := range []string{"dynamodb", "nats", "jaeger", "mailpit"} {
		service := ServiceByIDMust(t, id)
		got, err := ComposeServiceFromCatalog(service, "shared")
		if err != nil {
			t.Fatalf("ComposeServiceFromCatalog(%s) error = %v", id, err)
		}
		if !reflect.DeepEqual(got.Command, service.Command) || got.WorkingDir != service.WorkingDir {
			t.Errorf("%s runtime = command %q working_dir %q; want %q %q", id, got.Command, got.WorkingDir, service.Command, service.WorkingDir)
		}
		if !reflect.DeepEqual(got.Environment, service.Environment) {
			t.Errorf("%s environment = %#v; want %#v", id, got.Environment, service.Environment)
		}
	}
}

func TestAddInfrastructureServiceDeclaresNamedVolumes(t *testing.T) {
	file := NewComposeFile()
	if err := AddInfrastructureService(&file, ServiceByIDMust(t, "mongo"), "ai-launcher"); err != nil {
		t.Fatalf("AddInfrastructureService() error = %v", err)
	}
	if _, ok := file.Volumes["mongo-data"]; !ok {
		t.Fatalf("volumes = %#v; want mongo-data", file.Volumes)
	}
	if got := file.Services["mongo"].Volumes; !reflect.DeepEqual(got, []string{"mongo-data:/data/db"}) {
		t.Fatalf("mongo volumes = %#v", got)
	}
}

func TestAddInfrastructureServiceUsesProjectDataDirectory(t *testing.T) {
	file := NewComposeFile()
	dataDir := filepath.Join(t.TempDir(), ".ai-launcher", "data")
	if err := AddInfrastructureServiceWithDataDir(&file, ServiceByIDMust(t, "postgres"), "ai-launcher", dataDir); err != nil {
		t.Fatalf("AddInfrastructureServiceWithDataDir() error = %v", err)
	}
	if got := file.Services["postgres"].Volumes; !reflect.DeepEqual(got, []string{filepath.Join(dataDir, "postgres") + ":/var/lib/postgresql"}) {
		t.Fatalf("postgres volumes = %#v; want project-local bind", got)
	}
	if len(file.Volumes) != 0 {
		t.Fatalf("project data mode should not declare named volumes: %#v", file.Volumes)
	}

	multi := Service{ID: "mock", Name: "Mock", Image: "busybox:1", Volumes: []string{"config-data:/etc/mock", "state-data:/var/lib/mock"}}
	if err := AddInfrastructureServiceWithDataDir(&file, multi, "ai-launcher", dataDir); err != nil {
		t.Fatalf("multi-volume service error = %v", err)
	}
	for _, want := range []string{
		filepath.Join(dataDir, "mock", "config-data") + ":/etc/mock",
		filepath.Join(dataDir, "mock", "state-data") + ":/var/lib/mock",
	} {
		if !containsString(file.Services["mock"].Volumes, want) {
			t.Errorf("mock volumes = %#v; missing %q", file.Services["mock"].Volumes, want)
		}
	}

	readOnly := Service{ID: "readonly", Name: "Read-only", Image: "busybox:1", Volumes: []string{"config:/etc/mock:ro"}}
	if err := AddInfrastructureServiceWithDataDir(&file, readOnly, "ai-launcher", dataDir); err != nil {
		t.Fatalf("read-only service error = %v", err)
	}
	wantReadOnly := filepath.Join(dataDir, "readonly") + ":/etc/mock:ro"
	if got := file.Services["readonly"].Volumes; !reflect.DeepEqual(got, []string{wantReadOnly}) {
		t.Fatalf("read-only volumes = %#v; want %#v", got, []string{wantReadOnly})
	}
}

func TestComposeServiceFromRunConfigMirrorsDockerMounts(t *testing.T) {
	originalExists := ExistsOnHost
	ExistsOnHost = func(string) bool { return true }
	t.Cleanup(func() { ExistsOnHost = originalExists })

	service, err := ComposeServiceFromRunConfig(RunConfig{
		Runtime:              PodmanRuntime{},
		HomeDir:              "/home/tester",
		UID:                  501,
		GID:                  20,
		ProjectDir:           "/work/project",
		AgentCommands:        []string{"claude"},
		SSHConfig:            "/home/tester/.ssh",
		GHConfig:             "/home/tester/.config/gh",
		MountDockerSocket:    true,
		DockerSocketGroupID:  20,
		DockerSocketGroupSet: true,
		MemoryNativeBin:      "/home/tester/.local/share/ai-launcher/bin/ai-memory",
		HostBinaryMounts:     []string{"/opt/agents"},
		StackCacheMounts:     []string{"/home/tester/.nvm", "/home/tester/.m2"},
		TmuxMounts:           []string{"/home/tester/.tmux.conf", "/home/tester/.tmux/plugins"},
		WorktreeMounts:       []string{"/Volumes/Other Repo/feature"},
		DependencyMounts: []DependencyMount{{
			ID: "go.module-cache", Kind: DependencyPackage,
			HostPath: "/Users/tester/go/pkg/mod", ContainerPath: "/home/ai-launcher/go/pkg/mod", Mode: "ro",
		}},
		DependencyEnv: []string{"GOMODCACHE=/home/ai-launcher/go/pkg/mod"},
		Env: []string{
			"AI_MEMORY_SERVER_URL=http://localhost:9292",
			"AI_MEMORY_AUTH_TOKEN=secret-not-in-file",
		},
		AgentExecutable: "claude",
		MemoryLimit:     "4g",
		CPULimit:        "2.0",
		PIDsLimit:       512,
		ExposedPorts:    []PortMapping{{Host: 3000, Internal: 3000}},
		AddHostGateway:  true,
	}, []string{"claude", "--model", "sonnet"}, "ai-launcher")
	if err != nil {
		t.Fatalf("ComposeServiceFromRunConfig() error = %v", err)
	}
	if service.Build != "." || service.WorkingDir != "/work/project" || service.User != "501:20" {
		t.Fatalf("agent service identity = %#v", service)
	}
	if !reflect.DeepEqual(service.GroupAdd, []string{"20"}) {
		t.Fatalf("agent supplemental groups = %#v; want [20]", service.GroupAdd)
	}
	for _, want := range []string{
		"/work/project:/work/project",
		"/home/tester/.claude:/home/tester/.claude",
		"/home/tester/.ssh:/home/tester/.ssh:ro",
		"/home/tester/.config/gh:/home/tester/.config/gh:ro",
		"/home/tester/.local/share/ai-launcher/bin/ai-memory:/home/tester/.local/share/ai-launcher/bin/ai-memory:ro",
		"/opt/agents:/opt/agents:ro",
		"/home/tester/.nvm:/home/tester/.nvm",
		"/home/tester/.m2:/home/tester/.m2",
		"/home/tester/.tmux.conf:/home/tester/.tmux.conf:ro",
		"/home/tester/.tmux/plugins:/home/tester/.tmux/plugins:ro",
		"/Volumes/Other Repo/feature:/Volumes/Other Repo/feature",
		"/Users/tester/go/pkg/mod:/home/ai-launcher/go/pkg/mod:ro",
	} {
		if !containsString(service.Volumes, want) {
			t.Errorf("agent service missing volume %q: %v", want, service.Volumes)
		}
	}
	if service.Environment["HOME"] != "/home/tester" || service.Environment["AI_MEMORY_SERVER_URL"] != "http://host.containers.internal:9292" {
		t.Fatalf("agent environment = %#v", service.Environment)
	}
	if service.Environment["GOMODCACHE"] != "/home/ai-launcher/go/pkg/mod" {
		t.Fatalf("dependency environment = %#v", service.Environment)
	}
	if service.Environment["AI_MEMORY_AUTH_TOKEN"] != "${AI_MEMORY_AUTH_TOKEN:-}" {
		t.Fatalf("auth token must use compose interpolation: %#v", service.Environment)
	}
	if service.MemLimit != "4g" || service.CPUs != "2.0" || service.PIDsLimit != 512 || !service.StdinOpen || !service.TTY || !reflect.DeepEqual(service.Ports, []string{"3000:3000"}) {
		t.Fatalf("agent resources = %#v", service)
	}
	if !reflect.DeepEqual(service.ExtraHosts, []string{"host.containers.internal:host-gateway"}) {
		t.Fatalf("extra hosts = %#v", service.ExtraHosts)
	}
}

// The rendered agent service must not depend on the generator's own terminal:
// a compose file written without a TTY would otherwise differ from the one an
// interactive run expects, forcing spurious keep/replace prompts.
func TestComposeAgentTTYIsDeterministic(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		cfg := RunConfig{ProjectDir: "/tmp", AgentExecutable: "agent", Interactive: interactive}
		service, err := ComposeServiceFromRunConfig(cfg, []string{"agent"}, "")
		if err != nil {
			t.Fatalf("ComposeServiceFromRunConfig(Interactive=%v) error = %v", interactive, err)
		}
		if !service.TTY {
			t.Fatalf("ComposeServiceFromRunConfig(Interactive=%v) TTY = false; want always true", interactive)
		}
	}
}

func TestMaterializeCompose(t *testing.T) {
	project := t.TempDir()
	file := NewComposeFile()
	file.Services["agent"] = ComposeService{Build: "."}
	path, err := MaterializeCompose(project, file)
	if err != nil {
		t.Fatalf("MaterializeCompose() error = %v", err)
	}
	if !strings.HasSuffix(path, ".ai-launcher/docker-compose.yaml") {
		t.Fatalf("path = %q", path)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path was returned by the materializer under t.TempDir().
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(data), "version:") {
		t.Fatalf("compose file = %s", data)
	}
}

func TestMaterializeComposeCreatesOnlyServiceDataDirectories(t *testing.T) {
	project := t.TempDir()
	file := NewComposeFile()
	dataDir := filepath.Join(project, ".ai-launcher", "data")
	file.Services["postgres"] = ComposeService{
		Image:   "postgres:18",
		Volumes: []string{filepath.Join(dataDir, "postgres") + ":/var/lib/postgresql"},
	}
	if _, err := MaterializeCompose(project, file); err != nil {
		t.Fatalf("MaterializeCompose() error = %v", err)
	}
	for _, path := range []string{dataDir, filepath.Join(dataDir, "postgres")} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("data path %s = %v; want directory", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "redis")); !os.IsNotExist(err) {
		t.Fatalf("unexpected unrelated service data directory: %v", err)
	}
}

// TestMaterializeComposeWritesGeneratedFiles is the regression guard for a
// real bug caught by independent review: ensureComposeDataDirectories
// MkdirAll'd the egress proxy's squid.conf volume source as a directory
// (every service volume under dataDir was treated as one), so
// WriteComposeGeneratedFiles then failed writing the file — "file exists" or
// "not a directory" depending on call order. This exercises the actual write
// path (MaterializeCompose in a t.TempDir()), which the pure-generator tests
// in egressproxy_test.go and the in-memory-model tests in
// internal/launcher/compose_test.go never did.
func TestMaterializeComposeWritesGeneratedFiles(t *testing.T) {
	project := t.TempDir()
	dataDir := filepath.Join(project, ".ai-launcher", "data")
	configPath := EgressProxyConfigPath(dataDir)

	content, err := GenerateEgressProxyConfig([]string{"api.anthropic.com"})
	if err != nil {
		t.Fatalf("GenerateEgressProxyConfig() error = %v", err)
	}
	proxy, err := EgressProxyComposeService(configPath, "ai-launcher", "ai-launcher-egress")
	if err != nil {
		t.Fatalf("EgressProxyComposeService() error = %v", err)
	}

	file := NewComposeFile()
	file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge", Internal: true}
	file.Networks["ai-launcher-egress"] = ComposeNetwork{Driver: "bridge"}
	file.Services["agent"] = ComposeService{Build: ".", Networks: []string{"ai-launcher"}}
	file.Services[EgressProxyServiceID] = proxy
	file.GeneratedFiles = map[string]string{configPath: content}

	if _, err := MaterializeCompose(project, file); err != nil {
		t.Fatalf("MaterializeCompose() error = %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", configPath, err)
	}
	if info.IsDir() {
		t.Fatalf("%s materialized as a directory; want a regular file", configPath)
	}
	written, err := os.ReadFile(configPath) // #nosec G304 -- path under t.TempDir(), constructed by the test itself.
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", configPath, err)
	}
	if string(written) != content {
		t.Fatalf("written config = %q; want %q", written, content)
	}
}

// TestMaterializeComposeRewritesGeneratedFilesOnEveryCall guards the second
// bug from the same review: GeneratedFiles content is invisible to the
// rendered docker-compose.yaml (yaml:"-"), so a caller that only regenerates
// on YAML-diff (like cmd/ai-launcher's materializeComposeIfNeeded) would
// silently keep serving a stale squid.conf after the domain allowlist
// changes. MaterializeCompose itself must always rewrite GeneratedFiles
// unconditionally — this locks that in at the container-package level, below
// any caller-side change-detection.
func TestMaterializeComposeRewritesGeneratedFilesOnEveryCall(t *testing.T) {
	project := t.TempDir()
	dataDir := filepath.Join(project, ".ai-launcher", "data")
	configPath := EgressProxyConfigPath(dataDir)

	first := NewComposeFile()
	first.Services["agent"] = ComposeService{Build: "."}
	first.GeneratedFiles = map[string]string{configPath: "first"}
	if _, err := MaterializeCompose(project, first); err != nil {
		t.Fatalf("MaterializeCompose(first) error = %v", err)
	}

	second := NewComposeFile()
	second.Services["agent"] = ComposeService{Build: "."}
	second.GeneratedFiles = map[string]string{configPath: "second"}
	if _, err := MaterializeCompose(project, second); err != nil {
		t.Fatalf("MaterializeCompose(second) error = %v", err)
	}

	written, err := os.ReadFile(configPath) // #nosec G304 -- path under t.TempDir(), constructed by the test itself.
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", configPath, err)
	}
	if string(written) != "second" {
		t.Fatalf("written config = %q; want %q (stale content was not overwritten)", written, "second")
	}
}

func TestRenderComposeRejectsInvalidService(t *testing.T) {
	file := NewComposeFile()
	file.Services["broken"] = ComposeService{}
	if _, err := RenderCompose(file); err == nil {
		t.Fatal("RenderCompose() error = nil; want service validation error")
	}
	if _, err := RenderCompose(ComposeFile{}); err == nil {
		t.Fatal("RenderCompose() error = nil for empty compose; want validation error")
	}
	file = NewComposeFile()
	file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge"}
	file.Services["agent"] = ComposeService{
		Build:     ".",
		Networks:  []string{"missing"},
		DependsOn: []string{"postgres"},
		Volumes:   []string{"/:/host"},
		Healthcheck: map[string]any{
			"test": []string{"CMD-SHELL", "probe || true"},
		},
	}
	if _, err := RenderCompose(file); err == nil {
		t.Fatal("RenderCompose() accepted undeclared network/dependency/root volume/successful healthcheck")
	}
	file.Services["agent"] = ComposeService{
		Build: ".",
		Ports: []string{"5432:5432"},
	}
	file.Services["other"] = ComposeService{
		Image: "redis:7",
		Ports: []string{"5432:6379"},
	}
	if _, err := RenderCompose(file); err == nil {
		t.Fatal("RenderCompose() accepted a host port collision")
	}
	file = NewComposeFile()
	file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge"}
	file.Services["agent"] = ComposeService{
		Build:    ".",
		Networks: []string{"ai-launcher"},
		Healthcheck: map[string]any{
			"test": []string{"CMD-SHELL", "probe || true"},
		},
	}
	if _, err := RenderCompose(file); err == nil || !strings.Contains(err.Error(), "unconditionally") {
		t.Fatalf("RenderCompose() healthcheck error = %v; want unconditional-success rejection", err)
	}
	file.Services["agent"] = ComposeService{
		Build:   ".",
		Ports:   []string{"3000:3000", "3000:3000"},
		Volumes: []string{"cache:/work:invalid"},
	}
	file.Volumes["cache"] = ComposeVolume{}
	if _, err := RenderCompose(file); err == nil {
		t.Fatal("RenderCompose() accepted duplicate port or invalid volume mode")
	}
}

func TestComposeValidationPortableForms(t *testing.T) {
	if rendered, err := RenderCompose(ComposeFile{
		Services: map[string]ComposeService{"agent": {Build: "."}},
	}); err != nil || !strings.Contains(rendered, "services:") || strings.Contains(rendered, "version:") {
		t.Fatalf("RenderCompose() with Compose Specification = (%q, %v)", rendered, err)
	}

	validationCases := []struct {
		name string
		file ComposeFile
		want string
	}{
		{name: "invalid network name", file: ComposeFile{Networks: map[string]ComposeNetwork{"bad/name": {}}, Services: map[string]ComposeService{"agent": {Build: "."}}}, want: "invalid compose network"},
		{name: "invalid volume name", file: ComposeFile{Volumes: map[string]ComposeVolume{"bad/name": {}}, Services: map[string]ComposeService{"agent": {Build: "."}}}, want: "invalid compose volume"},
		{name: "invalid service name", file: ComposeFile{Services: map[string]ComposeService{"bad/name": {Build: "."}}}, want: "service name cannot be empty"},
		{name: "build and image", file: ComposeFile{Services: map[string]ComposeService{"agent": {Build: ".", Image: "busybox"}}}, want: "both build and image"},
		{name: "invalid port", file: ComposeFile{Services: map[string]ComposeService{"agent": {Build: ".", Ports: []string{"not-a-port"}}}}, want: "port"},
		{name: "undeclared volume", file: ComposeFile{Services: map[string]ComposeService{"agent": {Build: ".", Volumes: []string{"cache:/work"}}}}, want: "undeclared named volume"},
		{name: "relative destination", file: ComposeFile{Services: map[string]ComposeService{"agent": {Build: ".", Volumes: []string{"/tmp:work"}}}}, want: "non-absolute destination"},
		{name: "invalid mapping", file: ComposeFile{Services: map[string]ComposeService{"agent": {Build: ".", Volumes: []string{"/tmp"}}}}, want: "invalid volume mapping"},
	}
	for _, tc := range validationCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.file.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v; want substring %q", err, tc.want)
			}
		})
	}

	for _, value := range []string{"/tmp:/work", "C:\\data:/work", "C:\\data:/work:ro"} {
		file := NewComposeFile()
		file.Services["agent"] = ComposeService{Build: ".", Volumes: []string{value}}
		if err := file.Validate(); err != nil {
			t.Errorf("Validate() volume %q error = %v", value, err)
		}
	}
	for _, value := range []string{"C:\\:/work", ":/work", "C:\\data"} {
		file := NewComposeFile()
		file.Services["agent"] = ComposeService{Build: ".", Volumes: []string{value}}
		if err := file.Validate(); err == nil {
			t.Errorf("Validate() volume %q error = nil", value)
		}
	}

	healthcheckCases := []struct {
		name  string
		value map[string]any
		valid bool
	}{
		{name: "none", value: map[string]any{"test": []string{"none"}}, valid: true},
		{name: "string command", value: map[string]any{"test": []string{"CMD-SHELL", "probe"}}, valid: true},
		{name: "any command", value: map[string]any{"test": []any{"CMD", "probe"}}, valid: true},
		{name: "missing test", value: map[string]any{"interval": "1s"}},
		{name: "invalid list", value: map[string]any{"test": []any{"CMD"}}},
		{name: "invalid kind type", value: map[string]any{"test": []any{123, "probe"}}},
		{name: "invalid kind", value: map[string]any{"test": []any{"BAD", "probe"}}},
		{name: "non-list", value: map[string]any{"test": "probe"}},
		{name: "empty command", value: map[string]any{"test": []string{"CMD-SHELL", " "}}},
	}
	for _, tc := range healthcheckCases {
		t.Run("healthcheck/"+tc.name, func(t *testing.T) {
			err := validateComposeHealthcheck("agent", tc.value)
			if tc.valid && err != nil {
				t.Fatalf("validateComposeHealthcheck() error = %v", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("validateComposeHealthcheck() error = nil")
			}
		})
	}
}

func TestComposeServiceEdgeCases(t *testing.T) {
	if _, err := ComposeServiceFromCatalog(Service{}, ""); err == nil {
		t.Fatal("ComposeServiceFromCatalog() accepted an empty service")
	}
	if _, err := ComposeServiceFromCatalog(Service{ID: "custom"}, ""); err == nil {
		t.Fatal("ComposeServiceFromCatalog() accepted a service without an image")
	}
	custom, err := ComposeServiceFromCatalog(Service{ID: "custom", Image: "busybox", Ports: []PortMapping{{Internal: 8080}}}, "")
	if err != nil || !reflect.DeepEqual(custom.Networks, []string{"ai-launcher"}) || custom.Healthcheck != nil {
		t.Fatalf("ComposeServiceFromCatalog() custom service = %#v, err = %v", custom, err)
	}
	if _, err := ComposeServiceFromCatalog(Service{ID: "custom", Image: "busybox", Ports: []PortMapping{{Internal: 0}}}, "net"); err == nil {
		t.Fatal("ComposeServiceFromCatalog() accepted an invalid port")
	}

	file := ComposeFile{}
	if err := AddInfrastructureService(nil, Service{}, ""); err == nil {
		t.Fatal("AddInfrastructureService(nil) error = nil")
	}
	if err := AddInfrastructureService(&file, Service{ID: "custom", Image: "busybox"}, ""); err != nil {
		t.Fatalf("AddInfrastructureService() error = %v", err)
	}
	if _, ok := file.Services["custom"]; !ok {
		t.Fatalf("services = %#v", file.Services)
	}

	service, err := ComposeServiceFromRunConfig(RunConfig{
		Runtime:         PodmanRuntime{},
		ProjectDir:      "/tmp",
		AgentExecutable: "agent",
		Interactive:     true,
		Env:             []string{"malformed", "=missing-key", "CUSTOM=value"},
	}, []string{"agent"}, "")
	if err != nil {
		t.Fatalf("ComposeServiceFromRunConfig() edge case error = %v", err)
	}
	if service.Environment["CUSTOM"] != "value" || service.TTY != true || !reflect.DeepEqual(service.Networks, []string{"ai-launcher"}) {
		t.Fatalf("ComposeServiceFromRunConfig() = %#v", service)
	}
	if _, err := ComposeServiceFromRunConfig(RunConfig{MemoryLimit: "bad"}, nil, ""); err == nil {
		t.Fatal("ComposeServiceFromRunConfig() accepted invalid resources")
	}
	if _, err := ComposeServiceFromRunConfig(RunConfig{ProjectDir: "/tmp"}, nil, ""); err == nil {
		t.Fatal("ComposeServiceFromRunConfig() accepted an empty agent executable")
	}
}

func ServiceByIDMust(t *testing.T, id string) Service {
	t.Helper()
	service, ok := ServiceByID(id)
	if !ok {
		t.Fatalf("ServiceByID(%q) = false", id)
	}
	return service
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// An empty project directory falls back to the current working directory: the
// compose file lands under cwd/.ai-launcher.
func TestMaterializeComposeResolvesEmptyProjectDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	file := NewComposeFile()
	file.Services["agent"] = ComposeService{Build: "."}
	path, err := MaterializeCompose("\t", file)
	if err != nil {
		t.Fatalf("MaterializeCompose() error = %v", err)
	}
	want := filepath.Join(cwd, containerArtifactDirName, "docker-compose.yaml")
	if path != want {
		t.Fatalf("MaterializeCompose(\"\") path = %q; want %q (cwd fallback)", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("compose file not materialized under cwd: %v", err)
	}
}

// assertHardened is the shared predicate for the security baseline every
// generated service carries: cap_drop ALL (the default Docker capability set
// stripped) plus security_opt no-new-privileges:true (no setuid escalation).
// It is the docker backend's analogue of the seccomp/landlock hardening the
// ai-jail path gets from its sandbox.
func assertHardened(t *testing.T, label string, svc ComposeService) {
	t.Helper()
	if len(svc.CapDrop) != 1 || svc.CapDrop[0] != "ALL" {
		t.Errorf("%s CapDrop = %#v; want [ALL]", label, svc.CapDrop)
	}
	found := false
	for _, opt := range svc.SecurityOpt {
		if opt == "no-new-privileges:true" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%s SecurityOpt = %#v; want to contain no-new-privileges:true", label, svc.SecurityOpt)
	}
}

// TestEveryGeneratedServiceIsHardened is the property test that locks the
// hardening baseline across every constructor that produces a ComposeService:
// every catalog service, the agent (ComposeServiceFromRunConfig), and the
// egress proxy. If a new service or constructor is added without the baseline,
// this fails; if hardenedServiceSecurity is weakened, every arm fails. The
// proxy needs cap_add SETGID/SETUID to drop from root, and catalog services
// additionally need CHOWN to set up their data dir — both validated
// behaviorally by the container_integration suite.
func TestEveryGeneratedServiceIsHardened(t *testing.T) {
	t.Run("catalog services", func(t *testing.T) {
		for _, id := range ServiceIDs() {
			service, ok := ServiceByID(id)
			if !ok {
				t.Fatalf("ServiceByID(%q) not found", id)
			}
			svc, err := ComposeServiceFromCatalog(service, "ai-launcher")
			if err != nil {
				t.Fatalf("ComposeServiceFromCatalog(%q) error = %v", id, err)
			}
			assertHardened(t, "catalog:"+id, svc)
			// Catalog services chown their data dir and drop privileges, so the
			// minimal cap_add set is CHOWN + SETGID + SETUID (validated for redis
			// and postgres by the container_integration suite).
			if len(svc.CapAdd) != 3 || svc.CapAdd[0] != "CHOWN" || svc.CapAdd[1] != "SETGID" || svc.CapAdd[2] != "SETUID" {
				t.Errorf("catalog:%s CapAdd = %#v; want [CHOWN SETGID SETUID]", id, svc.CapAdd)
			}
		}
	})

	t.Run("agent service", func(t *testing.T) {
		svc, err := ComposeServiceFromRunConfig(RunConfig{
			ProjectDir:      "/project",
			AgentExecutable: "claude",
			Selection:       Selection{Agents: []AgentInstall{{Version: "1.0.0"}}},
		}, []string{"claude"}, "ai-launcher")
		if err != nil {
			t.Fatalf("ComposeServiceFromRunConfig() error = %v", err)
		}
		assertHardened(t, "agent", svc)
		if svc.ReadOnly {
			t.Error("agent ReadOnly = true; the agent writes to /tmp and its working directory and cannot be read-only")
		}
	})

	t.Run("egress proxy", func(t *testing.T) {
		svc, err := EgressProxyComposeService("/data/egress-proxy/squid.conf", "ai-launcher", "ai-launcher-egress")
		if err != nil {
			t.Fatalf("EgressProxyComposeService() error = %v", err)
		}
		assertHardened(t, "egress-proxy", svc)
		// squid starts as root and drops to its "proxy" user via setgid/setuid,
		// so it needs exactly those two capabilities handed back on top of
		// cap_drop ALL — see EgressProxyComposeService's doc comment.
		if len(svc.CapAdd) != 2 || svc.CapAdd[0] != "SETGID" || svc.CapAdd[1] != "SETUID" {
			t.Errorf("egress-proxy CapAdd = %#v; want [SETGID SETUID]", svc.CapAdd)
		}
		if svc.ReadOnly {
			t.Error("egress-proxy ReadOnly = true; squid writes pid/cache state and cannot run read-only")
		}
	})
}

// TestRenderedComposeCarriesHardening confirms the omitempty security fields
// actually reach the rendered YAML (a struct field that never marshals would
// make assertHardened pass while the running container stayed unhardened).
func TestRenderedComposeCarriesHardening(t *testing.T) {
	file := NewComposeFile()
	file.Networks["ai-launcher"] = ComposeNetwork{Driver: "bridge"}
	agent, err := ComposeServiceFromRunConfig(RunConfig{
		ProjectDir:      "/project",
		AgentExecutable: "claude",
		Selection:       Selection{Agents: []AgentInstall{{Version: "1.0.0"}}},
	}, []string{"claude"}, "ai-launcher")
	if err != nil {
		t.Fatalf("ComposeServiceFromRunConfig() error = %v", err)
	}
	file.Services["agent"] = agent
	rendered, err := RenderCompose(file)
	if err != nil {
		t.Fatalf("RenderCompose() error = %v", err)
	}
	for _, want := range []string{"cap_drop:", "- ALL", "no-new-privileges:true"} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("rendered compose missing %q:\n%s", want, rendered)
		}
	}
}
