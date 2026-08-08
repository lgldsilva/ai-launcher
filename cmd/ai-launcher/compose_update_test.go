package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

func plainArtifactsTestSelection() container.Selection {
	return container.Selection{
		Stacks: []string{"go"},
		Agents: []container.AgentInstall{{
			Command: "pi",
			Kind:    container.InstallScript,
			Script:  "curl -fsSL https://example.com/pi.sh | bash",
		}},
	}
}

func plainArtifactReviewByName(t *testing.T, reviews []artifactReview, name string) artifactReview {
	t.Helper()
	for _, review := range reviews {
		if review.Name == name {
			return review
		}
	}
	t.Fatalf("inspectPlainContainerArtifacts() returned no review for %s", name)
	return artifactReview{}
}

func materializePlainArtifacts(t *testing.T, dir string, selection container.Selection, global config.Global, choice composeUpdateChoice) []artifactReview {
	t.Helper()
	reviews, err := inspectPlainContainerArtifacts(dir, selection, container.DockerfileOptions{}, global)
	if err != nil {
		t.Fatalf("inspectPlainContainerArtifacts() error = %v", err)
	}
	for _, review := range reviews {
		if err := materializePlainArtifactIfNeeded(review, choice, &bytes.Buffer{}); err != nil {
			t.Fatalf("materializePlainArtifactIfNeeded(%s) error = %v", review.Name, err)
		}
	}
	return reviews
}

func TestDockerfileUpdateReviewPreservesManualChangesAndRemembersDecision(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	selection := plainArtifactsTestSelection()
	global := config.DefaultGlobal()

	reviews := materializePlainArtifacts(t, dir, selection, global, composeUpdateReplace)
	initial := plainArtifactReviewByName(t, reviews, "Dockerfile")
	generated := initial.Generated
	if generated == "" {
		t.Fatal("generated Dockerfile is empty")
	}

	// An unchanged re-generate is idempotent: no conflict, no diff.
	reviews, err := inspectPlainContainerArtifacts(dir, selection, container.DockerfileOptions{}, global)
	if err != nil {
		t.Fatal(err)
	}
	if review := plainArtifactReviewByName(t, reviews, "Dockerfile"); !review.Exists || review.Changed {
		t.Fatalf("unchanged Dockerfile review = %#v; want exists without changes", review)
	}

	manual := generated + "# local tweak\n"
	dockerfilePath := filepath.Join(dir, containerArtifactDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(manual), 0o600); err != nil { // #nosec G306 G703 -- private test fixture under t.TempDir().
		t.Fatal(err)
	}
	reviews, err = inspectPlainContainerArtifacts(dir, selection, container.DockerfileOptions{}, global)
	if err != nil {
		t.Fatal(err)
	}
	review := plainArtifactReviewByName(t, reviews, "Dockerfile")
	if !review.Changed || !strings.Contains(review.Diff, "-# local tweak") {
		t.Fatalf("manual Dockerfile review = %#v; want the tweak diff", review)
	}
	if err := materializePlainArtifactIfNeeded(review, composeUpdatePrompt, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "has local changes") || !strings.Contains(err.Error(), "-# local tweak") {
		t.Fatalf("prompt policy on manual Dockerfile error = %v; want the conflict with diff", err)
	}
	if err := materializePlainArtifactIfNeeded(review, composeUpdateKeep, &bytes.Buffer{}); err != nil {
		t.Fatalf("keep manual Dockerfile: %v", err)
	}
	kept, err := os.ReadFile(dockerfilePath) // #nosec G304 -- private test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != manual {
		t.Fatalf("kept Dockerfile = %q; want the manual tweak", kept)
	}
	// The approved pair no longer conflicts on the next review.
	reviews, err = inspectPlainContainerArtifacts(dir, selection, container.DockerfileOptions{}, global)
	if err != nil {
		t.Fatal(err)
	}
	review = plainArtifactReviewByName(t, reviews, "Dockerfile")
	if review.Changed {
		t.Fatalf("approved Dockerfile still reported as changed: %#v", review)
	}
	if err := materializePlainArtifactIfNeeded(review, composeUpdatePrompt, &bytes.Buffer{}); err != nil {
		t.Fatalf("approved manual Dockerfile prompted again: %v", err)
	}
	if err := materializePlainArtifactIfNeeded(review, composeUpdateReplace, &bytes.Buffer{}); err != nil {
		t.Fatalf("replace manual Dockerfile: %v", err)
	}
	replaced, err := os.ReadFile(dockerfilePath) // #nosec G304 -- private test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if string(replaced) != generated {
		t.Fatalf("replaced Dockerfile was not regenerated: %s", replaced)
	}
}

