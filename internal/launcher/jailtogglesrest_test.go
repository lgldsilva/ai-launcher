package launcher

import (
	"os"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// The remaining mirror: x11 and host-shm are 1.18 opt-ins and emit on any
// jail in the supported range when configured; the 2.9-verified toggles and
// the 2.0 audit journal emit only on a jail new enough for them.
func TestRemainingTogglesFollowTheirFloors(t *testing.T) {
	oldJail := buildAgentState(t, "1.20.1", config.JailFlags{X11: boolPtr(true), HostSHM: boolPtr(true)})
	for _, want := range []string{"--x11", "--host-shm"} {
		if !containsArg(oldJail, want) {
			t.Fatalf("argv = %#v; %s is a 1.18 opt-in and must emit on 1.20.1", oldJail, want)
		}
	}
	newJail := buildAgentState(t, "2.9.0", config.JailFlags{
		Audio:            boolPtr(true),
		TerminalPassthru: boolPtr(false),
		MacOSHostIPC:     boolPtr(true),
		UpdateCheck:      boolPtr(true),
		AuditLog:         boolPtr(true),
	})
	for _, want := range []string{"--audio", "--no-terminal-passthrough", "--macos-host-ipc", "--update-check", "--audit-log"} {
		if !containsArg(newJail, want) {
			t.Fatalf("argv = %#v; want %s", newJail, want)
		}
	}
	for _, tc := range []struct {
		version string
		flags   config.JailFlags
	}{
		{"2.0.9", config.JailFlags{Audio: boolPtr(true)}},
		{"2.8.1", config.JailFlags{UpdateCheck: boolPtr(true)}},
		{"", config.JailFlags{TerminalPassthru: boolPtr(true)}},
		{"1.20.1", config.JailFlags{AuditLog: boolPtr(true)}},
	} {
		for _, token := range buildAgentState(t, tc.version, tc.flags) {
			for _, banned := range []string{"--audio", "--terminal-passthrough", "--no-terminal-passthrough", "--macos-host-ipc", "--update-check", "--audit-log"} {
				if token == banned {
					t.Fatalf("ai-jail %q: argv = %#v; %s must stay silent there", tc.version, buildAgentState(t, tc.version, tc.flags), banned)
				}
			}
		}
	}
}

func TestRemainingToggleRefusalsNameTheirFloorBatch(t *testing.T) {
	base := func(version string, flags config.JailFlags) LaunchConfig {
		return LaunchConfig{
			Agent:       config.Agent{Command: "claude"},
			UseJail:     true,
			JailVersion: version,
			JailFlags:   flags,
		}
	}
	if _, ok := issueByCode(jailVersionValidate(base("2.8.1", config.JailFlags{Audio: boolPtr(true)})), "jail-toggles-require-ai-jail-2-9"); !ok {
		t.Fatal("preflight accepted audio below the verified floor")
	}
	if _, ok := issueByCode(jailVersionValidate(base("1.20.1", config.JailFlags{AuditLog: boolPtr(true)})), "jail-toggles-require-ai-jail-2"); !ok {
		t.Fatal("preflight accepted audit_log below ai-jail 2.0.0")
	}
	// Two batches misconfigured at once yield two named issues, not one
	// blurry one.
	both := jailVersionValidate(base("1.20.1", config.JailFlags{AuditLog: boolPtr(true), KVM: boolPtr(true)}))
	for _, code := range []string{"jail-toggles-require-ai-jail-2", "jail-toggles-require-ai-jail-2-5"} {
		if _, ok := issueByCode(both, code); !ok {
			t.Fatalf("preflight did not name the %s batch: %#v", code, both)
		}
	}
}

// --secret rides filtered egress only: the proxy is where the substitution
// happens, so entries without allow_hosts are inert.
func TestSecretsEmissionAndRefusals(t *testing.T) {
	cfg := LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		JailVersion: "2.9.0",
		JailFlags: config.JailFlags{
			AllowHosts: []string{"github.com"},
			Secrets:    []string{"GITHUB_TOKEN=github.com"},
		},
	}
	argv, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	expectTokens(t, argv, "--secret GITHUB_TOKEN=github.com --allow-host github.com")

	oldJail := cfg
	oldJail.JailVersion = "2.1.9"
	if _, ok := issueByCode(jailVersionValidate(oldJail), "secrets-require-ai-jail-2-2"); !ok {
		t.Fatal("preflight accepted secrets below ai-jail 2.2.0")
	}
	noEgress := cfg
	noEgress.JailFlags.AllowHosts = nil
	if _, ok := issueByCode(jailVersionValidate(noEgress), "secrets-require-filtered-egress"); !ok {
		t.Fatal("preflight accepted secrets without filtered egress")
	}
	badForm := cfg
	badForm.JailFlags.Secrets = []string{"just-a-key"}
	if _, ok := issueByCode(jailVersionValidate(badForm), "secret-invalid"); !ok {
		t.Fatal("preflight accepted a secret that is not KEY=host")
	}
}

func TestEnvFromFilesEmissionAndRefusals(t *testing.T) {
	cfg := LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		JailVersion: "2.9.0",
		JailFlags:   config.JailFlags{EnvFromFiles: []string{"/tmp/env"}},
	}
	argv, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	expectTokens(t, argv, "--env-from-file /tmp/env")

	oldJail := cfg
	oldJail.JailVersion = "1.20.1"
	if _, ok := issueByCode(jailVersionValidate(oldJail), "env-from-files-require-ai-jail-2"); !ok {
		t.Fatal("preflight accepted env_from_files below ai-jail 2.0.0")
	}
	missing := cfg
	missing.JailFlags.EnvFromFiles = []string{"/tmp/ai-launcher-definitely-missing-env-file"}
	issues := envFromFileIssues(missing, os.Stat)
	if _, ok := issueByCode(issues, "env-from-file-not-found"); !ok {
		t.Fatalf("preflight accepted a missing env file: %#v", issues)
	}
}

// ai-jail refuses group/world-readable env files at launch; pre-flight says
// it first (verified live against 2.9.0: "must be mode 0600 or stricter").
func TestEnvFromFileModeIsCheckedInPreflight(t *testing.T) {
	loose := t.TempDir() + "/loose-env"
	if err := os.WriteFile(loose, []byte("K=V\n"), 0o644); err != nil { // #nosec G306 -- the loose mode is the fixture under test
		t.Fatal(err)
	}
	cfg := LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		JailVersion: "2.9.0",
		JailFlags:   config.JailFlags{EnvFromFiles: []string{loose}},
	}
	if _, ok := issueByCode(envFromFileIssues(cfg, os.Stat), "env-from-file-not-locked-down"); !ok {
		t.Fatal("preflight accepted a group-readable env file")
	}
	if err := os.Chmod(loose, 0o600); err != nil {
		t.Fatal(err)
	}
	if issue := envFromFileIssues(cfg, os.Stat); len(issue) != 0 {
		t.Fatalf("preflight refused a 0600 env file: %#v", issue)
	}
}
