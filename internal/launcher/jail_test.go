package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func boolPtr(value bool) *bool { return &value }

func buildWithJailFlags(t *testing.T, flags config.JailFlags) []string {
	t.Helper()
	argv, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		Permissions: map[string]bool{},
		JailFlags:   flags,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return argv
}

func TestBuildMapsDefaultOffTogglesInBothForms(t *testing.T) {
	for name, toggle := range map[string]func(*config.JailFlags) **bool{
		"--lockdown":     func(f *config.JailFlags) **bool { return &f.Lockdown },
		"--private-home": func(f *config.JailFlags) **bool { return &f.PrivateHome },
		"--tailscale":    func(f *config.JailFlags) **bool { return &f.Tailscale },
	} {
		var flags config.JailFlags
		*toggle(&flags) = boolPtr(true)
		if argv := buildWithJailFlags(t, flags); !reflect.DeepEqual(argv, []string{"ai-jail", name, "claude"}) {
			t.Errorf("enabled %s: argv = %#v", name, argv)
		}
		*toggle(&flags) = boolPtr(false)
		negative := strings.Replace(name, "--", "--no-", 1)
		if argv := buildWithJailFlags(t, flags); !reflect.DeepEqual(argv, []string{"ai-jail", negative, "claude"}) {
			t.Errorf("disabled %s: argv = %#v; want %s", name, argv, negative)
		}
	}
}

func TestBuildMapsDefaultOnTogglesOnlyWhenDisabled(t *testing.T) {
	for name, toggle := range map[string]func(*config.JailFlags) **bool{
		"--no-gpu":        func(f *config.JailFlags) **bool { return &f.GPU },
		"--no-landlock":   func(f *config.JailFlags) **bool { return &f.Landlock },
		"--no-seccomp":    func(f *config.JailFlags) **bool { return &f.Seccomp },
		"--no-rlimits":    func(f *config.JailFlags) **bool { return &f.Rlimits },
		"--no-status-bar": func(f *config.JailFlags) **bool { return &f.StatusBar },
	} {
		var flags config.JailFlags
		*toggle(&flags) = boolPtr(false)
		if argv := buildWithJailFlags(t, flags); !reflect.DeepEqual(argv, []string{"ai-jail", name, "claude"}) {
			t.Errorf("disabled toggle: argv = %#v; want %s", argv, name)
		}
		*toggle(&flags) = boolPtr(true)
		if argv := buildWithJailFlags(t, flags); !reflect.DeepEqual(argv, []string{"ai-jail", "claude"}) {
			t.Errorf("explicitly enabled default-on toggle must emit nothing: argv = %#v", argv)
		}
	}
}

func TestBuildMapsJailListFlagsBrowserAndClaudeDir(t *testing.T) {
	argv := buildWithJailFlags(t, config.JailFlags{
		OverlayMaps:   []string{"/data", " "},
		Mask:          []string{"/etc/secrets"},
		DenyPaths:     []string{"/proc/kcore"},
		AllowTCPPorts: []int{8080, 443},
		Browser:       "hard",
		ClaudeDir:     "/home/tester/.claude",
	})
	want := []string{
		"ai-jail",
		"--overlay-map", "/data",
		"--mask", "/etc/secrets",
		"--deny-path", "/proc/kcore",
		"--allow-tcp-port", "8080",
		"--allow-tcp-port", "443",
		"--browser=hard",
		"--claude-dir", "/home/tester/.claude",
		"claude",
	}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}

func TestBuildMapsBrowserProfiles(t *testing.T) {
	for browser, flag := range map[string]string{"soft": "--browser=soft", "HARD": "--browser=hard", "off": "--no-browser"} {
		argv := buildWithJailFlags(t, config.JailFlags{Browser: browser})
		if !reflect.DeepEqual(argv, []string{"ai-jail", flag, "claude"}) {
			t.Errorf("browser %q: argv = %#v; want %s", browser, argv, flag)
		}
	}
}

func TestBuildJailExecEnablesProgrammaticMode(t *testing.T) {
	argv, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		UseMemory:   true,
		JailExec:    true,
		Permissions: map[string]bool{},
	})
	want := []string{"ai-jail", "--exec", "ai-memory", "run", "claude"}
	if err != nil || !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", argv, err, want)
	}
	// The interactive path (no JailExec) leaves the ai-jail PTY defaults alone.
	argv, err = Build(LaunchConfig{Agent: config.Agent{Command: "claude"}, UseJail: true, Permissions: map[string]bool{}})
	if err != nil || !reflect.DeepEqual(argv, []string{"ai-jail", "claude"}) {
		t.Fatalf("interactive Build() = %#v, %v", argv, err)
	}
}

