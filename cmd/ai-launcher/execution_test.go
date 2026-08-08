package main

import (
	"io"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An interrupted runtime child (for example a docker build) must return to
// the caller — so EnsureImage removes its temp build context — instead of the
// launcher dying on the signal.
func TestDockerRunnerInterruptReturnsToCaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGINT delivery differs on Windows")
	}
	stub := writeExecutableStub(t, "#!/bin/sh\nexec sleep 30\n")
	req := &launchRequest{
		in:     strings.NewReader(""),
		out:    io.Discard,
		errOut: io.Discard,
	}
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := req.dockerRunner([]string{stub})
		done <- result{code: code, err: err}
	}()
	// Let the child exec before interrupting.
	time.Sleep(300 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	select {
	case got := <-done:
		if got.err == nil && got.code == 0 {
			t.Fatal("dockerRunner() reported success after SIGINT")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("dockerRunner() did not return after SIGINT")
	}
}
