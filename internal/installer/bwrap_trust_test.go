package installer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// fakeFS is a probe over an in-memory table. A path absent from the table
// does not exist.
func fakeFS(files map[string]BwrapFile) BwrapProbe {
	return func(path string) (BwrapFile, error) {
		file, ok := files[path]
		if !ok {
			return BwrapFile{}, os.ErrNotExist
		}
		if file.Path == "" {
			file.Path = path
		}
		return file, nil
	}
}

func onPath(paths map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := paths[name]; ok {
			return path, nil
		}
		return "", errors.New("missing")
	}
}

var protectedStore = BwrapFile{UID: 0, Mode: 0o1775, IsDir: true}

// The cases are ai-jail's trusted_binary_metadata and
// nix_store_metadata_is_protected, one row per rule.
func TestTrustedBwrapFileFollowsAIJail(t *testing.T) {
	cases := []struct {
		name  string
		file  BwrapFile
		store *BwrapFile
		want  bool
	}{
		{"root 0755", BwrapFile{Path: "/usr/bin/bwrap", Mode: 0o755}, nil, true},
		{"root setuid 4755", BwrapFile{Path: "/usr/bin/bwrap", Mode: 0o4755}, nil, true},
		{"root group-writable", BwrapFile{Path: "/usr/bin/bwrap", Mode: 0o775}, nil, false},
		{"root world-writable", BwrapFile{Path: "/usr/bin/bwrap", Mode: 0o757}, nil, false},
		{"root not executable", BwrapFile{Path: "/usr/bin/bwrap", Mode: 0o644}, nil, false},
		{"directory", BwrapFile{Path: "/usr/bin/bwrap", Mode: 0o755, IsDir: true}, nil, false},
		{"linuxbrew user", BwrapFile{Path: "/home/linuxbrew/.linuxbrew/Cellar/bubblewrap/0.13.0/bin/bwrap", UID: 1000, Mode: 0o755}, nil, false},
		{"multi-user nix store", BwrapFile{Path: "/nix/store/abc-bubblewrap/bin/bwrap", UID: 30001, Mode: 0o555}, &protectedStore, true},
		{"nix file with a write bit", BwrapFile{Path: "/nix/store/abc-bubblewrap/bin/bwrap", UID: 30001, Mode: 0o755}, &protectedStore, false},
		{"group-writable store without sticky", BwrapFile{Path: "/nix/store/abc/bin/bwrap", UID: 30001, Mode: 0o555}, &BwrapFile{Mode: 0o775, IsDir: true}, false},
		{"single-user store", BwrapFile{Path: "/nix/store/abc/bin/bwrap", UID: 1000, Mode: 0o555}, &BwrapFile{UID: 1000, Mode: 0o755, IsDir: true}, false},
		{"read-only user store", BwrapFile{Path: "/nix/store/abc/bin/bwrap", UID: 1000, Mode: 0o555}, &BwrapFile{UID: 1000, Mode: 0o555, IsDir: true}, true},
		{"nobody-owned store in a user namespace", BwrapFile{Path: "/nix/store/abc/bin/bwrap", UID: 65534, Mode: 0o555}, &BwrapFile{UID: 65534, Mode: 0o1775, IsDir: true}, true},
		{"prefix is not the store", BwrapFile{Path: "/nix/storefront/bwrap", UID: 1000, Mode: 0o555}, &protectedStore, false},
	}
	for _, tc := range cases {
		files := map[string]BwrapFile{}
		if tc.store != nil {
			files[nixStore] = *tc.store
		}
		if got := trustedBwrapFile(tc.file, fakeFS(files)); got != tc.want {
			t.Errorf("%s: trusted = %t; want %t", tc.name, got, tc.want)
		}
	}
}

func TestResolveBubblewrapRefusesAnUntrustedBwrapOnPath(t *testing.T) {
	brewBwrap := "/home/linuxbrew/.linuxbrew/bin/bwrap"
	plan := ResolveBubblewrap(BwrapHost{
		GOOS:     "linux",
		Root:     true,
		LookPath: onPath(map[string]string{"bwrap": brewBwrap, "apt-get": "/usr/bin/apt-get"}),
		Probe:    fakeFS(map[string]BwrapFile{brewBwrap: {UID: 1000, Mode: 0o755}}),
	})
	if !plan.Needed || plan.Untrusted != brewBwrap {
		t.Fatalf("plan = %#v; a brew-owned bwrap does not satisfy ai-jail", plan)
	}
	if !strings.Contains(plan.Text, "apt-get install -y bubblewrap") {
		t.Fatalf("text = %q; want the distro package", plan.Text)
	}
}

// BWRAP_BIN is ignored by ai-jail when it fails the rule, and ai-jail then
// falls back to the fixed paths, so a root-owned /usr/bin/bwrap still counts.
func TestResolveBubblewrapFallsBackPastAnUntrustedBwrapBin(t *testing.T) {
	host := BwrapHost{
		GOOS:     "linux",
		BwrapBin: "/home/me/bin/bwrap",
		LookPath: onPath(nil),
		Probe: fakeFS(map[string]BwrapFile{
			"/home/me/bin/bwrap": {UID: 1000, Mode: 0o755},
			"/usr/bin/bwrap":     {Mode: 0o755},
		}),
	}
	if plan := ResolveBubblewrap(host); plan.Needed {
		t.Fatalf("plan = %#v; /usr/bin/bwrap is trusted", plan)
	}
	host.Probe = fakeFS(map[string]BwrapFile{"/home/me/bin/bwrap": {UID: 1000, Mode: 0o755}})
	if plan := ResolveBubblewrap(host); !plan.Needed || plan.Untrusted != "/home/me/bin/bwrap" {
		t.Fatalf("plan = %#v; an untrusted BWRAP_BIN alone does not count", plan)
	}
}

