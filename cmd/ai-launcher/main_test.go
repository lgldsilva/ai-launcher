package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// writeTestConfigs creates a global config with a custom harness and two
// profiles plus a local config, returning their paths.
func writeTestConfigs(t *testing.T, localYAML string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	globalYAML := `agents:
  - name: Custom
    command: custom-cli
    yolo_flag: --custom-yolo
    params:
      - name: model
        flag: --model
        takes_value: true
permissions:
  - id: jail
    name: Jail
    default: true
profiles:
  review:
    agent: custom-cli
    options:
      jail: false
      memory: false
      param_values:
        model: v1
  minimal:
    agent: custom-cli
`
	globalPath := filepath.Join(dir, "global.yaml")
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(localPath, []byte(localYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return globalPath, localPath
}

func runDryRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(args, strings.NewReader(""), &out, &errOut)
	return out.String(), err
}

func TestProfileFlagLayersOverLocalConfig(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: other-cli\noptions:\n  jail: false\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--profile", "review", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(out, "custom-cli --model v1") {
		t.Fatalf("dry-run = %q; want profile agent and param applied over local config", out)
	}
}

func TestExplicitFlagsOverrideProfile(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: other-cli\noptions:\n  jail: false\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath,
		"--profile", "review", "--agent", "codex", "--param", "model=v2", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(out, "custom-cli") || !strings.Contains(out, "codex") {
		t.Fatalf("dry-run = %q; want --agent to override the profile agent", out)
	}
	if strings.Contains(out, "--model v1") {
		t.Fatalf("dry-run = %q; want --param to override the profile param value", out)
	}
}

func TestProfileWithoutOptionsKeepsLocalOptions(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: other-cli\noptions:\n  jail: false\n  memory: true\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--profile", "minimal", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.HasPrefix(out, "ai-memory run custom-cli") {
		t.Fatalf("dry-run = %q; want local memory option retained by a profile without options", out)
	}
}

func TestSaveProfilePersistsMergedSelectionWithoutLaunching(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  memory: false\n")
	out, err := runDryRun(t, "--config", globalPath, "--local-config", localPath,
		"--save-profile", "saved", "--param", "model=v9", "--yolo")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(out, `profile "saved" saved`) {
		t.Fatalf("output = %q; want save confirmation", out)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := global.Profiles["saved"]
	if !ok {
		t.Fatalf("profiles = %#v; want saved", config.ProfileNames(global))
	}
	if profile.Agent != "custom-cli" || profile.Options == nil || !profile.Options.Yolo || profile.Options.ParamValues["model"] != "v9" {
		t.Fatalf("saved profile = %#v", profile)
	}
}

func TestListAndDeleteProfiles(t *testing.T) {
	globalPath, _ := writeTestConfigs(t, "agent: custom-cli\n")
	out, err := runDryRun(t, "--config", globalPath, "--list-profiles")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(out, "minimal") || !strings.Contains(out, "review") || !strings.Contains(out, "agent: custom-cli") {
		t.Fatalf("list output = %q", out)
	}

	out, err = runDryRun(t, "--config", globalPath, "--delete-profile", "review")
	if err != nil || !strings.Contains(out, `profile "review" deleted`) {
		t.Fatalf("delete output = %q, err = %v", out, err)
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if names := config.ProfileNames(global); len(names) != 1 || names[0] != "minimal" {
		t.Fatalf("profiles after delete = %#v", names)
	}
	if _, err = runDryRun(t, "--config", globalPath, "--delete-profile", "review"); err == nil {
		t.Fatal("deleting a missing profile = nil; want error")
	}
}

func TestListProfilesWithNoneSaved(t *testing.T) {
	globalPath, _ := writeTestConfigs(t, "agent: custom-cli\n")
	if _, err := runDryRun(t, "--config", globalPath, "--delete-profile", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := runDryRun(t, "--config", globalPath, "--delete-profile", "minimal"); err != nil {
		t.Fatal(err)
	}
	out, err := runDryRun(t, "--config", globalPath, "--list-profiles")
	if err != nil || !strings.Contains(out, "no profiles saved") {
		t.Fatalf("empty list output = %q, err = %v", out, err)
	}
}

func TestUnknownProfileReturnsError(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: custom-cli\n")
	if _, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--profile", "nope", "--dry-run"); err == nil || !strings.Contains(err.Error(), `profile "nope" not found`) {
		t.Fatalf("run() error = %v; want unknown profile error", err)
	}
}

func TestInvalidParamFlagReturnsError(t *testing.T) {
	globalPath, localPath := writeTestConfigs(t, "agent: custom-cli\n")
	if _, err := runDryRun(t, "--config", globalPath, "--local-config", localPath, "--param", "novalue", "--dry-run"); err == nil || !strings.Contains(err.Error(), "expected name=value") {
		t.Fatalf("run() error = %v; want invalid --param error", err)
	}
}
