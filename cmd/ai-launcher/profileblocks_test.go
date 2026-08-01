package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// profileBlocks has to name every block applyProfile can replace. A block that
// applyProfile takes over without a field here cannot be excused from the trust
// gate: localTrustFrom keeps reading the workspace file's value for it, and the
// launcher refuses the run over input a trusted profile already discarded.
//
// Structural rather than behavioural on purpose. The behavioural tests below
// only prove the blocks that exist today; this one fails the moment
// config.Profile grows a fifth block, which is when the mistake would be made.
func TestProfileBlocksCoversEveryApplyProfileBlock(t *testing.T) {
	blocks := reflect.TypeOf(profileBlocks{})
	declared := make(map[string]bool, blocks.NumField())
	for i := range blocks.NumField() {
		declared[blocks.Field(i).Name] = true
	}

	profile := reflect.TypeOf(config.Profile{})
	for i := range profile.NumField() {
		name := profile.Field(i).Name
		if !declared[unexport(name)] {
			t.Errorf("config.Profile.%s has no profileBlocks.%s: applyProfile can replace it, "+
				"so localTrustFrom must be able to stop attributing it to the workspace file",
				name, unexport(name))
		}
	}
	if got, want := blocks.NumField(), profile.NumField(); got != want {
		t.Errorf("profileBlocks has %d fields, config.Profile has %d; "+
			"the two are meant to mirror each other one-for-one", got, want)
	}
}

func unexport(name string) string {
	if name == "" {
		return name
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// writePermissionProfileConfigs builds a global config with a profile that owns
// a permissions block, and a workspace file that asks for a different one.
// Self-contained rather than an extension of writeProfileConfigs, whose fixture
// other tests assert against.
func writePermissionProfileConfigs(t *testing.T, localBody string) (globalPath, localPath string) {
	t.Helper()
	stubToolsOnPath(t, "custom-cli", "ai-jail", "ai-memory")
	dir := t.TempDir()
	globalYAML := `agents:
  - name: Custom
    command: custom-cli
    memory:
      run_harness: opencode
permissions:
  - id: jail
    name: Jail
    default: true
  - id: ssh
    name: SSH access
    requires: [jail]
  - id: docker
    name: Docker socket
    requires: [jail]
profiles:
  # Owns the permissions block: the operator's own snapshot, stored in the
  # trusted global config.
  sshprofile:
    agent: custom-cli
    permissions:
      ssh: true
  # No permissions block: the workspace file keeps ownership, so the trust
  # gate still has to police it.
  agentonly:
    agent: custom-cli
`
	globalPath = filepath.Join(dir, "global.yaml")
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	localPath = filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte(localBody), 0o600); err != nil {
		t.Fatal(err)
	}
	return globalPath, localPath
}

// A profile that declares permissions replaces the workspace file's map
// wholesale (applyProfile). The file's grant never reaches the argv, so
// refusing the launch over it blocks on input that was already discarded —
// the same defect that once made --profile unusable with `jail: false`.
func TestProfilePermissionsDiscardTheFilesGrantWithoutRefusing(t *testing.T) {
	globalPath, localPath := writePermissionProfileConfigs(t,
		"agent: custom-cli\npermissions:\n  docker: true\n")

	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--profile", "sshprofile", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; the profile replaced the permissions block, "+
			"so the file's docker: true never reaches the argv", err)
	}
	if !strings.Contains(stdout, "--ssh") {
		t.Errorf("stdout = %q; the profile's own ssh permission was lost", stdout)
	}
	// Token match, not substring: the argv always states the docker decision,
	// and "--no-docker" would satisfy a naive Contains(stdout, "docker").
	if slices.Contains(strings.Fields(stdout), "--docker") {
		t.Errorf("stdout = %q; the file's docker permission must not survive the profile", stdout)
	}
	if !slices.Contains(strings.Fields(stdout), "--no-docker") {
		t.Errorf("stdout = %q; the socket must be refused explicitly, not left to ai-jail's default", stdout)
	}
}

// The mirror image: fixing the leak must not turn the check into a no-op. With
// no permissions block in the profile the file still owns the map, and a
// repository granting itself the Docker socket is refused by name.
func TestFileOwnedPermissionsAreStillRefusedWithoutTheFlag(t *testing.T) {
	globalPath, localPath := writePermissionProfileConfigs(t,
		"agent: custom-cli\npermissions:\n  docker: true\n")

	_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--profile", "agentonly", "--dry-run")
	if err == nil {
		t.Fatal("run() = nil; a workspace file granting the Docker socket must be refused")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Errorf("error = %v; the refusal must name the permission it refused", err)
	}
}

// And the documented way through: the operator passing the flag themselves.
func TestFileOwnedPermissionIsAcceptedWhenTheOperatorPassesTheFlag(t *testing.T) {
	globalPath, localPath := writePermissionProfileConfigs(t,
		"agent: custom-cli\npermissions:\n  docker: true\n")

	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--profile", "agentonly", "--docker", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; --docker is the opt-in the refusal names", err)
	}
	if !slices.Contains(strings.Fields(stdout), "--docker") {
		t.Errorf("stdout = %q; the accepted permission must reach the argv", stdout)
	}
}
