package launcher

import (
	"reflect"
	"slices"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func buildDocker(t *testing.T, permissions map[string]bool, flags config.JailFlags) []string {
	t.Helper()
	if permissions == nil {
		permissions = map[string]bool{}
	}
	argv, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		Permissions: permissions,
		JailFlags:   flags,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return argv
}

// The whole point of the change: off is stated, not assumed.
//
// ai-jail <= 1.15.x bind-mounted an existing /var/run/docker.sock read-write
// with no flag and no warning, and write access to that socket is root on the
// host: `docker run -v /:/host` reads anything, past bubblewrap, Landlock,
// seccomp and every --mask, because the mount happens in a daemon that lives
// outside the sandbox (ai-jail issue #88). v1.16.0 made it opt-in. Emitting
// nothing would make the launcher's "docker off by default" a property of the
// installed ai-jail rather than of the command this launcher builds.
func TestJailAlwaysStatesTheDockerDecision(t *testing.T) {
	argv := buildDocker(t, nil, config.JailFlags{})
	if want := []string{"ai-jail", "--no-docker", "claude"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}

// Turning it on is the operator's explicit opt-in, and only theirs.
func TestDockerPermissionEmitsThePositiveForm(t *testing.T) {
	argv := buildDocker(t, map[string]bool{"docker": true}, config.JailFlags{})
	if want := []string{"ai-jail", "--docker", "claude"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("Build() = %#v; want %#v", argv, want)
	}
}

// jail_flags is the declarative surface and wins over the permission toggle,
// in both directions — the same rule every other capability follows, so the
// argv can never contradict itself with "--no-docker --docker".
func TestDockerJailFlagWinsOverThePermission(t *testing.T) {
	forcedOff := buildDocker(t, map[string]bool{"docker": true},
		config.JailFlags{Docker: boolPtr(false)})
	if want := []string{"ai-jail", "--no-docker", "claude"}; !reflect.DeepEqual(forcedOff, want) {
		t.Errorf("jail_flags.docker: false with the permission on: %#v; want %#v", forcedOff, want)
	}

	forcedOn := buildDocker(t, map[string]bool{"docker": false},
		config.JailFlags{Docker: boolPtr(true)})
	if want := []string{"ai-jail", "--docker", "claude"}; !reflect.DeepEqual(forcedOn, want) {
		t.Errorf("jail_flags.docker: true with the permission off: %#v; want %#v", forcedOn, want)
	}
}

// Exactly one form, always. A duplicate or a contradiction is an argv bug that
// ai-jail would resolve by its own precedence rather than the operator's.
func TestDockerDecisionIsEmittedExactlyOnce(t *testing.T) {
	cases := map[string][]string{
		"default":        buildDocker(t, nil, config.JailFlags{}),
		"permission on":  buildDocker(t, map[string]bool{"docker": true}, config.JailFlags{}),
		"flag on":        buildDocker(t, nil, config.JailFlags{Docker: boolPtr(true)}),
		"flag off":       buildDocker(t, nil, config.JailFlags{Docker: boolPtr(false)}),
		"flag beats on":  buildDocker(t, map[string]bool{"docker": true}, config.JailFlags{Docker: boolPtr(false)}),
		"flag beats off": buildDocker(t, map[string]bool{"docker": false}, config.JailFlags{Docker: boolPtr(true)}),
	}
	for name, argv := range cases {
		positive := slices.Contains(argv, "--docker")
		negative := slices.Contains(argv, "--no-docker")
		if positive == negative {
			t.Errorf("%s: argv = %#v; want exactly one of --docker / --no-docker", name, argv)
		}
		if count(argv, "--docker")+count(argv, "--no-docker") != 1 {
			t.Errorf("%s: argv = %#v; the decision must appear once", name, argv)
		}
	}
}

// Without the jail there is no ai-jail to tell anything to.
func TestDockerDecisionIsAbsentWithoutTheJail(t *testing.T) {
	argv, err := Build(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     false,
		Permissions: map[string]bool{"docker": true},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if slices.Contains(argv, "--docker") || slices.Contains(argv, "--no-docker") {
		t.Fatalf("Build() = %#v; a jail-less launch has no ai-jail to receive the flag", argv)
	}
}

// IsZero decides whether the trust gate treats a workspace file's jail_flags as
// "said nothing". A Docker field it cannot see is a jail_flags.docker: true that
// a repository could set without tripping the refusal.
func TestJailFlagsIsZeroSeesTheDockerField(t *testing.T) {
	if !(config.JailFlags{}).IsZero() {
		t.Fatal("an empty JailFlags must be zero")
	}
	if (config.JailFlags{Docker: boolPtr(true)}).IsZero() {
		t.Error("jail_flags.docker: true must be visible to IsZero, or the trust gate lets it through")
	}
	if (config.JailFlags{Docker: boolPtr(false)}).IsZero() {
		t.Error("jail_flags.docker: false must be visible to IsZero")
	}
}

func count(argv []string, want string) int {
	n := 0
	for _, arg := range argv {
		if arg == want {
			n++
		}
	}
	return n
}
