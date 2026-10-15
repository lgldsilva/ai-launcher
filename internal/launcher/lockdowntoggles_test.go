package launcher

import (
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// ai-jail 2.6.2 made --lockdown always force --unshare-net, so lockdown +
// unrestricted network flipped from networked to offline with the same argv.
// Pre-flight surfaces the flip on the releases where it applies; on older
// jails the combination still means network and stays silent.
func TestLockdownNetworkIssuesFollowTheUnshareFlip(t *testing.T) {
	base := func(version string, permissions map[string]bool) LaunchConfig {
		if permissions == nil {
			permissions = map[string]bool{}
		}
		return LaunchConfig{
			Agent:       config.Agent{Command: "claude"},
			UseJail:     true,
			JailVersion: version,
			JailFlags:   config.JailFlags{Lockdown: boolPtr(true), Network: boolPtr(true)},
			Permissions: permissions,
		}
	}
	issue, ok := issueByCode(jailVersionValidate(base("2.9.0", nil)), "lockdown-forces-offline")
	if !ok || !issue.Warning {
		t.Fatalf("preflight stayed silent about lockdown + network on 2.9.0: %#v", issue)
	}
	// The network permission is the other way the same contradiction forms.
	permOnly := LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		JailVersion: "2.6.2",
		JailFlags:   config.JailFlags{Lockdown: boolPtr(true)},
		Permissions: map[string]bool{config.PermissionNetwork: true},
	}
	if _, ok := issueByCode(jailVersionValidate(permOnly), "lockdown-forces-offline"); !ok {
		t.Fatal("preflight stayed silent about lockdown + network permission on 2.6.2")
	}
	for _, tc := range []struct {
		name    string
		version string
	}{
		{"before the flip", "2.6.1"},
		{"the old argv floor", "1.20.1"},
	} {
		if _, ok := issueByCode(jailVersionValidate(base(tc.version, nil)), "lockdown-forces-offline"); ok {
			t.Fatalf("%s: lockdown kept the network there; no warning owed", tc.name)
		}
	}
	// Filtered egress is not unrestricted network: lockdown + allow_hosts is
	// a coherent offline-plus-allowlist launch and stays silent.
	filtered := LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		JailVersion: "2.9.0",
		JailFlags:   config.JailFlags{Lockdown: boolPtr(true), AllowHosts: []string{"api.anthropic.com"}},
	}
	if _, ok := issueByCode(jailVersionValidate(filtered), "lockdown-forces-offline"); ok {
		t.Fatal("lockdown + filtered egress warned; only unrestricted network flips")
	}
	// No lockdown, no contradiction.
	plain := jailVersionConfig("2.9.0")
	if _, ok := issueByCode(jailVersionValidate(plain), "lockdown-forces-offline"); ok {
		t.Fatal("a launch without lockdown warned about lockdown")
	}
}
