package container

import (
	"reflect"
	"strings"
	"testing"
)

func testSelection(t *testing.T) Selection {
	t.Helper()
	sel, err := Normalize(
		[]string{"go"},
		[]AgentInstall{{Command: "claude", Kind: InstallRelease, Version: "2.1.0"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	return sel
}

func TestBuildRunCommandMinimal(t *testing.T) {
	sel := testSelection(t)
	tag, err := ImageTag(sel)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}

	got, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		ProjectDir:      "/Volumes/MSD512/Projetos/ai-launcher",
		AgentExecutable: "claude",
		Interactive:     true,
		AddHostGateway:  true,
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}

	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/Volumes/MSD512/Projetos/ai-launcher",
		"-v", "/Volumes/MSD512/Projetos/ai-launcher:/Volumes/MSD512/Projetos/ai-launcher",
		"--add-host=host.docker.internal:host-gateway",
		tag,
		"claude",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildRunCommand() = %#v\nwant %#v", got, want)
	}
}

func TestBuildRunCommandUsesInImageMemoryWrapper(t *testing.T) {
	sel := testSelection(t)
	got, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		ProjectDir:      "/work",
		AgentExecutable: "opencode",
		UseMemory:       true,
		MemoryHarness:   "opencode",
		AgentArgs:       []string{"--model", "fixture", "--yolo"},
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	tag, err := ImageTag(sel)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	want := []string{tag, "ai-memory", "run", "opencode", "--model", "fixture", "--yolo"}
	if len(got) < len(want) || !reflect.DeepEqual(got[len(got)-len(want):], want) {
		t.Fatalf("in-container command suffix = %#v; want %#v", got, want)
	}
}

func TestBuildRunCommandStartsTmuxAndMountsHostConfigReadOnly(t *testing.T) {
	sel := testSelection(t)
	origExists := ExistsOnHost
	ExistsOnHost = func(string) bool { return true }
	t.Cleanup(func() { ExistsOnHost = origExists })

	got, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		HomeDir:         "/home/tester",
		ProjectDir:      "/work",
		AgentExecutable: "pi",
		Tmux:            true,
		TmuxMounts:      []string{"/home/tester/.tmux.conf", "/home/tester/.tmux"},
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	assertContains(t, got, "-v", "/home/tester/.tmux.conf:/home/tester/.tmux.conf:ro")
	assertContains(t, got, "-v", "/home/tester/.tmux:/home/tester/.tmux:ro")
	wantSuffix := []string{"tmux", "new-session", "-A", "-s", "ai-launcher", "pi"}
	if len(got) < len(wantSuffix) || !reflect.DeepEqual(got[len(got)-len(wantSuffix):], wantSuffix) {
		t.Fatalf("tmux command suffix = %#v; want %#v", got, wantSuffix)
	}
}

