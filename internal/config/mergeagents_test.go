package config

import "testing"

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
