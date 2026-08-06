package gherkin_test

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
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
	Agent         string            `yaml:"agent"`
	Executable    string            `yaml:"executable"`
	Home          string            `yaml:"home"`
	ClearHome     bool              `yaml:"clear_home"`
	GOOS          string            `yaml:"goos"`
	Jail          bool              `yaml:"jail"`
	Docker        bool              `yaml:"docker"`
	Stacks        []string          `yaml:"stacks"`
	JailExec      bool              `yaml:"jail_exec"`
	JailFlags     config.JailFlags  `yaml:"jail_flags"`
	Memory        bool              `yaml:"memory"`
	Continue      bool              `yaml:"continue"`
	Fresh         bool              `yaml:"fresh"`
	RunHarness    string            `yaml:"run_harness"`
	NewWorkstream string            `yaml:"new_workstream"`
	Workstream    string            `yaml:"workstream"`
	Workspace     string            `yaml:"workspace"`
	Project       string            `yaml:"project"`
	Permissions   map[string]bool   `yaml:"permissions"`
	Mounts        []config.Mount    `yaml:"mounts"`
	Yolo          bool              `yaml:"yolo"`
	YoloFlag      string            `yaml:"yolo_flag"`
	Params        []config.Param    `yaml:"params"`
	ParamValues   map[string]string `yaml:"param_values"`
	Args          []string          `yaml:"args"`
	Missing       []string          `yaml:"missing_commands"`
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
	if runValidationScenario(t, scenario) {
		return
	}
	if runBuildScenario(t, scenario) {
		return
	}
	if runGlobalConfigScenario(t, scenario) {
		return
	}
	if runDefaultsScenario(t, scenario) {
		return
	}
	runLocalConfigScenario(t, scenario)
}

// runValidationScenario handles "Given a validation configuration" scenarios,
// reporting whether the scenario matched.
func runValidationScenario(t *testing.T, scenario featureScenario) bool {
	t.Helper()
	step, ok := scenario.step("Given a validation configuration")
	if !ok {
		return false
	}
	var spec launchSpec
	if err := yaml.Unmarshal([]byte(step.doc), &spec); err != nil {
		t.Fatalf("parse validation configuration: %v", err)
	}
	if spec.ClearHome {
		t.Setenv("HOME", "")
	}
	missing := make(map[string]bool, len(spec.Missing))
	for _, command := range spec.Missing {
		missing[command] = true
	}
	validator := launcher.NewValidator()
	validator.GOOS = spec.GOOS
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
	if got, want := issueCodes(issues), nonEmptyLines(expected.doc); !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
		t.Fatalf("Validate() issue codes = %#v; want %#v", got, want)
	}
	return true
}

// runBuildScenario handles "Given a launch configuration" scenarios,
// reporting whether the scenario matched.
func runBuildScenario(t *testing.T, scenario featureScenario) bool {
	t.Helper()
	step, ok := scenario.step("Given a launch configuration")
	if !ok {
		return false
	}
	var spec launchSpec
	if err := yaml.Unmarshal([]byte(step.doc), &spec); err != nil {
		t.Fatalf("parse launch configuration: %v", err)
	}
	if spec.ClearHome {
		t.Setenv("HOME", "")
	}
	launch := toLaunchConfig(spec)
	if spec.GOOS != "" {
		// Platform constraints (for example no ai-jail on Windows) are applied
		// before building, exactly like the CLI dispatch does.
		launch, _ = launcher.ConstrainToPlatform(launch, spec.GOOS, config.DefaultGlobal().Permissions)
	}
	argv, err := launcher.Build(launch)
	if failure, expected := scenario.failureExpectation(); expected {
		if err == nil || !strings.Contains(err.Error(), failure) {
			t.Fatalf("Build() error = %v; want containing %q", err, failure)
		}
		return true
	}
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	expected, ok := scenario.step("Then the command equals")
	if !ok {
		t.Fatal("launch scenario must define an expected command")
	}
	want := nonEmptyLines(expected.doc)
	// Docker scenarios pin a placeholder tag (ai-launcher-box:000000000000)
	// because the real tag is the content hash of the normalized selection.
	// Resolve it here so the contract asserts the structure without encoding
	// a hash that would change when the pinned test version changes.
	if launch.UseDocker {
		want = resolveDockerImageTag(want, launch.Docker.Selection)
	}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
	return true
}

// resolveDockerImageTag substitutes the real content-hashed tag for the
// placeholder in docker contract scenarios.
func resolveDockerImageTag(lines []string, selection container.Selection) []string {
	tag, err := container.ImageTag(selection)
	if err != nil {
		return lines
	}
	resolved := make([]string, len(lines))
	for i, line := range lines {
		if strings.HasPrefix(line, "ai-launcher-box:") {
			resolved[i] = tag
		} else {
			resolved[i] = line
		}
	}
	return resolved
}

