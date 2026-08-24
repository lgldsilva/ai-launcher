package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// benchCatalog builds a Global with n agents, each carrying two aliases, and
// installs a real executable for every agent command into a temp bin dir so
// exec.LookPath has to walk PATH for each candidate — the same work a real
// launch performs.
func benchCatalog(b *testing.B, n int) (Catalog, func()) {
	binDir := b.TempDir()
	agents := make([]config.Agent, 0, n)
	for i := range n {
		command := fmt.Sprintf("agent-%02d", i)
		if err := os.WriteFile(filepath.Join(binDir, command), []byte("#!/bin/sh\n"), 0o755); err != nil { // #nosec G306 -- executable stub must be runnable for exec.LookPath
			b.Fatal(err)
		}
		agents = append(agents, config.Agent{
			Name:    command,
			Command: command,
			Aliases: []string{command + "-alt1", command + "-alt2"},
		})
	}
	restorePath := os.Getenv("PATH")
	b.Cleanup(func() {
		if err := os.Setenv("PATH", restorePath); err != nil {
			b.Fatal(err)
		}
	})
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+restorePath); err != nil {
		b.Fatal(err)
	}
	return New(config.Global{Agents: agents}), func() {}
}

// BenchmarkResolveTargetAgent isolates the cost of resolving ONE known agent
// out of a full catalog — the exact work every CLI launch does (twice: once
// for the selection, once for the trust check).
func BenchmarkResolveTargetAgent(b *testing.B) {
	for _, size := range []int{5, 10, 20} {
		b.Run(fmt.Sprintf("agents=%d", size), func(b *testing.B) {
			cat, _ := benchCatalog(b, size)
			target := fmt.Sprintf("agent-%02d", size-1)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := cat.Resolve(target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAgentsFull is the listing path the TUI uses; it legitimately probes
// every agent. It is the reference point showing how much redundant probing
// Resolve does today.
func BenchmarkAgentsFull(b *testing.B) {
	cat, _ := benchCatalog(b, 10)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = cat.Agents()
	}
}
