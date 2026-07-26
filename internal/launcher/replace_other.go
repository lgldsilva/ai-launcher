//go:build !darwin && !linux

package launcher

import (
	"fmt"
	"os"
	"os/exec"
)

// Replace is the unsupported-platform stand-in for the unix process-image
// swap. Platforms without syscall.Exec cannot replace the running process,
// so the child runs in the foreground with the inherited terminal and
// Replace returns its exit error — the same error surface as the unix
// version (PATH resolution included), with a parent process kept alive.
func Replace(argv []string) error {
	return ReplaceWithEnv(argv, nil)
}

// ReplaceWithEnv is Replace with an explicit environment (nil inherits the
// current one). The path is resolved through PATH before spawning, matching
// the unix version's "resolve %q" error instead of exec's opaque one.
func ReplaceWithEnv(argv, env []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("cannot replace with an empty command")
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("resolve %q: %w", argv[0], err)
	}
	cmd := exec.Command(path, argv[1:]...) // #nosec G204 -- argv is built from the user's own launcher configuration; running it is the tool's purpose
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if env != nil {
		cmd.Env = env
	}
	return cmd.Run()
}
