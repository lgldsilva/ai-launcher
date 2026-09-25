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
	withApt := lookPathPresent(map[string]bool{"apt-get": true, "sudo": true})
	if plan := ResolveBubblewrap("darwin", "", false, withApt); plan.Needed {
		t.Fatalf("darwin plan = %#v; macOS uses sandbox-exec", plan)
	}
	withBwrap := lookPathPresent(map[string]bool{"bwrap": true, "apt-get": true})
	if plan := ResolveBubblewrap("linux", "", false, withBwrap); plan.Needed {
		t.Fatalf("plan = %#v; bwrap on PATH needs no package", plan)
	}
	if plan := ResolveBubblewrap("linux", "/nix/store/bwrap", false, withApt); plan.Needed {
		t.Fatalf("plan = %#v; BWRAP_BIN is already a choice", plan)
	}
}

func TestResolveBubblewrapMatchesEveryManager(t *testing.T) {
	for _, manager := range bubblewrapManagers() {
		present := map[string]bool{manager.Command: true, "sudo": true}
		plan := ResolveBubblewrap("linux", "", false, lookPathPresent(present))
		if !plan.Needed || strings.Join(plan.Argv, " ") != strings.Join(manager.execArgv(manager.Sudo), " ") {
			t.Fatalf("%s argv = %#v; want %v", manager.Command, plan.Argv, manager.execArgv(manager.Sudo))
		}
		if strings.Contains(plan.Text, " -n ") || strings.HasPrefix(plan.Text, "sudo -n ") {
			t.Fatalf("%s text = %q; the printed command must not pass sudo -n", manager.Command, plan.Text)
		}
	}
}

func TestResolveBubblewrapSkipsSudoForRootAndForAHostWithoutSudo(t *testing.T) {
	apt := lookPathPresent(map[string]bool{"apt-get": true, "sudo": true})
	asRoot := ResolveBubblewrap("linux", "", true, apt)
	if strings.Contains(strings.Join(asRoot.Argv, " "), "sudo") {
		t.Fatalf("root argv = %v; sudo is not installed in a typical root container", asRoot.Argv)
	}
	rootNoSudo := ResolveBubblewrap("linux", "", true, lookPathPresent(map[string]bool{"apt-get": true}))
	if got := strings.Join(rootNoSudo.Argv, " "); got != "env DEBIAN_FRONTEND=noninteractive sh -c apt-get update && apt-get install -y bubblewrap" {
		t.Fatalf("root argv = %q", got)
	}
}

// A normal user on a host with doas and no sudo cannot install a package by
// running the manager directly. The plan runs nothing and says to use root.
func TestResolveBubblewrapNamesARootShellForAUserWithoutSudo(t *testing.T) {
	plan := ResolveBubblewrap("linux", "", false, lookPathPresent(map[string]bool{"apk": true}))
	if !plan.Needed || plan.Argv != nil {
		t.Fatalf("plan = %#v; want no argv for a user without sudo", plan)
	}
	if plan.Text != "as root, run: apk add --no-interactive bubblewrap" {
		t.Fatalf("text = %q", plan.Text)
	}
	nix := ResolveBubblewrap("linux", "", false, lookPathPresent(map[string]bool{"nix": true}))
	if nix.Argv == nil || nix.Argv[0] != "nix" {
		t.Fatalf("nix plan = %#v; a per-user manager still runs without sudo", nix)
	}
}

func TestResolveBubblewrapSyncsXbpsAndEnablesNixFlakes(t *testing.T) {
	xbps := ResolveBubblewrap("linux", "", true, lookPathPresent(map[string]bool{"xbps-install": true}))
	if got := strings.Join(xbps.Argv, " "); got != "xbps-install -S -y bubblewrap" {
		t.Fatalf("xbps argv = %q", got)
	}
	nix := ResolveBubblewrap("linux", "", true, lookPathPresent(map[string]bool{"nix": true}))
	want := "nix --extra-experimental-features nix-command flakes profile install nixpkgs#bubblewrap"
	if got := strings.Join(nix.Argv, " "); got != want {
		t.Fatalf("nix argv = %q", got)
	}
	quoted := "nix --extra-experimental-features 'nix-command flakes' profile install nixpkgs#bubblewrap"
	if nix.Text != quoted {
		t.Fatalf("nix text = %q", nix.Text)
	}
}