func TestPlainArtifactUpdateConflictsOnLocalEdits(t *testing.T) {
	for _, name := range []string{"install-config.yaml", ".gitignore"} {
		t.Run(name, func(t *testing.T) {
			assertPlainArtifactConflictCycle(t, name)
		})
	}
}

// assertPlainArtifactConflictCycle drives one plain artifact through the
// manual-edit → conflict-with-diff → keep → approved cycle.
func assertPlainArtifactConflictCycle(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	selection := plainArtifactsTestSelection()
	global := config.DefaultGlobal()
	materializePlainArtifacts(t, dir, selection, global, composeUpdateReplace)

	path := filepath.Join(dir, containerArtifactDir, name)
	data, err := os.ReadFile(path) // #nosec G304 -- dir is a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	manual := string(data) + "# local edit\n"
	if err := os.WriteFile(path, []byte(manual), 0o600); err != nil { // #nosec G306 G703 -- private test fixture under t.TempDir().
		t.Fatal(err)
	}
	reviews, err := inspectPlainContainerArtifacts(dir, selection, container.DockerfileOptions{}, global)
	if err != nil {
		t.Fatal(err)
	}
	review := plainArtifactReviewByName(t, reviews, name)
	if !review.Changed || !strings.Contains(review.Diff, "-# local edit") {
		t.Fatalf("manual %s review = %#v; want the edit diff", name, review)
	}
	if err := materializePlainArtifactIfNeeded(review, composeUpdatePrompt, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), name) {
		t.Fatalf("prompt policy on manual %s error = %v; want a conflict naming the artifact", name, err)
	}
	if err := materializePlainArtifactIfNeeded(review, composeUpdateKeep, &bytes.Buffer{}); err != nil {
		t.Fatalf("keep manual %s: %v", name, err)
	}
	kept, err := os.ReadFile(path) // #nosec G304 -- private test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != manual {
		t.Fatalf("kept %s = %q; want the manual edit", name, kept)
	}
	// The approved pair no longer conflicts on the next review.
	reviews, err = inspectPlainContainerArtifacts(dir, selection, container.DockerfileOptions{}, global)
	if err != nil {
		t.Fatal(err)
	}
	if review := plainArtifactReviewByName(t, reviews, name); review.Changed {
		t.Fatalf("approved %s still reported as changed: %#v", name, review)
	}
}

func TestPlainArtifactUpdateRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	selection := plainArtifactsTestSelection()
	global := config.DefaultGlobal()
	materializePlainArtifacts(t, dir, selection, global, composeUpdateReplace)

	dockerfilePath := filepath.Join(dir, containerArtifactDir, "Dockerfile")
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("FROM scratch\n"), 0o600); err != nil { // #nosec G306 -- private test fixture.
		t.Fatal(err)
	}
	if err := os.Remove(dockerfilePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dockerfilePath); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectPlainContainerArtifacts(dir, selection, container.DockerfileOptions{}, global); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("inspectPlainContainerArtifacts() error = %v; want symlink refusal", err)
	}
}

