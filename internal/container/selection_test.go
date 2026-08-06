package container

import (
	"reflect"
	"strings"
	"testing"
)

func TestStackByID(t *testing.T) {
	for _, stack := range Stacks {
		if stack.ID == "" || stack.Name == "" || stack.Layer == "" {
			t.Fatalf("stack %#v must have ID, Name and Layer", stack)
		}
		got, ok := StackByID(stack.ID)
		if !ok || got.ID != stack.ID {
			t.Fatalf("StackByID(%q) = %#v, %v; want the catalog entry", stack.ID, got, ok)
		}
		if strings.Contains(stack.Layer, "\n\n") {
			t.Errorf("stack %q Layer has a double newline", stack.ID)
		}
	}
	if _, ok := StackByID("cobol"); ok {
		t.Fatal("StackByID(cobol) should be unknown")
	}
}

func TestValidStackIDs(t *testing.T) {
	t.Run("sorts and dedupes", func(t *testing.T) {
		got, err := ValidStackIDs([]string{"rust", "go", "rust", "  go  "})
		if err != nil {
			t.Fatalf("ValidStackIDs() error = %v", err)
		}
		want := []string{"go", "rust"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ValidStackIDs() = %v; want %v", got, want)
		}
	})
	t.Run("unknown stack", func(t *testing.T) {
		if _, err := ValidStackIDs([]string{"go", "cobol"}); err == nil {
			t.Fatal("ValidStackIDs with unknown stack should error")
		}
	})
	t.Run("empty input is fine", func(t *testing.T) {
		got, err := ValidStackIDs(nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("ValidStackIDs(nil) = %v, %v; want empty", got, err)
		}
	})
}

func TestAgentInstallValidate(t *testing.T) {
	t.Run("release requires pinned version", func(t *testing.T) {
		err := (AgentInstall{Command: "claude", Kind: InstallRelease}).Validate()
		if err == nil {
			t.Fatal("release without version should error")
		}
		err = (AgentInstall{Command: "claude", Kind: InstallRelease, Version: "latest"}).Validate()
		if err == nil {
			t.Fatal("release with 'latest' should error (C2)")
		}
		if err := (AgentInstall{Command: "claude", Kind: InstallRelease, Version: "1.2.3"}).Validate(); err != nil {
			t.Fatalf("pinned release should pass, got %v", err)
		}
	})
	t.Run("script requires script", func(t *testing.T) {
		if err := (AgentInstall{Command: "muse", Kind: InstallScript}).Validate(); err == nil {
			t.Fatal("script without script should error")
		}
		if err := (AgentInstall{Command: "muse", Kind: InstallScript, Script: "curl x | bash"}).Validate(); err != nil {
			t.Fatalf("script with script should pass, got %v", err)
		}
	})
	t.Run("host requires path", func(t *testing.T) {
		if err := (AgentInstall{Command: "kiro", Kind: InstallHostBinary}).Validate(); err == nil {
			t.Fatal("host without path should error")
		}
		if err := (AgentInstall{Command: "kiro", Kind: InstallHostBinary, HostPath: "/usr/local/bin/kiro"}).Validate(); err != nil {
			t.Fatalf("host with path should pass, got %v", err)
		}
	})
	t.Run("empty and unknown kinds", func(t *testing.T) {
		if err := (AgentInstall{Command: "x"}).Validate(); err == nil {
			t.Fatal("empty kind should error")
		}
		if err := (AgentInstall{Command: "x", Kind: "snap"}).Validate(); err == nil {
			t.Fatal("unknown kind should error")
		}
		if err := (AgentInstall{Kind: InstallRelease, Version: "1.0"}).Validate(); err == nil {
			t.Fatal("empty command should error")
		}
	})
}

func TestNormalize(t *testing.T) {
	t.Run("canonicalizes and sorts", func(t *testing.T) {
		got, err := Normalize(
			[]string{"python", "go"},
			[]AgentInstall{
				{Command: "muse", Kind: InstallScript, Script: "curl x | bash"},
				{Command: "claude", Kind: InstallRelease, Version: "2.1.0"},
			},
			nil,
		)
		if err != nil {
			t.Fatalf("Normalize() error = %v", err)
		}
		if !reflect.DeepEqual(got.Stacks, []string{"go", "python"}) {
			t.Fatalf("Stacks = %v; want sorted", got.Stacks)
		}
		if got.Agents[0].Command != "claude" || got.Agents[1].Command != "muse" {
			t.Fatalf("Agents not sorted by command: %v", got.Agents)
		}
		if !got.IncludeDevProfile {
			t.Fatal("IncludeDevProfile should default true")
		}
	})
	t.Run("duplicate agent errors", func(t *testing.T) {
		_, err := Normalize(nil, []AgentInstall{
			{Command: "claude", Kind: InstallRelease, Version: "1.0"},
			{Command: "claude", Kind: InstallRelease, Version: "2.0"},
		}, nil)
		if err == nil {
			t.Fatal("duplicate agent should error")
		}
	})
	t.Run("invalid stack errors", func(t *testing.T) {
		_, err := Normalize([]string{"cobol"}, nil, nil)
		if err == nil {
			t.Fatal("unknown stack should error")
		}
	})
	t.Run("profile override", func(t *testing.T) {
		got, err := Normalize([]string{"go"}, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, boolPtr(false))
		if err != nil {
			t.Fatalf("Normalize() error = %v", err)
		}
		if got.IncludeDevProfile {
			t.Fatal("IncludeDevProfile should be false")
		}
	})
}

func TestSelectionValidate(t *testing.T) {
	sel, err := Normalize([]string{"go"}, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if err := sel.Validate(); err != nil {
		t.Fatalf("canonical selection should validate, got %v", err)
	}
	bad := Selection{Stacks: []string{"cobol"}, Agents: sel.Agents}
	if err := bad.Validate(); err == nil {
		t.Fatal("selection with unknown stack should fail validation")
	}
	badAgents := Selection{Stacks: sel.Stacks, Agents: []AgentInstall{{Command: "x", Kind: InstallRelease}}}
	if err := badAgents.Validate(); err == nil {
		t.Fatal("selection with invalid agent should fail validation")
	}
}

func boolPtr(value bool) *bool { return &value }
