package config

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func toolAssets(t *testing.T, global Global, name string) []string {
	t.Helper()
	for _, tool := range global.Tools {
		if tool.Command != name {
			continue
		}
		if tool.Release == nil {
			t.Fatalf("tool %q has no release recipe", name)
		}
		keys := make([]string, 0, len(tool.Release.Assets))
		for key := range tool.Release.Assets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return keys
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func TestDefaultGlobalJailRecipeMatchesPublishedAssets(t *testing.T) {
	global := DefaultGlobal()
	// ai-jail v1.15 publishes only linux-x86_64 and macos-aarch64; there are
	// no linux-arm64, darwin-amd64, or Windows builds.
	if got, want := toolAssets(t, global, "ai-jail"), []string{"darwin-arm64", "linux-amd64"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ai-jail assets = %#v; want %#v", got, want)
	}
	// ai-memory publishes the four desktop tarballs and the windows zip.
	want := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64"}
	if got := toolAssets(t, global, "ai-memory"); !reflect.DeepEqual(got, want) {
		t.Errorf("ai-memory assets = %#v; want %#v", got, want)
	}
	for _, tool := range global.Tools {
		// The default catalog declares no unverified fallback: every tool
		// installs from checksum-verified release assets (invariant 4).
		if tool.SourceURL != "" {
			t.Errorf("tool %q declares a checksum-less source_url in the default catalog", tool.Command)
		}
	}
}

func TestJailFlagsRoundTripThroughLocalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.yaml")
	options := Options{
		Jail:   true,
		Memory: true,
		JailFlags: JailFlags{
			Lockdown:           boolPointer(true),
			GPU:                boolPointer(false),
			Browser:            "soft",
			ClaudeDir:          "/home/tester/.claude",
			Mask:               []string{"/etc/secrets"},
			DenyPaths:          []string{"/proc/kcore"},
			OverlayMaps:        []string{"/data"},
			AllowTCPPorts:      []int{8080},
			MaskExceptions:     []string{"/etc/secrets/allowed"},
			DenyPathExceptions: []string{"/proc/version"},
			HideDotdirs:        []string{".ssh"},
			StatusBarStyle:     "pastel",
		},
	}
	if err := SaveLocal(path, Local{Agent: "claude", Options: options}); err != nil {
		t.Fatalf("SaveLocal() error = %v", err)
	}
	loaded, err := LoadLocal(path)
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if !reflect.DeepEqual(loaded.Options.JailFlags, options.JailFlags) {
		t.Fatalf("jail flags = %#v; want %#v", loaded.Options.JailFlags, options.JailFlags)
	}
	if loaded.Options.JailFlags.IsZero() {
		t.Fatal("IsZero() = true for a populated JailFlags")
	}
	if !(JailFlags{}).IsZero() {
		t.Fatal("IsZero() = false for an empty JailFlags")
	}
}

func TestMemoryAuthTokenRoundTripThroughGlobalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "global.yaml")
	global := DefaultGlobal()
	global.MemoryAuthToken = "s3cret"
	if err := SaveGlobal(path, global); err != nil {
		t.Fatalf("SaveGlobal() error = %v", err)
	}
	loaded, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if loaded.MemoryAuthToken != "s3cret" {
		t.Fatalf("memory auth token = %q; want s3cret", loaded.MemoryAuthToken)
	}
}

func TestJailDependentIDsIncludesTransitiveRequirements(t *testing.T) {
	permissions := []Permission{
		{ID: "jail"},
		{ID: "docker", Requires: []string{"jail"}},
		{ID: "gpu", Requires: []string{"docker"}},
		{ID: "other"},
	}
	dependent := JailDependentIDs(permissions)
	for _, id := range []string{"jail", "docker", "gpu"} {
		if !dependent[id] {
			t.Errorf("JailDependentIDs() missing %q", id)
		}
	}
	if dependent["other"] {
		t.Error("JailDependentIDs() includes an unrelated permission")
	}
}

// IsZero must notice a deviation in every single field: dropping one operand
// from the && chain would make that field invisible to the zero check.
func TestJailFlagsIsZeroDetectsEverySingleFieldDeviation(t *testing.T) {
	mutators := map[string]func(*JailFlags){
		"lockdown":             func(f *JailFlags) { f.Lockdown = boolPointer(true) },
		"private_home":         func(f *JailFlags) { f.PrivateHome = boolPointer(false) },
		"tailscale":            func(f *JailFlags) { f.Tailscale = boolPointer(true) },
		"gpu":                  func(f *JailFlags) { f.GPU = boolPointer(true) },
		"display":              func(f *JailFlags) { f.Display = boolPointer(false) },
		"mise":                 func(f *JailFlags) { f.Mise = boolPointer(true) },
		"worktree":             func(f *JailFlags) { f.Worktree = boolPointer(false) },
		"landlock":             func(f *JailFlags) { f.Landlock = boolPointer(true) },
		"seccomp":              func(f *JailFlags) { f.Seccomp = boolPointer(false) },
		"rlimits":              func(f *JailFlags) { f.Rlimits = boolPointer(true) },
		"status_bar":           func(f *JailFlags) { f.StatusBar = boolPointer(true) },
		"hide_config":          func(f *JailFlags) { f.HideConfig = boolPointer(false) },
		"browser":              func(f *JailFlags) { f.Browser = "soft" },
		"claude_dir":           func(f *JailFlags) { f.ClaudeDir = "/home/tester/.claude" },
		"overlay_maps":         func(f *JailFlags) { f.OverlayMaps = []string{"/data"} },
		"mask":                 func(f *JailFlags) { f.Mask = []string{"/etc/secrets"} },
		"deny_paths":           func(f *JailFlags) { f.DenyPaths = []string{"/proc/kcore"} },
		"allow_tcp_ports":      func(f *JailFlags) { f.AllowTCPPorts = []int{8080} },
		"mask_exceptions":      func(f *JailFlags) { f.MaskExceptions = []string{"/srv/shared"} },
		"deny_path_exceptions": func(f *JailFlags) { f.DenyPathExceptions = []string{"/var/cache"} },
		"hide_dotdirs":         func(f *JailFlags) { f.HideDotdirs = []string{".aws"} },
		"status_bar_style":     func(f *JailFlags) { f.StatusBarStyle = "dark" },
	}
	for name, mutate := range mutators {
		var flags JailFlags
		mutate(&flags)
		if flags.IsZero() {
			t.Errorf("IsZero() = true with only %s set", name)
		}
	}
}

func boolPointer(value bool) *bool { return &value }