// runLocalConfigScenario handles "Given a local configuration" scenarios and
// fails when the scenario has no supported Given step.
func runLocalConfigScenario(t *testing.T, scenario featureScenario) {
	t.Helper()
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
	agent := config.Agent{Command: spec.Agent, YoloFlag: spec.YoloFlag, Params: spec.Params}
	if spec.Yolo || strings.TrimSpace(spec.YoloFlag) != "" {
		agent.SupportsYolo = true
	}
	if spec.RunHarness != "" {
		agent.Memory = &config.MemoryIntegration{RunHarness: spec.RunHarness}
	}
	return launcher.LaunchConfig{
		Agent:           agent,
		Executable:      spec.Executable,
		HomeDir:         spec.Home,
		UseJail:         spec.Jail,
		UseDocker:       spec.Docker,
		JailExec:        spec.JailExec,
		JailFlags:       spec.JailFlags,
		UseMemory:       spec.Memory,
		ContinueSession: spec.Continue,
		Fresh:           spec.Fresh,
		NewWorkstream:   spec.NewWorkstream,
		Workstream:      spec.Workstream,
		Workspace:       spec.Workspace,
		Project:         spec.Project,
		Permissions:     spec.Permissions,
		Mounts:          spec.Mounts,
		Yolo:            spec.Yolo,
		ExtraArgs:       spec.Args,
		ParamValues:     spec.ParamValues,
		Docker:          dockerRunConfig(spec),
	}
}

