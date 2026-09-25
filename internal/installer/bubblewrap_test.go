package installer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func lookPathPresent(present map[string]bool) func(string) (string, error) {
	return func(name string) (string, error) {
		if present[name] {
			return "/bin/" + name, nil
		}
		return "", errors.New("missing")
	}
}

func TestResolveBubblewrapSkipsNonLinuxAndExistingBwrap(t *testing.T) {
	withApt := lookPathPresent(map[string]bool{"apt-get": true})
	if plan := ResolveBubblewrap("darwin", "", withApt); plan.Needed {
		t.Fatalf("darwin plan = %#v; macOS uses sandbox-exec", plan)
	}
	withBwrap := lookPathPresent(map[string]bool{"bwrap": true, "apt-get": true})
	if plan := ResolveBubblewrap("linux", "", withBwrap); plan.Needed {
		t.Fatalf("plan = %#v; bwrap on PATH needs no package", plan)
	}
	if plan := ResolveBubblewrap("linux", "/nix/store/bwrap", withApt); plan.Needed {
		t.Fatalf("plan = %#v; BWRAP_BIN is already a choice", plan)
	}
}

func TestResolveBubblewrapMatchesEveryManager(t *testing.T) {
	for _, manager := range bubblewrapManagers() {
		present := map[string]bool{manager.Command: true}
		plan := ResolveBubblewrap("linux", "", lookPathPresent(present))
		if !plan.Needed || strings.Join(plan.Argv, " ") != strings.Join(manager.argv(), " ") {
			t.Fatalf("%s plan = %#v; want %v", manager.Command, plan, manager.argv())
		}
	}
}

func TestResolveBubblewrapPrefersPacmanOverAURHelpers(t *testing.T) {
	present := map[string]bool{"pacman": true, "pamac": true, "yay": true, "paru": true, "brew": true}
	plan := ResolveBubblewrap("linux", "", lookPathPresent(present))
	if got, want := plan.Argv, (packageManager{Command: "pacman", Args: []string{"-S", "--noconfirm", bubblewrapPackage}, Sudo: true}).argv(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v; want pacman %v", got, want)
	}
}

func TestResolveBubblewrapPrefersDnfOverYum(t *testing.T) {
	plan := ResolveBubblewrap("linux", "", lookPathPresent(map[string]bool{"dnf": true, "yum": true, "microdnf": true}))
	if len(plan.Argv) == 0 || plan.Argv[2] != "dnf" {
		t.Fatalf("argv = %v; want dnf", plan.Argv)
	}
}

func TestPropertyBubblewrapPlanPicksTheEarliestUnblockedManager(t *testing.T) {
	managers := bubblewrapManagers()
	rapid.Check(t, func(rt *rapid.T) {
		present := map[string]bool{}
		for _, manager := range managers {
			if rapid.Bool().Draw(rt, manager.Command) {
				present[manager.Command] = true
			}
		}
		plan := ResolveBubblewrap("linux", "", lookPathPresent(present))
		want, ok := firstUnblocked(managers, present)
		if !ok {
			if plan.Needed && plan.Argv != nil {
				rt.Fatalf("argv = %v; no manager was available", plan.Argv)
			}
			return
		}
		if strings.Join(plan.Argv, " ") != strings.Join(want.argv(), " ") {
			rt.Fatalf("argv = %v; want %v", plan.Argv, want.argv())
		}
	})
}

func firstUnblocked(managers []packageManager, present map[string]bool) (packageManager, bool) {
	for _, manager := range managers {
		if !present[manager.Command] {
			continue
		}
		skip := false
		for _, name := range manager.BlockedBy {
			if present[name] {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		return manager, true
	}
	return packageManager{}, false
}

func TestEnsureBubblewrapPrintsTheCommandWithoutRunningIt(t *testing.T) {
	var ran []string
	client := New(t.TempDir())
	client.GOOS = "linux"
	client.LookPath = lookPathPresent(map[string]bool{"apt-get": true})
	client.Run = func(context.Context, string, ...string) (string, error) {
		ran = append(ran, "ran")
		return "", nil
	}
	var out, errOut strings.Builder
	client.EnsureBubblewrap(context.Background(), false, &out, &errOut)
	if len(ran) != 0 {
		t.Fatal("package install ran without --install-system-deps")
	}
	if !strings.Contains(out.String(), "sudo -n env DEBIAN_FRONTEND=noninteractive apt-get install -y bubblewrap") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestEnsureBubblewrapRunsSudoWhenAsked(t *testing.T) {
	var got []string
	client := New(t.TempDir())
	client.GOOS = "linux"
	client.LookPath = lookPathPresent(map[string]bool{"apk": true})
	client.Run = func(_ context.Context, name string, args ...string) (string, error) {
		got = append([]string{name}, args...)
		return "", errors.New("sudo: a password is required")
	}
	var out, errOut strings.Builder
	client.EnsureBubblewrap(context.Background(), true, &out, &errOut)
	want := "sudo -n apk add --no-interactive bubblewrap"
	if strings.Join(got, " ") != want {
		t.Fatalf("ran %q; want %q", strings.Join(got, " "), want)
	}
	if !strings.Contains(errOut.String(), "password") {
		t.Fatalf("err = %q; a failed sudo must be visible and must not panic", errOut.String())
	}
}
