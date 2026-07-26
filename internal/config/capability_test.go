package config

import "testing"

func permissionByID(t *testing.T, id string) Permission {
	t.Helper()
	for _, permission := range DefaultGlobal().Permissions {
		if permission.ID == id {
			return permission
		}
	}
	t.Fatalf("permission %q is not in the built-in catalog", id)
	return Permission{}
}

// ai-jail auto-detects display, mise and worktree. A permission defaulting to
// true makes every launch force them on, and turning the toggle off cannot undo
// that — the boolean cannot express "auto". Default off means "leave ai-jail's
// auto-detection alone"; jail_flags is where a hard off lives.
func TestAutoDetectedPassthroughPermissionsDefaultOff(t *testing.T) {
	for _, id := range []string{"display", "mise", "worktree"} {
		if permission := permissionByID(t, id); permission.Default {
			t.Fatalf("permission %q defaults to true; ai-jail already auto-detects it", id)
		}
	}
}

// ai-jail documents display passthrough as X11/Wayland, Linux only.
func TestDisplayPermissionIsLinuxOnly(t *testing.T) {
	permission := permissionByID(t, "display")
	if len(permission.Platforms) != 1 || permission.Platforms[0] != "linux" {
		t.Fatalf("display platforms = %v; want [linux]", permission.Platforms)
	}
	if PermissionSupportedOn(permission, "darwin") {
		t.Fatal("display must not be offered on darwin")
	}
}

// The tri-state jail flags mirror ai-jail's own auto/on/off model, so every
// auto-mode capability needs a field to force it off.
func TestJailFlagsCoverTheAutoModePassthroughs(t *testing.T) {
	on := true
	flags := JailFlags{Display: &on, Mise: &on, Worktree: &on}
	if flags.IsZero() {
		t.Fatal("IsZero() = true; display, mise and worktree deviations must be visible")
	}
}
