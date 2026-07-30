package launcher

import (
	"path/filepath"
	"strings"
)

// SensitiveMountReason describes why a mount path was refused as too broad or
// dangerous. Reason carries the operator-facing label; Path is the cleaned
// form of the rejected target.
type SensitiveMountReason struct {
	Path   string
	Reason string
}

// systemTreesWithDescendants are trees where mounting any path at or under the
// root hands the sandbox host authority (system state, runtime sockets, other
// users' aggregate homes as the root itself). Descendants of /home and /Users
// stay allowed so an operator can mount their own project tree from a local
// config; only the all-user root itself is refused. Same for /Volumes et al.:
// the root covers every volume, but /Volumes/MSD512 is a normal project path.
var systemTreesWithDescendants = []struct {
	reason string
	paths  []string
}{
	{
		reason: "a system directory",
		paths: []string{
			"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev",
			"/proc", "/sys", "/var", "/tmp", "/opt", "/root", "/System",
			"/Library", "/Applications", "/private", "/private/etc",
			"/private/var", "/private/tmp", "/nix", "/snap",
		},
	},
}

// exactOnlyDeniedTrees are refused only as the root itself. Paths beneath them
// are legitimate operator mounts (home projects, attached volumes).
var exactOnlyDeniedTrees = []struct {
	reason string
	paths  []string
}{
	{
		reason: "the home directory of every user on this machine",
		paths: []string{
			"/home", "/Users",
			"/System/Volumes/Data", "/System/Volumes/Data/home",
			"/System/Volumes/Data/Users",
		},
	},
	{
		reason: "a mount-point root covering every attached volume",
		paths:  []string{"/Volumes", "/media", "/mnt", "/run", "/srv"},
	},
}

// DeniedMount reports whether path must not be exposed by an untrusted local
// config mount. Returns nil when the path is acceptable for unsaved-local
// trust checks. CLI mounts and launcher-saved configs skip this gate.
func DeniedMount(path string) *SensitiveMountReason {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || clean == "." {
		return nil
	}
	if clean == string(filepath.Separator) {
		return &SensitiveMountReason{Path: clean, Reason: "the filesystem root"}
	}

	// Container runtime control sockets — always refused, wherever they live.
	base := filepath.Base(clean)
	if base == "docker.sock" || base == "podman.sock" {
		return &SensitiveMountReason{Path: clean, Reason: "a container-control socket (" + base + ")"}
	}

	for _, group := range systemTreesWithDescendants {
		for _, denied := range group.paths {
			d := filepath.Clean(denied)
			if clean == d {
				return &SensitiveMountReason{Path: clean, Reason: group.reason}
			}
			if strings.HasPrefix(clean, d+string(filepath.Separator)) {
				return &SensitiveMountReason{Path: clean, Reason: "under " + group.reason}
			}
		}
	}

	for _, group := range exactOnlyDeniedTrees {
		for _, denied := range group.paths {
			if clean == filepath.Clean(denied) {
				return &SensitiveMountReason{Path: clean, Reason: group.reason}
			}
		}
	}

	return nil
}
