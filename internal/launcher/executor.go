package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
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
//
// When in is a real terminal (typical after the launcher TUI quits), the host
// is switched to raw mode for the session. Without that, line discipline keeps
// echo + cooked input: arrow keys show as ^[[A/^[[B and TUIs such as oc's
// preset menu cannot navigate.
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

	restore := prepareHostTTY(in, ptmx)
	defer restore()

	if in != nil {
		// Feeds host stdin into the PTY. The goroutine ends when the child
		// exits and ptmx is closed (a pending write fails with EIO); a copy
		// blocked *reading* a live host stdin cannot be cancelled portably,
		// which is acceptable because Run returns — and the launcher exits —
		// right after the child does.
		go func() { _, _ = io.Copy(ptmx, in) }()
	}
	// A nil out discards the child's output, but the PTY must still be
	// drained: with nothing reading it, the child blocks on a full pipe
	// buffer and cmd.Wait below never returns.
	if out == nil {
		out = io.Discard
	}
	if _, copyErr := io.Copy(out, ptmx); copyErr != nil && ctx.Err() == nil && !errors.Is(copyErr, io.EOF) && !errors.Is(copyErr, syscall.EIO) {
		return fmt.Errorf("read command output: %w", copyErr)
	}
	if waitErr := cmd.Wait(); waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return waitErr
	}
	return nil
}

// prepareHostTTY puts a real terminal stdin into raw mode and mirrors window
// size into the PTY. Non-terminal readers (tests, pipes) are left alone.
func prepareHostTTY(in io.Reader, ptmx *os.File) (restore func()) {
	restore = func() {}
	f, ok := in.(*os.File)
	if !ok || f == nil {
		return restore
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return restore
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return restore
	}
	restore = func() { _ = term.Restore(fd, state) }

	// Match the host size so full-screen TUIs (oc, opencode, pi) lay out correctly.
	_ = pty.InheritSize(f, ptmx)
	stopResize := watchTTYResize(f, ptmx)
	prev := restore
	restore = func() {
		stopResize()
		prev()
	}
	return restore
}
