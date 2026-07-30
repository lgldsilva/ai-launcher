package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// PTYExecutor runs an argv attached to a PTY, so interactive agents behave as
// if launched directly from a terminal. It is backed by creack/pty.
type PTYExecutor struct{}

// RunWithEnv executes argv with an explicit environment (nil inherits the
// current one) attached to a newly allocated PTY.
//
// When in is a real terminal (typical after the launcher TUI quits), the host
// is switched to raw mode for the session. Without that, line discipline keeps
// echo + cooked input: arrow keys show as ^[[A/^[[B and TUIs such as oc's
// preset menu cannot navigate.
//
// Lifecycle: the child runs in its own process group (Unix). Context cancel
// closes the PTY and SIGKILLs the whole group so a descendant holding the
// slave cannot pin io.Copy forever. Every post-start return path reaps the
// child — including PTY copy failures — so no orphan is left behind.
func (PTYExecutor) RunWithEnv(ctx context.Context, argv, env []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	if len(argv) == 0 {
		return fmt.Errorf("cannot execute an empty command")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Managed cancel: process-group kill rather than CommandContext alone,
	// which only signals the direct child and leaves PTY-holding descendants.
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv is built from the user's own launcher configuration; running it is the tool's purpose
	if env != nil {
		cmd.Env = env
	}
	configureProcessGroup(cmd)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("start %q: %w", argv[0], err)
	}
	// Close is idempotent enough for our purposes (second close returns an
	// error we ignore). Cancel watcher and the success path both may close.
	defer func() { _ = ptmx.Close() }()

	stopCancel := watchContextCancel(ctx, cmd, ptmx)
	defer stopCancel()

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
	// Tee the PTY stream into a rolling tail as well as the caller's out: the
	// user still sees everything live, but on failure the launcher can read the
	// child's final output (typically the underlying error, e.g. ai-memory's
	// "409 workstream is already active") to pattern-match a recovery hint.
	tail := &tailBuffer{cap: 8192}
	_, copyErr := io.Copy(io.MultiWriter(out, tail), ptmx)

	// Always reap. Close the master first so writers unblock; kill the group
	// when cancel fired or the output path failed for a real reason.
	_ = ptmx.Close()
	realCopyErr := copyErr != nil &&
		ctx.Err() == nil &&
		!errors.Is(copyErr, io.EOF) &&
		!errors.Is(copyErr, syscall.EIO)
	if ctx.Err() != nil || realCopyErr {
		killProcessGroup(cmd)
	}
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if realCopyErr {
		return fmt.Errorf("read command output: %w", copyErr)
	}
	if waitErr != nil {
		if t := strings.TrimSpace(tail.String()); t != "" {
			return fmt.Errorf("%w\nlast output:\n%s", waitErr, t)
		}
		return waitErr
	}
	return nil
}

// watchContextCancel closes the PTY and kills the process group when ctx is
// done, unblocking io.Copy that would otherwise hang on a retained slave.
func watchContextCancel(ctx context.Context, cmd *exec.Cmd, ptmx *os.File) (stop func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = ptmx.Close()
			killProcessGroup(cmd)
		case <-done:
		}
	}()
	return func() { close(done) }
}

// prepareHostTTY puts a real terminal stdin into raw mode and mirrors window
// size into the PTY. Non-terminal readers (tests, pipes) are left alone.
func prepareHostTTY(in io.Reader, ptmx *os.File) (restore func()) {
	restore = func() {
		// Intentionally empty: default restore when the host TTY was never
		// touched (non-terminal input or MakeRaw failed), so the deferred
		// restore() call in RunWithEnv is always safe.
	}
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

// tailBuffer is an io.Writer that retains only the last cap bytes written to it.
// It tees alongside the caller's real output so the launcher can surface the
// child's final output (its error message) in its own error without buffering
// an unbounded interactive session.
type tailBuffer struct {
	cap int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.cap {
		t.buf = t.buf[len(t.buf)-t.cap:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }
