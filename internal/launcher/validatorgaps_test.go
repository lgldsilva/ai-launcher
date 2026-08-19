package launcher

import (
	"os"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
)

// issueByCode returns the first issue with the given code, or false.
func issueByCode(issues []Issue, code string) (Issue, bool) {
	for _, issue := range issues {
		if issue.Code == code {
			return issue, true
		}
	}
	return Issue{}, false
}

// ai-jail classifies --allow-tcp-port as lockdown-only, so without lockdown the
// builder emits flags upstream silently ignores: the operator believes ports
// are open when nothing is. The warning is the only thing standing between that
// belief and a debugging session, so each arm of the condition is pinned.
func TestAllowTCPPortsWarnOnlyWhenLockdownIsNotForcedOn(t *testing.T) {
	base := LaunchConfig{UseJail: true}
	base.JailFlags.AllowTCPPorts = []int{8080}

	cases := []struct {
		name string
		cfg  func() LaunchConfig
		warn bool
	}{
		{
			name: "lockdown unset leaves the ports inert",
			cfg:  func() LaunchConfig { return base },
			warn: true,
		},
		{
			name: "lockdown explicitly off leaves the ports inert",
			cfg: func() LaunchConfig {
				cfg := base
				cfg.JailFlags.Lockdown = boolPtr(false)
				return cfg
			},
			warn: true,
		},
		{
			name: "lockdown forced on makes the ports take effect",
			cfg: func() LaunchConfig {
				cfg := base
				cfg.JailFlags.Lockdown = boolPtr(true)
				return cfg
			},
			warn: false,
		},
		{
			name: "no ports configured",
			cfg:  func() LaunchConfig { return LaunchConfig{UseJail: true} },
			warn: false,
		},
		{
			name: "jail off; jail-options-without-jail already covers it",
			cfg: func() LaunchConfig {
				cfg := base
				cfg.UseJail = false
				return cfg
			},
			warn: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := allowTCPPortIssues(tc.cfg())
			issue, found := issueByCode(issues, "allow-tcp-ports-without-lockdown")
			if found != tc.warn {
				t.Fatalf("warning present = %v; want %v (issues = %#v)", found, tc.warn, issues)
			}
			if found && !issue.Warning {
				t.Error("the launch must still be allowed to proceed; this is advisory")
			}
		})
	}
}

// An internal Compose network is classified two ways. With a service selected
// it takes effect and blocks ALL outbound traffic including the agent's own
// LLM API calls — that is a legitimate operator choice, so it stays advisory.
// With no service selected it is a silent no-op (the plain docker run backend
// has no Compose network), so the operator believes egress is blocked while it
// remains open — a false sense of security, which must be fatal, not warned.
func TestContainerNetworkInternalWarnsOnBlockedAgentButRejectsRequiresCompose(t *testing.T) {
	cases := []struct {
		name  string
		cfg   LaunchConfig
		code  string
		fatal bool
	}{
		{
			name: "docker with a service and internal network warns it blocks the agent",
			cfg: LaunchConfig{
				UseDocker: true,
				Services:  []string{"redis"},
				Docker:    container.RunConfig{NetworkInternal: true},
			},
			code:  "internal-network-blocks-agent",
			fatal: false,
		},
		{
			name: "docker with internal network but no services is rejected (no-op would reopen egress)",
			cfg: LaunchConfig{
				UseDocker: true,
				Docker:    container.RunConfig{NetworkInternal: true},
			},
			code:  "internal-network-requires-compose",
			fatal: true,
		},
		{
			name: "docker with a service, internal network, and allowed domains warns it restricts (not blocks) the agent",
			cfg: LaunchConfig{
				UseDocker:                      true,
				Services:                       []string{"redis"},
				Docker:                         container.RunConfig{NetworkInternal: true},
				ContainerNetworkAllowedDomains: []string{"api.anthropic.com"},
			},
			code:  "internal-network-restricts-agent",
			fatal: false,
		},
		{
			name: "internal network off raises nothing",
			cfg: LaunchConfig{
				UseDocker: true,
				Services:  []string{"redis"},
			},
			code: "",
		},
		{
			name: "jail mode ignores the docker-only setting",
			cfg: LaunchConfig{
				UseDocker: false,
				Docker:    container.RunConfig{NetworkInternal: true},
			},
			code: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := containerNetworkInternalIssues(tc.cfg)
			if tc.code == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %#v; want none", issues)
				}
				return
			}
			issue, found := issueByCode(issues, tc.code)
			if !found {
				t.Fatalf("issues = %#v; want code %q", issues, tc.code)
			}
			if issue.Warning != !tc.fatal {
				t.Errorf("issue %q Warning = %v; want fatal=%v", tc.code, issue.Warning, tc.fatal)
			}
		})
	}
}

