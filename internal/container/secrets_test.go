package container

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func secretComposeFile() ComposeFile {
	file := NewComposeFile()
	file.Services["authentik"] = ComposeService{
		Image:       "ghcr.io/goauthentik/server:2025.4.1",
		Environment: map[string]string{"AUTHENTIK_SECRET_KEY": GeneratedSecretMarker},
	}
	return file
}

func materializedSecret(t *testing.T, project string) string {
	t.Helper()
	secrets, err := existingComposeSecrets(project)
	if err != nil {
		t.Fatalf("existingComposeSecrets() error = %v", err)
	}
	return secrets["authentik"]["AUTHENTIK_SECRET_KEY"]
}

func TestMaterializeComposeGeneratesStablePerProjectSecret(t *testing.T) {
	project := t.TempDir()
	if _, err := MaterializeCompose(project, secretComposeFile()); err != nil {
		t.Fatalf("MaterializeCompose() error = %v", err)
	}
	first := materializedSecret(t, project)
	if len(first) != 64 {
		t.Fatalf("secret length = %d; want 64 hex chars (%q)", len(first), first)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("secret is not hex: %v (%q)", err, first)
	}
	data, err := os.ReadFile(filepath.Join(project, ".ai-launcher", "docker-compose.yaml")) // #nosec G304 -- path is the materializer output under t.TempDir().
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(data), GeneratedSecretMarker) {
		t.Fatalf("materialized compose still contains the secret marker:\n%s", data)
	}
	// A fresh ComposeFile (marker again) over the same project must reuse the
	// value already on disk: regeneration is idempotent.
	if _, err := MaterializeCompose(project, secretComposeFile()); err != nil {
		t.Fatalf("MaterializeCompose() second run error = %v", err)
	}
	if second := materializedSecret(t, project); second != first {
		t.Fatalf("secret changed across runs: %q -> %q; want stable per project", first, second)
	}
}

func TestMaterializeComposeGeneratesDistinctSecretsPerProject(t *testing.T) {
	firstProject := t.TempDir()
	secondProject := t.TempDir()
	if _, err := MaterializeCompose(firstProject, secretComposeFile()); err != nil {
		t.Fatalf("MaterializeCompose() first project error = %v", err)
	}
	if _, err := MaterializeCompose(secondProject, secretComposeFile()); err != nil {
		t.Fatalf("MaterializeCompose() second project error = %v", err)
	}
	first, second := materializedSecret(t, firstProject), materializedSecret(t, secondProject)
	if first == "" || second == "" || first == second {
		t.Fatalf("secrets must be non-empty and distinct per project: %q vs %q", first, second)
	}
}

func TestResolveComposeSecretsReusesOperatorCustomization(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, ".ai-launcher")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	custom := "services:\n  authentik:\n    environment:\n      AUTHENTIK_SECRET_KEY: operator-managed-value\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte(custom), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	resolved, err := ResolveComposeSecrets(project, secretComposeFile())
	if err != nil {
		t.Fatalf("ResolveComposeSecrets() error = %v", err)
	}
	if got := resolved.Services["authentik"].Environment["AUTHENTIK_SECRET_KEY"]; got != "operator-managed-value" {
		t.Fatalf("secret = %q; want the operator-managed value reused", got)
	}
}

func TestResolveComposeSecretsIgnoresUnparsableExistingFile(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, ".ai-launcher")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte(":\n- not yaml"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	resolved, err := ResolveComposeSecrets(project, secretComposeFile())
	if err != nil {
		t.Fatalf("ResolveComposeSecrets() error = %v; want a fresh secret despite the broken file", err)
	}
	if got := resolved.Services["authentik"].Environment["AUTHENTIK_SECRET_KEY"]; len(got) != 64 {
		t.Fatalf("secret = %q; want a fresh 64-char hex secret", got)
	}
}

func TestResolveComposeSecretsWithoutMarkersIsNoop(t *testing.T) {
	file := NewComposeFile()
	file.Services["agent"] = ComposeService{Build: ".", Environment: map[string]string{"PLAIN": "value"}}
	resolved, err := ResolveComposeSecrets(t.TempDir(), file)
	if err != nil {
		t.Fatalf("ResolveComposeSecrets() error = %v", err)
	}
	if resolved.Services["agent"].Environment["PLAIN"] != "value" {
		t.Fatalf("environment = %#v; want untouched", resolved.Services["agent"].Environment)
	}
}

func TestResolveComposeSecretsDoesNotMutateInput(t *testing.T) {
	file := secretComposeFile()
	if _, err := ResolveComposeSecrets(t.TempDir(), file); err != nil {
		t.Fatalf("ResolveComposeSecrets() error = %v", err)
	}
	if got := file.Services["authentik"].Environment["AUTHENTIK_SECRET_KEY"]; got != GeneratedSecretMarker {
		t.Fatalf("input environment mutated to %q; want the marker preserved", got)
	}
}

func TestMaskComposeSecretsIsDeterministicAndPure(t *testing.T) {
	file := secretComposeFile()
	masked := MaskComposeSecrets(file)
	if got := masked.Services["authentik"].Environment["AUTHENTIK_SECRET_KEY"]; got != generatedSecretDisplay {
		t.Fatalf("masked secret = %q; want %q", got, generatedSecretDisplay)
	}
	if got := file.Services["authentik"].Environment["AUTHENTIK_SECRET_KEY"]; got != GeneratedSecretMarker {
		t.Fatalf("input environment mutated to %q; want the marker preserved", got)
	}
	first, err := RenderCompose(MaskComposeSecrets(secretComposeFile()))
	if err != nil {
		t.Fatalf("RenderCompose() error = %v", err)
	}
	second, err := RenderCompose(MaskComposeSecrets(secretComposeFile()))
	if err != nil {
		t.Fatalf("RenderCompose() second error = %v", err)
	}
	if first != second {
		t.Fatalf("masked preview is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