// dockerRunConfig derives the container run inputs from a docker-enabled spec.
// The selection is deliberately NOT normalized here: a scenario that declares
// an unknown stack (validation contract) must flow through to the validator
// as an invalid selection rather than panicking at parse time. The agent is
// pinned to a placeholder version so the image tag stays deterministic; the
// catalog pins real versions at launch time. The workspace doubles as the
// same-path project mount.
func dockerRunConfig(spec launchSpec) container.RunConfig {
	if !spec.Docker {
		return container.RunConfig{}
	}
	return container.RunConfig{
		Selection: container.Selection{
			Stacks: spec.Stacks,
			Agents: []container.AgentInstall{{
				Command: spec.Agent,
				Kind:    container.InstallRelease,
				Version: "0.0.0-test",
			}},
		},
		Interactive:    true,
		AddHostGateway: true,
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
var profileAgentExpectation = regexp.MustCompile(`^(?:Then|And) profile "([^"]+)" has agent "([^"]+)"$`)
var profileOptionExpectation = regexp.MustCompile(`^(?:Then|And) profile "([^"]+)" option "([^"]+)" is (true|false)$`)
var profileParamExpectation = regexp.MustCompile(`^(?:Then|And) profile "([^"]+)" param "([^"]+)" is "([^"]*)"$`)

// runGlobalConfigScenario handles "Given a global configuration" scenarios,
// saving and reloading the document through the config layer and checking
// profile expectations. It reports whether the scenario matched.
func runGlobalConfigScenario(t *testing.T, scenario featureScenario) bool {
	t.Helper()
	step, ok := scenario.step("Given a global configuration")
	if !ok {
		return false
	}
	var global config.Global
	if err := yaml.Unmarshal([]byte(step.doc), &global); err != nil {
		t.Fatalf("parse global configuration: %v", err)
	}
	if _, ok := scenario.step("When the global configuration is saved and loaded"); !ok {
		t.Fatal("global configuration scenario must save and load the config")
	}
	path := filepath.Join(t.TempDir(), "global.yaml")
	if err := config.SaveGlobal(path, global); err != nil {
		t.Fatalf("SaveGlobal() error = %v", err)
	}
	loaded, err := config.LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	checked := 0
	for _, expectation := range scenario.steps {
		checked += checkProfileExpectation(t, loaded, expectation.text)
	}
	if checked == 0 {
		t.Fatal("global configuration scenario has no supported expectation")
	}
	return true
}

// checkProfileExpectation verifies one Then/And step against the loaded
// global config, returning 1 when the step was a recognized profile check.
func checkProfileExpectation(t *testing.T, global config.Global, text string) int {
	t.Helper()
	if checkProfileAgent(t, global, text) ||
		checkProfileOption(t, global, text) ||
		checkProfileParam(t, global, text) {
		return 1
	}
	return 0
}

// checkProfileAgent handles `profile "<name>" has agent "<agent>"` steps.
func checkProfileAgent(t *testing.T, global config.Global, text string) bool {
	t.Helper()
	match := profileAgentExpectation.FindStringSubmatch(text)
	if len(match) != 3 {
		return false
	}
	profile, ok := global.Profiles[match[1]]
	if !ok || profile.Agent != match[2] {
		t.Fatalf("profile %q agent = %q; want %q", match[1], profile.Agent, match[2])
	}
	return true
}

// checkProfileOption handles `profile "<name>" option "<option>" is <bool>` steps.
func checkProfileOption(t *testing.T, global config.Global, text string) bool {
	t.Helper()
	match := profileOptionExpectation.FindStringSubmatch(text)
	if len(match) != 4 {
		return false
	}
	options := profileOptionsFor(t, global, match[1])
	got, known := localOption(*options, match[2])
	if !known {
		t.Fatalf("unknown option in feature: %q", match[2])
	}
	if want := match[3] == "true"; got != want {
		t.Fatalf("profile %q option %q = %t; want %t", match[1], match[2], got, want)
	}
	return true
}

// checkProfileParam handles `profile "<name>" param "<param>" is "<value>"` steps.
func checkProfileParam(t *testing.T, global config.Global, text string) bool {
	t.Helper()
	match := profileParamExpectation.FindStringSubmatch(text)
	if len(match) != 4 {
		return false
	}
	options := profileOptionsFor(t, global, match[1])
	if got := options.ParamValues[match[2]]; got != match[3] {
		t.Fatalf("profile %q param %q = %q; want %q", match[1], match[2], got, match[3])
	}
	return true
}

// profileOptionsFor returns the options block of a profile, failing the test
// when the profile is missing or declares no options.
func profileOptionsFor(t *testing.T, global config.Global, name string) *config.Options {
	t.Helper()
	profile, ok := global.Profiles[name]
	if !ok || profile.Options == nil {
		t.Fatalf("profile %q has no options block", name)
	}
	return profile.Options
}

var toolAssetsExpectation = regexp.MustCompile(`^(?:Then|And) tool "([^"]+)" assets equal$`)

// runDefaultsScenario handles "Given the built-in global defaults" scenarios,
// pinning the release recipes shipped in DefaultGlobal against upstream
// drift (for example an ai-jail asset that no longer exists). It reports
// whether the scenario matched.
func runDefaultsScenario(t *testing.T, scenario featureScenario) bool {
	t.Helper()
	if _, ok := scenario.step("Given the built-in global defaults"); !ok {
		return false
	}
	checked := 0
	for _, step := range scenario.steps {
		match := toolAssetsExpectation.FindStringSubmatch(step.text)
		if len(match) != 2 {
			continue
		}
		checked++
		assertToolAssets(t, config.DefaultGlobal(), match[1], nonEmptyLines(step.doc))
	}
	if checked == 0 {
		t.Fatal("defaults scenario has no supported expectation")
	}
	return true
}

// assertToolAssets compares the sorted platform keys of a tool's release
// recipe with the expected list.
func assertToolAssets(t *testing.T, global config.Global, name string, want []string) {
	t.Helper()
	for _, tool := range global.Tools {
		if tool.Name != name {
			continue
		}
		if tool.Release == nil {
			t.Fatalf("tool %q has no release recipe", name)
		}
		keys := make([]string, 0, len(tool.Release.Assets))
		for key := range tool.Release.Assets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, want) {
			t.Fatalf("tool %q assets = %#v; want %#v", name, keys, want)
		}
		return
	}
	t.Fatalf("tool %q not found in the default catalog", name)
}

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
	file, err := os.Open(path) // #nosec G304 -- path comes from the test's own ../features glob
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	parser := &featureParser{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parser.consume(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if parser.inDoc {
		t.Fatalf("unterminated doc string in %s", path)
	}
	if len(parser.scenarios) == 0 {
		t.Fatalf("no scenarios in %s", path)
	}
	return parser.scenarios
}

// featureParser accumulates scenarios and steps while a feature file is
// scanned line by line.
type featureParser struct {
	scenarios []featureScenario
	current   *featureScenario
	inDoc     bool
	doc       []string
}

// consume feeds one raw line of the feature file into the parser.
func (p *featureParser) consume(rawLine string) {
	line := strings.TrimSpace(rawLine)
	if line == `"""` {
		p.toggleDoc()
		return
	}
	if p.inDoc {
		p.doc = append(p.doc, rawLine)
		return
	}
	if strings.HasPrefix(line, "Scenario: ") {
		p.scenarios = append(p.scenarios, featureScenario{name: strings.TrimPrefix(line, "Scenario: ")})
		p.current = &p.scenarios[len(p.scenarios)-1]
		return
	}
	if p.current != nil && isStepLine(line) {
		p.current.steps = append(p.current.steps, featureStep{text: line})
	}
}

// toggleDoc opens or closes a triple-quoted doc string, attaching the
// collected lines to the previous step when the block closes.
func (p *featureParser) toggleDoc() {
	p.inDoc = !p.inDoc
	if !p.inDoc && p.current != nil && len(p.current.steps) > 0 {
		p.current.steps[len(p.current.steps)-1].doc = strings.Join(p.doc, "\n")
		p.doc = nil
	}
}

// isStepLine reports whether the line starts a Given/When/Then/And step.
func isStepLine(line string) bool {
	return strings.HasPrefix(line, "Given ") ||
		strings.HasPrefix(line, "When ") ||
		strings.HasPrefix(line, "Then ") ||
		strings.HasPrefix(line, "And ")
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
