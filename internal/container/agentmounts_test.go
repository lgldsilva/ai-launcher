package container

import (
	"reflect"
	"testing"
)

func TestAgentMounts(t *testing.T) {
	home := "/home/lgldsilva"
	commands := []string{"claude", "codex", "opencode", "muse", "unknown-agent", " claude "}

	t.Run("all exist", func(t *testing.T) {
		got := AgentMounts(home, commands, func(path string) bool { return true })
		want := []AgentMount{
			{Agent: "claude", HostPath: "/home/lgldsilva/.claude", ReadOnly: true},
			{Agent: "claude", HostPath: "/home/lgldsilva/.claude.json", ReadOnly: true}, // R9.3
			{Agent: "codex", HostPath: "/home/lgldsilva/.codex", ReadOnly: true},
			{Agent: "opencode", HostPath: "/home/lgldsilva/.config/opencode", ReadOnly: true},
			{Agent: "opencode", HostPath: "/home/lgldsilva/.local/share/opencode", ReadOnly: true},
			{Agent: "muse", HostPath: "/home/lgldsilva/.muse", ReadOnly: true},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("AgentMounts() = %#v; want %#v", got, want)
		}
	})

	t.Run("skips missing paths", func(t *testing.T) {
		existing := map[string]bool{"/home/lgldsilva/.claude": true}
		got := AgentMounts(home, commands, func(path string) bool { return existing[path] })
		if len(got) != 1 || got[0].HostPath != "/home/lgldsilva/.claude" {
			t.Fatalf("AgentMounts() with missing paths = %#v; want only .claude", got)
		}
	})

	t.Run("empty home skips everything", func(t *testing.T) {
		got := AgentMounts("", commands, nil)
		if len(got) != 0 {
			t.Fatalf("AgentMounts() with empty home = %#v; want none", got)
		}
	})

	t.Run("nil existingFiles mounts everything", func(t *testing.T) {
		got := AgentMounts(home, []string{"muse"}, nil)
		if len(got) != 1 || got[0].HostPath != "/home/lgldsilva/.muse" {
			t.Fatalf("AgentMounts() with nil probe = %#v", got)
		}
	})
}

func TestExpandHome(t *testing.T) {
	tests := []struct {
		raw, home string
		want      string
	}{
		{"~/.claude", "/home/u", "/home/u/.claude"},
		{"~", "/home/u", "/home/u"},
		{"/abs/path", "/home/u", "/abs/path"},
		{"", "/home/u", ""},
		{"~/.claude", "", ""},
		{"~/a/../b", "/home/u", "/home/u/b"},
	}
	for _, tt := range tests {
		if got := ExpandHome(tt.raw, tt.home); got != tt.want {
			t.Errorf("ExpandHome(%q, %q) = %q; want %q", tt.raw, tt.home, got, tt.want)
		}
	}
}

func TestAgentMountsUnknownAgent(t *testing.T) {
	got := AgentMounts("/home/u", []string{"totally-unknown"}, nil)
	if len(got) != 0 {
		t.Fatalf("unknown agent should contribute no mounts, got %#v", got)
	}
}

func TestExistsOnHost(t *testing.T) {
	dir := t.TempDir()
	if !ExistsOnHost(dir) {
		t.Fatalf("ExistsOnHost(%q) = false; want true", dir)
	}
	if ExistsOnHost(dir + "/definitely-missing") {
		t.Fatal("ExistsOnHost on a missing path = true; want false")
	}
}

func TestStackCacheMounts(t *testing.T) {
	home := t.TempDir()
	// Create the host dirs that "exist" for the probe.
	dirs := map[string]bool{
		home + "/.nvm":    true,
		home + "/.cargo":  true,
		home + "/go":      true,
		home + "/.sdkman": true,
	}
	exists := func(path string) bool { return dirs[path] }

	t.Run("node stack", func(t *testing.T) {
		got := StackCacheMounts(home, []string{"node"}, exists)
		if len(got) != 1 || got[0] != home+"/.nvm" {
			t.Fatalf("StackCacheMounts(node) = %v; want [%s/.nvm]", got, home)
		}
	})
	t.Run("java stack", func(t *testing.T) {
		got := StackCacheMounts(home, []string{"java"}, exists)
		if len(got) != 1 || got[0] != home+"/.sdkman" {
			t.Fatalf("StackCacheMounts(java) = %v; want [%s/.sdkman]", got, home)
		}
	})
	t.Run("multiple dedup", func(t *testing.T) {
		got := StackCacheMounts(home, []string{"go", "node", "go"}, exists)
		if len(got) != 2 {
			t.Fatalf("StackCacheMounts(go,node,go) = %v; want 2 deduped", got)
		}
	})
	t.Run("missing dirs skipped", func(t *testing.T) {
		got := StackCacheMounts(home, []string{"rust"}, func(string) bool { return false })
		if len(got) != 0 {
			t.Fatalf("StackCacheMounts with no existing dirs = %v; want none", got)
		}
	})
	t.Run("empty home", func(t *testing.T) {
		if got := StackCacheMounts("", []string{"node"}, nil); len(got) != 0 {
			t.Fatalf("StackCacheMounts('') = %v; want none", got)
		}
	})
}
