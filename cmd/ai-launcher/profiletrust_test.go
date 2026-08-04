package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProfileConfigs builds a global config whose profiles exercise the two
// ways a profile interacts with the trust boundary and the option defaults,
// plus an empty local config. It is deliberately separate from
// writeTestConfigs: the profile-listing tests there assert an exact profile
// set, so extending that fixture would break them for an unrelated reason.
func writeProfileConfigs(t *testing.T) (globalPath, localPath string) {
	t.Helper()
	stubToolsOnPath(t, "custom-cli", "ai-jail", "ai-memory")
	dir := t.TempDir()
	globalYAML := `agents:
  - name: Custom
    command: custom-cli
    supports_memory: true
    supports_yolo: true
    memory:
      run_harness: opencode
permissions:
  - id: jail
    name: Jail
    default: true
profiles:
  # The operator's own snapshot, stored in the trusted global config: turning
  # the sandbox off here is an operator decision, not a repository's.
  nojail:
    agent: custom-cli
    options:
      jail: false
      memory: false
  # A hand-written profile that declares an options block without naming jail
  # or memory. Omission must keep the safe defaults, exactly as it does for
  # .ai-launch.yaml (ARCHITECTURE invariant 6).
  partial:
    agent: custom-cli
    options:
      yolo: true
  # No options block at all: applyProfile leaves the workspace file's options
  # in charge, so the trust boundary still has to police them.
  agentonly:
    agent: custom-cli
`
	globalPath = filepath.Join(dir, "global.yaml")
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	localPath = filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte("agent: custom-cli\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return globalPath, localPath
}

// A profile lives in the global config, which ARCHITECTURE invariant 2b lists
// as trusted alongside flags. Deriving the trust snapshot after the profile has
// been layered onto the local selection made a trusted `jail: false` look like
// a repository file lowering the sandbox, so --profile was refused outright —
// and the error blamed .ai-launch.yaml, which had said nothing of the sort.
func TestProfileMayDisableTheJailWithoutBeingRefused(t *testing.T) {
	globalPath, localPath := writeProfileConfigs(t)
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--profile", "nojail", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; a profile is trusted input and may disable the jail", err)
	}
	if strings.Contains(stdout, "ai-jail") {
		t.Fatalf("stdout = %q; the profile asked for no jail", stdout)
	}
}

// The trust boundary still has to bite on what the workspace file itself says:
// fixing the profile leak must not turn enforceLocalConfigTrust into a no-op.
// The dividing line is whether the profile took the options block over —
// applyProfile replaces it wholesale, so a file value it replaced never reaches
// the argv and there is nothing left to refuse.
func TestLocalJailFalseIsJudgedByWhoStillOwnsTheOptions(t *testing.T) {
	const localJailOff = "agent: custom-cli\noptions:\n  jail: false\n  memory: false\n"

	t.Run("profile without options leaves the file in charge", func(t *testing.T) {
		globalPath, localPath := writeProfileConfigs(t)
		if err := os.WriteFile(localPath, []byte(localJailOff), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
			"--profile", "agentonly", "--dry-run"); err == nil {
			t.Fatal("run() = nil; a local config disabling the jail must still be refused")
		}
	})

	t.Run("profile with options discards the file's toggles", func(t *testing.T) {
		globalPath, localPath := writeProfileConfigs(t)
		if err := os.WriteFile(localPath, []byte(localJailOff), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
			"--profile", "partial", "--dry-run")
		if err != nil {
			t.Fatalf("run() error = %v; the profile replaced the options block, so nothing was downgraded", err)
		}
		if !strings.HasPrefix(stdout, "ai-jail ") {
			t.Fatalf("stdout = %q; the profile's defaulted jail must win over the file's jail: false", stdout)
		}
	})
}

// Options.UnmarshalYAML fills in the safe defaults for .ai-launch.yaml, but a
// profile's options block went straight to Go zero values: a profile that names
// only `yolo` silently launched with the sandbox and memory off. That is the
// defect ARCHITECTURE invariant 6 exists to prevent, reintroduced through the
// global config instead of the workspace file.
func TestProfileOmittingJailAndMemoryKeepsTheSafeDefaults(t *testing.T) {
	globalPath, localPath := writeProfileConfigs(t)
	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--profile", "partial", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.HasPrefix(stdout, "ai-jail ") {
		t.Fatalf("stdout = %q; an omitted options.jail must keep the sandbox on", stdout)
	}
	if !strings.Contains(stdout, "ai-memory run") {
		t.Fatalf("stdout = %q; an omitted options.memory must keep memory on", stdout)
	}
	if !strings.Contains(stdout, "--yolo") {
		t.Fatalf("stdout = %q; the profile's explicit yolo was lost", stdout)
	}
}
