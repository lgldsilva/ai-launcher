package launcher

import (
	"os"
	"testing"
)

// The TUI and the CLI both needed this default and each grew its own copy;
// they disagreed on the Getwd error path, one leaving ProjectDir empty — the
// exact state the function exists to prevent. One implementation, three
// branches worth stating.
func TestEnsureDockerProjectDir(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("fills an empty project dir under docker", func(t *testing.T) {
		got := EnsureDockerProjectDir(LaunchConfig{UseDocker: true})
		if got.ProjectDir != wd {
			t.Errorf("ProjectDir = %q; want the working directory %q", got.ProjectDir, wd)
		}
	})

	t.Run("leaves an explicit project dir alone", func(t *testing.T) {
		got := EnsureDockerProjectDir(LaunchConfig{UseDocker: true, ProjectDir: "/w"})
		if got.ProjectDir != "/w" {
			t.Errorf("ProjectDir = %q; --project-dir is the operator's answer", got.ProjectDir)
		}
	})

	t.Run("does nothing without docker", func(t *testing.T) {
		got := EnsureDockerProjectDir(LaunchConfig{})
		if got.ProjectDir != "" {
			t.Errorf("ProjectDir = %q; jail gets the working directory implicitly", got.ProjectDir)
		}
	})
}
