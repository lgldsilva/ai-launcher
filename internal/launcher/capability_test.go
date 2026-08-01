package launcher

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func boolPointer(value bool) *bool { return &value }

func argvContains(argv []string, flag string) bool {
	for _, token := range argv {
		if token == flag {
			return true
		}
	}
	return false
}

// ai-jail treats an unset boolean as "auto" (enabled when the resource exists
// on the host), so forcing a capability on is a distinct, meaningful state from
// leaving it unset. Suppressing the positive form turns an explicit
// jail_flags.gpu=true into silent auto-detection.
func TestJailFlagsForceOnEmitsThePositiveForm(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:   config.Agent{Command: "claude"},
		UseJail: true,
		JailFlags: config.JailFlags{
			GPU:      boolPointer(true),
			Landlock: boolPointer(true),
			Seccomp:  boolPointer(true),
			Rlimits:  boolPointer(true),
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, flag := range []string{"--gpu", "--landlock", "--seccomp", "--rlimits"} {
		if !argvContains(got, flag) {
			t.Fatalf("Build() = %#v; want it to force %s on", got, flag)
		}
	}
}

// The auto-mode passthroughs must be switchable off from the declarative
// jail_flags block; without that, --no-display / --no-mise / --no-worktree are
// unreachable from any configuration path.
func TestJailFlagsCanForceOffAutoCapabilities(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:   config.Agent{Command: "claude"},
		UseJail: true,
		JailFlags: config.JailFlags{
			Display:  boolPointer(false),
			Mise:     boolPointer(false),
			Worktree: boolPointer(false),
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"ai-jail", "--no-docker", "--no-display", "--no-mise", "--no-worktree", "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

// A permission and a jail_flags entry can name the same ai-jail capability.
// Emitting both leaves the argv self-contradictory (--no-tailscale --tailscale)
// and clap's last-wins silently discards the explicit configuration.
func TestExplicitJailFlagWinsOverTheMatchingPermission(t *testing.T) {
	cases := []struct {
		name       string
		permission string
		flags      config.JailFlags
		want       string
		unwanted   string
	}{
		{
			name:       "tailscale",
			permission: "tailscale",
			flags:      config.JailFlags{Tailscale: boolPointer(false)},
			want:       "--no-tailscale",
			unwanted:   "--tailscale",
		},
		{
			name:       "gpu",
			permission: "gpu",
			flags:      config.JailFlags{GPU: boolPointer(false)},
			want:       "--no-gpu",
			unwanted:   "--gpu",
		},
		{
			name:       "display",
			permission: "display",
			flags:      config.JailFlags{Display: boolPointer(false)},
			want:       "--no-display",
			unwanted:   "--display",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := Build(LaunchConfig{
				Agent:       config.Agent{Command: "claude"},
				UseJail:     true,
				Permissions: map[string]bool{testCase.permission: true},
				JailFlags:   testCase.flags,
			})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if !argvContains(got, testCase.want) {
				t.Fatalf("Build() = %#v; want %s", got, testCase.want)
			}
			if argvContains(got, testCase.unwanted) {
				t.Fatalf("Build() = %#v; must not contradict itself with %s", got, testCase.unwanted)
			}
		})
	}
}

// Permissions left at their default (absent or off) must not touch the
// capability at all, so ai-jail keeps its own auto-detection.
func TestDisabledPassthroughPermissionsEmitNothing(t *testing.T) {
	got, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		Permissions: map[string]bool{"display": false, "mise": false, "worktree": false},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"ai-jail", "--no-docker", "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build() = %#v; want %#v", got, want)
	}
}

// Pre-flight validation must stay hermetic: probing upstream --version forks
// two processes on every launch and blocks the TUI event loop. The probe
// belongs to the explicit doctor surface.
func TestValidateDoesNotProbeUpstreamVersions(t *testing.T) {
	original := runVersionCommand
	probed := make([]string, 0)
	runVersionCommand = func(path string) ([]byte, error) {
		probed = append(probed, path)
		return []byte("ai-jail 0.1.0"), nil
	}
	defer func() { runVersionCommand = original }()

	validator := Validator{
		GOOS:     "linux",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	validator.Validate(LaunchConfig{
		Agent:     config.Agent{Command: "claude"},
		UseJail:   true,
		UseMemory: true,
	})
	if len(probed) > 0 {
		t.Fatalf("Validate() probed %v; pre-flight must not exec upstream binaries", probed)
	}
}

// Platform metadata comes from the effective catalog, not from a second copy of
// the built-in defaults: an operator who edits permissions[].platforms must see
// the same answer in the TUI and in pre-flight.
func TestValidatorUsesTheConfiguredPermissionCatalog(t *testing.T) {
	validator := Validator{
		GOOS:     "darwin",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
		Permissions: []config.Permission{
			{ID: "custom", Name: "Custom", Platforms: []string{"linux"}},
		},
	}
	issues := validator.Validate(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		Permissions: map[string]bool{"custom": true},
	})
	if len(issues) != 1 || issues[0].Code != "unsupported-platform" {
		t.Fatalf("issues = %#v; want one unsupported-platform warning from the configured catalog", issues)
	}
	if !strings.Contains(issues[0].Message, "custom") {
		t.Fatalf("message = %q; want the custom permission id", issues[0].Message)
	}
}
