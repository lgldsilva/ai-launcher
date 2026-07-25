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

type Executor interface {
	Run(context.Context, []string, io.Reader, io.Writer, io.Writer) error
}

type PTYExecutor struct{}

func (PTYExecutor) Run(ctx context.Context, argv []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	return PTYExecutor{}.RunWithEnv(ctx, argv, nil, in, out, errOut)
}

func (PTYExecutor) RunWithEnv(ctx context.Context, argv, env []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	if len(argv) == 0 {
		return fmt.Errorf("cannot execute an empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if env != nil {
		cmd.Env = env
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("start %q: %w", argv[0], err)
	}
	defer ptmx.Close()
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