func TestResolveBubblewrapPrefersPacmanOverAURHelpers(t *testing.T) {
	present := map[string]bool{"pacman": true, "pamac": true, "yay": true, "paru": true, "brew": true, "sudo": true}
	plan := ResolveBubblewrap("linux", "", false, lookPathPresent(present))
	want := (packageManager{Command: "pacman", Args: []string{"-S", "--noconfirm", bubblewrapPackage}, Sudo: true}).execArgv(true)
	if strings.Join(plan.Argv, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v; want pacman %v", plan.Argv, want)
	}
}

func TestResolveBubblewrapPrefersDnfOverYum(t *testing.T) {
	plan := ResolveBubblewrap("linux", "", false, lookPathPresent(map[string]bool{"dnf": true, "yum": true, "microdnf": true, "sudo": true}))
	if len(plan.Argv) < 3 || plan.Argv[2] != "dnf" {
		t.Fatalf("argv = %v; want dnf", plan.Argv)
	}
}

func TestPropertyBubblewrapPlanPicksTheEarliestUnblockedManager(t *testing.T) {
	managers := bubblewrapManagers()
	rapid.Check(t, func(rt *rapid.T) {
		present := drawBubblewrapPresence(rt, managers)
		root := rapid.Bool().Draw(rt, "root")
		plan := ResolveBubblewrap("linux", "", root, lookPathPresent(present))
		assertEarliestUnblockedPlan(rt, managers, present, root, plan)
	})
}

func drawBubblewrapPresence(rt *rapid.T, managers []packageManager) map[string]bool {
	present := map[string]bool{}
	for _, manager := range managers {
		if rapid.Bool().Draw(rt, manager.Command) {
			present[manager.Command] = true
		}
	}
	present["sudo"] = rapid.Bool().Draw(rt, "sudo")
	return present
}

func assertEarliestUnblockedPlan(rt *rapid.T, managers []packageManager, present map[string]bool, root bool, plan BubblewrapPlan) {
	want, ok := firstUnblocked(managers, present)
	if !ok {
		if plan.Needed && plan.Argv != nil {
			rt.Fatalf("argv = %v; no manager was available", plan.Argv)
		}
		return
	}
	elevate := want.Sudo && !root
	if elevate && !present["sudo"] {
		if plan.Argv != nil || !strings.HasPrefix(plan.Text, "as root, run: ") {
			rt.Fatalf("plan = %#v; a user without sudo gets a root-shell hint, not an argv", plan)
		}
		return
	}
	if strings.Join(plan.Argv, " ") != strings.Join(want.execArgv(elevate), " ") {
		rt.Fatalf("argv = %v; want %v", plan.Argv, want.execArgv(elevate))
	}
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
	t.Setenv("BWRAP_BIN", "")
	var ran []string
	client := New(t.TempDir())
	client.GOOS = "linux"
	client.LookPath = lookPathPresent(map[string]bool{"apt-get": true, "sudo": true})
	client.Run = func(context.Context, string, ...string) (string, error) {
		ran = append(ran, "ran")
		return "", nil
	}
	var out, errOut strings.Builder
	client.EnsureBubblewrap(context.Background(), false, &out, &errOut)
	if len(ran) != 0 {
		t.Fatal("package install ran without --install-system-deps")
	}
	want := "sudo env DEBIAN_FRONTEND=noninteractive sh -c 'apt-get update && apt-get install -y bubblewrap'"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("out = %q", out.String())
	}
	if strings.Contains(out.String(), "sudo -n") {
		t.Fatalf("out = %q; the printed command must be pasteable", out.String())
	}
}

func TestEnsureBubblewrapRunsSudoWhenAsked(t *testing.T) {
	t.Setenv("BWRAP_BIN", "")
	var got []string
	client := New(t.TempDir())
	client.GOOS = "linux"
	client.LookPath = lookPathPresent(map[string]bool{"apk": true, "sudo": true})
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
