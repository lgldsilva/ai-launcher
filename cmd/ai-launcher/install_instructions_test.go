package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallInstructionsRequiresTargetOrPrint(t *testing.T) {
	globalPath := writeSearchGlobal(t)
	stubMemoryRecordingArgv(t)
	_, _, err := runCapture(t, "--config", globalPath, "--install-instructions")
	if err == nil {
		t.Fatal("run() = nil; --install-instructions without --target/--print must be refused")
	}
	if !strings.Contains(err.Error(), "--target") || !strings.Contains(err.Error(), "--print") {
		t.Errorf("error = %v; want refusal naming --target or --print", err)
	}
}

func TestInstallInstructionsForwardsToAiMemory(t *testing.T) {
	record := stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)
	target := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(target, []byte("# project\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runCapture(t, "--config", globalPath, "--install-instructions",
		"--target", target, "--no-skills", "--compact"); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	got := readRecord(t, record)
	for _, want := range []string{
		"ARG install-instructions",
		"ARG --target", "ARG " + target,
		"ARG --no-skills",
		"ARG --compact",
		"ENV_URL http://memory.example.internal:49374",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("record = %q; missing %q", got, want)
		}
	}
}

func TestInstallInstructionsPrintSkipsTarget(t *testing.T) {
	record := stubMemoryRecordingArgv(t)
	globalPath := writeSearchGlobal(t)
	if _, _, err := runCapture(t, "--config", globalPath, "--install-instructions", "--print"); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	got := readRecord(t, record)
	if !strings.Contains(got, "ARG install-instructions") || !strings.Contains(got, "ARG --print") {
		t.Errorf("record = %q; want install-instructions --print", got)
	}
	if strings.Contains(got, "ARG --target") {
		t.Errorf("record = %q; --print preview must not invent a target", got)
	}
}

func TestInstallInstructionsCannotCombineWithInstall(t *testing.T) {
	globalPath := writeSearchGlobal(t)
	stubMemoryRecordingArgv(t)
	_, _, err := runCapture(t, "--config", globalPath, "--install", "--install-instructions", "--print")
	if err == nil {
		t.Fatal("run() = nil; --install-instructions must not ride --install")
	}
	if !strings.Contains(err.Error(), "--install-instructions") || !strings.Contains(err.Error(), "--install") {
		t.Errorf("error = %v; want combination refusal", err)
	}
}

func TestInstallInstructionsMissingAiMemorySuggestsInstall(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	globalPath := writeSearchGlobal(t)
	_, _, err := runCapture(t, "--config", globalPath, "--install-instructions", "--print")
	if err == nil {
		t.Fatal("run() = nil; a missing ai-memory must be reported")
	}
	if !strings.Contains(err.Error(), "ai-memory") || !strings.Contains(err.Error(), "--install") {
		t.Errorf("error = %v; want install hint", err)
	}
}