func TestContainerNetworkAllowedDomainsRejectsWithoutInternalNetwork(t *testing.T) {
	cases := []struct {
		name  string
		cfg   LaunchConfig
		code  string
		fatal bool
	}{
		{
			name: "domains configured without internal network is rejected (allowlist would be inert, egress open)",
			cfg: LaunchConfig{
				UseDocker:                      true,
				Services:                       []string{"redis"},
				ContainerNetworkAllowedDomains: []string{"api.anthropic.com"},
			},
			code:  "container-network-allowed-domains-without-internal-network",
			fatal: true,
		},
		{
			name: "domains configured with internal network on raises nothing here",
			cfg: LaunchConfig{
				UseDocker:                      true,
				Services:                       []string{"redis"},
				Docker:                         container.RunConfig{NetworkInternal: true},
				ContainerNetworkAllowedDomains: []string{"api.anthropic.com"},
			},
			code: "",
		},
		{
			name: "no domains configured raises nothing",
			cfg: LaunchConfig{
				UseDocker: true,
				Services:  []string{"redis"},
			},
			code: "",
		},
		{
			name: "jail mode ignores the docker-only allowlist (no fatal, mirrors internal-network)",
			cfg: LaunchConfig{
				UseDocker:                      false,
				ContainerNetworkAllowedDomains: []string{"api.anthropic.com"},
			},
			code: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := containerNetworkAllowedDomainsIssues(tc.cfg)
			if tc.code == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %#v; want none", issues)
				}
				return
			}
			issue, found := issueByCode(issues, tc.code)
			if !found {
				t.Fatalf("issues = %#v; want code %q", issues, tc.code)
			}
			if issue.Warning != !tc.fatal {
				t.Errorf("issue %q Warning = %v; want fatal=%v", tc.code, issue.Warning, tc.fatal)
			}
		})
	}
}

// WithPermissions is how the launch path hands pre-flight the *effective*
// catalog, so a hand-edited permissions[].platforms drives the TUI and the
// validator identically. Without it the validator falls back to the built-in
// defaults and silently disagrees with the config the operator is reading.
func TestWithPermissionsOverridesTheBuiltInPlatformRules(t *testing.T) {
	cfg := LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		Permissions: map[string]bool{"mise": true},
	}
	lookPath := func(string) (string, error) { return "/bin/stub", nil }
	stat := func(string) (os.FileInfo, error) { return nil, nil }
	base := Validator{LookPath: lookPath, Stat: stat, GOOS: "darwin"}

	// Built-in catalog: mise has no Platforms list, so it is supported anywhere.
	if _, found := issueByCode(base.Validate(cfg), "unsupported-platform"); found {
		t.Fatal("the built-in catalog leaves mise unrestricted")
	}

	// An operator who restricted mise to Linux must see that reflected here.
	restricted := base.WithPermissions([]config.Permission{
		{ID: "mise", Name: "mise integration", Platforms: []string{"linux"}},
	})
	issue, found := issueByCode(restricted.Validate(cfg), "unsupported-platform")
	if !found {
		t.Fatal("the supplied catalog restricted mise to linux; darwin must warn")
	}
	if !strings.Contains(issue.Message, "mise") || !strings.Contains(issue.Message, "darwin") {
		t.Errorf("message = %q; want the permission and the platform named", issue.Message)
	}
	if !issue.Warning {
		t.Error("an unsupported permission is advisory, not fatal")
	}

	// WithPermissions returns a copy: the receiver must not be mutated, or one
	// launch's catalog would leak into the next validator built from it.
	if len(base.Permissions) != 0 {
		t.Errorf("base.Permissions = %#v; WithPermissions must not mutate its receiver", base.Permissions)
	}
}

