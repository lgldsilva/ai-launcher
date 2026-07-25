package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// Executor runs an argv attached to a PTY so interactive agents behave as if
// launched directly from a terminal.
type Executor interface {
	// Run executes argv with the given stdio streams until it exits.
	Run(context.Context, []string, io.Reader, io.Writer, io.Writer) error
}

// PTYExecutor is the production Executor backed by creack/pty.
type PTYExecutor struct{}

// Run executes argv with the inherited environment.
func (PTYExecutor) Run(ctx context.Context, argv []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	return PTYExecutor{}.RunWithEnv(ctx, argv, nil, in, out, errOut)
}

// RunWithEnv executes argv with an explicit environment (nil inherits the
// current one) attached to a newly allocated PTY.
func (PTYExecutor) RunWithEnv(ctx context.Context, argv, env []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	if len(argv) == 0 {
		return fmt.Errorf("cannot execute an empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- argv is built from the user's own launcher configuration; running it is the tool's purpose
	if env != nil {
		cmd.Env = env
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("start %q: %w", argv[0], err)
	}
	defer func() { _ = ptmx.Close() }()
	if in != nil {
		go func() { _, _ = io.Copy(ptmx, in) }()
	}
	if out != nil {
		if _, copyErr := io.Copy(out, ptmx); copyErr != nil && ctx.Err() == nil && !errors.Is(copyErr, io.EOF) && !errors.Is(copyErr, syscall.EIO) {
			return fmt.Errorf("read command output: %w", copyErr)
		}
	}
	if waitErr := cmd.Wait(); waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return waitErr
	}
	return nil
}
