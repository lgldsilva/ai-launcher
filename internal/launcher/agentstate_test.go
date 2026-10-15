package launcher

import (
	"reflect"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func buildAgentState(t *testing.T, version string, flags config.JailFlags) []string {
	t.Helper()
	argv, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		JailVersion: version,
		Permissions: map[string]bool{},
		JailFlags:   flags,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return argv
}

// ai-jail 2.6.0 turned agent state on by default, so the same argv built a
// different sandbox across 2.5.x and 2.6.x. From 2.6.0 the launcher states
// the decision, and unset follows the upstream default — on — the same way
// the network decision adopted ai-jail's then-new default rather than
// freezing the old one.
func TestAgentStateFollowsTheUpstreamDefaultFrom2_6(t *testing.T) {
	argv := buildAgentState(t, "2.6.0", config.JailFlags{})
	if want := []string{"ai-jail", "--no-docker", "--no-network", "--agent-state", "claude"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}

// agent_state: false is the explicit way back to the pre-2.6 isolation, and
// forcing a capability on is a distinct state from leaving it unset, so both
// forms are always emitted.
func TestAgentStateFalseEmitsTheNegativeForm(t *testing.T) {
	argv := buildAgentState(t, "2.9.0", config.JailFlags{AgentState: boolPtr(false)})
	if want := []string{"ai-jail", "--no-docker", "--no-network", "--no-agent-state", "claude"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}

// Below 2.6.0 the flag is an unknown option, so nothing is emitted — the
// silence is pre-flight's to guarantee, via the agent-state floor refusal.
func TestAgentStateIsSilentBelow2_6AndWhenUnreadable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
	}{
		{"below the floor", "2.5.9"},
		{"floor release minus one patch line", "1.20.1"},
		{"unreadable version", ""},
	} {
		argv := buildAgentState(t, tc.version, config.JailFlags{})
		for _, token := range argv {
			if token == "--agent-state" || token == "--no-agent-state" {
				t.Fatalf("%s: Build() = %#v; agent_state must stay silent on ai-jail %q", tc.name, argv, tc.version)
			}
		}
	}
}

// --lockdown forces agent state off upstream, and the argv never contradicts
// itself — the same rule that keeps "--no-tailscale --tailscale" impossible.
func TestLockdownForcesAgentStateOff(t *testing.T) {
	argv := buildAgentState(t, "2.6.0", config.JailFlags{Lockdown: boolPtr(true), AgentState: boolPtr(true)})
	for _, want := range []string{"--lockdown", "--no-agent-state"} {
		found := false
		for _, token := range argv {
			if token == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("Build() = %#v; want %s alongside --lockdown", argv, want)
		}
	}
	for _, token := range argv {
		if token == "--agent-state" {
			t.Fatalf("Build() = %#v; --lockdown must force the negative form", argv)
		}
	}
}

// A configured agent_state on an ai-jail that rejects the flag would surface
// as an opaque clap error inside the jail — invariant 6b's exact failure.
// Pre-flight names the floor instead, and an unset agent_state is always
// valid. An unknown version is refused too, mirroring the allow-host gate:
// the probe staying silent must not turn into "new enough".
func TestAgentStateIssuesRefuseWhatTheJailCannotApply(t *testing.T) {
	base := func(version string) LaunchConfig {
		return LaunchConfig{
			Agent:       config.Agent{Command: "claude"},
			UseJail:     true,
			JailVersion: version,
			JailFlags:   config.JailFlags{AgentState: boolPtr(true)},
		}
	}
	for _, tc := range []struct {
		name    string
		version string
	}{
		{"below the floor", "2.5.9"},
		{"the old security floor", "1.20.1"},
		{"unreadable version", ""},
	} {
		if _, ok := issueByCode(jailVersionValidate(base(tc.version)), "agent-state-requires-ai-jail-2-6"); !ok {
			t.Fatalf("%s: preflight accepted a configured agent_state on ai-jail %q", tc.name, tc.version)
		}
	}
	for _, version := range []string{"2.6.0", "2.9.0"} {
		if _, ok := issueByCode(jailVersionValidate(base(version)), "agent-state-requires-ai-jail-2-6"); ok {
			t.Fatalf("preflight refused agent_state on ai-jail %s", version)
		}
	}
	unset := jailVersionConfig("1.20.1")
	if _, ok := issueByCode(jailVersionValidate(unset), "agent-state-requires-ai-jail-2-6"); ok {
		t.Fatal("an unset agent_state must never be refused")
	}
	withoutJail := base("2.5.9")
	withoutJail.UseJail = false
	if _, ok := issueByCode(jailVersionValidate(withoutJail), "agent-state-requires-ai-jail-2-6"); ok {
		t.Fatal("jail-options-without-jail already covers a set jail_flags key without the jail")
	}
}
