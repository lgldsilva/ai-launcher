package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

func TestServicesViewRendersCategoriesPortsAndVolumes(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		UseMemory:   true,
		Permissions: map[string]bool{},
		Services:    []string{"mongo"},
	})
	view := model.View()
	for _, want := range []string{"Services", "Databases (SQL)", "Databases (NoSQL)", "MongoDB", "27017:27017", "volumes=mongo-data:/data/db", "data=.ai-launcher/data/mongo"} {
		if !strings.Contains(view, want) {
			t.Fatalf("services view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "[✓] MongoDB") {
		t.Fatalf("selected service is not marked:\n%s", view)
	}
}

func TestServicesViewScrollsWithShortTerminal(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		Permissions: map[string]bool{},
	})
	model.height = 12
	model.section = model.servicesIndex()
	model.cursor = len(model.serviceIDs) - 1
	view := model.View()
	if !strings.Contains(view, "↓") && !strings.Contains(view, "↑") {
		t.Fatalf("short services view has no scroll indicator:\n%s", view)
	}
	if strings.Contains(view, "PostgreSQL") {
		t.Fatalf("short services view still renders the first service:\n%s", view)
	}
	if !strings.Contains(view, "Caddy") {
		t.Fatalf("short services view lost the highlighted tail service:\n%s", view)
	}
}

func TestComposePreviewRendersWhenServicesAreSelected(t *testing.T) {
	stubWindows(t, false)
	project := t.TempDir()
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Command: "custom-cli"},
		UseDocker:   true,
		ProjectDir:  project,
		HomeDir:     project,
		Services:    []string{"redis"},
		Permissions: map[string]bool{},
		Docker: container.RunConfig{
			ProjectDir:      project,
			AgentExecutable: "custom-cli",
			Selection:       container.Selection{Agents: []container.AgentInstall{{Command: "custom-cli", Version: "1.0.0", Kind: container.InstallRelease}}},
		},
	})
	view := model.View()
	if !strings.Contains(view, "Compose preview:") || !strings.Contains(view, "redis:") {
		t.Fatalf("view = %q; want Compose preview with redis service", view)
	}
}

func TestComposePreviewReportsErrorsInsteadOfFallingBackToDockerRun(t *testing.T) {
	stubWindows(t, false)
	project := t.TempDir()
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Command: "custom-cli"},
		UseDocker:   true,
		ProjectDir:  project,
		Services:    []string{"redis"},
		Permissions: map[string]bool{},
		Docker: container.RunConfig{
			ProjectDir:      project,
			AgentExecutable: "custom-cli",
			NetworkName:     "host",
			Selection:       container.Selection{Agents: []container.AgentInstall{{Command: "custom-cli", Version: "1.0.0", Kind: container.InstallRelease}}},
		},
	})
	view := model.View()
	if !strings.Contains(view, "Compose preview unavailable") {
		t.Fatalf("view = %q; want Compose error", view)
	}
	if strings.Contains(view, "Preview: docker") {
		t.Fatalf("view fell back to docker run after Compose failure: %q", view)
	}
}

func TestToggleServiceUpdatesCanonicalSelection(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		UseDocker:   true,
		UseMemory:   true,
		Permissions: map[string]bool{},
		Services:    []string{"redis"},
	})
	if model.servicesIndex() != 5 || model.sectionCount() != 6 {
		t.Fatalf("service layout = index %d, count %d; want 5, 6", model.servicesIndex(), model.sectionCount())
	}
	model = applyKey(t, model, runeKey("6"))
	if model.section != model.servicesIndex() {
		t.Fatalf("numeric jump to services = %d; want %d", model.section, model.servicesIndex())
	}
	redisIndex := -1
	for i, id := range model.serviceIDs {
		if id == "redis" {
			redisIndex = i
			break
		}
	}
	if redisIndex < 0 {
		t.Fatal("redis is missing from the service catalog")
	}
	for i := 0; i < redisIndex; i++ {
		model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if len(model.launch.Services) != 0 {
		t.Fatalf("services after toggling redis off = %v; want empty", model.launch.Services)
	}

	model.cursor = 0
	mongoIndex := -1
	for i, id := range model.serviceIDs {
		if id == "mongo" {
			mongoIndex = i
			break
		}
	}
	if mongoIndex < 0 {
		t.Fatal("mongo is missing from the service catalog")
	}
	for i := 0; i < mongoIndex; i++ {
		model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyDown})
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if len(model.launch.Services) != 1 || model.launch.Services[0] != "mongo" {
		t.Fatalf("services after toggling mongo on = %v; want [mongo]", model.launch.Services)
	}
	if !strings.Contains(model.status, "Service toggled") {
		t.Fatalf("status = %q; want service toggle feedback", model.status)
	}
}

func TestServicePortEditorUpdatesOverrideAndPreview(t *testing.T) {
	stubWindows(t, false)
	project := t.TempDir()
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		Agent:       config.Agent{Command: "custom-cli"},
		UseDocker:   true,
		ProjectDir:  project,
		Services:    []string{"wiremock"},
		Permissions: map[string]bool{},
		Docker: container.RunConfig{
			ProjectDir:      project,
			AgentExecutable: "custom-cli",
			Selection:       container.Selection{Agents: []container.AgentInstall{{Command: "custom-cli", Version: "1.0.0", Kind: container.InstallRelease}}},
		},
	})
	model.section = model.servicesIndex()
	for index, id := range model.serviceIDs {
		if id == "wiremock" {
			model.cursor = index
			break
		}
	}
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.textInputActive || model.textInputValue != "8080:8080" {
		t.Fatalf("service port editor = active:%v value:%q", model.textInputActive, model.textInputValue)
	}
	model.textInputValue = "18080:8080"
	model.commitTextInput()
	if got := model.launch.ContainerServicePorts["wiremock"]; len(got) != 1 || got[0].Host != 18080 {
		t.Fatalf("service port override = %#v", got)
	}
	if !strings.Contains(model.View(), "18080:8080") {
		t.Fatalf("view does not show overridden port:\n%s", model.View())
	}
}