// A trusted fixed path counts even when PATH has no bwrap, as in ai-jail.
func TestResolveBubblewrapFindsAFixedPathOffPath(t *testing.T) {
	plan := ResolveBubblewrap(BwrapHost{
		GOOS:     "linux",
		LookPath: onPath(map[string]string{"apt-get": "/usr/bin/apt-get"}),
		Probe:    fakeFS(map[string]BwrapFile{"/run/wrappers/bin/bwrap": {Mode: 0o4511}}),
	})
	if plan.Needed {
		t.Fatalf("plan = %#v; the NixOS wrapper is trusted", plan)
	}
}

func TestResolveBubblewrapSkipsNixOnAnUnprotectedStore(t *testing.T) {
	path := onPath(map[string]string{"nix": "/home/me/.nix-profile/bin/nix", "guix": "/usr/bin/guix"})
	single := fakeFS(map[string]BwrapFile{nixStore: {UID: 1000, Mode: 0o755, IsDir: true}})
	plan := ResolveBubblewrap(BwrapHost{GOOS: "linux", LookPath: path, Probe: single})
	if len(plan.Argv) == 0 || plan.Argv[0] != "guix" {
		t.Fatalf("argv = %v; a single-user nix store yields a bwrap ai-jail refuses", plan.Argv)
	}
	multi := fakeFS(map[string]BwrapFile{nixStore: protectedStore})
	plan = ResolveBubblewrap(BwrapHost{GOOS: "linux", LookPath: path, Probe: multi})
	if len(plan.Argv) == 0 || plan.Argv[0] != "nix" {
		t.Fatalf("argv = %v; a multi-user store keeps the nix row", plan.Argv)
	}
}

func TestBubblewrapTableDropsBrewAndSlackpkg(t *testing.T) {
	for _, manager := range bubblewrapManagers() {
		if manager.Command == "brew" || manager.Command == "slackpkg" {
			t.Fatalf("%s is back in the table", manager.Command)
		}
	}
	plan := resolve("linux", "", true, lookPathPresent(map[string]bool{"brew": true, "slackpkg": true}))
	if plan.Argv != nil {
		t.Fatalf("argv = %v; neither manager yields a bwrap ai-jail runs", plan.Argv)
	}
}

func TestEnsureBubblewrapReportsABwrapStillUntrustedAfterTheInstall(t *testing.T) {
	t.Setenv("BWRAP_BIN", "")
	client := New(t.TempDir())
	client.GOOS = "linux"
	client.LookPath = onPath(map[string]string{"apt-get": "/usr/bin/apt-get", "sudo": "/usr/bin/sudo"})
	client.BwrapProbe = fakeFS(nil)
	client.Run = func(context.Context, string, ...string) (string, error) { return "", nil }
	var out, errOut strings.Builder
	client.EnsureBubblewrap(context.Background(), true, &out, &errOut)
	if !strings.Contains(errOut.String(), "still sees no bwrap it trusts") {
		t.Fatalf("err = %q; a run that leaves no trusted bwrap must say so", errOut.String())
	}
}

func TestEnsureBubblewrapNamesTheUntrustedBwrap(t *testing.T) {
	t.Setenv("BWRAP_BIN", "")
	client := New(t.TempDir())
	client.GOOS = "linux"
	client.LookPath = onPath(map[string]string{"bwrap": "/opt/bwrap", "dnf": "/usr/bin/dnf", "sudo": "/usr/bin/sudo"})
	client.BwrapProbe = fakeFS(map[string]BwrapFile{"/opt/bwrap": {UID: 1000, Mode: 0o755}})
	var out, errOut strings.Builder
	client.EnsureBubblewrap(context.Background(), false, &out, &errOut)
	if !strings.Contains(errOut.String(), "does not trust the bwrap at /opt/bwrap") {
		t.Fatalf("err = %q", errOut.String())
	}
	if !strings.Contains(out.String(), "sudo dnf install -y bubblewrap") {
		t.Fatalf("out = %q", out.String())
	}
}

// Whatever the owner and mode, a bwrap outside the store is trusted exactly
// when root owns it, it is executable, and neither group nor world can write.
func TestPropertyTrustOutsideTheStoreIsRootAndNoSharedWrite(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		file := BwrapFile{
			Path: "/usr/bin/bwrap",
			UID:  rapid.SampledFrom([]uint32{0, 1, 1000, 65534}).Draw(rt, "uid"),
			Mode: rapid.Uint32Range(0, 0o7777).Draw(rt, "mode"),
		}
		want := file.UID == 0 && file.Mode&0o111 != 0 && file.Mode&0o022 == 0
		if got := trustedBwrapFile(file, fakeFS(nil)); got != want {
			rt.Fatalf("uid %d mode %o: trusted = %t; want %t", file.UID, file.Mode, got, want)
		}
	})
}
