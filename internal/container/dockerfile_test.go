package container

import (
	"strings"
	"testing"
)

func TestDockerfile(t *testing.T) {
	sel, err := Normalize(
		[]string{"go", "python"},
		[]AgentInstall{
			{Command: "muse", Kind: InstallScript, Script: "curl -fsSL https://dev.meta.ai/install.sh | bash"},
			{Command: "claude", Kind: InstallRelease, Version: "2.1.0"},
			{Command: "kiro-cli", Kind: InstallHostBinary, HostPath: "/opt/kiro/bin"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}

	// Determinism: same selection → same bytes.
	again, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() second call error = %v", err)
	}
	if df != again {
		t.Fatal("Dockerfile is not deterministic for the same selection")
	}

	for _, want := range []string{
		"FROM " + BaseImage,
		"# Stack: Go",
		"golang-go",
		"# Stack: Python",
		"python3-pip",
		"# Agent: claude",
		"pinned release 2.1.0 (checksum-verified)",
		"COPY ai-launcher /usr/local/bin/ai-launcher",
		"COPY install-config.yaml /etc/ai-launch/install-config.yaml",
		"RUN ai-launcher --config /etc/ai-launch/install-config.yaml --install",
		"# Agent: kiro-cli",
		"bind-mounted from host /opt/kiro/bin",
		"curl -fsSL https://dev.meta.ai/install.sh | bash",
		"ENV PATH=\"/root/.local/bin:${PATH}\"",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile missing %q\n---\n%s", want, df)
		}
	}

	// Layer order: stacks before agents (cache strategy).
	stackPos := strings.Index(df, "# Stack: Go")
	agentPos := strings.Index(df, "# Agent: claude")
	if stackPos < 0 || agentPos < 0 || stackPos > agentPos {
		t.Fatalf("stack layer must come before agent layer (stack=%d agent=%d)", stackPos, agentPos)
	}
	// Dev profile before stacks.
	devPos := strings.Index(df, "ENV PATH=")
	if devPos < 0 || devPos > stackPos {
		t.Fatalf("dev profile must come before stacks (dev=%d stack=%d)", devPos, stackPos)
	}
}

func TestDockerfileErrors(t *testing.T) {
	t.Run("no stacks", func(t *testing.T) {
		sel, err := Normalize(nil, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, nil)
		if err != nil {
			t.Fatalf("Normalize() error = %v", err)
		}
		if _, err := Dockerfile(sel); err != ErrNoStacks {
			t.Fatalf("Dockerfile() = %v; want ErrNoStacks", err)
		}
	})
	t.Run("no agents", func(t *testing.T) {
		sel, err := Normalize([]string{"go"}, nil, nil)
		if err != nil {
			t.Fatalf("Normalize() error = %v", err)
		}
		if _, err := Dockerfile(sel); err != ErrNoAgents {
			t.Fatalf("Dockerfile() = %v; want ErrNoAgents", err)
		}
	})
	t.Run("invalid selection", func(t *testing.T) {
		_, err := Dockerfile(Selection{Stacks: []string{"cobol"}})
		if err == nil {
			t.Fatal("Dockerfile with invalid selection should error")
		}
	})
}

func TestDockerfileNoDevProfile(t *testing.T) {
	sel, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}},
		boolPtr(false),
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}
	if strings.Contains(df, "Runtime essentials") {
		t.Fatal("dev profile should be omitted when disabled")
	}
	if !strings.Contains(df, "FROM "+BaseImage) {
		t.Fatal("FROM line must survive without dev profile")
	}
}

func TestScriptLine(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"curl x | bash", "RUN curl x | bash\n"},
		{"RUN curl x | bash", "RUN curl x | bash\n"},
		{"  curl x | bash\n", "RUN curl x | bash\n"},
	}
	for _, tt := range tests {
		if got := ScriptLine(tt.in); got != tt.want {
			t.Errorf("ScriptLine(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}
