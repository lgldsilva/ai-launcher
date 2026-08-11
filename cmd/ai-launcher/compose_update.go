package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"github.com/lgldsilva/ai-launcher/internal/tui"
)

type composeUpdateChoice string

const (
	composeUpdatePrompt  composeUpdateChoice = "prompt"
	composeUpdateKeep    composeUpdateChoice = "keep"
	composeUpdateReplace composeUpdateChoice = "replace"
)

const composeApprovalFile = ".compose-approval.json"
const composeArtifactName = "docker-compose.yaml"

// artifactReview compares one generated project artifact with the copy on
// disk: Exists/Changed drive the keep/replace/prompt flow and Diff feeds both
// the interactive review and the non-interactive conflict error.
type artifactReview struct {
	Path      string
	Name      string
	Generated string
	Current   string
	Exists    bool
	Changed   bool
	Diff      string
}

type composeArtifactReview struct {
	artifactReview
	Compose container.ComposeFile
}

type composeApproval struct {
	CurrentHash   string `json:"current_hash"`
	GeneratedHash string `json:"generated_hash"`
}

// composeApprovalLedger stores one approval entry per generated artifact,
// keyed by file name. The flat fields are the legacy single-artifact format
// (Compose only) and are migrated into Artifacts on the next save.
type composeApprovalLedger struct {
	Artifacts     map[string]composeApproval `json:"artifacts,omitempty"`
	CurrentHash   string                     `json:"current_hash,omitempty"`
	GeneratedHash string                     `json:"generated_hash,omitempty"`
}

type artifactConflictError struct {
	review artifactReview
}

func (e *artifactConflictError) Error() string {
	return fmt.Sprintf("%s has local changes; review the diff before replacing it:\n%s", e.review.Path, e.review.Diff)
}

func parseComposeUpdateChoice(value string) (composeUpdateChoice, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(composeUpdatePrompt):
		return composeUpdatePrompt, nil
	case string(composeUpdateKeep):
		return composeUpdateKeep, nil
	case string(composeUpdateReplace), "overwrite", "regenerate":
		return composeUpdateReplace, nil
	default:
		return "", fmt.Errorf("invalid --compose-update %q; use prompt, keep or replace", value)
	}
}

