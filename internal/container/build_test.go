package container

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func testBuildSelection(t *testing.T) Selection {
	t.Helper()
	sel, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{Command: "kilo", Kind: InstallRelease, Version: "1.0.0"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	return sel
}

func TestPrepareBuildContext(t *testing.T) {
	sel := testBuildSelection(t)
	launcherPath := filepath.Join(t.TempDir(), "ai-launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test stub, no real secrets
		t.Fatal(err)
	}
	installCfg := "version: \"2.1\"\nagents:\n  - name: Kilo\n    command: kilo\n"

	ctx, cleanup, err := PrepareBuildContext(sel, installCfg, launcherPath)
	if err != nil {
		t.Fatalf("PrepareBuildContext() error = %v", err)
	}
	defer cleanup()

	// The Dockerfile must contain the install steps for release agents.
	df, err := os.ReadFile(ctx.DockerfilePath)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	for _, want := range []string{
		"COPY ai-launcher /usr/local/bin/ai-launcher",
		"COPY --chmod=0644 install-config.yaml /etc/ai-launch/install-config.yaml",
		"RUN ai-launcher --config /etc/ai-launch/install-config.yaml --install --agent kilo",
	} {
		if !strings.Contains(string(df), want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
	// The install config and the launcher binary must be in the context.
	if _, err := os.Stat(ctx.InstallConfigPath); err != nil {
		t.Fatalf("install config missing: %v", err)
	}
	if _, err := os.Stat(ctx.LauncherBinaryPath); err != nil {
		t.Fatalf("launcher binary missing: %v", err)
	}
	// The tag is the content hash of the selection.
	if !strings.HasPrefix(ctx.ImageTag, "ai-launcher-box:") {
		t.Fatalf("ImageTag = %q", ctx.ImageTag)
	}
}

func TestMaterializeArtifacts(t *testing.T) {
	selection, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{Command: "muse", Kind: InstallScript, Script: "curl -fsSL https://example.test/muse.sh | bash"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	global := config.Global{Agents: []config.Agent{{Name: "Muse", Command: "muse", SourceURL: "https://example.test/muse.sh"}}}
	artifacts, err := MaterializeArtifacts(t.TempDir(), selection, global)
	if err != nil {
		t.Fatalf("MaterializeArtifacts() error = %v", err)
	}
	for _, path := range []string{artifacts.DockerfilePath, artifacts.InstallConfigPath, artifacts.GitignorePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("materialized artifact %s: %v", path, err)
		}
	}
	dockerfile, err := os.ReadFile(artifacts.DockerfilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM " + BaseImage, "# Stack: Go", "# Agent: muse"} {
		if !strings.Contains(string(dockerfile), want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
	installConfig, err := os.ReadFile(artifacts.InstallConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installConfig), "command: \"muse\"") {
		t.Errorf("install-config.yaml = %q; want the selected agent", installConfig)
	}
	gitignore, err := os.ReadFile(artifacts.GitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gitignore) != materializedGitignore {
		t.Fatalf(".gitignore = %q; want %q", gitignore, materializedGitignore)
	}
}

func TestMaterializeArtifactsRejectsDockerfileSymlink(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, ".ai-launcher")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(project, "outside-dockerfile")
	if err := os.WriteFile(target, []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	selection := Selection{
		Stacks:            []string{"go"},
		Agents:            []AgentInstall{{Command: "muse", Kind: InstallScript, Script: "curl x | bash"}},
		IncludeDevProfile: true,
	}
	global := config.Global{Agents: []config.Agent{{Name: "Muse", Command: "muse", SourceURL: "https://example.test/muse.sh"}}}
	if _, err := MaterializeArtifacts(project, selection, global); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("MaterializeArtifacts() error = %v; want Dockerfile symlink refusal", err)
	}
}

func TestMaterializeLauncherBinary(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(source, []byte("linux-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := MaterializeLauncherBinary(project, source)
	if err != nil {
		t.Fatalf("MaterializeLauncherBinary() error = %v", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path was returned by the materializer under t.TempDir().
	if err != nil || string(data) != "linux-binary" {
		t.Fatalf("materialized binary = %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("binary mode = %o; want 755", info.Mode().Perm())
	}
}

func TestPrepareBuildContextNoLauncherNeeded(t *testing.T) {
	// A script-only selection does not COPY the launcher binary.
	sel, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{Command: "muse", Kind: InstallScript, Script: "curl x | bash"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	ctx, cleanup, err := PrepareBuildContext(sel, "", "")
	if err != nil {
		t.Fatalf("PrepareBuildContext() error = %v", err)
	}
	defer cleanup()
	if ctx.LauncherBinaryPath != filepath.Join(ctx.Dir, "ai-launcher") {
		t.Fatalf("LauncherBinaryPath = %q", ctx.LauncherBinaryPath)
	}
	if _, err := os.Stat(ctx.LauncherBinaryPath); !os.IsNotExist(err) {
		t.Fatal("script-only selection must not require a launcher binary in the context")
	}
}

func TestPrepareBuildContextRequiresLauncherForMemory(t *testing.T) {
	sel, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{Command: "pi", Kind: InstallScript, Script: "curl x | bash", NeedsMemory: true}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if _, _, err := PrepareBuildContext(sel, "version: \"2.1\"\n", ""); err == nil {
		t.Fatal("memory selection without launcher should error")
	}
}

func TestPrepareBuildContextRequiresLauncherForRelease(t *testing.T) {
	sel := testBuildSelection(t)
	if _, _, err := PrepareBuildContext(sel, "version: \"2.1\"\n", ""); err == nil {
		t.Fatal("PrepareBuildContext() with a release agent and no launcher should error")
	}
}

func TestPrepareBuildContextInvalidSelection(t *testing.T) {
	if _, _, err := PrepareBuildContext(Selection{Stacks: []string{"cobol"}}, "", ""); err == nil {
		t.Fatal("PrepareBuildContext() with an invalid selection should error")
	}
}

func TestBuildCommand(t *testing.T) {
	ctx := &BuildContext{Dir: "/tmp/ctx", ImageTag: "ai-launcher-box:abc123"}
	got := BuildCommand(ctx, DockerRuntime{})
	want := []string{"docker", "build", "--tag", "ai-launcher-box:abc123", "/tmp/ctx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCommand() = %#v; want %#v", got, want)
	}
}

func TestBuildCommandPodman(t *testing.T) {
	ctx := &BuildContext{Dir: "/tmp/ctx", ImageTag: "ai-launcher-box:abc123"}
	got := BuildCommand(ctx, PodmanRuntime{})
	want := []string{"podman", "build", "--tag", "ai-launcher-box:abc123", "/tmp/ctx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCommand(Podman) = %#v; want %#v", got, want)
	}
}

func TestBuildCommandUsesDockerContext(t *testing.T) {
	ctx := &BuildContext{Dir: "/tmp/ctx", ImageTag: "ai-launcher-box:abc123"}
	got := BuildCommandWithContext(ctx, DockerRuntime{}, "remote-builder")
	want := []string{"docker", "--context", "remote-builder", "build", "--tag", "ai-launcher-box:abc123", "/tmp/ctx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCommandWithContext() = %#v; want %#v", got, want)
	}
}

func TestEnsureImage(t *testing.T) {
	sel := testBuildSelection(t)
	launcherPath := filepath.Join(t.TempDir(), "ai-launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\n"), 0o700); err != nil { // #nosec G306 -- test stub, no real secrets
		t.Fatal(err)
	}

	t.Run("image exists", func(t *testing.T) {
		runner := func(argv []string) (int, error) {
			if reflect.DeepEqual(argv, []string{"docker", "image", "inspect", mustTag(t, sel)}) {
				return 0, nil
			}
			return 1, errors.New("unexpected argv")
		}
		tag, cleanup, err := EnsureImage(DockerRuntime{}, sel, "version: \"2.1\"\n", launcherPath, runner)
		if err != nil {
			t.Fatalf("EnsureImage() error = %v", err)
		}
		cleanup()
		if tag != mustTag(t, sel) {
			t.Fatalf("EnsureImage() tag = %q", tag)
		}
	})

	t.Run("builds when missing", func(t *testing.T) {
		var built bool
		runner := func(argv []string) (int, error) {
			if argv[0] == "docker" && argv[1] == "build" {
				built = true
				return 0, nil
			}
			return 1, nil
		}
		tag, cleanup, err := EnsureImage(DockerRuntime{}, sel, "version: \"2.1\"\n", launcherPath, runner)
		if err != nil {
			t.Fatalf("EnsureImage() error = %v", err)
		}
		cleanup()
		if !built {
			t.Fatal("EnsureImage() did not build a missing image")
		}
		if tag != mustTag(t, sel) {
			t.Fatalf("EnsureImage() tag = %q", tag)
		}
	})

	t.Run("build failure", func(t *testing.T) {
		runner := func(argv []string) (int, error) {
			if argv[0] == "docker" && argv[1] == "build" {
				return 3, nil
			}
			return 1, nil
		}
		if _, _, err := EnsureImage(DockerRuntime{}, sel, "version: \"2.1\"\n", launcherPath, runner); err == nil {
			t.Fatal("EnsureImage() with a failing build should error")
		}
	})
}

func TestEnsureImageUsesRuntime(t *testing.T) {
	sel := testBuildSelection(t)
	launcherPath := filepath.Join(t.TempDir(), "ai-launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\n"), 0o700); err != nil { // #nosec G306 -- test stub, no real secrets
		t.Fatal(err)
	}
	var commands [][]string
	runner := func(argv []string) (int, error) {
		commands = append(commands, append([]string(nil), argv...))
		if len(commands) == 1 {
			return 1, nil
		}
		return 0, nil
	}
	_, cleanup, err := EnsureImage(PodmanRuntime{}, sel, "version: \"2.1\"\n", launcherPath, runner)
	if err != nil {
		t.Fatalf("EnsureImage(Podman) error = %v", err)
	}
	cleanup()
	if len(commands) != 2 || commands[0][0] != "podman" || commands[0][1] != "image" || commands[1][0] != "podman" || commands[1][1] != "build" {
		t.Fatalf("EnsureImage(Podman) commands = %#v; want podman inspect/build", commands)
	}
}

func mustTag(t *testing.T, sel Selection) string {
	t.Helper()
	tag, err := ImageTag(sel)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	return tag
}

// Cleanup must remove the whole context directory (not just the Dockerfile).
func TestPrepareBuildContextCleanupRemovesDir(t *testing.T) {
	sel := testBuildSelection(t)
	launcherPath := filepath.Join(t.TempDir(), "ai-launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\n"), 0o700); err != nil { // #nosec G306 -- test stub, no real secrets
		t.Fatal(err)
	}
	ctx, cleanup, err := PrepareBuildContext(sel, "version: \"2.1\"\n", launcherPath)
	if err != nil {
		t.Fatalf("PrepareBuildContext() error = %v", err)
	}
	dir := ctx.Dir
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove %s", dir)
	}
}

// An invalid selection must clean up its temp dir even when Dockerfile
// generation fails (no leaked context directories).
func TestPrepareBuildContextErrorCleansUp(t *testing.T) {
	dir, err := os.MkdirTemp("", "pre-cleanup-check-")
	if err != nil {
		t.Fatal(err)
	}
	_ = dir
	// Selection with no agents fails validation inside PrepareBuildContext.
	sel, err := Normalize([]string{"go"}, nil, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	_, cleanup, err := PrepareBuildContext(sel, "", "")
	if err == nil {
		t.Fatal("PrepareBuildContext() with no agents should error")
	}
	if cleanup != nil {
		cleanup()
	}
}

// EnsureImage surfaces a runner transport error (docker not on PATH).
func TestEnsureImageRunnerError(t *testing.T) {
	sel := testBuildSelection(t)
	launcherPath := filepath.Join(t.TempDir(), "ai-launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\n"), 0o700); err != nil { // #nosec G306 -- test stub, no real secrets
		t.Fatal(err)
	}
	runner := func(argv []string) (int, error) {
		return -1, errors.New("docker not found")
	}
	if _, _, err := EnsureImage(DockerRuntime{}, sel, "version: \"2.1\"\n", launcherPath, runner); err == nil {
		t.Fatal("EnsureImage() with a failing runner should error")
	}
}

func TestPrepareBuildContextWithOptionsDockerCLI(t *testing.T) {
	sel := testBuildSelection(t)
	launcherPath := filepath.Join(t.TempDir(), "ai-launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test stub, no real secrets
		t.Fatal(err)
	}
	ctx, cleanup, err := PrepareBuildContextWithOptions(sel, DockerfileOptions{DockerCLI: true}, "version: \"2.1\"\n", launcherPath)
	if err != nil {
		t.Fatalf("PrepareBuildContextWithOptions() error = %v", err)
	}
	defer cleanup()
	df, err := os.ReadFile(ctx.DockerfilePath)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !strings.Contains(string(df), "docker.io") {
		t.Errorf("Dockerfile with DockerCLI option missing docker.io:\n%s", df)
	}
}

func TestMaterializeArtifactsWithOptionsDockerCLI(t *testing.T) {
	selection, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{Command: "muse", Kind: InstallScript, Script: "curl -fsSL https://example.test/muse.sh | bash"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	global := config.Global{Agents: []config.Agent{{Name: "Muse", Command: "muse", SourceURL: "https://example.test/muse.sh"}}}
	artifacts, err := MaterializeArtifactsWithOptions(t.TempDir(), selection, DockerfileOptions{DockerCLI: true}, global)
	if err != nil {
		t.Fatalf("MaterializeArtifactsWithOptions() error = %v", err)
	}
	dockerfile, err := os.ReadFile(artifacts.DockerfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "docker.io") {
		t.Errorf("materialized Dockerfile with DockerCLI option missing docker.io:\n%s", dockerfile)
	}
}

func TestEnsureImageWithOptions(t *testing.T) {
	sel := testBuildSelection(t)
	launcherPath := filepath.Join(t.TempDir(), "ai-launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\n"), 0o700); err != nil { // #nosec G306 -- test stub, no real secrets
		t.Fatal(err)
	}
	var built bool
	runner := func(argv []string) (int, error) {
		if argv[0] == "docker" && argv[1] == "build" {
			built = true
			return 0, nil
		}
		return 1, nil
	}
	tag, cleanup, err := EnsureImageWithOptions(DockerRuntime{}, sel, DockerfileOptions{DockerCLI: true}, "version: \"2.1\"\n", launcherPath, runner)
	if err != nil {
		t.Fatalf("EnsureImageWithOptions() error = %v", err)
	}
	cleanup()
	if !built {
		t.Fatal("EnsureImageWithOptions() did not build a missing image")
	}
	wantTag, err := ImageTagWithOptions(sel, DockerfileOptions{DockerCLI: true})
	if err != nil {
		t.Fatalf("ImageTagWithOptions() error = %v", err)
	}
	if tag != wantTag {
		t.Fatalf("EnsureImageWithOptions() tag = %q, want %q", tag, wantTag)
	}
}

// An empty project directory falls back to the current working directory:
// the artifacts must land under cwd/.ai-launcher.
func TestMaterializeArtifactsResolvesEmptyProjectDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	selection, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{Command: "muse", Kind: InstallScript, Script: "curl -fsSL https://example.test/muse.sh | bash"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	global := config.Global{Agents: []config.Agent{{Name: "Muse", Command: "muse", SourceURL: "https://example.test/muse.sh"}}}
	artifacts, err := MaterializeArtifacts("  ", selection, global)
	if err != nil {
		t.Fatalf("MaterializeArtifacts() error = %v", err)
	}
	wantDir := filepath.Join(cwd, containerArtifactDirName)
	if artifacts.Dir != wantDir {
		t.Fatalf("MaterializeArtifacts(\"\") Dir = %q; want %q (cwd fallback)", artifacts.Dir, wantDir)
	}
	if _, err := os.Stat(artifacts.DockerfilePath); err != nil {
		t.Fatalf("Dockerfile not materialized under cwd: %v", err)
	}
}

// An empty project directory resolves to the current working directory here
// too, so the copied launcher lands under cwd/.ai-launcher.
func TestMaterializeLauncherBinaryResolvesEmptyProjectDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	source := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(source, []byte("linux-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := MaterializeLauncherBinary(" ", source)
	if err != nil {
		t.Fatalf("MaterializeLauncherBinary() error = %v", err)
	}
	want := filepath.Join(cwd, containerArtifactDirName, launcherBinaryName)
	if path != want {
		t.Fatalf("MaterializeLauncherBinary(\"\") path = %q; want %q (cwd fallback)", path, want)
	}
}

// An explicit project directory must win over the cwd fallback: the launcher
// is copied inside the given project even when the test runs elsewhere.
func TestMaterializeLauncherBinaryWritesInsideGivenProjectDir(t *testing.T) {
	t.Chdir(t.TempDir()) // elsewhere: the fallback must not trigger
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(source, []byte("linux-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := MaterializeLauncherBinary(project, source)
	if err != nil {
		t.Fatalf("MaterializeLauncherBinary() error = %v", err)
	}
	want := filepath.Join(project, containerArtifactDirName, launcherBinaryName)
	if path != want {
		t.Fatalf("MaterializeLauncherBinary(project) path = %q; want %q", path, want)
	}
}
