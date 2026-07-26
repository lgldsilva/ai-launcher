package config

import (
	"reflect"
	"testing"
)

func agentByCommand(agents []Agent, command string) (Agent, bool) {
	for _, agent := range agents {
		if agent.Command == command {
			return agent, true
		}
	}
	return Agent{}, false
}

// A user config listing any agent replaced the whole built-in list, so an entry
// written by hand — or materialized by an older release — silently lost
// Memory.RunHarness, YoloFlag and Params. That is why the builder needed a
// hardcoded "oc" remap to paper over the gap.
func TestUserAgentsMergePerEntryWithTheDefaults(t *testing.T) {
	defaults := DefaultGlobal()
	user := Global{Agents: []Agent{
		{Name: "OpenCode Presets", Command: "oc", SupportsMemory: true},
		{Name: "Mine", Command: "mine", SupportsYolo: true},
	}}
	merged := mergeGlobalDefaults(defaults, user)

	oc, ok := agentByCommand(merged.Agents, "oc")
	if !ok {
		t.Fatal("merged catalog lost the oc entry")
	}
	if oc.Memory == nil || oc.Memory.RunHarness != "opencode" {
		t.Errorf("oc.Memory = %#v; a field the user did not set must fall back to the default", oc.Memory)
	}
	if oc.YoloFlag != "--auto" {
		t.Errorf("oc.YoloFlag = %q; want the default --auto", oc.YoloFlag)
	}
	if oc.Name != "OpenCode Presets" {
		t.Errorf("oc.Name = %q; the user value must win", oc.Name)
	}

	if _, ok := agentByCommand(merged.Agents, "mine"); !ok {
		t.Error("a user-defined agent must survive the merge")
	}
	if _, ok := agentByCommand(merged.Agents, "claude"); !ok {
		t.Error("built-in agents the user did not list must survive the merge")
	}
}

// A user entry that deliberately overrides a default field keeps its value.
func TestUserAgentFieldsWinOverTheDefaults(t *testing.T) {
	merged := mergeGlobalDefaults(DefaultGlobal(), Global{Agents: []Agent{
		{Name: "Claude", Command: "claude", YoloFlag: "--my-flag"},
	}})
	claude, ok := agentByCommand(merged.Agents, "claude")
	if !ok {
		t.Fatal("merged catalog lost the claude entry")
	}
	if claude.YoloFlag != "--my-flag" {
		t.Fatalf("claude.YoloFlag = %q; the user value must win", claude.YoloFlag)
	}
}

// With no user entries the merge must return the defaults unchanged.
func TestMergeAgentsEmptyUserKeepsDefaults(t *testing.T) {
	defaults := DefaultGlobal().Agents
	merged := mergeAgents(defaults, nil)
	if !reflect.DeepEqual(merged, defaults) {
		t.Fatalf("mergeAgents(defaults, nil) = %#v; want the defaults", merged)
	}
}

// The `seen` map is what stops an overridden built-in from being appended a
// second time by the user-remainder loop.
func TestMergedAgentAppearsOnceWhenUserOverridesBuiltin(t *testing.T) {
	merged := mergeGlobalDefaults(DefaultGlobal(), Global{Agents: []Agent{
		{Name: "Claude", Command: "claude", YoloFlag: "--mine"},
	}})
	count := 0
	for _, agent := range merged.Agents {
		if agent.Command == "claude" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("claude appears %d times after the merge; want exactly 1", count)
	}
}

// A `continue` flipped to `break` in the defaults loop drops every built-in
// declared after the overridden entry.
func TestMergeKeepsBuiltinsDeclaredAfterAnOverriddenEntry(t *testing.T) {
	merged := mergeGlobalDefaults(DefaultGlobal(), Global{Agents: []Agent{
		{Name: "OpenCode Presets", Command: "oc", SupportsMemory: true},
	}})
	for _, command := range []string{"gemini", "qwen", "cline"} {
		if _, ok := agentByCommand(merged.Agents, command); !ok {
			t.Errorf("built-in %q declared after the overridden entry was dropped", command)
		}
	}
}

// Every overridable field falls back to the built-in only when the user left
// it empty; a set user value always wins.
func TestMergeAgentFieldFallbacksAndOverrides(t *testing.T) {
	base := Agent{
		Name: "Base", Command: "base", Aliases: []string{"b1", "b2"},
		SourceURL: "https://base.test", Description: "base description",
		YoloFlag: "--base", Params: []Param{{Name: "model", Flag: "--model"}},
		Release: &GitHubRelease{Repository: "acme/base"},
		Memory:  &MemoryIntegration{Client: "base"},
	}
	t.Run("empty user fields inherit the base", func(t *testing.T) {
		merged := mergeAgent(base, Agent{Command: "base"})
		if merged.Name != "Base" || !reflect.DeepEqual(merged.Aliases, base.Aliases) ||
			merged.SourceURL != "https://base.test" || merged.Description != "base description" ||
			merged.YoloFlag != "--base" || len(merged.Params) != 1 ||
			merged.Release == nil || merged.Release.Repository != "acme/base" || merged.Memory == nil {
			t.Fatalf("merged = %#v; empty fields must inherit the base", merged)
		}
	})
	t.Run("set user fields win", func(t *testing.T) {
		override := Agent{
			Name: "Mine", Command: "base", Aliases: []string{"only-mine"},
			SourceURL: "https://mine.test", Description: "my description",
			YoloFlag: "--mine", Params: []Param{{Name: "fast", Flag: "--fast"}},
			Release: &GitHubRelease{Repository: "acme/mine"},
			Memory:  &MemoryIntegration{Client: "mine"},
		}
		merged := mergeAgent(base, override)
		if !reflect.DeepEqual(merged, override) {
			t.Fatalf("merged = %#v; a fully specified override must win outright %#v", merged, override)
		}
	})
}

// The built-in catalog exercises the same fallbacks end to end: a user kilo
// entry without a release recipe must keep the published one, and a user kimi
// entry without aliases must keep the built-in aliases.
func TestMergeGlobalDefaultsInheritsCatalogReleaseAndAliases(t *testing.T) {
	merged := mergeGlobalDefaults(DefaultGlobal(), Global{Agents: []Agent{
		{Name: "Kilo Code", Command: "kilo"},
		{Name: "Kimi Code", Command: "kimi"},
		{Name: "Claude", Command: "claude", Description: "my claude"},
	}})
	kilo, _ := agentByCommand(merged.Agents, "kilo")
	if kilo.Release == nil || kilo.Release.Repository != "Kilo-Org/kilocode" {
		t.Fatalf("kilo.Release = %#v; want the built-in recipe", kilo.Release)
	}
	kimi, _ := agentByCommand(merged.Agents, "kimi")
	if !reflect.DeepEqual(kimi.Aliases, []string{"kimi-cli", "kimi-code"}) {
		t.Fatalf("kimi.Aliases = %#v; want the built-in aliases", kimi.Aliases)
	}
	claude, _ := agentByCommand(merged.Agents, "claude")
	if claude.Description != "my claude" {
		t.Fatalf("claude.Description = %q; the user value must win", claude.Description)
	}
	if len(claude.Params) == 0 || claude.Params[0].Name != "model" {
		t.Fatalf("claude.Params = %#v; empty user params must inherit the built-in model param", claude.Params)
	}
}
