//go:build !darwin && !linux

package launcher

import (
	"fmt"
	"os"
	"os/exec"
)

// Replace is unavailable on unsupported platforms; the CLI falls back to its
// PTY executor there.
func Replace(argv []string) error {
	return ReplaceWithEnv(argv, nil)
}

func ReplaceWithEnv(argv, env []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("cannot replace with an empty command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if env != nil {
		cmd.Env = env
	}
	return cmd.Run()
}
