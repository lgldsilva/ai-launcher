package launcher

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// upsertEnv must not mutate the backing array of the slice it receives,
// because callers may still hold the original environment.
func TestUpsertEnvDoesNotMutateOriginalSlice(t *testing.T) {
	original := []string{"PATH=/usr/bin", "AI_MEMORY_AUTH_TOKEN=old", "HOME=/home/tester"}
	wantOriginal := []string{"PATH=/usr/bin", "AI_MEMORY_AUTH_TOKEN=old", "HOME=/home/tester"}

	got := upsertEnv(original, "AI_MEMORY_AUTH_TOKEN", "new")
	if !reflect.DeepEqual(original, wantOriginal) {
		t.Fatalf("original env mutated = %#v; want %#v", original, wantOriginal)
	}

	want := []string{"PATH=/usr/bin", "HOME=/home/tester", "AI_MEMORY_AUTH_TOKEN=new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upsertEnv() = %#v; want %#v", got, want)
	}
}

func TestUpsertEnvRemovesKeyWhenValueEmpty(t *testing.T) {
	env := []string{"PATH=/usr/bin", "AI_MEMORY_SERVER_URL=http://old", "HOME=/home/tester"}
	got := upsertEnv(env, "AI_MEMORY_SERVER_URL", "")
	for _, entry := range got {
		if entry == "AI_MEMORY_SERVER_URL=http://old" {
			t.Fatalf("upsertEnv() kept the old value: %#v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("upsertEnv() = %#v; want 2 entries", got)
	}
}

// ai-jail 1.18 forwards a minimal allowlist and drops everything else, so a
// variable the sandbox needs has to be named. These are the ones the launcher
// is responsible for: the three it owns, the agent credential-store override,
// and whatever the catalog says the agent reads.
func TestJailEnvPassthroughNamesWhatTheSandboxNeeds(t *testing.T) {
	cfg := LaunchConfig{
		Agent:     config.Agent{Command: "claude", EnvPassthrough: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"}},
		UseJail:   true,
		UseMemory: true,
	}
	env := []string{
		"AI_MEMORY_SERVER_URL=http://localhost:8080",
		"AI_MEMORY_AUTH_TOKEN=x",
		"ANTHROPIC_API_KEY=x",
		"PATH=/usr/bin",
	}
	got := JailEnvPassthrough(cfg, env)
	want := []string{"AI_MEMORY_SERVER_URL", "AI_MEMORY_AUTH_TOKEN", "ANTHROPIC_API_KEY"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JailEnvPassthrough() = %v; want %v", got, want)
	}
}

// Only variables that actually exist are named. ai-jail ignores an absent one,
// but an argv listing thirty unset variables is noise in the one place an
// operator reads the composed command.
func TestJailEnvPassthroughSkipsAbsentVariables(t *testing.T) {
	cfg := LaunchConfig{
		Agent:     config.Agent{Command: "claude", EnvPassthrough: []string{"ANTHROPIC_API_KEY"}},
		UseJail:   true,
		UseMemory: true,
	}
	if got := JailEnvPassthrough(cfg, []string{"PATH=/usr/bin"}); len(got) != 0 {
		t.Fatalf("JailEnvPassthrough() = %v; want nothing when no named variable is set", got)
	}
}

// Without the memory integration the AI_MEMORY_* variables are not the
// launcher's to forward — Environment() strips them from the child, so naming
// them would promise something that is not there.
func TestJailEnvPassthroughOmitsMemoryKeysWithoutMemory(t *testing.T) {
	cfg := LaunchConfig{Agent: config.Agent{Command: "claude"}, UseJail: true}
	env := []string{"AI_MEMORY_AUTH_TOKEN=x", "AI_MEMORY_SERVER_URL=http://x"}
	if got := JailEnvPassthrough(cfg, env); len(got) != 0 {
		t.Fatalf("JailEnvPassthrough() = %v; want no memory keys when memory is off", got)
	}
}

// A name repeated by the catalog and by the launcher's own list is emitted
// once: ai-jail would accept the duplicate, but the argv is read by people.
func TestJailEnvPassthroughDeduplicates(t *testing.T) {
	cfg := LaunchConfig{
		Agent:     config.Agent{Command: "claude", EnvPassthrough: []string{"AI_MEMORY_SERVER_URL", "AI_MEMORY_SERVER_URL"}},
		UseJail:   true,
		UseMemory: true,
	}
	got := JailEnvPassthrough(cfg, []string{"AI_MEMORY_SERVER_URL=http://x"})
	if len(got) != 1 {
		t.Fatalf("JailEnvPassthrough() = %v; want the name once", got)
	}
}

// The value never reaches the argv: --env takes the bare name and ai-jail
// copies from its own environment, which is how AI_MEMORY_AUTH_TOKEN stays out
// of process listings (ARCHITECTURE invariant 7).
func TestJailEnvArgvCarriesNamesNeverValues(t *testing.T) {
	argv, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		Permissions: map[string]bool{config.PermissionJail: true},
		JailEnv:     []string{"AI_MEMORY_AUTH_TOKEN", "ANTHROPIC_API_KEY"},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--env AI_MEMORY_AUTH_TOKEN") {
		t.Fatalf("Build() = %v; want the bare --env name", argv)
	}
	for _, arg := range argv {
		if strings.Contains(arg, "=") && strings.HasPrefix(arg, "AI_MEMORY") {
			t.Fatalf("argv carries a NAME=VALUE pair (%q); the value must stay in the environment", arg)
		}
	}
}
