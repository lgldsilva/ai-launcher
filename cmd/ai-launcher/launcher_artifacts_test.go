package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLauncherSourceRootOnlyAcceptsOwnModule(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested", "project")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/lgldsilva/ai-launcher\n"), 0o600); err != nil { // #nosec G306 -- test fixture is private.
		t.Fatal(err)
	}
	if got := launcherSourceRoot(nested); got != root {
		t.Fatalf("launcherSourceRoot(own module) = %q; want %q", got, root)
	}

	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "go.mod"), []byte("module example.com/foreign\n"), 0o600); err != nil { // #nosec G306 -- test fixture is private.
		t.Fatal(err)
	}
	if got := launcherSourceRoot(foreign); got != "" {
		t.Fatalf("launcherSourceRoot(foreign module) = %q; want empty", got)
	}
}

func TestBuildLauncherLinuxFailsClearlyWithoutGoToolchain(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/lgldsilva/ai-launcher\n"), 0o600); err != nil { // #nosec G306 -- test fixture is private.
		t.Fatal(err)
	}
	t.Setenv("GO_SRC_PATH", root)
	t.Setenv("PATH", t.TempDir())
	var out, errOut bytes.Buffer
	if _, err := buildLauncherLinux(&out, &errOut); err == nil || !strings.Contains(err.Error(), "go toolchain not found in PATH") {
		t.Fatalf("buildLauncherLinux() error = %v; want a clear go-toolchain-not-found failure", err)
	}
}

func TestEmbeddedLauncherSourceRootFindsTheCheckoutForDevelopmentBuilds(t *testing.T) {
	root := embeddedLauncherSourceRoot()
	if root == "" {
		t.Fatal("embeddedLauncherSourceRoot() = empty; want the current checkout in a source test")
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("embedded source root %q has no go.mod: %v", root, err)
	}
}

func TestModuleDirective(t *testing.T) {
	if got := moduleDirective([]byte("// comment\nmodule github.com/lgldsilva/ai-launcher\n\ngo 1.24\n")); got != "github.com/lgldsilva/ai-launcher" {
		t.Fatalf("moduleDirective() = %q", got)
	}
	if got := moduleDirective([]byte("go 1.24\n")); got != "" {
		t.Fatalf("moduleDirective(missing) = %q; want empty", got)
	}
}

func TestExtractLauncherArchive(t *testing.T) {
	const payload = "linux launcher payload"
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "bin/ai-launcher", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tarWriter, payload); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	path, err := extractLauncherArchive(archive.Bytes())
	if err != nil {
		t.Fatalf("extractLauncherArchive() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	data, err := os.ReadFile(path) // #nosec G304 -- path was returned by the archive extractor.
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != payload {
		t.Fatalf("extracted payload = %q; want %q", data, payload)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("extracted mode = %o; want 755", info.Mode().Perm())
	}
}

func TestReleaseChecksum(t *testing.T) {
	valid := strings.Repeat("a", 64)
	if got, ok := releaseChecksum(valid+"  ai-launcher_1_linux_arm64.tar.gz\n", "ai-launcher_1_linux_arm64.tar.gz"); !ok || got != valid {
		t.Fatalf("releaseChecksum(valid) = %q, %t", got, ok)
	}
	for _, checksums := range []string{
		"not-a-hash  ai-launcher.tar.gz",
		valid + "  another.tar.gz",
		valid[:63] + "  ai-launcher.tar.gz",
	} {
		if got, ok := releaseChecksum(checksums, "ai-launcher.tar.gz"); ok || got != "" {
			t.Fatalf("releaseChecksum(%q) = %q, %t; want no match", checksums, got, ok)
		}
	}
}

func TestRegularArtifactExistsRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".ai-launcher")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := regularArtifactExists(filepath.Join(link, "docker-compose.yaml")); err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("regularArtifactExists() error = %v; want symlink rejection", err)
	}
}

// The hash is what makes the launcher part of the image identity, so it has to
// be stable for one build and shaped like a tag component.
func TestLauncherExecutableHashIsStableAndShort(t *testing.T) {
	hash := launcherExecutableHash()
	if len(hash) != 12 {
		t.Fatalf("launcherExecutableHash() = %q; want 12 hex characters", hash)
	}
	for _, r := range hash {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("launcherExecutableHash() = %q; want lowercase hex", hash)
		}
	}
	if again := launcherExecutableHash(); again != hash {
		t.Fatalf("launcherExecutableHash() = %q then %q; the same binary must hash the same", hash, again)
	}
}
