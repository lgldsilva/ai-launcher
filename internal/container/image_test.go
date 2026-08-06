package container

import (
	"strings"
	"testing"
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
