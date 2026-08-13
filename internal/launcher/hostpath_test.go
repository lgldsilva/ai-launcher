package launcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestExecutableSymlinkMapsTargetDirectory(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "downloads")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(realDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(realDir, "grok")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil { // #nosec G306 -- fixture must be executable like the real grok target
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "grok")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	got, err := Build(LaunchConfig{
		Agent:      config.Agent{Command: "grok"},
		Executable: link,
		UseJail:    true,
		UseMemory:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--map "+realDir) {
		t.Fatalf("must map the symlink target dir, got %s", joined)
	}
	if !strings.Contains(joined, "--executable "+real) {
		t.Fatalf("must pass the resolved binary, got %s", joined)
	}
}

func TestMemoryExecutableDirectoryIsMapped(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:            config.Agent{Command: "claude"},
		UseJail:          true,
		UseMemory:        true,
		MemoryExecutable: "/opt/mem/bin/ai-memory",
		Executable:       "/opt/agents/claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ai-jail", "--no-docker",
		"--map", "/opt/mem/bin",
		"--map", "/opt/agents",
		"/opt/mem/bin/ai-memory", "run", "claude",
		"--executable", "/opt/agents/claude",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

func TestContinueUsesResolvedMemoryExecutable(t *testing.T) {
	got, err := Build(LaunchConfig{
		ContinueSession:  true,
		UseJail:          true,
		UseMemory:        true,
		MemoryExecutable: "/opt/mem/bin/ai-memory",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ai-jail", "--no-docker",
		"--map", "/opt/mem/bin",
		"/opt/mem/bin/ai-memory", "run",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

func TestResolveHostBinariesFillsMemoryAfterJailToggle(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "ai-memory")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil { // #nosec G306 -- PATH stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	empty := ResolveHostBinaries(LaunchConfig{UseJail: false, UseMemory: true})
	if empty.MemoryExecutable != "" {
		t.Fatalf("memory path filled without jail: %q", empty.MemoryExecutable)
	}

	got := ResolveHostBinaries(LaunchConfig{UseJail: true, UseMemory: true})
	if got.MemoryExecutable != stub {
		t.Fatalf("MemoryExecutable = %q; want %q", got.MemoryExecutable, stub)
	}
}

func TestLiveJailRunsGrokHelp(t *testing.T) {
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" || os.Getenv("MUTATION_BASE") != "" {
		t.Skip("live jail exec is host-only")
	}
	requireRealCLI(t, "ai-jail", "ai-jail")
	path := requireRealCLI(t, "grok", "grok")
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	argv, err := Build(LaunchConfig{
		Agent:      config.Agent{Command: "grok"},
		Executable: resolved,
		UseJail:    true,
		JailExec:   true,
		ExtraArgs:  []string{"--help"},
	})
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv is produced by Build from a test-owned LaunchConfig
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home)
	done := make(chan error, 1)
	var out []byte
	go func() {
		out, err = cmd.CombinedOutput()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("live jail grok --help: %v\nargv=%s\n%s", err, strings.Join(argv, " "), out)
		}
	case <-time.After(25 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		t.Fatal("timed out")
	}
	if !strings.Contains(string(out), "Usage:") && !strings.Contains(string(out), "grok") {
		t.Fatalf("unexpected grok help:\n%s", out)
	}
}

// requireRealCLI skips when name is missing or is a mutation stub (exit 0).
func requireRealCLI(t *testing.T, name, needle string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skip(name + " not installed")
	}
	if info, err := os.Stat(path); err == nil && info.Size() < 64 {
		data, readErr := os.ReadFile(path) // #nosec G304 -- LookPath result for a fixed tool name
		if readErr == nil && strings.Contains(string(data), "exit 0") {
			t.Skip(name + " is a test stub")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--help").CombinedOutput() // #nosec G204 -- path is LookPath of a fixed tool name
	if err != nil || !strings.Contains(strings.ToLower(string(out)), strings.ToLower(needle)) {
		t.Skip(name + " is not the real CLI")
	}
	return path
}