// inspectGeneratedArtifact reviews one generated artifact against the copy on
// disk. A file whose current and generated hashes were both approved before
// (the operator already kept or replaced this exact pair) is not a conflict.
func inspectGeneratedArtifact(path, name, generated string) (artifactReview, error) {
	review := artifactReview{Path: path, Name: name, Generated: generated}
	exists, err := regularArtifactExists(path)
	if err != nil {
		return artifactReview{}, err
	}
	if !exists {
		return review, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is a fixed project-local generated artifact.
	if err != nil {
		return artifactReview{}, fmt.Errorf("read generated artifact %s: %w", path, err)
	}
	review.Exists = true
	review.Current = string(data)
	if review.Current == review.Generated {
		return review, nil
	}
	approved, err := loadComposeApproval(path)
	if err != nil {
		return artifactReview{}, err
	}
	if approved.CurrentHash == hashCompose(review.Current) && approved.GeneratedHash == hashCompose(review.Generated) {
		return review, nil
	}
	review.Changed = true
	review.Diff = unifiedComposeDiff(path, review.Current, review.Generated)
	return review, nil
}

// dockerComposeProjectDir resolves the same project directory BuildCompose
// used internally (prepareDockerRunConfig's firstNonEmpty(cfg.Workspace,
// cfg.Project, ...)) for the Compose file, its secrets, its approval ledger,
// and any GeneratedFiles content — all of it must agree with where
// BuildCompose put service data directories (workspace/.ai-launcher/data/...),
// or every generated-file write fails its pathWithin() guard whenever the
// workspace differs from the process cwd. Only call this after BuildCompose
// has already succeeded for the same cfg — it guarantees at least one of
// Workspace/Project is non-empty.
func dockerComposeProjectDir(cfg launcher.LaunchConfig) string {
	if dir := strings.TrimSpace(cfg.Workspace); dir != "" {
		return dir
	}
	return strings.TrimSpace(cfg.Project)
}

func inspectComposeArtifact(cfg launcher.LaunchConfig) (composeArtifactReview, error) {
	compose, err := launcher.BuildCompose(cfg)
	if err != nil {
		return composeArtifactReview{}, fmt.Errorf("build compose: %w", err)
	}
	projectDir := dockerComposeProjectDir(cfg)
	compose, err = container.ResolveComposeSecrets(projectDir, compose)
	if err != nil {
		return composeArtifactReview{}, fmt.Errorf("resolve compose secrets: %w", err)
	}
	generated, err := container.RenderCompose(compose)
	if err != nil {
		return composeArtifactReview{}, fmt.Errorf("render compose: %w", err)
	}
	path := filepath.Join(projectDir, containerArtifactDir, composeArtifactName)
	review, err := inspectGeneratedArtifact(path, "Compose", generated)
	if err != nil {
		return composeArtifactReview{}, err
	}
	return composeArtifactReview{artifactReview: review, Compose: compose}, nil
}

// applyArtifactReview executes a resolved keep/replace decision: keep records
// the operator's hashes so the same pair stops prompting, replace writes the
// generated content and approves it.
func applyArtifactReview(review artifactReview, choice composeUpdateChoice, writeGenerated func() error) error {
	switch choice {
	case composeUpdateKeep:
		if !review.Exists {
			return fmt.Errorf("cannot keep %s because it does not exist", review.Path)
		}
		return saveComposeApproval(review.Path, review.Current, review.Generated)
	case composeUpdateReplace:
		if err := writeGenerated(); err != nil {
			return err
		}
		return saveComposeApproval(review.Path, review.Generated, review.Generated)
	default:
		return fmt.Errorf("compose update choice %q was not resolved", choice)
	}
}

// materializeComposeIfNeeded is the only write path for the generated
// Compose file. A changed file is never silently replaced: callers must pass
// an explicit choice, or surface the conflict to an interactive reviewer.
func materializeComposeIfNeeded(cfg launcher.LaunchConfig, choice composeUpdateChoice, out io.Writer) error {
	if len(cfg.Services) == 0 || !cfg.UseDocker {
		return nil
	}
	if choice == "" {
		choice = composeUpdatePrompt
	}
	review, err := inspectComposeArtifact(cfg)
	if err != nil {
		return err
	}
	projectDir := dockerComposeProjectDir(cfg)
	writeGenerated := func() error {
		_, err := container.MaterializeCompose(projectDir, review.Compose)
		return err
	}
	// GeneratedFiles (e.g. the egress proxy's squid.conf) carry content that
	// never appears in the rendered docker-compose.yaml (ComposeFile.GeneratedFiles
	// is yaml:"-"), so review.Changed — which diffs the rendered YAML text —
	// cannot detect a domain-allowlist edit: the YAML is byte-identical for
	// any allowed-domains list. Without this, tightening or changing the
	// allowlist after the first launch would silently keep enforcing the old
	// policy. Regenerate unconditionally, decoupled from the YAML approval
	// flow below (this file is enforcement config, not something an operator
	// hand-edits — "do not edit by hand" is already in its header comment).
	if err := container.WriteComposeGeneratedFiles(projectDir, review.Compose); err != nil {
		return err
	}
	relPath := filepath.Join(containerArtifactDir, composeArtifactName)
	if !review.Exists {
		if err := writeGenerated(); err != nil {
			return err
		}
		return saveComposeApproval(review.Path, review.Generated, review.Generated)
	}
	if choice == composeUpdateReplace && review.Current != review.Generated {
		if err := applyArtifactReview(review.artifactReview, choice, writeGenerated); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "replaced %s with generated Compose\n", relPath)
		return nil
	}
	if !review.Changed {
		return nil
	}
	if choice == composeUpdatePrompt {
		return &artifactConflictError{review: review.artifactReview}
	}
	if err := applyArtifactReview(review.artifactReview, choice, writeGenerated); err != nil {
		return err
	}
	if choice == composeUpdateKeep {
		_, _ = fmt.Fprintf(out, "kept custom %s\n", relPath)
	} else {
		_, _ = fmt.Fprintf(out, "replaced %s with generated Compose\n", relPath)
	}
	return nil
}

