package launcher

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestBuildUsesAllowHostAsFilteredEgress(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		JailVersion: config.MinAllowHostAIJailVersion,
		Permissions: map[string]bool{config.PermissionNetwork: true},
		JailFlags:   config.JailFlags{AllowHosts: []string{" api.anthropic.com ", "github.com"}},
	})
	want := []string{
		"ai-jail", "--no-docker", "--no-network",
		"--allow-host", "api.anthropic.com",
		"--allow-host", "github.com",
		"claude",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
	}
}

func TestBuildLeavesNetworkAloneWhenAllowHostsDoNotApply(t *testing.T) {
	cases := []struct {
		name    string
		version string
		hosts   []string
	}{
		{"older than 2.0", "1.20.1", []string{"api.anthropic.com"}},
		{"blank entries", "2.2.0", []string{"  ", ""}},
		{"not a hostname", "2.2.0", []string{"https://api.anthropic.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Build(LaunchConfig{
				Agent:       config.Agent{Command: "claude"},
				UseJail:     true,
				JailVersion: tc.version,
				Permissions: map[string]bool{config.PermissionNetwork: true},
				JailFlags:   config.JailFlags{AllowHosts: tc.hosts},
			})
			want := []string{"ai-jail", "--no-docker", "--network", "claude"}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("Build() = %#v, %v; want %#v", got, err, want)
			}
		})
	}
}

func TestAllowHostIssues(t *testing.T) {
	host := []string{"api.anthropic.com"}
	cases := []struct {
		name    string
		cfg     LaunchConfig
		code    string
		message string
	}{
		{
			name: "jail off",
			cfg:  LaunchConfig{JailFlags: config.JailFlags{AllowHosts: host}},
		},
		{
			name: "empty list",
			cfg:  LaunchConfig{UseJail: true, JailFlags: config.JailFlags{}},
		},
		{
			name: "blank entries",
			cfg:  LaunchConfig{UseJail: true, JailFlags: config.JailFlags{AllowHosts: []string{"  ", ""}}},
		},
		{
			name:    "not a hostname",
			cfg:     LaunchConfig{UseJail: true, JailFlags: config.JailFlags{AllowHosts: []string{"https://api.anthropic.com"}}},
			code:    "allow-host-invalid",
			message: "https://api.anthropic.com",
		},
		{
			name: "explicit network",
			cfg: LaunchConfig{
				UseJail:     true,
				JailVersion: "2.2.0",
				JailFlags:   config.JailFlags{Network: boolPtr(true), AllowHosts: host},
			},
			code:    "allow-host-conflicts-with-network",
			message: "--network",
		},
		{
			name: "network forced off still allows the list",
			cfg: LaunchConfig{
				UseJail:     true,
				JailVersion: "2.2.0",
				JailFlags:   config.JailFlags{Network: boolPtr(false), AllowHosts: host},
			},
		},
		{
			name: "exact 2.0.0 floor",
			cfg: LaunchConfig{
				UseJail:     true,
				JailVersion: "  " + config.MinAllowHostAIJailVersion + "  ",
				JailFlags:   config.JailFlags{AllowHosts: host},
			},
		},
		{
			name:    "unknown jail version",
			cfg:     LaunchConfig{UseJail: true, JailFlags: config.JailFlags{AllowHosts: host}},
			code:    "allow-host-requires-ai-jail-2",
			message: "unknown",
		},
		{
			name: "jail older than 2.0",
			cfg: LaunchConfig{
				UseJail:     true,
				JailVersion: "1.20.1",
				JailFlags:   config.JailFlags{AllowHosts: host},
			},
			code:    "allow-host-requires-ai-jail-2",
			message: "1.20.1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := allowHostIssues(tc.cfg)
			if tc.code == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %#v; want none", issues)
				}
				return
			}
			issue, ok := issueByCode(issues, tc.code)
			if !ok {
				t.Fatalf("issues = %#v; want %s", issues, tc.code)
			}
			if tc.message != "" && !strings.Contains(issue.Message, tc.message) {
				t.Fatalf("message = %q; want it to contain %q", issue.Message, tc.message)
			}
		})
	}
}
