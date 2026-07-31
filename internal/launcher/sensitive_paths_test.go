package launcher

import (
	"strings"
	"testing"
)

func TestDeniedMountRootAndSystemTrees(t *testing.T) {
	mustRefuse := []string{
		"/",
		"/etc", "/etc/passwd", "/usr", "/usr/bin",
		"/var", "/var/log", "/var/run/docker.sock",
		"/tmp", "/tmp/x", "/proc", "/sys",
		"/root", "/private/etc",
		"/home", "/Users",
		"/Volumes", "/media", "/mnt", "/run", "/srv",
	}
	for _, path := range mustRefuse {
		t.Run("refuse "+path, func(t *testing.T) {
			if got := DeniedMount(path); got == nil {
				t.Fatalf("DeniedMount(%q) = nil; want refusal", path)
			}
		})
	}
}

func TestDeniedMountControlSockets(t *testing.T) {
	for _, path := range []string{
		"/var/run/docker.sock",
		"/run/podman.sock",
		"/Users/me/.docker/run/docker.sock",
	} {
		t.Run(path, func(t *testing.T) {
			got := DeniedMount(path)
			if got == nil {
				t.Fatalf("DeniedMount(%q) = nil; control sockets must be refused", path)
			}
			if !strings.Contains(got.Reason, "socket") {
				t.Errorf("reason = %q; want socket mention", got.Reason)
			}
		})
	}
}

func TestDeniedMountAllowsProjectPaths(t *testing.T) {
	// Operator home projects and attached volumes stay mountable from local
	// config; only the all-user / volume roots themselves are refused.
	safe := []string{
		"/storage/cache",
		"/storage/Projetos/ai-launcher",
		"/Users/me/code",
		"/home/me/src",
		"/Volumes/MSD512/Projetos",
		"/media/data/work",
	}
	for _, path := range safe {
		t.Run(path, func(t *testing.T) {
			if got := DeniedMount(path); got != nil {
				t.Fatalf("DeniedMount(%q) = %v; project paths must be allowed", path, got)
			}
		})
	}
}