func TestBuildRunCommandPodman(t *testing.T) {
	sel := testSelection(t)
	got, err := BuildRunCommand(RunConfig{
		Runtime:         PodmanRuntime{},
		Selection:       sel,
		ProjectDir:      "/work",
		AgentExecutable: "claude",
		Env:             []string{"AI_MEMORY_SERVER_URL=http://localhost:9292"},
		AddHostGateway:  true,
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	if got[0] != "podman" || got[1] != "run" {
		t.Fatalf("Podman argv starts with %#v; want podman run", got[:2])
	}
	assertArg(t, got, "--add-host=host.containers.internal:host-gateway")
	assertContains(t, got, "-e", "AI_MEMORY_SERVER_URL=http://host.containers.internal:9292")
}

func TestBuildRunCommandUsesDockerContext(t *testing.T) {
	sel := testSelection(t)
	got, err := BuildRunCommand(RunConfig{
		Runtime:         DockerRuntime{},
		Context:         "remote-builder",
		Selection:       sel,
		ProjectDir:      "/work",
		AgentExecutable: "claude",
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	if !reflect.DeepEqual(got[:4], []string{"docker", "--context", "remote-builder", "run"}) {
		t.Fatalf("Docker context argv prefix = %#v; want docker --context remote-builder run", got[:4])
	}
}

func TestBuildRunCommandFull(t *testing.T) {
	sel := testSelection(t)
	// The full fixture mounts paths under a fake home; stub the existence
	// probe so the mounts survive the M5 filtering.
	origExists := ExistsOnHost
	ExistsOnHost = func(string) bool { return true }
	t.Cleanup(func() { ExistsOnHost = origExists })
	got, err := BuildRunCommand(RunConfig{
		Selection:            sel,
		HomeDir:              "/home/lgldsilva",
		UID:                  501,
		GID:                  20,
		ProjectDir:           "/home/lgldsilva/work",
		AgentCommands:        []string{"claude"},
		GHConfig:             "/home/lgldsilva/.config/gh",
		SSHConfig:            "/home/lgldsilva/.ssh",
		MountDockerSocket:    true,
		DockerSocketPath:     "/var/run/docker.sock",
		DockerSocketGroupID:  20,
		DockerSocketGroupSet: true,
		MemoryNativeBin:      "/home/lgldsilva/.local/share/ai-launcher/bin/ai-memory",
		DependencyMounts: []DependencyMount{{
			ID: "go.module-cache", Kind: DependencyPackage,
			HostPath: "/Users/lgldsilva/go/pkg/mod", ContainerPath: "/home/ai-launcher/go/pkg/mod", Mode: "ro",
		}},
		DependencyEnv: []string{"GOMODCACHE=/home/ai-launcher/go/pkg/mod"},
		Env: []string{
			"AI_MEMORY_SERVER_URL=http://localhost:9292",
			"AI_MEMORY_AUTH_TOKEN=sekret",
			"ALREADY_HOST=http://host.docker.internal:9000",
		},
		Interactive:     true,
		AgentExecutable: "/usr/local/bin/claude",
		AgentArgs:       []string{"--model", "sonnet"},
		AddHostGateway:  true,
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}

	joined := reflect.DeepEqual(got, got) // no-op; kept for clarity
	_ = joined

	// Assert the critical pieces individually for readable failures.
	assertContains(t, got, "--user", "501:20")
	assertContains(t, got, "--group-add", "20")
	assertContains(t, got, "-v", "/home/lgldsilva/work:/home/lgldsilva/work")
	assertContains(t, got, "-v", "/home/lgldsilva/.claude:/home/lgldsilva/.claude")
	assertContains(t, got, "-v", "/home/lgldsilva/.claude.json:/home/lgldsilva/.claude.json")
	assertContains(t, got, "-v", "/home/lgldsilva/.config/gh:/home/lgldsilva/.config/gh:ro")
	assertContains(t, got, "-v", "/home/lgldsilva/.ssh:/home/lgldsilva/.ssh:ro")
	assertContains(t, got, "-v", "/var/run/docker.sock:/var/run/docker.sock")
	assertContains(t, got, "-v", "/home/lgldsilva/.local/share/ai-launcher/bin/ai-memory:/home/lgldsilva/.local/share/ai-launcher/bin/ai-memory:ro")
	assertContains(t, got, "-v", "/Users/lgldsilva/go/pkg/mod:/home/ai-launcher/go/pkg/mod:ro")
	assertContains(t, got, "-e", "GOMODCACHE=/home/ai-launcher/go/pkg/mod")
	assertContains(t, got, "-e", "AI_MEMORY_SERVER_URL=http://host.docker.internal:9292")
	assertContains(t, got, "-e", "AI_MEMORY_AUTH_TOKEN=sekret")
	assertContains(t, got, "-e", "ALREADY_HOST=http://host.docker.internal:9000")
	assertArg(t, got, "--add-host=host.docker.internal:host-gateway")
	assertArg(t, got, "/usr/local/bin/claude")
	assertContains(t, got, "--model", "sonnet")

	// Order contract: mounts before image, image before command.
	assertOrder(t, got, "-v", "/usr/local/bin/claude")
	assertOrder(t, got, "/usr/local/bin/claude", "--model")
}

func TestBuildRunCommandNonInteractive(t *testing.T) {
	sel := testSelection(t)
	got, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		ProjectDir:      "/work",
		AgentExecutable: "claude",
		Interactive:     false,
		AddHostGateway:  false,
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	if got[3] != "-i" {
		t.Fatalf("non-interactive mode should emit -i, got %q", got[3])
	}
	if contains(got, "--add-host=host.docker.internal:host-gateway") {
		t.Fatal("AddHostGateway=false must not emit the gateway flag")
	}
}

func TestBuildRunCommandHostGatewayDisabledKeepsLoopbackAndNoGateway(t *testing.T) {
	sel := testSelection(t)
	got, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		ProjectDir:      "/work",
		AgentExecutable: "claude",
		Env:             []string{"MCP_URL=http://localhost:8080/mcp"},
		AddHostGateway:  false,
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	assertContains(t, got, "-e", "MCP_URL=http://localhost:8080/mcp")
	if contains(got, "MCP_URL=http://host.docker.internal:8080/mcp") {
		t.Fatal("disabled host gateway must not rewrite loopback MCP URLs")
	}
	if contains(got, "--add-host=host.docker.internal:host-gateway") {
		t.Fatal("disabled host gateway must not expose the gateway entry")
	}
}

func TestBuildRunCommandWithResources(t *testing.T) {
	sel := testSelection(t)
	tag, err := ImageTag(sel)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	got, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		ProjectDir:      "/work",
		AgentExecutable: "claude",
		MemoryLimit:     "4g",
		CPULimit:        "2.0",
		PIDsLimit:       512,
		ExposedPorts:    []PortMapping{{Host: 3000, Internal: 3000}, {Host: 5353, Internal: 53, Protocol: "udp"}},
		NetworkName:     "bridge",
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	for _, pair := range [][2]string{
		{"--memory", "4g"},
		{"--cpus", "2.0"},
		{"--pids-limit", "512"},
		{"-p", "3000:3000"},
		{"-p", "5353:53/udp"},
		{"--network", "bridge"},
	} {
		assertContains(t, got, pair[0], pair[1])
	}
	assertOrder(t, got, "--memory", tag)
	assertOrder(t, got, "--network", tag)
}

func TestParsePortMappings(t *testing.T) {
	got, err := ParsePortMappings([]string{"3000:3000", "8080", "5353:53/UDP"})
	if err != nil {
		t.Fatalf("ParsePortMappings() error = %v", err)
	}
	want := []PortMapping{
		{Host: 3000, Internal: 3000},
		{Host: 8080, Internal: 8080},
		{Host: 5353, Internal: 53, Protocol: "udp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePortMappings() = %#v; want %#v", got, want)
	}
}

func TestPortMappingDockerFlagDefaultsTCP(t *testing.T) {
	if got := (PortMapping{Host: 3000, Internal: 3000, Protocol: "tcp"}).DockerFlag(); got != "3000:3000" {
		t.Fatalf("DockerFlag() = %q; want 3000:3000", got)
	}
	if got := (PortMapping{Internal: 8080}).DockerFlag(); got != "8080:8080" {
		t.Fatalf("DockerFlag() with omitted host = %q; want 8080:8080", got)
	}
}

func TestBuildRunCommandRejectsInvalidResources(t *testing.T) {
	sel := testSelection(t)
	tests := []struct {
		name string
		cfg  RunConfig
	}{
		{name: "memory", cfg: RunConfig{MemoryLimit: "0g"}},
		{name: "memory decimal", cfg: RunConfig{MemoryLimit: "1.5g"}},
		{name: "memory below docker minimum", cfg: RunConfig{MemoryLimit: "1m"}},
		{name: "memory signed overflow", cfg: RunConfig{MemoryLimit: "8589934592g"}},
		{name: "cpu", cfg: RunConfig{CPULimit: "fast"}},
		{name: "cpu precision", cfg: RunConfig{CPULimit: "0.0000000001"}},
		{name: "cpu exponent", cfg: RunConfig{CPULimit: "1e3"}},
		{name: "pids", cfg: RunConfig{PIDsLimit: -1}},
		{name: "port", cfg: RunConfig{ExposedPorts: []PortMapping{{Host: 0, Internal: 70000}}}},
		{name: "host network with ports", cfg: RunConfig{NetworkName: "host", ExposedPorts: []PortMapping{{Host: 3000, Internal: 3000}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.Selection = sel
			tt.cfg.ProjectDir = "/work"
			tt.cfg.AgentExecutable = "claude"
			if _, err := BuildRunCommand(tt.cfg); err == nil {
				t.Fatal("BuildRunCommand() error = nil; want validation error")
			}
		})
	}
}

func TestBuildRunCommandAcceptsDockerMemoryBoundaries(t *testing.T) {
	sel := testSelection(t)
	for _, value := range []string{"6m", "6144k", "6291456b", "4g", "9223372036854775807b"} {
		t.Run(value, func(t *testing.T) {
			_, err := BuildRunCommand(RunConfig{
				Selection:       sel,
				ProjectDir:      "/work",
				AgentExecutable: "claude",
				MemoryLimit:     value,
			})
			if err != nil {
				t.Fatalf("BuildRunCommand() error = %v", err)
			}
		})
	}
}

func TestBuildRunCommandErrors(t *testing.T) {
	sel := testSelection(t)
	tests := []struct {
		name string
		cfg  RunConfig
	}{
		{"invalid selection", RunConfig{Selection: Selection{Stacks: []string{"cobol"}}, ProjectDir: "/w", AgentExecutable: "x"}},
		{"no project dir", RunConfig{Selection: sel, AgentExecutable: "x"}},
		{"no agent executable", RunConfig{Selection: sel, ProjectDir: "/w"}},
	}
	for _, tt := range tests {
		if _, err := BuildRunCommand(tt.cfg); err == nil {
			t.Errorf("%s: expected error", tt.name)
		}
	}
}

func TestBuildRunCommandSkipsEmptyEnv(t *testing.T) {
	sel := testSelection(t)
	got, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		ProjectDir:      "/w",
		AgentExecutable: "claude",
		Env:             []string{"", "NO_EQUALS", "EMPTY_VALUE="},
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	for _, entry := range got {
		if entry == "-e" {
			t.Fatalf("empty env entries must be skipped, got %#v", got)
		}
	}
}

func TestMountSpec(t *testing.T) {
	if got := mountSpec("/home/u/.claude", true); got != "/home/u/.claude:/home/u/.claude:ro" {
		t.Fatalf("mountSpec ro = %q", got)
	}
	if got := mountSpec("/w", false); got != "/w:/w" {
		t.Fatalf("mountSpec rw = %q", got)
	}
}

func TestBuildRunCommandDefaultSocket(t *testing.T) {
	sel := testSelection(t)
	got, err := BuildRunCommand(RunConfig{
		Selection:         sel,
		ProjectDir:        "/w",
		AgentExecutable:   "claude",
		MountDockerSocket: true, // empty DockerSocketPath → default
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	assertContains(t, got, "-v", "/var/run/docker.sock:/var/run/docker.sock")
}

func TestBuildRunCommandPodmanSkipsDefaultSocket(t *testing.T) {
	sel := testSelection(t)
	got, err := BuildRunCommand(RunConfig{
		Runtime:           PodmanRuntime{},
		Selection:         sel,
		ProjectDir:        "/w",
		AgentExecutable:   "claude",
		MountDockerSocket: true,
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	if contains(got, "-v") && contains(got, ":/run/podman/podman.sock") {
		t.Fatalf("rootless Podman should not invent a socket mount: %#v", got)
	}
}

func assertContains(t *testing.T, argv []string, wantFlag, wantValue string) {
	t.Helper()
	for i, entry := range argv {
		if entry == wantFlag && i+1 < len(argv) && argv[i+1] == wantValue {
			return
		}
	}
	t.Fatalf("argv %#v missing pair (%q, %q)", argv, wantFlag, wantValue)
}

// assertArg asserts a self-contained argument (a flag with an inline value or
// a bare positional) appears verbatim in argv.
func assertArg(t *testing.T, argv []string, value string) {
	t.Helper()
	if !contains(argv, value) {
		t.Fatalf("argv %#v missing argument %q", argv, value)
	}
}

func assertOrder(t *testing.T, argv []string, before, after string) {
	t.Helper()
	bi, ai := -1, -1
	for i, entry := range argv {
		if entry == before && bi == -1 {
			bi = i
		}
		if entry == after && ai == -1 {
			ai = i
		}
	}
	if bi == -1 || ai == -1 || bi > ai {
		t.Fatalf("argv %#v: %q (index %d) must come before %q (index %d)", argv, before, bi, after, ai)
	}
}

func contains(argv []string, value string) bool {
	for _, entry := range argv {
		if entry == value {
			return true
		}
	}
	return false
}

// M5: a credential mount whose source does not exist on the host must be
// skipped, not emitted (docker refuses a -v source that is not there).
func TestBuildRunCommandSkipsMissingMountSources(t *testing.T) {
	sel := testSelection(t)
	origExists := ExistsOnHost
	ExistsOnHost = func(string) bool { return false } // nothing exists
	t.Cleanup(func() { ExistsOnHost = origExists })

	got, err := BuildRunCommand(RunConfig{
		Selection:         sel,
		HomeDir:           "/home/nobody",
		ProjectDir:        "/work",
		AgentCommands:     []string{"claude"},
		GHConfig:          "/home/nobody/.config/gh",
		SSHConfig:         "/home/nobody/.ssh",
		AgentExecutable:   "claude",
		MountDockerSocket: true, // socket always mounted when requested
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	joined := strings.Join(got, " ")
	for _, absent := range []string{".claude", ".config/gh", ".ssh"} {
		if strings.Contains(joined, absent) {
			t.Errorf("argv %q must not mount the missing source %q", joined, absent)
		}
	}
	// The project (rw) and the docker socket are explicit, not existence
	// filtered.
	assertArg(t, got, "/work:/work")
	assertArg(t, got, "/var/run/docker.sock:/var/run/docker.sock")
}

// C1: HOME must be passed explicitly so same-path credential/cache mounts
// resolve under the host home (docker does not inherit the parent HOME).
func TestBuildRunCommandEmitsHome(t *testing.T) {
	sel := testSelection(t)
	got, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		HomeDir:         "/home/tester",
		ProjectDir:      "/w",
		AgentExecutable: "claude",
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	assertContains(t, got, "-e", "HOME=/home/tester")
	// Without a home, no -e HOME is emitted.
	got2, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		ProjectDir:      "/w",
		AgentExecutable: "claude",
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	for _, entry := range got2 {
		if entry == "-e" {
			t.Fatalf("no -e HOME expected without HomeDir, got %#v", got2)
		}
	}
}

// C2: overlays must appear as -v flags BEFORE the image argument, or docker
// would treat them as the agent's native args.
func TestBuildRunCommandOverlaysBeforeImage(t *testing.T) {
	sel := testSelection(t)
	overlay := OverlayFile{HostPath: "/home/u/.claude.json", RewrittenPath: "/tmp/ov/.claude.json"}
	got, err := BuildRunCommand(RunConfig{
		Selection:       sel,
		HomeDir:         "/home/u",
		ProjectDir:      "/w",
		AgentExecutable: "claude",
		Overlays:        []OverlayFile{overlay},
	})
	if err != nil {
		t.Fatalf("BuildRunCommand() error = %v", err)
	}
	overlaySpec := overlay.OverlayMountSpec()
	tag, err := ImageTag(sel)
	if err != nil {
		t.Fatalf("ImageTag() error = %v", err)
	}
	imageIdx, overlayIdx := -1, -1
	for i, entry := range got {
		if entry == tag {
			imageIdx = i
		}
		if entry == overlaySpec {
			overlayIdx = i
		}
	}
	if imageIdx == -1 || overlayIdx == -1 {
		t.Fatalf("argv %#v missing image or overlay", got)
	}
	if overlayIdx > imageIdx {
		t.Fatalf("overlay (idx %d) must precede image (idx %d): %#v", overlayIdx, imageIdx, got)
	}
	// The -v flag and its spec must be adjacent.
	assertContains(t, got, "-v", overlaySpec)
}

// imageName must tag with the same Dockerfile options the build side used:
// the options are hashed into the tag, so a run config carrying DockerCLI
// references the CLI-bearing image, not the minimal one.
func TestImageNameMirrorsDockerCLIBuildOption(t *testing.T) {
	sel := testBuildSelection(t)
	plain, err := RunConfig{Selection: sel}.imageName()
	if err != nil {
		t.Fatalf("imageName() error = %v", err)
	}
	withCLI, err := RunConfig{Selection: sel, DockerCLI: true}.imageName()
	if err != nil {
		t.Fatalf("imageName() with DockerCLI error = %v", err)
	}
	if plain == withCLI {
		t.Fatal("the DockerCLI toggle must change the referenced image tag")
	}
	want, err := ImageTagWithOptions(sel, DockerfileOptions{DockerCLI: true})
	if err != nil {
		t.Fatalf("ImageTagWithOptions() error = %v", err)
	}
	if withCLI != want {
		t.Fatalf("imageName() with DockerCLI = %q, want %q", withCLI, want)
	}
}

// A memory launch without an explicit harness token falls back to the agent
// executable: ai-memory run needs a non-empty harness argument.
func TestInContainerCommandFallsBackToAgentExecutable(t *testing.T) {
	cfg := RunConfig{
		UseMemory:       true,
		AgentExecutable: "claude",
		AgentArgs:       []string{"--continue"},
	}
	got := cfg.InContainerCommand()
	want := []string{"ai-memory", "run", "claude", "--continue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InContainerCommand() = %#v; want %#v (harness falls back to the agent executable)", got, want)
	}
}

// An explicit memory harness wins over the agent executable, and the managed
// runner path is forwarded as --executable before the native args.
func TestInContainerCommandUsesMemoryHarness(t *testing.T) {
	cfg := RunConfig{
		UseMemory:        true,
		MemoryHarness:    "claude",
		MemoryExecutable: "/opt/ai-memory/claude",
		AgentExecutable:  "claude-native",
		AgentArgs:        []string{"-p", "hi"},
	}
	got := cfg.InContainerCommand()
	want := []string{"ai-memory", "run", "claude", "--executable", "/opt/ai-memory/claude", "-p", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InContainerCommand() = %#v; want %#v", got, want)
	}
}

// Worktree roots sort by path length first (shallow roots before nested ones)
// and lexically within the same length, so dedup keeps the broadest mount.
func TestWorktreeMountPathsOrdersByLengthThenLexically(t *testing.T) {
	cfg := RunConfig{
		ProjectDir:     "/project",
		WorktreeMounts: []string{"/aaaa", "/ccc", "/zz", "/bbb", "/aaa"},
	}
	got := worktreeMountPaths(cfg)
	want := []string{"/zz", "/aaa", "/bbb", "/ccc", "/aaaa"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("worktreeMountPaths() = %#v; want %#v", got, want)
	}
}

func TestPathCoveredBy(t *testing.T) {
	for _, tt := range []struct {
		name       string
		path, root string
		want       bool
	}{
		{"child of root", "/x/y", "/x", true},
		{"root itself", "/x", "/x", true},
		{"sibling escapes", "/other", "/x", false},
		{"dot root never covers", "sub/dir", ".", false},
		{"empty root never covers", "/x", "", false},
		{"empty path never covered", "", "/x", false},
		{"prefix lookalike escapes", "/x-other", "/x", false},
	} {
		if got := pathCoveredBy(tt.path, tt.root); got != tt.want {
			t.Errorf("pathCoveredBy(%q, %q) = %v; want %v (%s)", tt.path, tt.root, got, tt.want, tt.name)
		}
	}
}

// Existing cache dirs render as read-write same-path mounts, deduplicated,
// with blank entries skipped.
func TestStackCacheMountSpecs(t *testing.T) {
	origExists := ExistsOnHost
	ExistsOnHost = func(string) bool { return true }
	t.Cleanup(func() { ExistsOnHost = origExists })

	got := stackCacheMountSpecs([]string{"/cache/go", "  ", "/cache/go", "/cache/pip"})
	want := []string{"-v", "/cache/go:/cache/go", "-v", "/cache/pip:/cache/pip"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stackCacheMountSpecs() = %#v; want %#v", got, want)
	}
}

// A cache dir missing on the host is skipped rather than mounted.
func TestStackCacheMountSpecsSkipsMissing(t *testing.T) {
	origExists := ExistsOnHost
	ExistsOnHost = func(path string) bool { return path == "/cache/go" }
	t.Cleanup(func() { ExistsOnHost = origExists })

	got := stackCacheMountSpecs([]string{"/cache/go", "/cache/missing"})
	want := []string{"-v", "/cache/go:/cache/go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stackCacheMountSpecs() = %#v; want %#v", got, want)
	}
}

// A relative project directory produced "-v meu-time:meu-time", which Docker
// reads as a named volume with a relative destination and refuses with an
// opaque daemon error — after the launcher had already decided the launch was
// fine. The refusal belongs here, naming the value.
func TestValidateProjectDirRequiresAnAbsolutePath(t *testing.T) {
	for _, tt := range []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{"absolute", "/work", false},
		{"absolute with spaces", "/work/my project", false},
		{"empty", "", true},
		{"blank", "   ", true},
		{"relative", "meu-time", true},
		{"dot relative", "./work", true},
		{"parent relative", "../work", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProjectDir(tt.dir)
			if tt.wantErr != (err != nil) {
				t.Fatalf("ValidateProjectDir(%q) error = %v; wantErr %t", tt.dir, err, tt.wantErr)
			}
		})
	}
}

func TestBuildRunCommandRejectsARelativeProjectDir(t *testing.T) {
	cfg := RunConfig{
		Selection:       testSelection(t),
		ProjectDir:      "meu-time",
		AgentExecutable: "claude",
	}
	_, err := BuildRunCommand(cfg)
	if err == nil {
		t.Fatal("BuildRunCommand() = nil; a relative project directory must be refused")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error = %v; want it explaining the path must be absolute", err)
	}
}