func TestResolveContainerArtifactsUpdate(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	selection := plainArtifactsTestSelection()
	global := config.DefaultGlobal()
	launchConfig := launcher.LaunchConfig{Docker: container.RunConfig{Selection: selection}}

	// Nothing on disk: the prompt policy passes through without a conflict.
	choice, err := resolveContainerArtifactsUpdate(launchConfig, global, composeUpdatePrompt, strings.NewReader(""), &bytes.Buffer{}, false)
	if err != nil || choice != composeUpdatePrompt {
		t.Fatalf("resolve without artifacts = %q, %v; want prompt without error", choice, err)
	}
	// An explicit policy is never rewritten.
	choice, err = resolveContainerArtifactsUpdate(launchConfig, global, composeUpdateReplace, strings.NewReader(""), &bytes.Buffer{}, false)
	if err != nil || choice != composeUpdateReplace {
		t.Fatalf("resolve with explicit replace = %q, %v; want replace", choice, err)
	}

	materializePlainArtifacts(t, dir, selection, global, composeUpdateReplace)
	installPath := filepath.Join(dir, containerArtifactDir, "install-config.yaml")
	data, err := os.ReadFile(installPath) // #nosec G304 -- private test fixture.
	if err != nil {
		t.Fatal(err)
	}
	manual := string(data) + "# local edit\n"
	if err := os.WriteFile(installPath, []byte(manual), 0o600); err != nil { // #nosec G306 G703 -- private test fixture under t.TempDir().
		t.Fatal(err)
	}

	// Non-interactive prompt fails with the conflict and the diff.
	if _, err := resolveContainerArtifactsUpdate(launchConfig, global, composeUpdatePrompt, strings.NewReader(""), &bytes.Buffer{}, false); err == nil ||
		!strings.Contains(err.Error(), "has local changes") || !strings.Contains(err.Error(), "-# local edit") {
		t.Fatalf("non-interactive resolve error = %v; want the conflict with diff", err)
	}
	// Interactive prompt: the operator keeps the local file.
	var out bytes.Buffer
	choice, err = resolveContainerArtifactsUpdate(launchConfig, global, composeUpdatePrompt, strings.NewReader("m\n"), &out, true)
	if err != nil || choice != composeUpdateKeep {
		t.Fatalf("interactive resolve = %q, %v; want keep", choice, err)
	}
	if !strings.Contains(out.String(), "has local changes") {
		t.Fatalf("interactive prompt output = %q; want the conflict notice", out.String())
	}
	// The resolved keep is honored by the materialization.
	reviews, err := inspectPlainContainerArtifacts(dir, selection, container.DockerfileOptions{}, global)
	if err != nil {
		t.Fatal(err)
	}
	if err := materializePlainArtifactIfNeeded(plainArtifactReviewByName(t, reviews, "install-config.yaml"), choice, &bytes.Buffer{}); err != nil {
		t.Fatalf("materialize with resolved keep: %v", err)
	}
	kept, err := os.ReadFile(installPath) // #nosec G304 -- private test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != manual {
		t.Fatalf("kept install-config.yaml = %q; want the manual edit", kept)
	}
}

func TestComposeApprovalLedgerCoversEveryArtifact(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, composeArtifactName)
	dockerfilePath := filepath.Join(dir, "Dockerfile")

	if err := saveComposeApproval(composePath, "compose-current", "compose-generated"); err != nil {
		t.Fatalf("saveComposeApproval(compose) error = %v", err)
	}
	if err := saveComposeApproval(dockerfilePath, "dockerfile-current", "dockerfile-generated"); err != nil {
		t.Fatalf("saveComposeApproval(Dockerfile) error = %v", err)
	}
	compose, err := loadComposeApproval(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if compose.CurrentHash != hashCompose("compose-current") || compose.GeneratedHash != hashCompose("compose-generated") {
		t.Fatalf("compose approval = %#v; the Dockerfile save dropped the Compose entry", compose)
	}
	dockerfile, err := loadComposeApproval(dockerfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if dockerfile.CurrentHash != hashCompose("dockerfile-current") || dockerfile.GeneratedHash != hashCompose("dockerfile-generated") {
		t.Fatalf("Dockerfile approval = %#v; want the saved hashes", dockerfile)
	}
	info, err := os.Stat(composeApprovalPath(composePath))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode = %o; want 0600", info.Mode().Perm())
	}
}

func TestComposeApprovalLedgerMigratesLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, composeArtifactName)
	legacy := composeApproval{CurrentHash: hashCompose("legacy-current"), GeneratedHash: hashCompose("legacy-generated")}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(composeApprovalPath(composePath), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadComposeApproval(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Fatalf("legacy compose approval = %#v; want %#v", got, legacy)
	}
	// An unrelated artifact has no approval in a legacy ledger.
	other, err := loadComposeApproval(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if other != (composeApproval{}) {
		t.Fatalf("legacy Dockerfile approval = %#v; want zero", other)
	}
	// Saving another artifact migrates the flat entry without losing it.
	if err := saveComposeApproval(filepath.Join(dir, "Dockerfile"), "a", "b"); err != nil {
		t.Fatal(err)
	}
	got, err = loadComposeApproval(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Fatalf("migrated compose approval = %#v; want %#v", got, legacy)
	}
}

func TestGenerateExtendsComposeUpdateProtectionToPlainArtifacts(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	globalPath, localPath, _ := writeTestConfigs(t, "agent: custom-cli\noptions:\n  jail: false\n  docker: true\n  memory: false\n  stacks: [go]\n")
	generate := func(extra ...string) (string, error) {
		args := append([]string{"generate", "--config", globalPath, "--local-config", localPath, "--no-jail", "--docker-backend"}, extra...)
		stdout, _, err := runCapture(t, args...)
		return stdout, err
	}
	if _, err := generate(); err != nil {
		t.Fatalf("initial generate error = %v", err)
	}
	dockerfilePath := filepath.Join(dir, ".ai-launcher", "Dockerfile")
	data, err := os.ReadFile(dockerfilePath) // #nosec G304 -- dir is a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	manual := string(data) + "# local tweak\n"
	if err := os.WriteFile(dockerfilePath, []byte(manual), 0o600); err != nil { // #nosec G306 G703 -- private test fixture under t.TempDir().
		t.Fatal(err)
	}

	// The default prompt policy refuses to overwrite the edit non-interactively.
	if _, err := generate(); err == nil ||
		!strings.Contains(err.Error(), "Dockerfile") || !strings.Contains(err.Error(), "has local changes") {
		t.Fatalf("generate with a local Dockerfile edit error = %v; want the conflict", err)
	}
	// keep preserves the edit and remembers the decision.
	stdout, err := generate("--compose-update", "keep")
	if err != nil {
		t.Fatalf("generate --compose-update keep error = %v", err)
	}
	if !strings.Contains(stdout, "kept custom .ai-launcher/Dockerfile") {
		t.Fatalf("keep stdout = %q; want the kept notice", stdout)
	}
	kept, err := os.ReadFile(dockerfilePath) // #nosec G304 -- dir is a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != manual {
		t.Fatalf("kept Dockerfile = %q; want the manual tweak", kept)
	}
	if _, err := generate(); err != nil {
		t.Fatalf("approved Dockerfile prompted again: %v", err)
	}
	// replace regenerates the artifact.
	stdout, err = generate("--compose-update", "replace")
	if err != nil {
		t.Fatalf("generate --compose-update replace error = %v", err)
	}
	if !strings.Contains(stdout, "replaced .ai-launcher/Dockerfile with generated Dockerfile") {
		t.Fatalf("replace stdout = %q; want the replaced notice", stdout)
	}
	replaced, err := os.ReadFile(dockerfilePath) // #nosec G304 -- dir is a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(replaced), "local tweak") {
		t.Fatalf("replaced Dockerfile kept the manual edit: %s", replaced)
	}
}
