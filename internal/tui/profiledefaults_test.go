package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// loadGlobalWithProfile parses a real global config file, which is how profiles
// reach the TUI in production. Building config.Profile values in Go skips the
// unmarshaler that applies the option defaults, so it would not exercise the
// path this test is about.
func loadGlobalWithProfile(t *testing.T, profilesYAML string) config.Global {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: \"2.0\"\nprofiles:\n"+profilesYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	return global
}

// loadProfile assigns m.launch.UseJail straight from profile.Options.Jail, so
// the TUI inherited the same defect as the CLI: a profile whose options block
// never names jail turned the sandbox off on load, with the status line still
// reporting a plain "Profile loaded". Fixing the defaults in the config parser
// has to fix this screen too, since it reads the same parsed value.
func TestLoadingAProfileWithoutJailKeyKeepsTheSandboxOn(t *testing.T) {
	stubWindows(t, false)
	global := loadGlobalWithProfile(t, "  partial:\n    agent: claude\n    options:\n      yolo: true\n")
	model := NewModel(global, launcher.LaunchConfig{UseJail: true, UseMemory: true, Permissions: map[string]bool{}})

	model = applyKey(t, model, runeKey("5"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})

	if !model.launch.UseJail {
		t.Error("UseJail = false; a profile that never names jail must not disable the sandbox")
	}
	if !model.launch.UseMemory {
		t.Error("UseMemory = false; a profile that never names memory must not disable it")
	}
	if !model.launch.Yolo {
		t.Error("Yolo = false; the profile's explicit toggle was lost")
	}
}

// homeWithEscapingSymlink builds a home directory containing one hidden
// symlink whose target resolves outside it — the shape ARCHITECTURE invariant
// 9 exists for — and returns the home and the resolved target.
func homeWithEscapingSymlink(t *testing.T) (home, target string) {
	t.Helper()
	home = t.TempDir()
	outside := t.TempDir()
	target = filepath.Join(outside, "cache")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".cache")); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	return home, resolved
}

// Invariant 9 mounts every home dotfile symlink target that escapes $HOME,
// because ai-jail recreates the symlink inside the sandbox without its target.
// The launcher merges those before opening the TUI, but loadProfile replaced
// m.launch.Mounts wholesale — so loading any profile that declares mounts
// silently dropped them and the invariant stopped holding for that launch,
// with ~/.cache dangling inside the jail again and no warning anywhere.
func TestLoadingAProfileKeepsTheHomeSymlinkAutoMounts(t *testing.T) {
	stubWindows(t, false)
	home, autoTarget := homeWithEscapingSymlink(t)
	global := loadGlobalWithProfile(t,
		"  scoped:\n    agent: claude\n    mounts:\n      - path: /reference\n        mode: ro\n")

	model := NewModel(global, launcher.LaunchConfig{
		HomeDir:     home,
		UseJail:     true,
		Permissions: map[string]bool{},
		Mounts:      []config.Mount{{Path: autoTarget, Mode: "rw"}},
	})
	model = applyKey(t, model, runeKey("5"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})

	var sawAuto, sawProfile bool
	for _, mount := range model.launch.Mounts {
		switch mount.Path {
		case autoTarget:
			sawAuto = true
		case "/reference":
			sawProfile = true
		}
	}
	if !sawProfile {
		t.Errorf("mounts = %#v; the profile's own mounts must be applied", model.launch.Mounts)
	}
	if !sawAuto {
		t.Errorf("mounts = %#v; want the home symlink auto-mount %q kept (invariant 9)", model.launch.Mounts, autoTarget)
	}
}

// With the jail off there is no tmpfs $HOME and nothing dangles, so the
// auto-mounts have no reason to exist and a profile's mount list stands alone.
func TestLoadingAProfileWithoutJailDoesNotReaddAutoMounts(t *testing.T) {
	stubWindows(t, false)
	home, autoTarget := homeWithEscapingSymlink(t)
	global := loadGlobalWithProfile(t,
		"  scoped:\n    agent: claude\n    mounts:\n      - path: /reference\n        mode: ro\n    options:\n      jail: false\n")

	model := NewModel(global, launcher.LaunchConfig{
		HomeDir:     home,
		UseJail:     false,
		Permissions: map[string]bool{},
	})
	model = applyKey(t, model, runeKey("5"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})

	for _, mount := range model.launch.Mounts {
		if mount.Path == autoTarget {
			t.Fatalf("mounts = %#v; no jail means no dangling symlinks to compensate for", model.launch.Mounts)
		}
	}
}

// The same gap through the other door: applyJailAutoDetection runs in the
// launcher only when the jail is already on, so an `ai-launcher --no-jail` run
// whose operator turns Jail back on in the Options section reached a state
// nobody had prepared the mounts for.
func TestTurningTheJailOnInTheTUIAddsTheAutoMounts(t *testing.T) {
	stubWindows(t, false)
	home, autoTarget := homeWithEscapingSymlink(t)

	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{
		HomeDir:     home,
		UseJail:     false,
		Permissions: map[string]bool{},
	})
	// Options is the fourth section; Jail is its first row.
	model = applyKey(t, model, runeKey("4"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if !model.launch.UseJail {
		t.Fatalf("UseJail = false; the toggle did not fire (section=%d cursor=%d)", model.section, model.cursor)
	}

	for _, mount := range model.launch.Mounts {
		if mount.Path == autoTarget {
			return
		}
	}
	t.Fatalf("mounts = %#v; want the home symlink auto-mount %q after enabling the jail", model.launch.Mounts, autoTarget)
}

// The counterpart: a profile that does say jail: false is an operator decision
// stored in the trusted global config, and loading it must still turn the
// sandbox off.
func TestLoadingAProfileWithExplicitJailFalseStillDisablesTheSandbox(t *testing.T) {
	stubWindows(t, false)
	global := loadGlobalWithProfile(t,
		"  off:\n    agent: claude\n    options:\n      jail: false\n      memory: false\n")
	model := NewModel(global, launcher.LaunchConfig{UseJail: true, UseMemory: true, Permissions: map[string]bool{}})

	model = applyKey(t, model, runeKey("5"))
	model = applyKey(t, model, tea.KeyMsg{Type: tea.KeySpace})

	if model.launch.UseJail {
		t.Error("UseJail = true; an explicit jail: false must be honored")
	}
	if model.launch.UseMemory {
		t.Error("UseMemory = true; an explicit memory: false must be honored")
	}
}
