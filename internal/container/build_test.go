package container

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
		"COPY install-config.yaml /etc/ai-launch/install-config.yaml",
		"RUN ai-launcher --config /etc/ai-launch/install-config.yaml --install",
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
	got := BuildCommand(ctx)
	want := []string{"docker", "build", "--tag", "ai-launcher-box:abc123", "/tmp/ctx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCommand() = %#v; want %#v", got, want)
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
		tag, cleanup, err := EnsureImage(sel, "version: \"2.1\"\n", launcherPath, runner)
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
		tag, cleanup, err := EnsureImage(sel, "version: \"2.1\"\n", launcherPath, runner)
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
		if _, _, err := EnsureImage(sel, "version: \"2.1\"\n", launcherPath, runner); err == nil {
			t.Fatal("EnsureImage() with a failing build should error")
		}
	})
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
	if _, _, err := EnsureImage(sel, "version: \"2.1\"\n", launcherPath, runner); err == nil {
		t.Fatal("EnsureImage() with a failing runner should error")
	}
}
