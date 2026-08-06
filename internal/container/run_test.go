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

func TestBuildRunCommandFull(t *testing.T) {
	sel := testSelection(t)
	// The full fixture mounts paths under a fake home; stub the existence
	// probe so the mounts survive the M5 filtering.
	origExists := ExistsOnHost
	ExistsOnHost = func(string) bool { return true }
	t.Cleanup(func() { ExistsOnHost = origExists })
	got, err := BuildRunCommand(RunConfig{
		Selection:         sel,
		HomeDir:           "/home/lgldsilva",
		UID:               501,
		GID:               20,
		ProjectDir:        "/home/lgldsilva/work",
		AgentCommands:     []string{"claude"},
		GHConfig:          "/home/lgldsilva/.config/gh",
		SSHConfig:         "/home/lgldsilva/.ssh",
		MountDockerSocket: true,
		DockerSocketPath:  "/var/run/docker.sock",
		MemoryNativeBin:   "/home/lgldsilva/.local/share/ai-launcher/bin/ai-memory",
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
	assertContains(t, got, "-v", "/home/lgldsilva/work:/home/lgldsilva/work")
	assertContains(t, got, "-v", "/home/lgldsilva/.claude:/home/lgldsilva/.claude:ro")
	assertContains(t, got, "-v", "/home/lgldsilva/.claude.json:/home/lgldsilva/.claude.json:ro")
	assertContains(t, got, "-v", "/home/lgldsilva/.config/gh:/home/lgldsilva/.config/gh:ro")
	assertContains(t, got, "-v", "/home/lgldsilva/.ssh:/home/lgldsilva/.ssh:ro")
	assertContains(t, got, "-v", "/var/run/docker.sock:/var/run/docker.sock")
	assertContains(t, got, "-v", "/home/lgldsilva/.local/share/ai-launcher/bin/ai-memory:/home/lgldsilva/.local/share/ai-launcher/bin/ai-memory:ro")
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
