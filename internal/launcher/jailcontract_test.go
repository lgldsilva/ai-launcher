package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

const sampleCapabilities = `{
  "version": "3.0.1",
  "schema_version": 1,
  "flags": ["--harness", "--request-file", "--request", "--json"],
  "features": {
    "harness_flag": true,
    "request_file": true,
    "status_json": true,
    "secret_private_http": true,
    "configure_requests": true,
    "inside_marker": false
  },
  "exit_codes": {
    "cancelled": 130,
    "required_grant": 3,
    "blocked": 4,
    "backend_unavailable": 5
  }
}`

func contract31(t *testing.T) JailContract {
	t.Helper()
	contract, err := ParseJailCapabilities([]byte("noise\n" + sampleCapabilities + "\ntrailing"))
	if err != nil {
		t.Fatalf("ParseJailCapabilities() error = %v", err)
	}
	contract.Probed = true
	return contract
}

func TestParseJailCapabilitiesReadsTheWrapperContract(t *testing.T) {
	contract := contract31(t)
	if contract.Version != "3.0.1" || contract.SchemaVersion != 1 {
		t.Fatalf("contract = %#v", contract)
	}
	if !contract.Features.HarnessFlag || !contract.Features.RequestFile || !contract.Features.StatusJSON {
		t.Fatalf("features = %#v", contract.Features)
	}
	if contract.Features.InsideMarker {
		t.Fatal("inside_marker is true; 3.0.1 documents it as false")
	}
	if contract.ExitCodes.RequiredGrant != 3 || contract.ExitCodes.Blocked != 4 || contract.ExitCodes.BackendUnavailable != 5 || contract.ExitCodes.Cancelled != 130 {
		t.Fatalf("exit codes = %#v", contract.ExitCodes)
	}
	if !strings.Contains(contract.DoctorNote(), "inside-marker=false") || !strings.Contains(contract.DoctorNote(), "required_grant=3") {
		t.Fatalf("DoctorNote() = %q", contract.DoctorNote())
	}
}