func TestBuildForwardsMemoryScopeAndWorkstream(t *testing.T) {
	argv, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "codex"},
		UseMemory:   true,
		Workspace:   "acme",
		Project:     "billing",
		Workstream:  "release-1",
		Permissions: map[string]bool{},
	})
	want := []string{"ai-memory", "run", "--workspace", "acme", "--project", "billing", "--workstream", "release-1", "codex"}
	if err != nil || !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", argv, err, want)
	}
}

func TestBuildNewWorkstreamWinsOverResume(t *testing.T) {
	argv, err := Build(LaunchConfig{
		Agent:         config.Agent{Command: "codex"},
		UseMemory:     true,
		NewWorkstream: "fresh",
		Workstream:    "old",
	})
	want := []string{"ai-memory", "run", "--new", "fresh", "codex"}
	if err != nil || !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", argv, err, want)
	}
}

func TestBuildContinueRunsAiMemoryWithoutHarness(t *testing.T) {
	argv, err := Build(LaunchConfig{
		ContinueSession: true,
		UseJail:         true,
		UseMemory:       true,
		JailExec:        true,
		Workspace:       "acme",
		Permissions:     map[string]bool{},
	})
	want := []string{"ai-jail", "--exec", "ai-memory", "run", "--workspace", "acme"}
	if err != nil || !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", argv, err, want)
	}
	if _, err := Build(LaunchConfig{ContinueSession: true}); err == nil {
		t.Fatal("continue without ai-memory should fail")
	}
}

func TestEnvironmentPropagatesAuthToken(t *testing.T) {
	t.Setenv("AI_MEMORY_AUTH_TOKEN", "stale")
	env := Environment(LaunchConfig{UseMemory: true, MemoryAuthToken: " s3cret "})
	count := 0
	for _, entry := range env {
		if entry == "AI_MEMORY_AUTH_TOKEN=s3cret" {
			count++
		}
		if entry == "AI_MEMORY_AUTH_TOKEN=stale" {
			t.Fatal("stale token was not replaced")
		}
	}
	if count != 1 {
		t.Fatalf("AI_MEMORY_AUTH_TOKEN entries = %d; want exactly one", count)
	}
}

func TestEnvironmentOmitsAuthTokenWhenUnsetOrMemoryOff(t *testing.T) {
	for _, cfg := range []LaunchConfig{
		{UseMemory: true},
		{UseMemory: false, MemoryAuthToken: "s3cret"},
	} {
		t.Setenv("AI_MEMORY_AUTH_TOKEN", "")
		for _, entry := range Environment(cfg) {
			if strings.HasPrefix(entry, "AI_MEMORY_AUTH_TOKEN=") && entry != "AI_MEMORY_AUTH_TOKEN=" {
				t.Fatalf("token leaked into environment for %#v: %q", cfg, entry)
			}
		}
	}
}

