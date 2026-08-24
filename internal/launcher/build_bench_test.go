package launcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// benchLaunchConfig returns a representative native+jail+memory launch: the
// default path every `ai-launcher <agent>` invocation builds.
func benchLaunchConfig() LaunchConfig {
	return LaunchConfig{
		Agent: config.Agent{
			Name:           "codex",
			Command:        "codex",
			SupportsMemory: true,
			CatalogCommand: "codex",
		},
		Executable:       "/usr/local/bin/codex",
		MemoryExecutable: "/usr/local/bin/ai-memory",
		HomeDir:          "/home/user",
		MemoryServerURL:  "http://127.0.0.1:8787",
		UseJail:          true,
		UseMemory:        true,
		JailExec:         true,
	}
}

func BenchmarkBuild(b *testing.B) {
	cfg := benchLaunchConfig()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Build(cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBuildRealPaths measures Build against binaries that actually exist
// behind symlink chains, so filepath.EvalSymlinks does real lstat work — the
// shape of a production launch.
func BenchmarkBuildRealPaths(b *testing.B) {
	root := b.TempDir()
	realBin := filepath.Join(root, "real", "tool")
	if err := os.MkdirAll(filepath.Dir(realBin), 0o750); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.MkdirAll(linkDir, 0o750); err != nil {
		b.Fatal(err)
	}
	execLink := filepath.Join(linkDir, "codex")
	memLink := filepath.Join(linkDir, "ai-memory")
	for _, link := range []string{execLink, memLink} {
		if err := os.Symlink(realBin, link); err != nil {
			b.Fatal(err)
		}
	}
	cfg := benchLaunchConfig()
	cfg.Executable = execLink
	cfg.MemoryExecutable = memLink
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Build(cfg); err != nil {
			b.Fatal(err)
		}
	}
}
