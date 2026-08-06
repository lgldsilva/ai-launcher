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
