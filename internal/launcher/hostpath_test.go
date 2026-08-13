package launcher

import (
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
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(realDir, "grok")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
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

func TestLiveJailRunsGrokHelp(t *testing.T) {
	if _, err := exec.LookPath("ai-jail"); err != nil {
		t.Skip("ai-jail not installed")
	}
	path, err := exec.LookPath("grok")
	if err != nil {
		t.Skip("grok not installed")
	}
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
	cmd := exec.Command(argv[0], argv[1:]...)
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