func TestLoadingServiceProfileRestoresSelection(t *testing.T) {
	stubWindows(t, false)
	original := detectContainerRuntime
	detectContainerRuntime = func(string) (container.Runtime, error) { return container.DockerRuntime{}, nil }
	t.Cleanup(func() { detectContainerRuntime = original })
	global := config.DefaultGlobal()
	global.Profiles = map[string]config.Profile{
		"infra": {Agent: "claude", Options: &config.Options{Docker: true, Services: []string{"redis", "mongo"}}},
	}
	model := NewModel(global, launcher.LaunchConfig{UseDocker: true, Permissions: map[string]bool{}})
	model.loadProfile("infra")
	if got := model.launch.Services; len(got) != 2 || got[0] != "redis" || got[1] != "mongo" {
		t.Fatalf("profile services = %v; want [redis mongo]", got)
	}
}

func TestProfilePersistsAllContainerFields(t *testing.T) {
	stubWindows(t, false)
	original := detectContainerRuntime
	detectContainerRuntime = func(string) (container.Runtime, error) { return container.PodmanRuntime{}, nil }
	t.Cleanup(func() { detectContainerRuntime = original })
	global := config.DefaultGlobal()
	launch := launcher.LaunchConfig{
		Agent:                 config.Agent{Command: "claude"},
		UseDocker:             true,
		UseMemory:             true,
		Fresh:                 true,
		ContainerRuntime:      "podman",
		ContainerEnvironment:  map[string]string{"POSTGRES_URL": "postgres://custom/db"},
		ContainerServicePorts: map[string][]config.PortMapping{"wiremock": {{Host: 18080, Internal: 8080}}},
		Permissions:           map[string]bool{},
		Services:              []string{"postgres", "redis"},
		Docker: container.RunConfig{
			Runtime:      container.PodmanRuntime{},
			MemoryLimit:  "4g",
			CPULimit:     "2.0",
			PIDsLimit:    512,
			ExposedPorts: []container.PortMapping{{Host: 3000, Internal: 3000}},
			NetworkName:  "dev-net",
			Selection:    container.Selection{Stacks: []string{"go", "python"}},
		},
	}
	model := NewModel(global, launch)
	model.hooks.SaveProfile = func(string, launcher.LaunchConfig) error { return nil }
	model.saveProfileAs("all-container")
	profile, ok := model.profiles["all-container"]
	if !ok || profile.Options == nil {
		t.Fatalf("saved profile = %#v; want options", profile)
	}
	if got := profile.Options.Services; len(got) != 2 || got[0] != "postgres" || got[1] != "redis" {
		t.Fatalf("saved services = %#v", got)
	}
	if profile.Options.ContainerMemory != "4g" || profile.Options.ContainerCPUs != "2.0" || profile.Options.ContainerPIDs != 512 || profile.Options.ContainerNetwork != "dev-net" || profile.Options.ContainerRuntime != "podman" {
		t.Fatalf("saved resources/runtime = %#v", profile.Options)
	}
	if profile.Options.ContainerEnvironment["POSTGRES_URL"] != "postgres://custom/db" || !profile.Options.Fresh {
		t.Fatalf("saved environment/fresh = %#v", profile.Options)
	}
	if got := profile.Options.ContainerServicePorts["wiremock"]; len(got) != 1 || got[0].Host != 18080 {
		t.Fatalf("saved service port overrides = %#v", profile.Options.ContainerServicePorts)
	}

	global.Profiles = model.profiles
	reloaded := NewModel(global, launcher.LaunchConfig{Permissions: map[string]bool{}})
	reloaded.loadProfile("all-container")
	if !reloaded.launch.UseDocker || reloaded.launch.UseJail || reloaded.launch.ContainerRuntime != "podman" {
		t.Fatalf("reloaded backend = docker:%v jail:%v runtime:%q", reloaded.launch.UseDocker, reloaded.launch.UseJail, reloaded.launch.ContainerRuntime)
	}
	if len(reloaded.launch.Services) != 2 || reloaded.launch.Services[1] != "redis" || reloaded.launch.ContainerEnvironment["POSTGRES_URL"] != "postgres://custom/db" {
		t.Fatalf("reloaded services/environment = %#v/%#v", reloaded.launch.Services, reloaded.launch.ContainerEnvironment)
	}
	if got := reloaded.launch.ContainerServicePorts["wiremock"]; len(got) != 1 || got[0].Host != 18080 {
		t.Fatalf("reloaded service port overrides = %#v", reloaded.launch.ContainerServicePorts)
	}
	if reloaded.launch.Docker.MemoryLimit != "4g" || reloaded.launch.Docker.CPULimit != "2.0" || reloaded.launch.Docker.PIDsLimit != 512 || reloaded.launch.Docker.NetworkName != "dev-net" || len(reloaded.launch.Docker.ExposedPorts) != 1 {
		t.Fatalf("reloaded resources = %#v", reloaded.launch.Docker)
	}
}
