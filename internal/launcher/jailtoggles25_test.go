package launcher

import (
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func buildToggles25(t *testing.T, version string, flags config.JailFlags) []string {
	t.Helper()
	return buildAgentState(t, version, flags)
}

// The 2.5.0 batch follows the tri-state model: nil leaves ai-jail's own
// default in charge (toolchains is on there, kvm and the credentials are
// off), and a configured value forces its form — emitted only when the
// detected ai-jail knows the flag.
func TestToggles25EmitOnlyWhenConfiguredOnANewJail(t *testing.T) {
	argv := buildToggles25(t, "2.9.0", config.JailFlags{})
	for _, token := range argv {
		for _, flag := range []string{"--toolchains", "--no-toolchains", "--kvm", "--github", "--aws", "--kube", "--gcloud", "--docker-config"} {
			if token == flag {
				t.Fatalf("Build() = %#v; an unset toggle must emit nothing (%s)", argv, flag)
			}
		}
	}
	forced := buildToggles25(t, "2.9.0", config.JailFlags{
		Toolchains: boolPtr(false),
		KVM:        boolPtr(true),
		GitHub:     boolPtr(true),
		AWS:        boolPtr(false),
	})
	for _, want := range []string{"--no-toolchains", "--kvm", "--github", "--no-aws"} {
		found := false
		for _, token := range forced {
			if token == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("Build() = %#v; want %s", forced, want)
		}
	}
}

// Below the floor the flags do not exist; the argv must not carry them even
// when configured, because pre-flight is what refuses the configuration —
// and a silent skip keeps a TUI preview from printing a broken command.
func TestToggles25StaySilentBelowTheFloor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
	}{
		{"the 2.5.0 floor minus one", "2.4.9"},
		{"the old argv floor", "1.20.1"},
		{"unreadable version", ""},
	} {
		argv := buildToggles25(t, tc.version, config.JailFlags{Toolchains: boolPtr(false), KVM: boolPtr(true), GitHub: boolPtr(true)})
		for _, token := range argv {
			for _, flag := range []string{"--toolchains", "--no-toolchains", "--kvm", "--github"} {
				if token == flag {
					t.Fatalf("%s: Build() = %#v; must stay silent on ai-jail %q", tc.name, argv, tc.version)
				}
			}
		}
	}
}

func TestToggles25RefusedBelowTheFloor(t *testing.T) {
	base := func(version string) LaunchConfig {
		return LaunchConfig{
			Agent:       config.Agent{Command: "claude"},
			UseJail:     true,
			JailVersion: version,
			JailFlags:   config.JailFlags{KVM: boolPtr(true)},
		}
	}
	for _, version := range []string{"2.4.9", "1.20.1", ""} {
		if _, ok := issueByCode(jailVersionValidate(base(version)), "jail-toggles-require-ai-jail-2-5"); !ok {
			t.Fatalf("preflight accepted a configured 2.5 toggle on ai-jail %q", version)
		}
	}
	if _, ok := issueByCode(jailVersionValidate(base("2.5.0")), "jail-toggles-require-ai-jail-2-5"); ok {
		t.Fatal("preflight refused a 2.5 toggle on ai-jail 2.5.0")
	}
	unset := jailVersionConfig("1.20.1")
	if _, ok := issueByCode(jailVersionValidate(unset), "jail-toggles-require-ai-jail-2-5"); ok {
		t.Fatal("unset toggles must never be refused")
	}
}

// forward_ports rides the filtered-egress decision like allow_hosts: one
// --forward-port per entry, emitted only on a jail new enough to accept it.
func TestForwardPortsEmitOn29AndStaySilentBelow(t *testing.T) {
	on29 := buildToggles25(t, "2.9.0", config.JailFlags{ForwardPorts: []int{8080, 3000}})
	expectOrdered(t, on29, "--no-network", "--forward-port")
	for _, want := range []string{"--forward-port 8080", "--forward-port 3000"} {
		expectTokens(t, on29, want)
	}
	for _, version := range []string{"2.8.1", ""} {
		argv := buildToggles25(t, version, config.JailFlags{ForwardPorts: []int{8080}})
		for _, token := range argv {
			if token == "--forward-port" {
				t.Fatalf("Build() on ai-jail %q emitted --forward-port: %#v", version, argv)
			}
		}
	}
}

func TestForwardPortIssuesMirrorUpstreamRefusals(t *testing.T) {
	base := func(version string, flags config.JailFlags, permissions map[string]bool) LaunchConfig {
		if permissions == nil {
			permissions = map[string]bool{}
		}
		return LaunchConfig{
			Agent:       config.Agent{Command: "claude"},
			UseJail:     true,
			JailVersion: version,
			JailFlags:   flags,
			Permissions: permissions,
		}
	}
	if _, ok := issueByCode(jailVersionValidate(base("2.6.0", config.JailFlags{ForwardPorts: []int{8080}}, nil)), "forward-ports-require-ai-jail-2-9"); !ok {
		t.Fatal("preflight accepted forward_ports below ai-jail 2.9.0")
	}
	for _, tc := range []struct {
		name        string
		flags       config.JailFlags
		permissions map[string]bool
	}{
		{"explicit network flag", config.JailFlags{ForwardPorts: []int{8080}, Network: boolPtr(true)}, nil},
		{"network permission", config.JailFlags{ForwardPorts: []int{8080}}, map[string]bool{config.PermissionNetwork: true}},
	} {
		if _, ok := issueByCode(jailVersionValidate(base("2.9.0", tc.flags, tc.permissions)), "forward-ports-conflict-with-network"); !ok {
			t.Fatalf("%s: preflight accepted forward_ports with unrestricted network", tc.name)
		}
	}
	// Filtered egress is the compatible mode: --no-network --allow-host composes
	// with --forward-port (ai-jail 2.6.2 confirmed the proxy composition).
	filtered := base("2.9.0", config.JailFlags{ForwardPorts: []int{8080}, AllowHosts: []string{"api.anthropic.com"}}, map[string]bool{config.PermissionNetwork: true})
	if _, ok := issueByCode(jailVersionValidate(filtered), "forward-ports-conflict-with-network"); ok {
		t.Fatal("preflight refused forward_ports alongside filtered egress")
	}
	if _, ok := issueByCode(jailVersionValidate(base("2.9.0", config.JailFlags{ForwardPorts: []int{80, 8080}}, nil)), "forward-port-invalid"); !ok {
		t.Fatal("preflight accepted a privileged forward port")
	}
	many := make([]int, 257)
	for i := range many {
		many[i] = 1024 + i
	}
	if _, ok := issueByCode(jailVersionValidate(base("2.9.0", config.JailFlags{ForwardPorts: many}, nil)), "forward-ports-too-many"); !ok {
		t.Fatal("preflight accepted more relays than ai-jail runs")
	}
}

// 1024 and 65535 are legal, and exactly 256 relays is the cap, not one past it.
func TestForwardPortBoundsAreInclusive(t *testing.T) {
	cfg := func(ports []int) LaunchConfig {
		return LaunchConfig{
			Agent:       config.Agent{Command: "claude"},
			UseJail:     true,
			JailVersion: "2.9.0",
			JailFlags:   config.JailFlags{ForwardPorts: ports},
		}
	}
	if _, ok := issueByCode(jailVersionValidate(cfg([]int{1024, 65535})), "forward-port-invalid"); ok {
		t.Fatal("preflight rejected the inclusive bounds 1024 and 65535")
	}
	exact := make([]int, forwardPortsMax)
	for i := range exact {
		exact[i] = 2000 + i
	}
	if _, ok := issueByCode(jailVersionValidate(cfg(exact)), "forward-ports-too-many"); ok {
		t.Fatal("preflight rejected exactly 256 relays, which is ai-jail's cap")
	}
	if _, ok := issueByCode(jailVersionValidate(cfg([]int{65536})), "forward-port-invalid"); !ok {
		t.Fatal("preflight accepted a port above 65535")
	}
}

// expectTokens asserts the tokens of want (space-separated) appear in argv
// consecutively, in order.
func expectTokens(t *testing.T, argv []string, want string) {
	t.Helper()
	wantTokens := strings.Fields(want)
	for i := 0; i+len(wantTokens) <= len(argv); i++ {
		match := true
		for j, token := range wantTokens {
			if argv[i+j] != token {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Fatalf("argv = %#v; want the consecutive tokens %q", argv, want)
}

func expectOrdered(t *testing.T, argv []string, first, second string) {
	t.Helper()
	firstAt, secondAt := -1, -1
	for i, token := range argv {
		if token == first && firstAt < 0 {
			firstAt = i
		}
		if token == second {
			secondAt = i
		}
	}
	if firstAt < 0 || secondAt < 0 || firstAt > secondAt {
		t.Fatalf("argv = %#v; want %s before %s", argv, first, second)
	}
}
