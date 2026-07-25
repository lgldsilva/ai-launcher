package installer

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveNameMatchesAcceptsWindowsExeSuffix(t *testing.T) {
	if !archiveNameMatches("ai-memory.exe", "ai-memory") {
		t.Fatal("archiveNameMatches must accept the .exe suffix of Windows assets")
	}
	if !archiveNameMatches("dir/ai-memory", "ai-memory") {
		t.Fatal("archiveNameMatches must accept a nested path")
	}
	if archiveNameMatches("other.exe", "ai-memory") {
		t.Fatal("archiveNameMatches accepted an unrelated binary")
	}
}

func TestTargetPathAppendsExeOnWindows(t *testing.T) {
	installer := &Installer{HomeDir: t.TempDir(), GOOS: "windows", GOARCH: "amd64"}
	target, err := installer.targetPath("", "ai-memory")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(target, filepath.Join("bin", "ai-memory.exe")) {
		t.Fatalf("windows target = %q; want an .exe path under .local/bin", target)
	}
	installer.GOOS = "linux"
	target, err = installer.targetPath("", "ai-memory")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(target, ".exe") {
		t.Fatalf("linux target = %q; want no .exe suffix", target)
	}
}
