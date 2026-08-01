package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeParamConfigs builds a catalog whose agent declares the two param shapes
// that matter: one that takes a value (model selection) and one that is a bare
// flag the file can switch on.
func writeParamConfigs(t *testing.T, localBody string) (globalPath, localPath string) {
	t.Helper()
	stubToolsOnPath(t, "custom-cli", "ai-jail", "ai-memory")
	dir := t.TempDir()
	globalYAML := `agents:
  - name: Custom
    command: custom-cli
    memory:
      run_harness: opencode
    params:
      - name: model
        flag: --model
        takes_value: true
      - name: unsafe
        flag: --dangerously-allow
        takes_value: false
permissions:
  - id: jail
    name: Jail
    default: true
profiles:
  withparams:
    agent: custom-cli
    options:
      jail: true
      memory: true
      param_values:
        model: opus
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

// param_values is narrower than extra_args — a value can only land behind a
// flag the catalog already declares — but "narrower" is not "inert". The file
// still chooses the model the agent runs as, and can switch on a
// takes_value: false flag by name. That is an operator decision, so it takes
// the same explicit opt-in as yolo, extra_args and jail_flags.
func TestFileSuppliedParamValuesAreRefusedWithoutTheFlag(t *testing.T) {
	globalPath, localPath := writeParamConfigs(t,
		"agent: custom-cli\noptions:\n  param_values:\n    model: opus\n")

	_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err == nil {
		t.Fatal("run() = nil; a workspace file choosing the model must be refused")
	}
	if !strings.Contains(err.Error(), "param_values") || !strings.Contains(err.Error(), "--param") {
		t.Errorf("error = %v; the refusal must name what it refused and the opt-in", err)
	}
}

// A bare flag the catalog declares with takes_value: false is the sharper case:
// the file is not passing data, it is switching a behaviour on by name.
func TestFileSuppliedBooleanParamIsRefusedToo(t *testing.T) {
	globalPath, localPath := writeParamConfigs(t,
		"agent: custom-cli\noptions:\n  param_values:\n    unsafe: \"true\"\n")

	_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
	if err == nil {
		t.Fatal("run() = nil; a workspace file switching on a catalog flag must be refused")
	}
	if !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("error = %v; the refusal must name the param", err)
	}
}

// The refusal names every param, in a stable order: map iteration is random in
// Go, and an error message that reorders itself between runs is not one an
// operator can grep for or a test can assert on.
func TestParamRefusalNamesEveryParamInAStableOrder(t *testing.T) {
	globalPath, localPath := writeParamConfigs(t,
		"agent: custom-cli\noptions:\n  param_values:\n    unsafe: \"true\"\n    model: opus\n")

	var first string
	for i := range 12 {
		_, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath, "--dry-run")
		if err == nil {
			t.Fatal("run() = nil; the params must be refused")
		}
		if i == 0 {
			first = err.Error()
			if !strings.Contains(first, "model, unsafe") {
				t.Fatalf("error = %v; want both params, sorted", err)
			}
			continue
		}
		if err.Error() != first {
			t.Fatalf("run %d error = %q; want the stable message %q", i, err, first)
		}
	}
}

// --param is the opt-in the refusal names. Passing one accepts the block: the
// operator has looked at it, which is the whole distinction this gate draws.
func TestParamValuesAreAcceptedWhenTheOperatorPassesParam(t *testing.T) {
	globalPath, localPath := writeParamConfigs(t,
		"agent: custom-cli\noptions:\n  param_values:\n    model: opus\n")

	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--param", "model=sonnet", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; --param is the documented opt-in", err)
	}
	if !strings.Contains(stdout, "--model sonnet") {
		t.Errorf("stdout = %q; the flag value must win over the file's", stdout)
	}
}

// A profile owning the options block owns param_values with it. The file's
// values never reach the argv, so there is nothing left to refuse — the same
// rule that applies to jail, yolo, extra_args and jail_flags.
func TestProfileOwnedParamValuesAreNotRefused(t *testing.T) {
	globalPath, localPath := writeParamConfigs(t,
		"agent: custom-cli\noptions:\n  param_values:\n    model: opus\n")

	stdout, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--profile", "withparams", "--dry-run")
	if err != nil {
		t.Fatalf("run() error = %v; the profile replaced the options block", err)
	}
	if !strings.Contains(stdout, "--model opus") {
		t.Errorf("stdout = %q; the profile's own param must reach the argv", stdout)
	}
}

// And the gate must not fire on a file that says nothing about params.
func TestNoParamValuesMeansNoRefusal(t *testing.T) {
	globalPath, localPath := writeParamConfigs(t, "agent: custom-cli\n")

	if _, _, err := runCapture(t, "--config", globalPath, "--local-config", localPath,
		"--dry-run"); err != nil {
		t.Fatalf("run() error = %v; a file with no param_values is not lowering anything", err)
	}
}
