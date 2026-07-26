package launcher

import (
	"os"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func mountModeValidator(t *testing.T) Validator {
	t.Helper()
	return Validator{
		GOOS:     "linux",
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
}

func issueWithCode(issues []Issue, code string) *Issue {
	for i := range issues {
		if issues[i].Code == code {
			return &issues[i]
		}
	}
	return nil
}

// The gh permission needs read-write access to ~/.config/gh (gh auth login
// writes there). A configured read-only mount covering ~/.config satisfies the
// path dedup but not the permission: the degradation must be visible.
func TestValidatorWarnsWhenPermissionMountCoveredReadOnly(t *testing.T) {
	issues := mountModeValidator(t).Validate(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		HomeDir:     "/home/tester",
		UseJail:     true,
		Permissions: map[string]bool{"gh": true},
		Mounts:      []config.Mount{{Path: "/home/tester/.config", Mode: "ro"}},
	})
	issue := issueWithCode(issues, "permission-mount-downgraded")
	if issue == nil {
		t.Fatalf("issues = %#v; want permission-mount-downgraded", issues)
	}
	if !issue.Warning {
		t.Error("an explicit operator choice degrades the permission; warn, don't fail")
	}
	if !strings.Contains(issue.Message, "gh") {
		t.Errorf("message must name the permission: %q", issue.Message)
	}
}

// A covering read-write mount already grants everything the permission needs,
// so there is nothing to report.
func TestValidatorAcceptsPermissionMountCoveredReadWrite(t *testing.T) {
	issues := mountModeValidator(t).Validate(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		HomeDir:     "/home/tester",
		UseJail:     true,
		Permissions: map[string]bool{"gh": true},
		Mounts:      []config.Mount{{Path: "/home/tester/.config", Mode: "rw"}},
	})
	if issue := issueWithCode(issues, "permission-mount-downgraded"); issue != nil {
		t.Fatalf("rw coverage must not warn: %#v", issues)
	}
}

// With no home directory known the permission's mount is omitted (fail
// closed). The omission must say so, like every other silent-drop fix.
func TestValidatorWarnsWhenPermissionMountHasNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	issues := mountModeValidator(t).Validate(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		Permissions: map[string]bool{"gh": true},
	})
	issue := issueWithCode(issues, "permission-mount-without-home")
	if issue == nil {
		t.Fatalf("issues = %#v; want permission-mount-without-home", issues)
	}
	if !issue.Warning {
		t.Error("the mount is omitted by design; warn, don't fail")
	}
}