// materializePlainArtifactIfNeeded applies the Compose overwrite protection to
// a plain generated artifact (Dockerfile, install-config.yaml, .gitignore): a
// locally changed file is never silently replaced without an explicit
// keep/replace decision or a previously approved hash pair.
func materializePlainArtifactIfNeeded(review artifactReview, choice composeUpdateChoice, out io.Writer) error {
	if choice == "" {
		choice = composeUpdatePrompt
	}
	relPath := filepath.Join(containerArtifactDir, filepath.Base(review.Path))
	writeGenerated := func() error {
		if err := ensureArtifactDir(filepath.Dir(review.Path)); err != nil {
			return err
		}
		return writeGeneratedArtifact(review.Path, []byte(review.Generated))
	}
	if !review.Exists {
		if err := writeGenerated(); err != nil {
			return err
		}
		if err := saveComposeApproval(review.Path, review.Generated, review.Generated); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "generated %s\n", relPath)
		return nil
	}
	if choice == composeUpdateReplace && review.Current != review.Generated {
		if err := applyArtifactReview(review, choice, writeGenerated); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "replaced %s with generated %s\n", relPath, review.Name)
		return nil
	}
	if !review.Changed {
		_, _ = fmt.Fprintf(out, "generated %s\n", relPath)
		return nil
	}
	if choice == composeUpdatePrompt {
		return &artifactConflictError{review: review}
	}
	if err := applyArtifactReview(review, choice, writeGenerated); err != nil {
		return err
	}
	if choice == composeUpdateKeep {
		_, _ = fmt.Fprintf(out, "kept custom %s\n", relPath)
	} else {
		_, _ = fmt.Fprintf(out, "replaced %s with generated %s\n", relPath, review.Name)
	}
	return nil
}

func resolveComposeUpdate(cfg launcher.LaunchConfig, requested composeUpdateChoice, in io.Reader, out io.Writer, interactive bool) (composeUpdateChoice, error) {
	if len(cfg.Services) == 0 || !cfg.UseDocker {
		return requested, nil
	}
	review, err := inspectComposeArtifact(cfg)
	if err != nil {
		return requested, err
	}
	if !review.Changed || requested != composeUpdatePrompt {
		return requested, nil
	}
	if !interactive {
		return requested, &artifactConflictError{review: review.artifactReview}
	}
	return promptComposeUpdate(review.artifactReview, in, out)
}

func promptComposeUpdate(review artifactReview, in io.Reader, out io.Writer) (composeUpdateChoice, error) {
	_, _ = fmt.Fprintf(out, "ai-launcher: %s has local changes\n\n%s\n", review.Path, review.Diff)
	_, _ = fmt.Fprintln(out, "[m] keep current file  [s] replace with generated  [q] cancel")
	reader := bufio.NewReader(in)
	for {
		_, _ = fmt.Fprintf(out, "Choice [m/s/q]: ")
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return composeUpdatePrompt, fmt.Errorf("compose update was not confirmed: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "m", "manter", "keep":
			return composeUpdateKeep, nil
		case "s", "substituir", "replace", "overwrite":
			return composeUpdateReplace, nil
		case "q", "cancelar", "cancel":
			return composeUpdatePrompt, errors.New("compose update cancelled")
		default:
			_, _ = fmt.Fprintln(out, "invalid choice; use m, s or q")
		}
		if err != nil {
			return composeUpdatePrompt, fmt.Errorf("read compose update choice: %w", err)
		}
	}
}

