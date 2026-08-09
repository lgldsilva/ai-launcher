package container

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// GeneratedSecretMarker marks a catalog environment value that must become a
// per-project random secret when the Compose file is materialized. A real
// default in the catalog would be a hardcoded credential in the repository,
// while generating inside the renderer would break its determinism: the same
// ComposeFile must always render to the same YAML. Resolution therefore
// happens exactly once, at materialization time, and the value written to
// disk is reused on every later run.
const GeneratedSecretMarker = "ai-launcher:generated-secret"

// generatedSecretDisplay is the deterministic placeholder shown in previews,
// where no secret may be generated as a side effect of painting a screen.
const generatedSecretDisplay = "<generated-on-first-launch>"

// ResolveComposeSecrets substitutes every GeneratedSecretMarker environment
// value with a stable per-project secret: a value already present in the
// project's existing Compose file (including one the operator customized) is
// reused unchanged, otherwise a fresh 256-bit random hex secret is generated.
// Callers that only render for display must use MaskComposeSecrets instead.
func ResolveComposeSecrets(projectDir string, file ComposeFile) (ComposeFile, error) {
	if !composeHasSecretMarkers(file) {
		return file, nil
	}
	file.Services = cloneComposeServices(file.Services)
	if strings.TrimSpace(projectDir) == "" {
		resolved, err := os.Getwd()
		if err != nil {
			return ComposeFile{}, fmt.Errorf("resolve project directory for compose secrets: %w", err)
		}
		projectDir = resolved
	}
	existing, err := existingComposeSecrets(projectDir)
	if err != nil {
		return ComposeFile{}, err
	}
	for name, service := range file.Services {
		if len(service.Environment) == 0 {
			continue
		}
		environment := service.Environment
		cloned := false
		for key, value := range service.Environment {
			if value != GeneratedSecretMarker {
				continue
			}
			if !cloned {
				environment = cloneStringMap(service.Environment)
				cloned = true
			}
			resolved := existing[name][key]
			if resolved == "" || resolved == GeneratedSecretMarker {
				resolved, err = generateComposeSecret()
				if err != nil {
					return ComposeFile{}, err
				}
			}
			environment[key] = resolved
		}
		if cloned {
			service.Environment = environment
			file.Services[name] = service
		}
	}
	return file, nil
}

// MaskComposeSecrets replaces markers with a fixed display placeholder so
// previews stay deterministic and free of side effects.
func MaskComposeSecrets(file ComposeFile) ComposeFile {
	if !composeHasSecretMarkers(file) {
		return file
	}
	file.Services = cloneComposeServices(file.Services)
	for name, service := range file.Services {
		if len(service.Environment) == 0 {
			continue
		}
		environment := service.Environment
		cloned := false
		for key, value := range service.Environment {
			if value != GeneratedSecretMarker {
				continue
			}
			if !cloned {
				environment = cloneStringMap(service.Environment)
				cloned = true
			}
			environment[key] = generatedSecretDisplay
		}
		if cloned {
			service.Environment = environment
			file.Services[name] = service
		}
	}
	return file
}

func cloneComposeServices(services map[string]ComposeService) map[string]ComposeService {
	clone := make(map[string]ComposeService, len(services))
	for name, service := range services {
		clone[name] = service
	}
	return clone
}

func composeHasSecretMarkers(file ComposeFile) bool {
	for _, service := range file.Services {
		for _, value := range service.Environment {
			if value == GeneratedSecretMarker {
				return true
			}
		}
	}
	return false
}

// existingComposeSecrets recovers previously materialized secret values from
// the project's Compose file. A missing file yields an empty map; an
// unparsable one also yields an empty map because the keep/replace conflict
// review is the right place to surface a manually broken file.
func existingComposeSecrets(projectDir string) (map[string]map[string]string, error) {
	secrets := make(map[string]map[string]string)
	path := filepath.Join(projectDir, containerArtifactDirName, "docker-compose.yaml")
	data, err := os.ReadFile(path) // #nosec G304 -- path is the fixed project-local Compose artifact.
	if errors.Is(err, fs.ErrNotExist) {
		return secrets, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read existing compose secrets: %w", err)
	}
	var document struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return secrets, nil
	}
	for name, service := range document.Services {
		for key, value := range service.Environment {
			if value == "" || value == GeneratedSecretMarker {
				continue
			}
			if secrets[name] == nil {
				secrets[name] = make(map[string]string)
			}
			secrets[name][key] = value
		}
	}
	return secrets, nil
}

func generateComposeSecret() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate compose secret: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
