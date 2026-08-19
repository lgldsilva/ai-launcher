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

// An internal Compose network blocks ALL outbound traffic, including the
// agent's own LLM API calls, and silently does nothing when no Compose
// service is selected (BuildCompose only runs with at least one service).
// Both surprises need their own warning so the operator sees them before a
// launch quietly fails or the toggle turns out to be a no-op.
func TestContainerNetworkInternalWarnsOnBlockedAgentAndOnRequiresCompose(t *testing.T) {
	cases := []struct {
		name string
		cfg  LaunchConfig
		code string
	}{
		{
			name: "docker with a service and internal network warns it blocks the agent",
			cfg: LaunchConfig{
				UseDocker: true,
				Services:  []string{"redis"},
				Docker:    container.RunConfig{NetworkInternal: true},
			},
			code: "internal-network-blocks-agent",
		},
		{
			name: "docker with internal network but no services warns it requires compose",
			cfg: LaunchConfig{
				UseDocker: true,
				Docker:    container.RunConfig{NetworkInternal: true},
			},
			code: "internal-network-requires-compose",
		},
		{
			name: "docker with a service, internal network, and allowed domains warns it restricts (not blocks) the agent",
			cfg: LaunchConfig{
				UseDocker:                      true,
				Services:                       []string{"redis"},
				Docker:                         container.RunConfig{NetworkInternal: true},
				ContainerNetworkAllowedDomains: []string{"api.anthropic.com"},
			},
			code: "internal-network-restricts-agent",
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
			if !issue.Warning {
				t.Error("the launch must still be allowed to proceed; this is advisory")
			}
		})
	}
}

func TestContainerNetworkAllowedDomainsWarnsWithoutInternalNetwork(t *testing.T) {
	cases := []struct {
		name string
		cfg  LaunchConfig
		code string
	}{
		{
			name: "domains configured without internal network warns the allowlist is inert",
			cfg: LaunchConfig{
				UseDocker:                      true,
				Services:                       []string{"redis"},
				ContainerNetworkAllowedDomains: []string{"api.anthropic.com"},
			},
			code: "container-network-allowed-domains-without-internal-network",
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
			if !issue.Warning {
				t.Error("the launch must still be allowed to proceed; this is advisory")
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
		Agent:     config.Agent{Command: "claude"},
		UseDocker: true,
		Workspace: workspace,
		Docker:    container.RunConfig{Selection: selection},
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