func hashCompose(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func composeApprovalPath(composePath string) string {
	return filepath.Join(filepath.Dir(composePath), composeApprovalFile)
}

// loadComposeApproval returns the approval entry recorded for one artifact.
// The ledger holds one entry per generated artifact, keyed by file name; the
// legacy flat format (a single Compose approval) is honored for the Compose
// file until the next save migrates it.
func loadComposeApproval(artifactPath string) (composeApproval, error) {
	path := composeApprovalPath(artifactPath)
	exists, err := regularArtifactExists(path)
	if err != nil {
		return composeApproval{}, err
	}
	if !exists {
		return composeApproval{}, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- derived metadata stays beside the fixed generated artifacts.
	if err != nil {
		return composeApproval{}, fmt.Errorf("read Compose approval %s: %w", path, err)
	}
	var ledger composeApprovalLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return composeApproval{}, fmt.Errorf("parse Compose approval %s: %w", path, err)
	}
	name := filepath.Base(artifactPath)
	if approval, ok := ledger.Artifacts[name]; ok {
		return approval, nil
	}
	if name == composeArtifactName {
		return composeApproval{CurrentHash: ledger.CurrentHash, GeneratedHash: ledger.GeneratedHash}, nil
	}
	return composeApproval{}, nil
}

// saveComposeApproval records the operator's decision for one artifact without
// dropping the decisions already recorded for the other generated artifacts.
func saveComposeApproval(artifactPath, current, generated string) error {
	path := composeApprovalPath(artifactPath)
	ledger := composeApprovalLedger{}
	exists, err := regularArtifactExists(path)
	if err != nil {
		return err
	}
	if exists {
		data, err := os.ReadFile(path) // #nosec G304 -- derived metadata stays beside the fixed generated artifacts.
		if err != nil {
			return fmt.Errorf("read Compose approval %s: %w", path, err)
		}
		if err := json.Unmarshal(data, &ledger); err != nil {
			return fmt.Errorf("parse Compose approval %s: %w", path, err)
		}
	}
	if ledger.Artifacts == nil {
		ledger.Artifacts = map[string]composeApproval{}
	}
	if ledger.CurrentHash != "" || ledger.GeneratedHash != "" {
		if _, ok := ledger.Artifacts[composeArtifactName]; !ok {
			ledger.Artifacts[composeArtifactName] = composeApproval{CurrentHash: ledger.CurrentHash, GeneratedHash: ledger.GeneratedHash}
		}
		ledger.CurrentHash, ledger.GeneratedHash = "", ""
	}
	ledger.Artifacts[filepath.Base(artifactPath)] = composeApproval{CurrentHash: hashCompose(current), GeneratedHash: hashCompose(generated)}
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Compose approval: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ai-launcher-compose-approval-*")
	if err != nil {
		return fmt.Errorf("create Compose approval: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect Compose approval: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Compose approval: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Compose approval: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace Compose approval: %w", err)
	}
	return nil
}

type composeDiffLine struct {
	prefix byte
	text   string
}

// unifiedComposeDiff is intentionally line-oriented and dependency-free. It
// produces a complete, deterministic diff suitable for both the TUI review
// screen and non-interactive diagnostics.
func unifiedComposeDiff(path, current, generated string) string {
	oldLines := composeDiffLines(current)
	newLines := composeDiffLines(generated)
	rows := make([][]int, len(oldLines)+1)
	for i := range rows {
		rows[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				rows[i][j] = rows[i+1][j+1] + 1
			} else if rows[i+1][j] >= rows[i][j+1] {
				rows[i][j] = rows[i+1][j]
			} else {
				rows[i][j] = rows[i][j+1]
			}
		}
	}
	ops := make([]composeDiffLine, 0, len(oldLines)+len(newLines))
	for i, j := 0, 0; i < len(oldLines) || j < len(newLines); {
		switch {
		case i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j]:
			ops = append(ops, composeDiffLine{' ', oldLines[i]})
			i++
			j++
		case i < len(oldLines) && (j == len(newLines) || rows[i+1][j] >= rows[i][j+1]):
			ops = append(ops, composeDiffLine{'-', oldLines[i]})
			i++
		default:
			ops = append(ops, composeDiffLine{'+', newLines[j]})
			j++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (current)\n+++ %s (generated)\n", path, path)
	for _, op := range ops {
		b.WriteByte(op.prefix)
		b.WriteString(op.text)
		b.WriteByte('\n')
	}
	return b.String()
}

func composeDiffLines(value string) []string {
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

// ensureArtifactDir creates the project-local artifact directory when missing,
// refusing a symlinked path like the other artifact writes.
func ensureArtifactDir(dir string) error {
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("container artifact directory %s is a symlink", dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("container artifact path %s is not a directory", dir)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect container artifact directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create container artifact directory: %w", err)
	}
	return nil
}

// writeGeneratedArtifact mirrors the atomic 0600 write used by the container
// package for generated artifacts, so the overwrite-protection write path for
// the plain artifacts keeps the same durability and symlink refusal.
func writeGeneratedArtifact(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("container artifact %s is a symlink", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect container artifact %s: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ai-launcher-artifact-*")
	if err != nil {
		return fmt.Errorf("create temporary container artifact %s: %w", path, err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary container artifact %s: %w", path, err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect container artifact %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary container artifact %s: %w", path, err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace container artifact %s: %w", path, err)
	}
	return nil
}

func composeReviewForTUI(cfg launcher.LaunchConfig, choice *composeUpdateChoice) (*tui.ComposeUpdateReview, error) {
	review, err := inspectComposeArtifact(cfg)
	if err != nil {
		return nil, err
	}
	if !review.Changed {
		return nil, nil
	}
	return &tui.ComposeUpdateReview{
		Diff: review.Diff,
		Choose: func(selected tui.ComposeUpdateChoice) error {
			switch selected {
			case tui.KeepCompose:
				*choice = composeUpdateKeep
			case tui.ReplaceCompose:
				*choice = composeUpdateReplace
			default:
				return fmt.Errorf("unknown Compose review choice %d", selected)
			}
			return nil
		},
	}, nil
}
