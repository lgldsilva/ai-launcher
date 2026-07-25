package gherkin_test

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

type featureScenario struct {
	name  string
	steps []featureStep
}

type featureStep struct {
	text string
	doc  string
}

type launchSpec struct {
	Agent         string          `yaml:"agent"`
	Executable    string          `yaml:"executable"`
	Home          string          `yaml:"home"`
	ClearHome     bool            `yaml:"clear_home"`
	Jail          bool            `yaml:"jail"`
	Memory        bool            `yaml:"memory"`
	NewWorkstream string          `yaml:"new_workstream"`
	Permissions   map[string]bool `yaml:"permissions"`
	Mounts        []config.Mount  `yaml:"mounts"`
	Yolo          bool            `yaml:"yolo"`
	Args          []string        `yaml:"args"`
	Missing       []string        `yaml:"missing_commands"`
}

func TestGherkinLauncherContract(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "features", "*.feature"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("feature discovery = %v, %v; want one or more feature files", paths, err)
	}
	for _, path := range paths {
		for _, scenario := range parseFeature(t, path) {
			scenario := scenario
			t.Run(scenario.name, func(t *testing.T) { runScenario(t, scenario) })
		}
	}
}

func runScenario(t *testing.T, scenario featureScenario) {
	t.Helper()
	if step, ok := scenario.step("Given a validation configuration"); ok {
		var spec launchSpec
		if err := yaml.Unmarshal([]byte(step.doc), &spec); err != nil {
			t.Fatalf("parse validation configuration: %v", err)
		}
		missing := make(map[string]bool, len(spec.Missing))
		for _, command := range spec.Missing {
			missing[command] = true
		}
		validator := launcher.NewValidator()
		validator.LookPath = func(command string) (string, error) {
			if missing[command] {
				return "", errors.New("not found")
			}
			return "/test/bin/" + command, nil
		}
		issues := validator.Validate(toLaunchConfig(spec))
		expected, ok := scenario.step("Then issue codes equal")
		if !ok {
			t.Fatal("validation scenario must define expected issue codes")
		}
		if got, want := issueCodes(issues), nonEmptyLines(expected.doc); !reflect.DeepEqual(got, want) {
			t.Fatalf("Validate() issue codes = %#v; want %#v", got, want)
		}
		return
	}
	if step, ok := scenario.step("Given a launch configuration"); ok {
		var spec launchSpec
		if err := yaml.Unmarshal([]byte(step.doc), &spec); err != nil {
			t.Fatalf("parse launch configuration: %v", err)
		}
		if spec.ClearHome {
			t.Setenv("HOME", "")
		}
		argv, err := launcher.Build(toLaunchConfig(spec))
		if failure, expected := scenario.failureExpectation(); expected {
			if err == nil || !strings.Contains(err.Error(), failure) {
				t.Fatalf("Build() error = %v; want containing %q", err, failure)
			}
			return
		}
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		expected, ok := scenario.step("Then the command equals")
		if !ok {
			t.Fatal("launch scenario must define an expected command")
		}
		want := nonEmptyLines(expected.doc)
		if !reflect.DeepEqual(argv, want) {
			t.Fatalf("Build() = %#v; want %#v", argv, want)
		}
		return
	}

	local, ok := scenario.step("Given a local configuration")
	if !ok {
		t.Fatal("scenario has no supported Given step")
	}
	path := filepath.Join(t.TempDir(), "local.yaml")
	if err := os.WriteFile(path, []byte(local.doc), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadLocal(path)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	for _, step := range scenario.steps {
		match := optionExpectation.FindStringSubmatch(step.text)
		if len(match) == 0 {
			continue
		}
		want := match[2] == "true"
		got, known := localOption(loaded.Options, match[1])
		if !known {
			t.Fatalf("unknown option in feature: %q", match[1])
		}
		if got != want {
			t.Errorf("option %q = %t; want %t", match[1], got, want)
		}
	}
}

func toLaunchConfig(spec launchSpec) launcher.LaunchConfig {
	return launcher.LaunchConfig{
		Agent:         config.Agent{Command: spec.Agent},
		Executable:    spec.Executable,
		HomeDir:       spec.Home,
		UseJail:       spec.Jail,
		UseMemory:     spec.Memory,
		NewWorkstream: spec.NewWorkstream,
		Permissions:   spec.Permissions,
		Mounts:        spec.Mounts,
		Yolo:          spec.Yolo,
		ExtraArgs:     spec.Args,
	}
}

func issueCodes(issues []launcher.Issue) []string {
	codes := make([]string, 0, len(issues))
	for _, issue := range issues {
		codes = append(codes, issue.Code)
	}
	return codes
}

var optionExpectation = regexp.MustCompile(`^(?:Then|And) option "([^"]+)" is (true|false)$`)
var failureExpectation = regexp.MustCompile(`^Then command construction fails with "([^"]+)"$`)

func (s featureScenario) step(text string) (featureStep, bool) {
	for _, step := range s.steps {
		if step.text == text {
			return step, true
		}
	}
	return featureStep{}, false
}

func (s featureScenario) failureExpectation() (string, bool) {
	for _, step := range s.steps {
		if match := failureExpectation.FindStringSubmatch(step.text); len(match) == 2 {
			return match[1], true
		}
	}
	return "", false
}

func localOption(options config.Options, name string) (bool, bool) {
	switch name {
	case "jail":
		return options.Jail, true
	case "memory":
		return options.Memory, true
	case "yolo":
		return options.Yolo, true
	default:
		return false, false
	}
}

func parseFeature(t *testing.T, path string) []featureScenario {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var scenarios []featureScenario
	var current *featureScenario
	inDoc := false
	var doc []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		rawLine := scanner.Text()
		line := strings.TrimSpace(rawLine)
		if line == "\"\"\"" {
			inDoc = !inDoc
			if !inDoc && current != nil && len(current.steps) > 0 {
				current.steps[len(current.steps)-1].doc = strings.Join(doc, "\n")
				doc = nil
			}
			continue
		}
		if inDoc {
			doc = append(doc, rawLine)
			continue
		}
		if strings.HasPrefix(line, "Scenario: ") {
			scenarios = append(scenarios, featureScenario{name: strings.TrimPrefix(line, "Scenario: ")})
			current = &scenarios[len(scenarios)-1]
			continue
		}
		if current != nil && (strings.HasPrefix(line, "Given ") || strings.HasPrefix(line, "When ") || strings.HasPrefix(line, "Then ") || strings.HasPrefix(line, "And ")) {
			current.steps = append(current.steps, featureStep{text: line})
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if inDoc {
		t.Fatalf("unterminated doc string in %s", path)
	}
	if len(scenarios) == 0 {
		t.Fatalf("no scenarios in %s", path)
	}
	return scenarios
}

func nonEmptyLines(doc string) []string {
	var result []string
	for _, line := range strings.Split(doc, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func Example_featureSyntax() {
	fmt.Println("Feature scenarios use Given/When/Then with triple-quoted YAML and argv blocks.")
	// Output: Feature scenarios use Given/When/Then with triple-quoted YAML and argv blocks.
}