func TestParseJailCapabilitiesFillsMissingExitCodes(t *testing.T) {
	contract, err := ParseJailCapabilities([]byte(`{"version":"3.0.1","schema_version":1,"features":{"harness_flag":true,"inside_marker":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if contract.ExitCodes.RequiredGrant != 3 || contract.ExitCodes.Blocked != 4 || contract.ExitCodes.BackendUnavailable != 5 || contract.ExitCodes.Cancelled != 130 {
		t.Fatalf("exit codes = %#v; omitted numbers use the 3.0.1 defaults", contract.ExitCodes)
	}
	contract.Probed = true
	if !strings.Contains(contract.DoctorNote(), "inside-marker=true") {
		t.Fatalf("DoctorNote() = %q", contract.DoctorNote())
	}
	if jailExitAdvice("nope", 9) != "" {
		t.Fatal("an unknown exit class produced advice")
	}
}

func TestParseJailStatusRejectsANonContract(t *testing.T) {
	if _, err := ParseJailStatus([]byte("not json")); err == nil {
		t.Fatal("plain text parsed as status")
	}
	if _, err := ParseJailStatus([]byte(`{"version":"3.0.1"}`)); err == nil {
		t.Fatal("status without schema_version parsed")
	}
}

func TestParseJailCapabilitiesRejectsANonContract(t *testing.T) {
	if _, err := ParseJailCapabilities([]byte("ai-jail 2.14.0")); err == nil {
		t.Fatal("plain version text parsed as a contract")
	}
	if _, err := ParseJailCapabilities([]byte(`{"version":"3.0.0"}`)); err == nil {
		t.Fatal("a document without schema_version parsed")
	}
}

func TestParseJailStatusReadsBlockers(t *testing.T) {
	status, err := ParseJailStatus([]byte(`{
		"version":"3.0.1",
		"schema_version":1,
		"blockers":[
			{"code":"required_grant_not_approved","message":"request pending"},
			{"code":"","message":"ignored"}
		],
		"requests":{"pending":[{"requester":"ai-launcher","kind":"host","value":"api.anthropic.com"}]}
	}`))
	if err != nil {
		t.Fatalf("ParseJailStatus() error = %v", err)
	}
	lines := FormatJailStatus(status)
	if len(lines) != 1 || !strings.Contains(lines[0], "ai-jail-status required_grant_not_approved") {
		t.Fatalf("lines = %#v", lines)
	}
	if len(status.Requests.Pending) != 1 || status.Requests.Pending[0].Value != "api.anthropic.com" {
		t.Fatalf("pending = %#v", status.Requests.Pending)
	}
}

func TestClassifyJailExitUsesContractCodes(t *testing.T) {
	contract := contract31(t)
	cases := []struct {
		code  int
		class string
	}{
		{3, "required_grant"},
		{4, "blocked"},
		{5, "backend_unavailable"},
		{130, "cancelled"},
	}
	for _, tc := range cases {
		class, advice, ok := ClassifyJailExit(tc.code, contract)
		if !ok || class != tc.class || !strings.Contains(advice, tc.class) {
			t.Errorf("ClassifyJailExit(%d) = %q, %q, %v", tc.code, class, advice, ok)
		}
		if JailExitHint("exit status 3\n"+advice) != advice {
			t.Errorf("JailExitHint lost %q", advice)
		}
	}
	if _, _, ok := ClassifyJailExit(7, contract); ok {
		t.Fatal("a child exit of 7 was classified as a contract exit")
	}
	if _, _, ok := ClassifyJailExit(3, JailContract{}); ok {
		t.Fatal("exit 3 was classified without a probed contract")
	}
	probedWithoutFeatures := JailContract{Probed: true, Version: "2.14.0"}
	if _, _, ok := ClassifyJailExit(3, probedWithoutFeatures); ok {
		t.Fatal("a 2.x probe classified exit 3")
	}
	if JailExitHint("exit status 3") != "" {
		t.Fatal("a bare exit status 3 was treated as a contract failure")
	}
}

func TestClassifyJailExitErrorReadsTheProcessStatus(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 4")
	err := cmd.Run()
	class, advice, ok := ClassifyJailExitError(err, contract31(t))
	if !ok || class != "blocked" || !strings.Contains(advice, "blocked") {
		t.Fatalf("ClassifyJailExitError() = %q, %q, %v (err %v)", class, advice, ok, err)
	}
	if _, _, ok := ClassifyJailExitError(nil, contract31(t)); ok {
		t.Fatal("a nil error was classified")
	}
}

func TestJailContractFeatureDetectionPrefersTheProbe(t *testing.T) {
	probed := contract31(t)
	if !probed.SupportsHarness("2.14.0") || !probed.SupportsRequestFile("2.14.0") {
		t.Fatal("a probed contract must win over an older version string")
	}
	silent := JailContract{Probed: true}
	if silent.SupportsHarness("3.0.1") || silent.SupportsRequestFile(config.MinJailContractVersion) {
		t.Fatal("a probed contract with the feature off must not fall back to the version")
	}
	unprobed := JailContract{}
	if !unprobed.SupportsHarness("3.0.1") || unprobed.SupportsHarness("3.0.0") || unprobed.SupportsHarness("2.14.0") {
		t.Fatal("version fallback must start at 3.0.1 and stay off for 2.x and 3.0.0")
	}
}

func TestBuildKeepsThe2xArgvWhenTheContractIsAbsent(t *testing.T) {
	cfg := LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		UseMemory:   true,
		JailVersion: "2.14.0",
	}
	argv, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, "\n")
	if strings.Contains(joined, "--harness") || strings.Contains(joined, "--request-file") {
		t.Fatalf("argv = %s; 2.14 must keep the legacy argv", joined)
	}
}

func TestBuildDeclaresTheHarnessFromAProbedContract(t *testing.T) {
	cfg := LaunchConfig{
		Agent:        config.Agent{Command: "oc", Memory: &config.MemoryIntegration{RunHarness: "opencode"}},
		UseJail:      true,
		UseMemory:    true,
		JailVersion:  "3.0.1",
		JailContract: contract31(t),
	}
	argv, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) < 3 || argv[1] != "--harness" || argv[2] != "opencode" {
		t.Fatalf("argv = %#v; want --harness opencode before the command", argv)
	}
}

func TestBuildOmitsHarnessOnContinue(t *testing.T) {
	cfg := LaunchConfig{
		Agent:           config.Agent{Command: "claude"},
		UseJail:         true,
		UseMemory:       true,
		ContinueSession: true,
		JailVersion:     "3.0.1",
		JailContract:    contract31(t),
	}
	argv, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "--harness") {
		t.Fatalf("argv = %#v; continue has no harness token to declare", argv)
	}
}

func TestRequestFileIsForwardedOnlyWhenAdvertised(t *testing.T) {
	base := LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		JailVersion: "3.0.1",
		JailFlags:   config.JailFlags{RequestFile: "/secure/requests.json"},
	}
	with := base
	with.JailContract = contract31(t)
	argv, err := Build(with)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSequence(argv, "--request-file", "/secure/requests.json") {
		t.Fatalf("argv = %#v; want --request-file", argv)
	}
	without := base
	without.JailContract = JailContract{Probed: true}
	argv, err = Build(without)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "--request-file") {
		t.Fatalf("argv = %#v; a contract without request_file must not emit the flag", argv)
	}
	issues := requestFileIssues(without, nil)
	if len(issues) != 1 || issues[0].Code != "request-file-unsupported" {
		t.Fatalf("issues = %#v; want request-file-unsupported", issues)
	}
	old := base
	old.JailVersion = "2.14.0"
	issues = requestFileIssues(old, nil)
	if len(issues) != 1 || issues[0].Code != "request-file-requires-ai-jail-3-0-1" {
		t.Fatalf("issues = %#v; want the 3.0.1 floor", issues)
	}
	unknown := LaunchConfig{UseJail: true, JailFlags: config.JailFlags{RequestFile: "/secure/requests.json"}}
	issues = requestFileIssues(unknown, func() (string, error) { return "", os.ErrNotExist })
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "unknown") {
		t.Fatalf("issues = %#v; an unreadable version is not evidence the flag parses", issues)
	}
	if issues := requestFileIssues(LaunchConfig{JailFlags: config.JailFlags{RequestFile: "/secure/requests.json"}}, nil); issues != nil {
		t.Fatalf("issues = %#v; request_file without a jail is not this launcher's flag", issues)
	}
	if issues := requestFileIssues(LaunchConfig{UseJail: true, JailVersion: "3.0.1"}, nil); issues != nil {
		t.Fatalf("issues = %#v; an empty path emits nothing", issues)
	}
}

func TestInspectRequestFileMatchesTheContract(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	outside := filepath.Join(root, "outside.json")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := inspectRequestFile(outside, project); got != "" {
		t.Fatalf("inspect outside = %q", got)
	}
	if got := inspectRequestFile("requests.json", project); !strings.Contains(got, "absolute") {
		t.Fatalf("relative = %q", got)
	}
	inside := filepath.Join(project, "requests.json")
	if err := os.WriteFile(inside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := inspectRequestFile(inside, project); !strings.Contains(got, "outside") {
		t.Fatalf("inside = %q", got)
	}
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if got := inspectRequestFile(link, project); !strings.Contains(got, "symlink") {
		t.Fatalf("symlink = %q", got)
	}
	loose := filepath.Join(root, "loose.json")
	if err := os.WriteFile(loose, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o666); err != nil { // #nosec G302 -- the mode is the case under test
		t.Fatal(err)
	}
	if got := inspectRequestFile(loose, project); !strings.Contains(got, "writable") {
		t.Fatalf("mode = %q", got)
	}
	hard := filepath.Join(root, "hard.json")
	if err := os.Link(outside, hard); err != nil {
		t.Fatal(err)
	}
	if got := inspectRequestFile(hard, project); !strings.Contains(got, "hard link") {
		t.Fatalf("hard link = %q", got)
	}
	if got := inspectRequestFile(filepath.Join(root, "missing.json"), project); !strings.Contains(got, "does not exist") {
		t.Fatalf("missing = %q", got)
	}
	if got := inspectRequestFile(project, ""); !strings.Contains(got, "regular file") {
		t.Fatalf("directory = %q", got)
	}
	guarded := filepath.Join(root, "guarded.json")
	if err := os.WriteFile(guarded, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if issues := requestFileIssues(LaunchConfig{
		UseJail:     true,
		JailVersion: "3.0.1",
		JailFlags:   config.JailFlags{RequestFile: guarded},
	}, fixedDir(project)); issues != nil {
		t.Fatalf("issues = %#v; a guarded file outside the project is accepted", issues)
	}
}

func TestRequestFileOwnershipRules(t *testing.T) {
	if got := requestFileOwnership(10, 1, 10, true); got != "" {
		t.Fatalf("same owner = %q", got)
	}
	if got := requestFileOwnership(10, 1, 11, true); !strings.Contains(got, "owned") {
		t.Fatalf("other owner = %q", got)
	}
	if got := requestFileOwnership(10, 2, 10, true); !strings.Contains(got, "hard link") {
		t.Fatalf("nlink = %q", got)
	}
	if got := requestFileOwnership(10, 2, 11, false); got != "" {
		t.Fatalf("unknown identity = %q", got)
	}
}

func TestJailStoreDirAndIssues(t *testing.T) {
	home := t.TempDir()
	store, ok := JailStoreDir(home, "")
	if !ok || store != filepath.Join(home, ".config", "ai-jail") {
		t.Fatalf("JailStoreDir() = %q, %v", store, ok)
	}
	custom := filepath.Join(home, "xdg")
	store, ok = JailStoreDir(home, custom)
	if !ok || store != filepath.Join(custom, "ai-jail") {
		t.Fatalf("custom store = %q, %v", store, ok)
	}
	if _, ok := JailStoreDir("", "relative"); ok {
		t.Fatal("a relative XDG_CONFIG_HOME was accepted")
	}
	if _, ok := JailStoreDir("", ""); ok {
		t.Fatal("a store was invented with no home and no XDG base")
	}
	if component, ok := symlinkComponent(""); ok || component != "" {
		t.Fatalf("empty path = %q, %v", component, ok)
	}
	cfg := LaunchConfig{UseJail: true, Agent: config.Agent{Command: "claude"}, JailVersion: "3.0.0"}
	issues := jailStoreIssues(cfg, fixedDir(home), fixedDir(home), "")
	if len(issues) != 1 || issues[0].Code != "jail-xdg-store-inside-project" {
		t.Fatalf("issues = %#v; launching from $HOME contains the store", issues)
	}
	old := cfg
	old.JailVersion = "2.14.0"
	if issues := jailStoreIssues(old, fixedDir(home), fixedDir(home), ""); len(issues) != 0 {
		t.Fatalf("2.14 issues = %#v; the XDG store does not exist yet", issues)
	}
	if issues := jailStoreIssues(cfg, fixedDir(home), fixedDir(home), "relative/config"); len(issues) != 1 || issues[0].Code != "jail-xdg-store-invalid" {
		t.Fatalf("relative XDG issues = %#v", issues)
	}
	outside := t.TempDir()
	linkHome := filepath.Join(outside, "home")
	if err := os.MkdirAll(filepath.Join(linkHome, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "real-store")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(linkHome, ".config", "ai-jail")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(outside, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	issues = jailStoreIssues(cfg, fixedDir(project), fixedDir(linkHome), "")
	if len(issues) != 1 || issues[0].Code != "jail-xdg-store-symlink" {
		t.Fatalf("symlink issues = %#v", issues)
	}
	if issues := jailStoreIssues(cfg, nil, fixedDir(home), ""); issues != nil {
		t.Fatalf("issues = %#v; no working directory means no store judgement", issues)
	}
	probed := LaunchConfig{UseJail: true, JailContract: contract31(t)}
	if !jailUsesExternalStore(probed) {
		t.Fatal("a probed 3.0.1 contract uses the XDG store even without a version string")
	}
}

func TestProbeJailContractReadsJSONAndRemembersAMissingBinary(t *testing.T) {
	resetJailContractCache(t)
	originalLook, originalRun := lookPathCommand, runJailCommand
	t.Cleanup(func() { lookPathCommand, runJailCommand = originalLook, originalRun })

	lookPathCommand = func(string) (string, error) { return "", os.ErrNotExist }
	if got := ProbeJailContract(); got.Probed || got.Path != "" {
		t.Fatalf("missing binary = %#v; want an unprobed contract", got)
	}

	resetJailContractCache(t)
	lookPathCommand = func(string) (string, error) { return "/bin/ai-jail", nil }
	runJailCommand = func(string, ...string) ([]byte, error) {
		return []byte(sampleCapabilities), nil
	}
	got := ProbeJailContract()
	if !got.Probed || !got.Features.HarnessFlag || got.Path != "/bin/ai-jail" {
		t.Fatalf("parsed contract = %#v", got)
	}
}

func TestProbeJailContractMemoizesAMissingContract(t *testing.T) {
	resetJailContractCache(t)
	calls := 0
	originalLook, originalRun := lookPathCommand, runJailCommand
	lookPathCommand = func(string) (string, error) { return "/bin/ai-jail", nil }
	runJailCommand = func(string, ...string) ([]byte, error) {
		calls++
		return []byte("error: unrecognized subcommand 'capabilities'\n"), nil
	}
	t.Cleanup(func() { lookPathCommand, runJailCommand = originalLook, originalRun })
	first := ProbeJailContract()
	second := ProbeJailContract()
	if !first.Probed || first.Features.HarnessFlag || second.Features.RequestFile {
		t.Fatalf("contract = %#v; a 2.x binary is probed and has no features", first)
	}
	if calls != 1 {
		t.Fatalf("capabilities probed %d times", calls)
	}
	if _, ok := ProbeJailStatus(first); ok {
		t.Fatal("status ran without status_json")
	}
}

func TestProbeJailStatusParsesJSON(t *testing.T) {
	original := runJailCommand
	runJailCommand = func(_ string, args ...string) ([]byte, error) {
		if len(args) == 0 || args[0] != "status" {
			t.Fatalf("args = %#v", args)
		}
		return []byte(`{"version":"3.0.1","schema_version":1,"blockers":[{"code":"backend_unavailable","message":"bwrap missing"}]}`), nil
	}
	t.Cleanup(func() { runJailCommand = original })
	contract := contract31(t)
	contract.Path = "/bin/ai-jail"
	status, ok := ProbeJailStatus(contract)
	if !ok {
		t.Fatal("status JSON was not accepted")
	}
	lines := FormatJailStatus(status)
	if len(lines) != 1 || !strings.Contains(lines[0], "backend_unavailable") {
		t.Fatalf("lines = %#v", lines)
	}
}

func resetJailContractCache(t *testing.T) {
	t.Helper()
	jailContractCache.once = sync.Once{}
	jailContractCache.value = JailContract{}
	t.Cleanup(func() {
		jailContractCache.once = sync.Once{}
		jailContractCache.value = JailContract{}
	})
}

func containsSequence(argv []string, want ...string) bool {
	if len(want) == 0 || len(argv) < len(want) {
		return false
	}
	for i := 0; i+len(want) <= len(argv); i++ {
		match := true
		for j := range want {
			if argv[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
