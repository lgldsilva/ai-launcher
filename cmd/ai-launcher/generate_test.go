package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateMaterializesContainerArtifactsWithoutLaunching(t *testing.T) {
	sourceDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_SRC_PATH", sourceDir)
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  docker: true\n  memory: true\n  yolo: true\n  param_values:\n    model: sonnet\n  extra_args: [--verbose]\n  stacks: [go]\n  services: [redis]\n")
	stdout, _, err := runCapture(t, "generate", "--config", globalPath, "--local-config", localPath,
		"--no-jail", "--docker-backend", "--yolo", "--param", "model=sonnet", "--args=--verbose")
	if err != nil {
		t.Fatalf("run(generate) error = %v", err)
	}
	if !strings.Contains(stdout, "generated .ai-launcher/Dockerfile") ||
		!strings.Contains(stdout, "generated .ai-launcher/install-config.yaml") {
		t.Fatalf("stdout = %q; want generated artifact messages", stdout)
	}
	dockerfile, err := os.ReadFile(filepath.Join(dir, ".ai-launcher", "Dockerfile")) // #nosec G304 -- dir is a test-owned temporary directory
	if err != nil {
		t.Fatalf("Dockerfile was not generated: %v", err)
	}
	for _, want := range []string{"FROM ", "# Stack: Go", "# Agent: custom-cli"} {
		if !strings.Contains(string(dockerfile), want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
	installConfig, err := os.ReadFile(filepath.Join(dir, ".ai-launcher", "install-config.yaml")) // #nosec G304 -- dir is a test-owned temporary directory
	if err != nil {
		t.Fatalf("install-config.yaml was not generated: %v", err)
	}
	if !strings.Contains(string(installConfig), "command: \"custom-cli\"") {
		t.Fatalf("install-config.yaml = %q; want custom-cli", installConfig)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ai-launcher", ".gitignore")); err != nil {
		t.Fatalf(".gitignore was not generated: %v", err)
	}
	compose, err := os.ReadFile(filepath.Join(dir, ".ai-launcher", "docker-compose.yaml")) // #nosec G304 -- dir is a test-owned temporary directory
	if err != nil {
		t.Fatalf("docker-compose.yaml was not generated: %v", err)
	}
	for _, want := range []string{"- ai-memory", "- run", "- opencode", "- --model", "- sonnet", "- --yolo", "- --verbose"} {
		if !strings.Contains(string(compose), want) {
			t.Errorf("docker-compose.yaml missing %q:\n%s", want, compose)
		}
	}
}

func TestSaveInContainerModeMaterializesArtifactsWithoutRuntime(t *testing.T) {
	sourceDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_SRC_PATH", sourceDir)
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	globalPath, _, _ := writeTestConfigs(t, "")
	stdout, _, err := runCapture(t, "--config", globalPath, "--agent", "custom-cli",
		"--no-jail", "--docker-backend", "--stack", "go", "--save")
	if err != nil {
		t.Fatalf("run(--save) error = %v", err)
	}
	if !strings.Contains(stdout, "generated .ai-launcher/Dockerfile") {
		t.Fatalf("stdout = %q; --save should materialize the Dockerfile", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ai-launcher", "config.yaml")); err != nil {
		t.Fatalf("config.yaml was not saved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ai-launcher", "Dockerfile")); err != nil {
		t.Fatalf("Dockerfile was not materialized: %v", err)
	}
}
