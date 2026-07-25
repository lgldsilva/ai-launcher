//go:build darwin || linux

package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Replace turns the launcher into the selected process. This avoids keeping
// an extra PTY parent alive and leaves terminal ownership/lifecycle to the
// actual harness and the shell that started ai-launcher.
func Replace(argv []string) error {
	return ReplaceWithEnv(argv, nil)
}

// ReplaceWithEnv is Replace with an explicit environment (nil inherits the
// current one). The path is resolved through PATH before exec'ing.
func ReplaceWithEnv(argv, env []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("cannot replace with an empty command")
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("resolve %q: %w", argv[0], err)
	}
	if env == nil {
		env = os.Environ()
	}
	return syscall.Exec(path, argv, env) // #nosec G204 -- argv is built from the user's own launcher configuration; exec'ing it is the tool's purpose
}
