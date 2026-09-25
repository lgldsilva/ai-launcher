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

func TestVersionGatesIncludeTheFirstAcceptingRelease(t *testing.T) {
	if !MemoryDisablesAutowire("2.3.0") || MemoryDisablesAutowire("2.2.9") || MemoryDisablesAutowire("") {
		t.Fatal("MemoryDisablesAutowire must be true at 2.3.0 and false below it or when unreadable")
	}
	if !jailSupportsAllowHost("2.0.0") || jailSupportsAllowHost("1.9.9") || jailSupportsAllowHost("") {
		t.Fatal("jailSupportsAllowHost must be true at 2.0.0 and false below it or when unreadable")
	}
}

func TestBuildOmitsNoAutowireUntilAIMemory23(t *testing.T) {
	for _, version := range []string{"", "2.2.0"} {
		argv := mustBuild(t, LaunchConfig{
			Agent:         config.Agent{Command: "claude"},
			UseMemory:     true,
			MemoryVersion: version,
		})
		if strings.Contains(strings.Join(argv, " "), "--no-autowire") {
			t.Fatalf("version %q argv = %v; --no-autowire would be an unknown option", version, argv)
		}
	}
}

func TestBuildKeepsAutowireWhenTheOperatorOptsIn(t *testing.T) {
	argv := mustBuild(t, LaunchConfig{
		Agent:          config.Agent{Command: "claude"},
		UseMemory:      true,
		MemoryVersion:  "2.4.0",
		MemoryAutowire: true,
	})
	if strings.Contains(strings.Join(argv, " "), "--no-autowire") {
		t.Fatalf("argv = %v; AI_MEMORY_RUN_AUTOWIRE=true must keep autowire", argv)
	}
}

func TestAllowHostsWithMemoryRequiresHTTPSOnTheList(t *testing.T) {
	base := LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		UseMemory:   true,
		JailVersion: "2.2.0",
		JailFlags:   config.JailFlags{AllowHosts: []string{"api.anthropic.com"}},
	}
	cases := []struct {
		name       string
		url        string
		want       string
		absent     bool
		extraHosts []string
	}{
		{"default loopback", "", "allow-host-memory-needs-https", false, nil},
		{"explicit http", "http://127.0.0.1:49374", "allow-host-memory-needs-https", false, nil},
		{"https host missing", "https://aimemory.example", "allow-host-omits-memory-server", false, nil},
		{"https host listed via parent", "https://memory.aimemory.example/wiki", "", true, []string{"aimemory.example"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.MemoryServerURL = tc.url
			cfg.JailFlags.AllowHosts = append(append([]string{}, base.JailFlags.AllowHosts...), tc.extraHosts...)
			assertAllowHostMemoryIssue(t, linuxValidator().Validate(cfg), tc.absent, tc.want)
		})
	}
}

func assertAllowHostMemoryIssue(t *testing.T, issues []Issue, absent bool, want string) {
	t.Helper()
	if absent {
		if _, ok := issueByCode(issues, "allow-host-memory-needs-https"); ok {
			t.Fatalf("issues = %#v; a listed https server must be allowed", issues)
		}
		if _, ok := issueByCode(issues, "allow-host-omits-memory-server"); ok {
			t.Fatalf("issues = %#v; parent domain must cover the memory host", issues)
		}
		return
	}
	if _, ok := issueByCode(issues, want); !ok {
		t.Fatalf("issues = %#v; want %s", issues, want)
	}
}

func TestOpenCode2RequiresAIMemory23WhenTheVersionIsKnown(t *testing.T) {
	cfg := LaunchConfig{
		Agent:         config.Agent{Command: "opencode2", SupportsMemory: true},
		UseMemory:     true,
		MemoryVersion: "2.2.0",
	}
	if _, ok := issueByCode(linuxValidator().Validate(cfg), "memory-harness-version"); !ok {
		t.Fatal("opencode2 was allowed on ai-memory 2.2.0")
	}
	cfg.MemoryVersion = ""
	if _, ok := issueByCode(linuxValidator().Validate(cfg), "memory-harness-version"); ok {
		t.Fatal("an unreadable ai-memory version must not invent a harness refusal")
	}
}

func TestAllowHostsOnDockerWarnsInsteadOfLockingNetwork(t *testing.T) {
	cfg := dockerLaunchConfig(t)
	cfg.JailFlags.AllowHosts = []string{"api.anthropic.com"}
	issue, ok := issueByCode(linuxValidator().Validate(cfg), "allow-host-without-jail")
	if !ok || !issue.Warning {
		t.Fatalf("issue = %#v; docker must warn that allow_hosts is not applied", issue)
	}
}

func linuxValidator() *Validator {
	return &Validator{
		GOOS:     "linux",
		LookPath: func(string) (string, error) { return "/bin/tool", nil },
	}
}

func mustBuild(t *testing.T, cfg LaunchConfig) []string {
	t.Helper()
	argv, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return argv
}
