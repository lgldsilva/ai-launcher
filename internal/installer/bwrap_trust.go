package installer

import "strings"

// BwrapFile is what ai-jail reads before it runs a bwrap binary: the path
// with symlinks resolved, the owner, and the raw st_mode permission bits
// (sticky bit included).
type BwrapFile struct {
	Path  string
	UID   uint32
	Mode  uint32
	IsDir bool
}

// BwrapProbe resolves symlinks in path and stats the result. It is a field
// on the callers so tests never touch the host's real /usr/bin/bwrap.
type BwrapProbe func(path string) (BwrapFile, error)

// nixStore is the only non-root-owned location ai-jail accepts, and only
// while the store itself is protected.
const nixStore = "/nix/store"

// bwrapCandidates are the fixed paths ai-jail tries after BWRAP_BIN and
// before PATH (ai-jail src/sandbox/bwrap.rs, BWRAP_CANDIDATES).
var bwrapCandidates = []string{
	"/usr/bin/bwrap",
	"/bin/bwrap",
	"/usr/local/bin/bwrap",
	"/run/wrappers/bin/bwrap",
	"/run/current-system/sw/bin/bwrap",
}

// bwrapSearch is the outcome of looking for a bwrap ai-jail would run.
// Untrusted is the first bwrap that exists but that ai-jail refuses, so the
// preflight can say why a bwrap on PATH does not count.
type bwrapSearch struct {
	Found     bool
	Untrusted string
}

// findTrustedBwrap mirrors ai-jail's bwrap_binary_path: BWRAP_BIN first,
// then the fixed candidates, then PATH. A nil probe keeps the older
// PATH-only answer, for callers that have no filesystem to ask. PATH is
// read through lookPath, which returns only its first match; ai-jail keeps
// scanning, so a trusted bwrap behind an untrusted one is reported missing.
func findTrustedBwrap(bwrapBin string, lookPath func(string) (string, error), probe BwrapProbe) bwrapSearch {
	bwrapBin = strings.TrimSpace(bwrapBin)
	if probe == nil {
		return bwrapSearch{Found: bwrapBin != "" || commandFound(lookPath, "bwrap")}
	}
	var paths []string
	if bwrapBin != "" {
		paths = append(paths, bwrapBin)
	}
	paths = append(paths, bwrapCandidates...)
	if lookPath != nil {
		if path, err := lookPath("bwrap"); err == nil {
			paths = append(paths, path)
		}
	}
	var search bwrapSearch
	for _, path := range paths {
		file, err := probe(path)
		if err != nil {
			continue
		}
		if trustedBwrapFile(file, probe) {
			return bwrapSearch{Found: true}
		}
		if search.Untrusted == "" {
			search.Untrusted = file.Path
		}
	}
	return search
}

// trustedBwrapFile is ai-jail's trusted_binary_metadata: an executable
// regular file owned by root with no group or world write, or a file with
// no write bits at all inside a protected Nix store.
func trustedBwrapFile(file BwrapFile, probe BwrapProbe) bool {
	if file.IsDir || file.Mode&0o111 == 0 {
		return false
	}
	if file.UID == 0 {
		return file.Mode&0o022 == 0
	}
	if strings.HasPrefix(file.Path, nixStore+"/") && nixStoreProtected(probe) {
		return file.Mode&0o222 == 0
	}
	return false
}

// nixStoreProtected is ai-jail's nix_store_metadata_is_protected. A
// group-writable store needs the sticky bit; a root- or nobody-owned store
// must not be world-writable; a store owned by anyone else, such as a
// single-user install, must carry no write bits at all.
func nixStoreProtected(probe BwrapProbe) bool {
	if probe == nil {
		return false
	}
	store, err := probe(nixStore)
	if err != nil || !store.IsDir {
		return false
	}
	if store.Mode&0o020 != 0 && store.Mode&0o1000 == 0 {
		return false
	}
	if store.UID == 0 || store.UID == 65534 {
		return store.Mode&0o002 == 0
	}
	return store.Mode&0o222 == 0
}
