package container

import (
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
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
		"SHELL [\"/bin/bash\", \"-o\", \"pipefail\", \"-c\"]",
		"# Stack: Go",
		"golang-go",
		"# Stack helpers: Go",
		"go clean -modcache -cache",
		"# Stack: Python",
		"python3-pip",
		"# Agent: claude",
		"pinned release 2.1.0 (checksum-verified)",
		"COPY --chmod=0755 ai-launcher /usr/local/bin/ai-launcher",
		"COPY --chmod=0644 install-config.yaml /etc/ai-launch/install-config.yaml",
		"RUN ai-launcher --config /etc/ai-launch/install-config.yaml --install --agent claude",
		"# Agent: kiro-cli",
		"bind-mounted from host /opt/kiro/bin",
		"curl -fsSL https://dev.meta.ai/install.sh | bash",
		"RUN command -v 'muse' >/dev/null",
		"ENV PATH=\"/home/ai-launcher/.local/bin:${PATH}\"",
		"RUN groupadd --system ai-launcher",
		"USER ai-launcher",
		"tmux",
		"ENV HOME=\"/home/ai-launcher\"",
		"HEALTHCHECK NONE",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile missing %q\n---\n%s", want, df)
		}
	}
	// The recursive chown pass is gone: helpers and installers run as the
	// least-privilege user, so ownership is right at creation time and no
	// gigabyte-duplicating chown layer is emitted.
	if strings.Contains(df, "RUN chown -R") {
		t.Errorf("Dockerfile must not contain a recursive chown layer:\n%s", df)
	}
	// The docker CLI is conditional on the docker permission; the zero
	// options render the minimal image without it.
	if strings.Contains(df, "docker.io") {
		t.Errorf("Dockerfile without DockerCLI option must not install docker.io:\n%s", df)
	}
	// The image entrypoint always runs as the least-privilege user.
	if !strings.HasSuffix(df, "USER ai-launcher\n") {
		t.Errorf("Dockerfile must end with USER ai-launcher:\n%s", df)
	}
	if strings.Contains(df, `\n`) {
		t.Fatalf("Dockerfile contains a literal escaped newline:\n%s", df)
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

func TestDockerfileIncludesNodeOnlyWhenSelectionNeedsIt(t *testing.T) {
	goSelection, err := Normalize([]string{"go"}, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, nil)
	if err != nil {
		t.Fatalf("Normalize(go) error = %v", err)
	}
	goDockerfile, err := Dockerfile(goSelection)
	if err != nil {
		t.Fatalf("Dockerfile(go) error = %v", err)
	}
	if strings.Contains(goDockerfile, "/opt/nvm") || strings.Contains(goDockerfile, "/usr/local/lib/nvm-bin") {
		t.Fatalf("Go-only Dockerfile unexpectedly contains Node paths:\n%s", goDockerfile)
	}
	if strings.Contains(goDockerfile, "chown -R ai-launcher:ai-launcher /home/ai-launcher /opt") {
		t.Fatalf("Go-only Dockerfile chowns an unrelated toolchain:\n%s", goDockerfile)
	}

	nodeSelection, err := Normalize([]string{"node"}, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, nil)
	if err != nil {
		t.Fatalf("Normalize(node) error = %v", err)
	}
	nodeDockerfile, err := Dockerfile(nodeSelection)
	if err != nil {
		t.Fatalf("Dockerfile(node) error = %v", err)
	}
	// The npm global prefix is handed to the least-privilege user inside the
	// NodeProfile RUN (cheap: the directory was just created), replacing the
	// old end-of-build recursive chown.
	for _, want := range []string{"/opt/nvm", "/usr/local/lib/nvm-bin", "chown -R ai-launcher:ai-launcher /usr/local/lib/nvm-bin"} {
		if !strings.Contains(nodeDockerfile, want) {
			t.Errorf("Node Dockerfile missing %q:\n%s", want, nodeDockerfile)
		}
	}
	if strings.Contains(nodeDockerfile, "chown -R ai-launcher:ai-launcher /opt/nvm") {
		t.Errorf("/opt/nvm must stay root-owned (read via /usr/local/bin symlinks):\n%s", nodeDockerfile)
	}

	npmSelection, err := Normalize([]string{"go"}, []AgentInstall{{Command: "gemini", Kind: InstallNpm, NpmPackage: "@google/gemini-cli"}}, nil)
	if err != nil {
		t.Fatalf("Normalize(npm) error = %v", err)
	}
	npmDockerfile, err := Dockerfile(npmSelection)
	if err != nil {
		t.Fatalf("Dockerfile(npm) error = %v", err)
	}
	if !strings.Contains(npmDockerfile, "/opt/nvm") {
		t.Fatalf("npm agent Dockerfile should include Node:\n%s", npmDockerfile)
	}
}

func TestDockerfileSelectsSDKMANOnlyForJava(t *testing.T) {
	goSelection, err := Normalize([]string{"go"}, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, nil)
	if err != nil {
		t.Fatalf("Normalize(go) error = %v", err)
	}
	goDockerfile, err := Dockerfile(goSelection)
	if err != nil {
		t.Fatalf("Dockerfile(go) error = %v", err)
	}
	if strings.Contains(goDockerfile, "SDKMAN_DIR") || strings.Contains(goDockerfile, "/opt/sdkman") {
		t.Fatalf("Go Dockerfile unexpectedly contains SDKMAN: %s", goDockerfile)
	}

	javaSelection, err := Normalize([]string{"java"}, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, nil)
	if err != nil {
		t.Fatalf("Normalize(java) error = %v", err)
	}
	javaDockerfile, err := Dockerfile(javaSelection)
	if err != nil {
		t.Fatalf("Dockerfile(java) error = %v", err)
	}
	if !strings.Contains(javaDockerfile, "ENV SDKMAN_DIR=\"/opt/sdkman\"") || !strings.Contains(javaDockerfile, "/opt/sdkman") {
		t.Fatalf("Java Dockerfile is missing SDKMAN paths: %s", javaDockerfile)
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

func TestDockerfileInstallsAndChecksMemoryTool(t *testing.T) {
	sel, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{
			Command:     "pi",
			Kind:        InstallScript,
			Script:      "curl -fsSL https://pi.dev/install.sh | bash",
			NeedsMemory: true,
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}
	for _, want := range []string{
		"# Tool: ai-memory",
		"RUN ai-launcher --config /etc/ai-launch/install-config.yaml --install --agent ai-memory",
		"RUN command -v ai-memory >/dev/null",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("memory Dockerfile missing %q:\n%s", want, df)
		}
	}
}

func TestDockerfileDoesNotInstallMemoryForPlainAgent(t *testing.T) {
	sel, err := Normalize([]string{"go"}, []AgentInstall{{Command: "muse", Kind: InstallScript, Script: "curl x | bash"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}
	if strings.Contains(df, "--install --agent ai-memory") || strings.Contains(df, "# Tool: ai-memory") {
		t.Fatalf("plain-agent Dockerfile unexpectedly installs ai-memory:\n%s", df)
	}
}

func TestDockerfileInstallsSemidxToolOnPath(t *testing.T) {
	sel, err := Normalize([]string{"go"}, []AgentInstall{{Command: "pi", Kind: InstallScript, Script: "curl pi | bash"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	sel.Tools = []ToolInstall{{
		Command: "semidx",
		Version: "0.44.9",
		Kind:    InstallRelease,
		Release: &config.GitHubRelease{Repository: "lgldsilva/semidx"},
	}}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}
	for _, want := range []string{
		"# Tool: semidx",
		"pinned release 0.44.9 (checksum-verified)",
		"RUN ai-launcher --config /etc/ai-launch/install-config.yaml --install --agent semidx",
		"RUN command -v 'semidx' >/dev/null",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("semidx Dockerfile missing %q:\n%s", want, df)
		}
	}
}

func TestScriptLine(t *testing.T) {
	tests := []struct {
		agent AgentInstall
		want  string
	}{
		{AgentInstall{Script: "curl x | bash"}, "RUN curl x | bash\n"},
		{AgentInstall{Script: "RUN curl x | bash"}, "RUN curl x | bash\n"},
		{AgentInstall{Script: "  curl x | bash\n"}, "RUN curl x | bash\n"},
		{AgentInstall{Command: "devin", Script: "curl x | bash", AllowSetupFailure: true}, "RUN set +e; curl x | bash; status=$?; set -e; if test \"$status\" -ne 0 && ! command -v 'devin' >/dev/null 2>&1; then exit \"$status\"; fi; exit 0\n"},
	}
	for _, tt := range tests {
		if got := ScriptLine(tt.agent); got != tt.want {
			t.Errorf("ScriptLine(%#v) = %q; want %q", tt.agent, got, tt.want)
		}
	}
}

func TestDockerfileDockerCLIOnlyWithOption(t *testing.T) {
	sel, err := Normalize([]string{"go"}, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	without, err := DockerfileWithOptions(sel, DockerfileOptions{})
	if err != nil {
		t.Fatalf("DockerfileWithOptions() error = %v", err)
	}
	if strings.Contains(without, "docker.io") {
		t.Fatalf("minimal Dockerfile unexpectedly installs the docker CLI:\n%s", without)
	}
	with, err := DockerfileWithOptions(sel, DockerfileOptions{DockerCLI: true})
	if err != nil {
		t.Fatalf("DockerfileWithOptions(DockerCLI) error = %v", err)
	}
	if !strings.Contains(with, "docker.io") {
		t.Fatalf("docker-permission Dockerfile missing the docker CLI:\n%s", with)
	}
	// The CLI layer is an apt layer: it must run as root, before the switch
	// to the least-privilege user.
	cliPos := strings.Index(with, "docker.io")
	userPos := strings.Index(with, "USER ai-launcher")
	if cliPos < 0 || userPos < 0 || cliPos > userPos {
		t.Fatalf("docker CLI layer must come before the USER switch (cli=%d user=%d)", cliPos, userPos)
	}
	// Determinism: same selection + options → same bytes.
	again, err := DockerfileWithOptions(sel, DockerfileOptions{DockerCLI: true})
	if err != nil {
		t.Fatalf("DockerfileWithOptions(DockerCLI) second call error = %v", err)
	}
	if with != again {
		t.Fatal("DockerfileWithOptions is not deterministic for the same selection and options")
	}
}

func TestDockerfileHelpersAndAgentsRunAsLeastPrivilegeUser(t *testing.T) {
	sel, err := Normalize(
		[]string{"go", "node"},
		[]AgentInstall{{Command: "gemini", Kind: InstallNpm, NpmPackage: "@google/gemini-cli"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}
	userPos := strings.Index(df, "USER ai-launcher")
	if userPos < 0 {
		t.Fatalf("Dockerfile never switches to the least-privilege user:\n%s", df)
	}
	// Everything after the first USER switch that RUNs an install must be a
	// helper or an agent/tool layer — stack toolchain layers stay root.
	for _, marker := range []string{"# Stack helpers: Go", "# Stack helpers: Node", "# Agent: gemini"} {
		pos := strings.Index(df, marker)
		if pos < 0 || pos < userPos {
			t.Fatalf("%s must come after the USER switch (marker=%d user=%d)", marker, pos, userPos)
		}
	}
	// A second stack toolchain layer after a helper switches back to root.
	// Stacks sort before emission (go < node), so the node toolchain layer
	// follows the Go helpers, and its USER root instruction sits between the
	// "# Stack: Node" marker and the layer body.
	goHelpers := strings.Index(df, "# Stack helpers: Go")
	nodeStack := strings.Index(df, "# Stack: Node")
	if goHelpers < 0 || nodeStack < 0 || nodeStack < goHelpers {
		t.Fatalf("expected Go helpers before the Node stack layer (helpers=%d node=%d)", goHelpers, nodeStack)
	}
	if !strings.HasPrefix(df[nodeStack:], "# Stack: Node\nUSER root\n") {
		t.Fatalf("Node toolchain layer must switch back to root after the Go helpers:\n%s", df)
	}
	// The npm agent layer cleans its download cache.
	if !strings.Contains(df, "RUN npm install -g @google/gemini-cli && npm cache clean --force") {
		t.Errorf("npm agent layer must clean the npm cache:\n%s", df)
	}
}

func TestDockerfileZshStack(t *testing.T) {
	sel, err := Normalize([]string{"zsh"}, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}
	for _, want := range []string{"# Stack: zsh", "apt-get install -y --no-install-recommends \\\n    zsh"} {
		if !strings.Contains(df, want) {
			t.Errorf("zsh Dockerfile missing %q:\n%s", want, df)
		}
	}
	if strings.Contains(df, "oh-my-zsh") {
		t.Errorf("zsh stack must not install oh-my-zsh:\n%s", df)
	}
}

func TestDockerfileCreatesInstallConfigDirAsRoot(t *testing.T) {
	// /etc/ai-launch must exist with a traversable mode before the config
	// COPYs: BuildKit applies COPY --chmod to an implicitly created parent
	// directory too, which left it 0644 root:root and made the install-config
	// unreadable (EACCES) to the least-privilege installer — a failure the
	// config fallback then silently hid, building with default versions
	// instead of the pinned ones.
	sel, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "2.1.0"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	sel.Tools = []ToolInstall{{Command: "semidx", Version: "0.44.9", Kind: InstallRelease, Release: &config.GitHubRelease{Repository: "lgldsilva/semidx"}}}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}

	const setup = "RUN install -d -m 0755 /etc/ai-launch"
	// Exactly once, even with several config consumers (agent + tool).
	if count := strings.Count(df, setup); count != 1 {
		t.Fatalf("install-config dir setup must appear exactly once, got %d:\n%s", count, df)
	}
	// The Go helpers run as the least-privilege user, so the setup needs an
	// explicit switch back to root; it must precede every config COPY, and
	// the installers switch right back afterwards.
	if !strings.Contains(df, "USER root\n"+setup+"\nUSER ai-launcher\n") {
		t.Fatalf("install-config dir setup must run as root immediately before the installer section:\n%s", df)
	}
	setupPos := strings.Index(df, setup)
	copyPos := strings.Index(df, "COPY --chmod=0644 install-config.yaml")
	if copyPos < 0 || setupPos > copyPos {
		t.Fatalf("install-config dir setup must precede the config COPY (setup=%d copy=%d):\n%s", setupPos, copyPos, df)
	}
}

func TestDockerfileSkipsInstallConfigDirWithoutConfigConsumers(t *testing.T) {
	sel, err := Normalize([]string{"go"}, []AgentInstall{{Command: "muse", Kind: InstallScript, Script: "curl x | bash"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}
	if strings.Contains(df, "install -d -m 0755") || strings.Contains(df, "install-config.yaml") {
		t.Fatalf("script-only Dockerfile unexpectedly touches the install-config:\n%s", df)
	}
}

func TestDockerfileGoHelpersJoinPath(t *testing.T) {
	// gopls/goimports land in ~/go/bin; without it on PATH the helpers are
	// installed but unreachable by name.
	sel, err := Normalize([]string{"go"}, []AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "1.0"}}, nil)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	df, err := Dockerfile(sel)
	if err != nil {
		t.Fatalf("Dockerfile() error = %v", err)
	}
	if !strings.Contains(df, "go clean -modcache -cache\nENV PATH=\"/home/ai-launcher/go/bin:${PATH}\"") {
		t.Fatalf("Go helpers must put ~/go/bin on PATH right after the cache clean:\n%s", df)
	}
}
