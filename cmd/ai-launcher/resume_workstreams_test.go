package main

import (
	"strings"
	"testing"
)

func TestWorkstreamsForwardsToAiMemory(t *testing.T) {
	record := stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)

	stdout, _, err := runCapture(t, "--config", globalPath, "--workstreams",
		"--workspace", "acme", "--project", "billing", "--limit", "10", "--json")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stdout, "searched") {
		t.Errorf("stdout = %q; ai-memory's own output must reach the terminal", stdout)
	}
	got := readRecord(t, record)
	for _, want := range []string{
		"ARG workstreams",
		"ARG --workspace", "ARG acme",
		"ARG --project", "ARG billing",
		"ARG --limit", "ARG 10",
		"ARG --json",
		"ENV_URL http://memory.example.internal:49374",
		"ENV_TOKEN s3cr3t",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("record = %q; missing %q", got, want)
		}
	}
	if strings.Contains(got, "ai-jail") {
		t.Errorf("record = %q; workstreams must not be jail-wrapped", got)
	}
}

func TestResumeForwardsPickerFlags(t *testing.T) {
	record := stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)

	if _, _, err := runCapture(t, "--config", globalPath, "--resume",
		"--workspace", "acme", "--all", "--search", "auth", "--limit", "5",
		"--yolo", "--fresh"); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	got := readRecord(t, record)
	for _, want := range []string{
		"ARG resume",
		"ARG --workspace", "ARG acme",
		"ARG --all",
		"ARG --search", "ARG auth",
		"ARG --limit", "ARG 5",
		"ARG --yolo",
		"ARG --fresh",
		"ENV_URL http://memory.example.internal:49374",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("record = %q; missing %q", got, want)
		}
	}
	if strings.Contains(got, "ai-jail") {
		t.Errorf("record = %q; resume must not be jail-wrapped", got)
	}
}

func TestMemorySideCommandsAreMutuallyExclusive(t *testing.T) {
	globalPath := writeSearchGlobal(t)
	stubMemoryRecordingArgv(t)

	_, _, err := runCapture(t, "--config", globalPath, "--workstreams", "--resume")
	if err == nil {
		t.Fatal("run() = nil; --workstreams and --resume must not compose")
	}
	if !strings.Contains(err.Error(), "--workstreams") || !strings.Contains(err.Error(), "--resume") {
		t.Errorf("error = %v; want mutual-exclusion message", err)
	}
}

func TestResumeAllRequiresResume(t *testing.T) {
	globalPath := writeSearchGlobal(t)
	_, _, err := runCapture(t, "--config", globalPath, "--all")
	if err == nil {
		t.Fatal("run() = nil; --all alone must be refused")
	}
	if !strings.Contains(err.Error(), "--all") || !strings.Contains(err.Error(), "--resume") {
		t.Errorf("error = %v; want --all requires --resume", err)
	}
}

func TestResumeMissingAiMemorySuggestsInstall(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	globalPath := writeSearchGlobal(t)
	_, _, err := runCapture(t, "--config", globalPath, "--resume")
	if err == nil {
		t.Fatal("run() = nil; a missing ai-memory must be reported")
	}
	if !strings.Contains(err.Error(), "ai-memory") || !strings.Contains(err.Error(), "--install") {
		t.Errorf("error = %v; want install hint", err)
	}
}