// dockerValidatorConfig is a minimal docker-backend launch whose only variable
// is the project directory under test.
func dockerValidatorConfig(t *testing.T, workspace string) LaunchConfig {
	t.Helper()
	selection, err := container.Normalize(
		[]string{"go"},
		[]container.AgentInstall{{Command: "claude", Kind: container.InstallRelease, Version: "2.1.0"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	return LaunchConfig{
		Agent:      config.Agent{Command: "claude"},
		UseDocker:  true,
		ProjectDir: workspace,
		Docker:     container.RunConfig{Selection: selection},
	}
}

func dockerValidatorIssues(cfg LaunchConfig) []Issue {
	return Validator{
		LookPath: func(string) (string, error) { return "/usr/bin/docker", nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
		GOOS:     "linux",
	}.Validate(cfg)
}

// The container backend borrows Workspace as a directory, so an ai-memory scope
// name reaches Docker as a mount source. Until the fields are separated,
// pre-flight is what turns that into a refusal the operator can act on instead
// of an opaque daemon error about an invalid mount.
func TestDockerIssuesRejectARelativeProjectDir(t *testing.T) {
	issue, ok := issueByCode(dockerValidatorIssues(dockerValidatorConfig(t, "meu-time")), "docker-project-dir-not-absolute")
	if !ok {
		t.Fatal("preflight accepted a relative container project directory")
	}
	if issue.Warning {
		t.Error("the issue is a warning; a launch that cannot mount the project must not proceed")
	}
	if !strings.Contains(issue.Message, "meu-time") {
		t.Errorf("message = %q; want it naming the offending value", issue.Message)
	}
}

// The same configuration with an absolute directory is clean, so the check
// cannot be passing for an unrelated reason.
func TestDockerIssuesAcceptAnAbsoluteProjectDir(t *testing.T) {
	if issue, ok := issueByCode(dockerValidatorIssues(dockerValidatorConfig(t, "/work")), "docker-project-dir-not-absolute"); ok {
		t.Fatalf("issue = %v; an absolute project directory must be accepted", issue)
	}
}

// A workspace saved by the old docker autosave keeps launching: ai-memory
// accepts any string as a scope name, so the only symptom is a phantom,
// path-shaped workspace splitting the project's history. The warning is how an
// operator finds the one already sitting in their config.
func TestMemoryScopePathIssuesWarnAboutAPathShapedScope(t *testing.T) {
	dir := t.TempDir()
	cfg := LaunchConfig{
		Agent:     config.Agent{Command: "claude"},
		UseMemory: true,
		Workspace: dir,
	}
	issues := Validator{
		LookPath: func(string) (string, error) { return "/usr/bin/" + aiMemoryCommand, nil },
		Stat:     os.Stat,
		GOOS:     "linux",
	}.Validate(cfg)

	issue, ok := issueByCode(issues, "memory-scope-looks-like-a-path")
	if !ok {
		t.Fatalf("issues = %v; want memory-scope-looks-like-a-path", issues)
	}
	if !issue.Warning {
		t.Error("the issue is fatal; a path-shaped scope still launches and must not block")
	}
	if !strings.Contains(issue.Message, dir) {
		t.Errorf("message = %q; want it naming the offending value", issue.Message)
	}
}

// An ordinary scope name, a relative string, and a path that does not exist are
// all left alone: only an existing absolute directory is evidence of the bug.
func TestMemoryScopePathIssuesIgnoreOrdinaryScopes(t *testing.T) {
	for _, name := range []string{"acme", "billing/eu", "/no/such/directory/here"} {
		cfg := LaunchConfig{
			Agent:     config.Agent{Command: "claude"},
			UseMemory: true,
			Workspace: name,
		}
		issues := Validator{
			LookPath: func(string) (string, error) { return "/usr/bin/" + aiMemoryCommand, nil },
			Stat:     os.Stat,
			GOOS:     "linux",
		}.Validate(cfg)
		if issue, ok := issueByCode(issues, "memory-scope-looks-like-a-path"); ok {
			t.Errorf("workspace %q produced %v; want no warning", name, issue)
		}
	}
}

// The check belongs to the memory integration: without it there is no scope.
func TestMemoryScopePathIssuesRequireMemory(t *testing.T) {
	dir := t.TempDir()
	cfg := LaunchConfig{
		Agent:     config.Agent{Command: "claude"},
		UseMemory: false,
		Workspace: dir,
	}
	issues := Validator{
		LookPath: func(string) (string, error) { return "/usr/bin/claude", nil },
		Stat:     os.Stat,
		GOOS:     "linux",
	}.Validate(cfg)
	if issue, ok := issueByCode(issues, "memory-scope-looks-like-a-path"); ok {
		t.Errorf("issue = %v; without memory there is no scope to warn about", issue)
	}
}

// jailVersionConfig is a minimal jail launch whose only variable is the
// detected ai-jail version.
func jailVersionConfig(version string) LaunchConfig {
	return LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		JailVersion: version,
	}
}

func jailVersionValidate(cfg LaunchConfig) []Issue {
	return Validator{
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
		GOOS:     "linux",
	}.Validate(cfg)
}

// Below the floor the launcher emits flags the installed ai-jail rejects with
// "unknown option", raised inside the wrapper where it reads as the launcher
// being broken. Pre-flight names the real problem instead.
func TestJailVersionIssuesRejectAnInstallBelowTheFloor(t *testing.T) {
	issue, ok := issueByCode(jailVersionValidate(jailVersionConfig("1.14.2")), "jail-version-too-old")
	if !ok {
		t.Fatal("preflight accepted an ai-jail below the floor")
	}
	if issue.Warning {
		t.Error("the issue is a warning; the composed argv cannot run on this build")
	}
	if !strings.Contains(issue.Message, "1.14.2") || !strings.Contains(issue.Message, "--no-jail") {
		t.Errorf("message = %q; want the detected version and a way forward", issue.Message)
	}
}

// Above the tested bound the launch proceeds: a newer upstream may work, and
// refusing would strand every operator the day a release lands.
func TestJailVersionIssuesWarnAboveTheTestedBound(t *testing.T) {
	issue, ok := issueByCode(jailVersionValidate(jailVersionConfig(config.UntestedAIJailVersion)), "jail-version-untested")
	if !ok {
		t.Fatal("preflight said nothing about an ai-jail above the validated range")
	}
	if !issue.Warning {
		t.Error("the issue is fatal; a newer ai-jail must warn, not block")
	}
}

// A version inside the range, an unreadable probe, and a jail-less launch are
// all silent — the last two because "unknown" is not evidence of anything.
func TestJailVersionIssuesStaySilentWhenThereIsNothingToSay(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  LaunchConfig
	}{
		{"inside the range", jailVersionConfig(config.MinAIJailVersion)},
		{"probe failed", jailVersionConfig("")},
		{"jail disabled", LaunchConfig{Agent: config.Agent{Command: "claude"}, JailVersion: "1.14.2"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, issue := range jailVersionValidate(tt.cfg) {
				if strings.HasPrefix(issue.Code, "jail-version-") {
					t.Errorf("issue = %v; want no version judgement", issue)
				}
			}
		})
	}
}
