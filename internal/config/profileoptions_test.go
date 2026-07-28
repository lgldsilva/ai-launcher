package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeGlobalWithProfiles writes a global config carrying the given profiles
// block and returns its path.
func writeGlobalWithProfiles(t *testing.T, profilesYAML string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "version: \"2.0\"\nprofiles:\n" + profilesYAML
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ARCHITECTURE invariant 6 says an omitted jail or memory means true. It used
// to hold only for .ai-launch.yaml, because LoadLocal probed the document for
// declared keys while a profile's options block decoded straight into Go's zero
// values. A profile naming only `yolo` therefore launched with the sandbox and
// memory off — the invariant's own defect, arriving through the global config.
func TestProfileOptionsKeepSafeDefaultsForOmittedKeys(t *testing.T) {
	path := writeGlobalWithProfiles(t, "  partial:\n    agent: claude\n    options:\n      yolo: true\n")
	global, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	options := global.Profiles["partial"].Options
	if options == nil {
		t.Fatal("profile options = nil; want the parsed block")
	}
	if !options.Jail {
		t.Error("options.Jail = false; an omitted jail key must keep the sandbox on")
	}
	if !options.Memory {
		t.Error("options.Memory = false; an omitted memory key must keep memory on")
	}
	if !options.Yolo {
		t.Error("options.Yolo = false; the explicitly declared key was lost")
	}
}

// The default is for omission only. Denial is a different answer and has to
// survive, or a profile could never turn either integration off.
func TestProfileOptionsPreserveExplicitFalse(t *testing.T) {
	path := writeGlobalWithProfiles(t,
		"  off:\n    agent: claude\n    options:\n      jail: false\n      memory: false\n")
	global, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	options := global.Profiles["off"].Options
	if options == nil {
		t.Fatal("profile options = nil; want the parsed block")
	}
	if options.Jail {
		t.Error("options.Jail = true; an explicit jail: false must be preserved")
	}
	if options.Memory {
		t.Error("options.Memory = true; an explicit memory: false must be preserved")
	}
}

// A profile with no options block at all leaves the toggles to the workspace
// file, so the pointer must stay nil rather than materializing a defaulted
// block that would silently override the local selection.
func TestProfileWithoutOptionsBlockKeepsNilOptions(t *testing.T) {
	path := writeGlobalWithProfiles(t, "  bare:\n    agent: claude\n")
	global, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	if options := global.Profiles["bare"].Options; options != nil {
		t.Fatalf("profile options = %#v; want nil so the local options stay in charge", options)
	}
}

// The scalar extra_args form takes the same defaulting path, so it must not be
// the one spelling where a partial options block still turns the sandbox off.
func TestProfileScalarExtraArgsFormKeepsSafeDefaults(t *testing.T) {
	path := writeGlobalWithProfiles(t,
		"  scalar:\n    agent: claude\n    options:\n      extra_args: \"--model sonnet\"\n")
	global, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	options := global.Profiles["scalar"].Options
	if options == nil {
		t.Fatal("profile options = nil; want the parsed block")
	}
	if !options.Jail || !options.Memory {
		t.Errorf("options = %#v; the scalar form must keep the safe defaults too", options)
	}
	if len(options.ExtraArgs) != 2 || options.ExtraArgs[0] != "--model" {
		t.Errorf("options.ExtraArgs = %#v; want the scalar string split into words", options.ExtraArgs)
	}
}
