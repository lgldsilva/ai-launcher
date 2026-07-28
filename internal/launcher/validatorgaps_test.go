package launcher

import (
	"os"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
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
