package container

import (
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestImageTag(t *testing.T) {
	selA, err := Normalize(
		[]string{"go", "python"},
		[]AgentInstall{
			{Command: "claude", Kind: InstallRelease, Version: "2.1.0"},
			{Command: "muse", Kind: InstallScript, Script: "curl x | bash"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	// Same selection in different tick order must hash identically.
	selB, err := Normalize(
		[]string{"python", "go"},
		[]AgentInstall{
			{Command: "muse", Kind: InstallScript, Script: "curl x | bash"},
			{Command: "claude", Kind: InstallRelease, Version: "2.1.0"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	tagA, err := ImageTag(selA)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	tagB, err := ImageTag(selB)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	if tagA != tagB {
		t.Fatalf("same selection produced different tags: %q vs %q", tagA, tagB)
	}
	if !strings.HasPrefix(tagA, "ai-launcher-box:") || len(tagA) != len("ai-launcher-box:")+12 {
		t.Fatalf("tag %q must be ai-launcher-box:<12 hex>", tagA)
	}

	// A version bump must change the tag (C2: no lying cache hits).
	selBumped, err := Normalize(
		[]string{"go", "python"},
		[]AgentInstall{
			{Command: "claude", Kind: InstallRelease, Version: "2.2.0"},
			{Command: "muse", Kind: InstallScript, Script: "curl x | bash"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	tagBumped, err := ImageTag(selBumped)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	if tagBumped == tagA {
		t.Fatal("version bump must produce a different tag")
	}

	// Stack change must change the tag.
	selNoPython, err := Normalize(
		[]string{"go"},
		[]AgentInstall{
			{Command: "claude", Kind: InstallRelease, Version: "2.1.0"},
			{Command: "muse", Kind: InstallScript, Script: "curl x | bash"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	tagNoPython, err := ImageTag(selNoPython)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	if tagNoPython == tagA {
		t.Fatal("stack change must produce a different tag")
	}
}

func TestImageTagErrors(t *testing.T) {
	if _, err := ImageTag(Selection{Stacks: []string{"cobol"}}); err == nil {
		t.Fatal("ImageTag with invalid selection should error")
	}
}

func TestImageTagDevProfileFlag(t *testing.T) {
	agents := []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}
	withDev, err := Normalize([]string{"go"}, agents, boolPtr(true))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	withoutDev, err := Normalize([]string{"go"}, agents, boolPtr(false))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	tagWith, _ := ImageTag(withDev)
	tagWithout, _ := ImageTag(withoutDev)
	if tagWith == tagWithout {
		t.Fatal("dev-profile flag must be part of the tag")
	}
}

func TestImageTagHostBinaryPath(t *testing.T) {
	agents := []AgentInstall{{Command: "kiro-cli", Kind: InstallHostBinary, HostPath: "/a/bin"}}
	selA, err := Normalize([]string{"go"}, agents, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	agentsB := []AgentInstall{{Command: "kiro-cli", Kind: InstallHostBinary, HostPath: "/b/bin"}}
	selB, err := Normalize([]string{"go"}, agentsB, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	tagA, _ := ImageTag(selA)
	tagB, _ := ImageTag(selB)
	if tagA == tagB {
		t.Fatal("different host paths must produce different tags")
	}
}

// H1: a changed install script or npm package must change the tag (the image
// content changed, so the cache must not lie).
func TestImageTagChangesWithScript(t *testing.T) {
	agentsA := []AgentInstall{{Command: "muse", Kind: InstallScript, Script: "curl -fsSL https://a.dev/install.sh | bash"}}
	agentsB := []AgentInstall{{Command: "muse", Kind: InstallScript, Script: "curl -fsSL https://b.dev/install.sh | bash"}}
	selA, err := Normalize([]string{"go"}, agentsA, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	selB, err := Normalize([]string{"go"}, agentsB, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	tagA, _ := ImageTag(selA)
	tagB, _ := ImageTag(selB)
	if tagA == tagB {
		t.Fatal("different install scripts must produce different tags")
	}
}

func TestImageTagChangesWithNpmPackage(t *testing.T) {
	agentsA := []AgentInstall{{Command: "gemini", Kind: InstallNpm, NpmPackage: "@google/gemini-cli"}}
	agentsB := []AgentInstall{{Command: "gemini", Kind: InstallNpm, NpmPackage: "@custom/gemini"}}
	selA, err := Normalize([]string{"go"}, agentsA, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	selB, err := Normalize([]string{"go"}, agentsB, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	tagA, _ := ImageTag(selA)
	tagB, _ := ImageTag(selB)
	if tagA == tagB {
		t.Fatal("different npm packages must produce different tags")
	}
}

func TestImageTagChangesWithSetupFailureFlag(t *testing.T) {
	base := AgentInstall{Command: "devin", Kind: InstallScript, Script: "curl -fsSL https://x.dev/install.sh | bash"}
	selA, err := Normalize([]string{"go"}, []AgentInstall{base}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	withFlag := base
	withFlag.AllowSetupFailure = true
	selB, err := Normalize([]string{"go"}, []AgentInstall{withFlag}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	tagA, _ := ImageTag(selA)
	tagB, _ := ImageTag(selB)
	if tagA == tagB {
		t.Fatal("the setup-failure flag must be part of the tag")
	}
}

func TestImageTagChangesWithMemoryRequirement(t *testing.T) {
	plain := AgentInstall{Command: "pi", Kind: InstallScript, Script: "curl -fsSL https://pi.dev/install.sh | bash"}
	withMemory := plain
	withMemory.NeedsMemory = true
	selA, err := Normalize([]string{"go"}, []AgentInstall{plain}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	selB, err := Normalize([]string{"go"}, []AgentInstall{withMemory}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	tagA, _ := ImageTag(selA)
	tagB, _ := ImageTag(selB)
	if tagA == tagB {
		t.Fatal("memory requirement must produce a different image tag")
	}
}

// The Node prerequisite changes the Dockerfile (nvm layer), so it must be
// part of the tag just like the memory requirement.
func TestImageTagChangesWithNodeRequirement(t *testing.T) {
	plain := AgentInstall{Command: "pi", Kind: InstallScript, Script: "curl -fsSL https://pi.dev/install.sh | bash"}
	withNode := plain
	withNode.NeedsNode = true
	selA, err := Normalize([]string{"go"}, []AgentInstall{plain}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	selB, err := Normalize([]string{"go"}, []AgentInstall{withNode}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	tagA, _ := ImageTag(selA)
	tagB, _ := ImageTag(selB)
	if tagA == tagB {
		t.Fatal("node requirement must produce a different image tag")
	}
}

func TestImageTagChangesWithAuxiliaryTool(t *testing.T) {
	base, err := Normalize([]string{"go"}, []AgentInstall{{Command: "pi", Kind: InstallScript, Script: "curl pi | bash"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	withTool := base
	withTool.Tools = []ToolInstall{{
		Command: "semidx",
		Version: "0.44.9",
		Kind:    InstallRelease,
		Release: &config.GitHubRelease{Repository: "lgldsilva/semidx", Assets: map[string]string{"linux-amd64": "semidx_0.44.9_linux_amd64.tar.gz"}},
	}}
	baseTag, err := ImageTag(base)
	if err != nil {
		t.Fatalf("ImageTag(base) error = %v", err)
	}
	toolTag, err := ImageTag(withTool)
	if err != nil {
		t.Fatalf("ImageTag(withTool) error = %v", err)
	}
	if baseTag == toolTag {
		t.Fatal("adding an auxiliary tool must change the image tag")
	}
}

// A changed auxiliary-tool recipe (repository or asset set) changes the image
// contents, so it must change the tag — the tool-side of H1.
func TestImageTagChangesWithToolRecipe(t *testing.T) {
	base, err := Normalize([]string{"go"}, []AgentInstall{{Command: "pi", Kind: InstallScript, Script: "curl pi | bash"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	withTool := func(release *config.GitHubRelease) Selection {
		sel := base
		sel.Tools = []ToolInstall{{Command: "semidx", Version: "0.44.9", Kind: InstallRelease, Release: release}}
		return sel
	}
	tagFor := func(sel Selection) string {
		t.Helper()
		tag, err := ImageTag(sel)
		if err != nil {
			t.Fatalf("ImageTag() error = %v", err)
		}
		return tag
	}
	repoA := &config.GitHubRelease{Repository: "lgldsilva/semidx", Assets: map[string]string{"linux-amd64": "semidx_a.tar.gz"}}
	repoB := &config.GitHubRelease{Repository: "fork/semidx", Assets: map[string]string{"linux-amd64": "semidx_a.tar.gz"}}
	assetsExtra := &config.GitHubRelease{Repository: "lgldsilva/semidx", Assets: map[string]string{"linux-amd64": "semidx_a.tar.gz", "darwin-arm64": "semidx_b.tar.gz"}}
	if tagFor(withTool(repoA)) == tagFor(withTool(repoB)) {
		t.Fatal("different tool repositories must produce different tags")
	}
	if tagFor(withTool(repoA)) == tagFor(withTool(assetsExtra)) {
		t.Fatal("different tool asset sets must produce different tags")
	}
	// The same recipe content always hashes identically (asset keys are sorted
	// before hashing, so map iteration order cannot leak into the tag).
	again := &config.GitHubRelease{Repository: "lgldsilva/semidx", Assets: map[string]string{"linux-amd64": "semidx_a.tar.gz"}}
	if tagFor(withTool(repoA)) != tagFor(withTool(again)) {
		t.Fatal("identical tool recipes must produce identical tags")
	}
}

// The Dockerfile build options change the image contents, so they must change
// the tag: a same-selection image built without the docker CLI must not be
// reused for a launch that was granted the docker permission.
func TestImageTagWithOptionsChangesTag(t *testing.T) {
	sel, err := Normalize([]string{"go"}, []AgentInstall{{Command: "claude", Kind: InstallScript, Script: "https://example.test/install.sh"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	plain, err := ImageTagWithOptions(sel, DockerfileOptions{})
	if err != nil {
		t.Fatalf("ImageTagWithOptions() error = %v", err)
	}
	withCLI, err := ImageTagWithOptions(sel, DockerfileOptions{DockerCLI: true})
	if err != nil {
		t.Fatalf("ImageTagWithOptions(DockerCLI) error = %v", err)
	}
	if plain == withCLI {
		t.Fatal("the docker CLI option must change the image tag")
	}
	legacy, err := ImageTag(sel)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	if legacy != plain {
		t.Fatal("ImageTag must delegate to the zero options")
	}
}