func TestEnvironmentSetsNativeBinWhenManagedRunnerExists(t *testing.T) {
	home := t.TempDir()
	native := filepath.Join(home, ".local", "share", "ai-launcher", "bin", "ai-memory")
	if err := os.MkdirAll(filepath.Dir(native), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("native"), 0o755); err != nil { // #nosec G306 -- the fixture must be executable like the real managed binary
		t.Fatal(err)
	}
	env := Environment(LaunchConfig{UseMemory: true, HomeDir: home})
	count := 0
	for _, entry := range env {
		if entry == "AI_MEMORY_NATIVE_BIN="+native {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("AI_MEMORY_NATIVE_BIN entries = %d; want exactly one pointing at %q", count, native)
	}
}

func TestEnvironmentOmitsNativeBinWhenManagedRunnerMissingOrMemoryOff(t *testing.T) {
	home := t.TempDir()
	native := filepath.Join(home, ".local", "share", "ai-launcher", "bin", "ai-memory")
	for _, cfg := range []LaunchConfig{
		{UseMemory: true, HomeDir: home},
		{UseMemory: false, HomeDir: home},
	} {
		for _, entry := range Environment(cfg) {
			if strings.HasPrefix(entry, "AI_MEMORY_NATIVE_BIN=") {
				t.Fatalf("AI_MEMORY_NATIVE_BIN set without a managed binary or memory: %q", entry)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(native), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("native"), 0o755); err != nil { // #nosec G306 -- the fixture must be executable like the real managed binary
		t.Fatal(err)
	}
	for _, entry := range Environment(LaunchConfig{UseMemory: false, HomeDir: home}) {
		if strings.HasPrefix(entry, "AI_MEMORY_NATIVE_BIN=") {
			t.Fatalf("AI_MEMORY_NATIVE_BIN set with memory disabled: %q", entry)
		}
	}
}

func TestManagedNativeRunnerPathAppendsExeForWindowsTarget(t *testing.T) {
	originalWindows := isWindows
	isWindows = func() bool { return true }
	t.Cleanup(func() { isWindows = originalWindows })
	originalHome := userHomeDir
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = originalHome })

	path := managedNativeRunnerPath("")
	if !strings.HasSuffix(path, filepath.Join("ai-launcher", "bin", "ai-memory.exe")) {
		t.Fatalf("windows managed path = %q; want the ai-memory.exe suffix under ai-launcher/bin", path)
	}
	isWindows = func() bool { return false }
	path = managedNativeRunnerPath("")
	if strings.HasSuffix(path, ".exe") {
		t.Fatalf("linux managed path = %q; want no .exe suffix", path)
	}
}

func TestConstrainToPlatformDropsJailOnWindows(t *testing.T) {
	cfg := LaunchConfig{
		UseJail:     true,
		Permissions: map[string]bool{"jail": true, "ssh": true, "gpu": true, "docker": true},
	}
	constrained, issues := ConstrainToPlatform(cfg, "windows", config.DefaultGlobal().Permissions)
	if constrained.UseJail {
		t.Fatal("jail must be dropped on Windows")
	}
	for id, enabled := range constrained.Permissions {
		if enabled {
			t.Errorf("permission %q must be disabled on Windows", id)
		}
	}
	if len(issues) != 1 || issues[0].Code != "jail-unsupported-windows" || !issues[0].Warning {
		t.Fatalf("issues = %#v; want one jail-unsupported-windows warning", issues)
	}
	if !strings.Contains(issues[0].Message, "ssh") {
		t.Fatalf("warning should list dropped permissions: %q", issues[0].Message)
	}
}

func TestConstrainToPlatformIsANoOpElsewhere(t *testing.T) {
	cfg := LaunchConfig{UseJail: true, Permissions: map[string]bool{"ssh": true}}
	constrained, issues := ConstrainToPlatform(cfg, "linux", config.DefaultGlobal().Permissions)
	if len(issues) != 0 || !constrained.UseJail || !constrained.Permissions["ssh"] {
		t.Fatalf("linux constraint = %#v, %#v; want unchanged", constrained, issues)
	}
	constrained, issues = ConstrainToPlatform(LaunchConfig{Permissions: map[string]bool{}}, "windows", config.DefaultGlobal().Permissions)
	if len(issues) != 0 {
		t.Fatalf("windows without jail produced issues: %#v", issues)
	}
	_ = constrained
}

func TestValidatorWarnsForJailOptionsWithoutJail(t *testing.T) {
	v := Validator{
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := v.Validate(LaunchConfig{
		Agent:     config.Agent{Command: "claude"},
		JailFlags: config.JailFlags{Lockdown: boolPtr(true)},
	})
	if len(issues) != 1 || issues[0].Code != "jail-options-without-jail" || !issues[0].Warning {
		t.Fatalf("issues = %#v; want one jail-options-without-jail warning", issues)
	}
}

func TestValidatorWarnsInsteadOfFailingForWindowsJail(t *testing.T) {
	v := Validator{
		GOOS:     "windows",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := v.Validate(LaunchConfig{Agent: config.Agent{Command: "codex"}, UseJail: true})
	if len(issues) != 1 || issues[0].Code != "jail-unsupported-windows" || !issues[0].Warning {
		t.Fatalf("windows jail issues = %#v; want one jail-unsupported-windows warning", issues)
	}
	issues = v.Validate(LaunchConfig{Agent: config.Agent{Command: "codex"}, Permissions: map[string]bool{"ssh": true}})
	if len(issues) != 1 || issues[0].Code != "permission-without-jail" || !issues[0].Warning {
		t.Fatalf("windows permission issues = %#v; want one permission-without-jail warning", issues)
	}
}

func TestValidatorSkipsAgentLookupForContinueSessions(t *testing.T) {
	v := Validator{
		LookPath: func(command string) (string, error) {
			if command == "ai-memory" {
				return "/bin/ai-memory", nil
			}
			return "", errors.New("missing")
		},
		Stat: func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := v.Validate(LaunchConfig{ContinueSession: true, UseMemory: true})
	if len(issues) != 0 {
		t.Fatalf("continue session produced issues: %#v", issues)
	}
}
