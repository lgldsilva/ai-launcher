package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// stubHangingMemory writes an ai-memory stub that never exits, so the caller's
// deadline is the only thing that can end the run.
func stubHangingMemory(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), aiMemoryCommand)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 600\n"), 0o700); err != nil { // #nosec G306 -- the stub must be executable
		t.Fatal(err)
	}
	return path
}

// wireMemory execs `ai-memory install-mcp` / `install-hooks` through
// exec.CommandContext, but every caller handed it context.Background(): an
// upstream that hangs — waiting on a prompt, on a dead server, on a stalled
// download — froze `ai-launcher --install` with no deadline and no output.
// The child is the launcher's own subprocess, so bounding it is the launcher's
// job.
func TestMemoryWiringGivesUpOnAHangingUpstream(t *testing.T) {
	previousTimeout, previousDelay := memoryWireTimeout, memoryWireCleanupDelay
	memoryWireTimeout = 300 * time.Millisecond
	// The stub's `sleep` is a grandchild that keeps the pipes open, which is
	// exactly the case WaitDelay covers; shortened so the test does not pay
	// the production cleanup window.
	memoryWireCleanupDelay = 300 * time.Millisecond
	t.Cleanup(func() {
		memoryWireTimeout, memoryWireCleanupDelay = previousTimeout, previousDelay
	})

	var out, errOut bytes.Buffer
	streams := installStreams{out: &out, errOut: &errOut, trace: discardTrace()}
	cfg := memoryWireConfig{memoryPath: stubHangingMemory(t), home: t.TempDir()}

	done := make(chan error, 1)
	go func() {
		done <- wireMemory(context.Background(), cfg, &config.MemoryIntegration{
			Client: "claude-code", InstallMCP: true,
		}, streams)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("wireMemory() = nil; a child that never exits must surface as an error")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("wireMemory() never returned; the ai-memory exec has no deadline")
	}
}

// The deadline must not fire on a child that simply does its job, or a slow
// but healthy install-hooks would start reporting phantom failures.
func TestMemoryWiringLetsAWellBehavedUpstreamFinish(t *testing.T) {
	path := filepath.Join(t.TempDir(), aiMemoryCommand)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- the stub must be executable
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	streams := installStreams{out: &out, errOut: &errOut, trace: discardTrace()}
	cfg := memoryWireConfig{memoryPath: path, home: t.TempDir()}

	if err := wireMemory(context.Background(), cfg, &config.MemoryIntegration{
		Client: "claude-code", InstallMCP: true,
	}, streams); err != nil {
		t.Fatalf("wireMemory() error = %v; a child that exits 0 must succeed", err)
	}
}
