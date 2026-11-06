package gherkin_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// cliInvocationGiven selects the scenarios that exercise the real binary. The
// help route lives in package main's flag wiring (cmd/ai-launcher), which a
// test binary cannot import, so the contract runs the built executable.
const cliInvocationGiven = "Given the launcher binary built from this tree"

// cliExecTimeout bounds one binary invocation so a regression that starts the
// interactive TUI cannot wedge the suite.
const cliExecTimeout = time.Minute

var (
	cliBinaryOnce sync.Once
	cliBinaryPath string
	cliBinaryDir  string
	cliBinaryErr  error
)

// buildLauncherBinary compiles the real CLI once per test binary and returns
// its path. -buildvcs=false matches the Makefile: the checkout may be a dirty
// git tree, and VCS stamping would fail the build instead of the test.
func buildLauncherBinary() (string, error) {
	cliBinaryOnce.Do(func() {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			cliBinaryErr = err
			return
		}
		cliBinaryDir, err = os.MkdirTemp("", "ai-launcher-gherkin-*")
		if err != nil {
			cliBinaryErr = err
			return
		}
		args := []string{"build", "-buildvcs=false", "-o", filepath.Join(cliBinaryDir, "ai-launcher"), "./cmd/ai-launcher"}
		build := exec.Command("go", args...) // #nosec G204 -- fixed module path, no user input
		build.Dir = root
		if output, err := build.CombinedOutput(); err != nil {
			cliBinaryErr = fmt.Errorf("go build ./cmd/ai-launcher: %w\n%s", err, output)
			return
		}
		cliBinaryPath = filepath.Join(cliBinaryDir, "ai-launcher")
	})
	return cliBinaryPath, cliBinaryErr
}

// cliRun records what one binary invocation printed and how it exited.
type cliRun struct {
	exitCode int
	stdout   string
	stderr   string
}

// runLauncherBinary executes the built CLI with args in a throwaway HOME and
// working directory, so the host's own configuration cannot leak into the
// output the contract asserts on.
func runLauncherBinary(t *testing.T, args []string) cliRun {
	t.Helper()
	path, err := buildLauncherBinary()
	if err != nil {
		t.Fatalf("build launcher binary: %v", err)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ctx, cancel := context.WithTimeout(context.Background(), cliExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- args come from the feature file in this repository
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = childEnv(t.TempDir())
	cmd.Dir = t.TempDir()
	err = cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("invocation of %v exceeded %s", args, cliExecTimeout)
	}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		return cliRun{exitCode: exitErr.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
	case err != nil:
		t.Fatalf("run %v: %v", args, err)
	}
	return cliRun{exitCode: 0, stdout: stdout.String(), stderr: stderr.String()}
}

// childEnv clears the variables that would point the launcher at the host's
// own configuration, then repoints HOME at the given directory. The appended
// assignment alone is not enough: getenv returns the first match, so an
// inherited HOME must actually leave the environment.
func childEnv(home string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "AI_LAUNCHER_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+home)
}

var (
	cliExitCodeExpectation     = regexp.MustCompile(`^(?:Then|And) the exit code is (\d+)$`)
	cliStdoutEmptyExpectation  = regexp.MustCompile(`^(?:Then|And) the standard output is empty$`)
	cliStderrStartsExpectation = regexp.MustCompile(`^(?:Then|And) the standard error starts with "(.+)"$`)
	cliUsageFlagExpectation    = regexp.MustCompile(`^(?:Then|And) the usage lists the flag "(-?[a-z0-9][a-z0-9-]*)"$`)
)

// runCLIInvocationScenario handles scenarios that run the real binary,
// reporting whether the scenario matched.
func runCLIInvocationScenario(t *testing.T, scenario featureScenario) bool {
	t.Helper()
	if _, ok := scenario.step(cliInvocationGiven); !ok {
		return false
	}
	when, ok := scenario.step("When the launcher is invoked with the arguments")
	if !ok {
		t.Fatal("binary invocation scenario must define the arguments to pass")
	}
	run := runLauncherBinary(t, nonEmptyLines(when.doc))
	checked := 0
	for _, step := range scenario.steps {
		checked += checkCLIExpectation(t, run, step.text)
	}
	if checked == 0 {
		t.Fatal("binary invocation scenario has no supported expectation")
	}
	return true
}

// checkCLIExpectation verifies one Then/And step against one binary run,
// returning 1 when the step was a recognized expectation.
func checkCLIExpectation(t *testing.T, run cliRun, text string) int {
	t.Helper()
	if match := cliExitCodeExpectation.FindStringSubmatch(text); match != nil {
		want, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("parse exit code %q: %v", match[1], err)
		}
		if run.exitCode != want {
			t.Fatalf("exit code = %d; want %d\nstdout:\n%s\nstderr:\n%s", run.exitCode, want, run.stdout, run.stderr)
		}
		return 1
	}
	if cliStdoutEmptyExpectation.MatchString(text) {
		if run.stdout != "" {
			t.Fatalf("standard output = %q; want empty", run.stdout)
		}
		return 1
	}
	if match := cliStderrStartsExpectation.FindStringSubmatch(text); match != nil {
		if !strings.HasPrefix(run.stderr, match[1]) {
			t.Fatalf("standard error does not start with %q:\n%s", match[1], run.stderr)
		}
		return 1
	}
	if match := cliUsageFlagExpectation.FindStringSubmatch(text); match != nil {
		assertUsageListsFlag(t, run.stderr, strings.TrimLeft(match[1], "-"))
		return 1
	}
	return 0
}

// assertUsageListsFlag fails unless one stderr line opens with the flag's
// PrintDefaults form ("  -name").
func assertUsageListsFlag(t *testing.T, stderr, name string) {
	t.Helper()
	flagLine := "  -" + name
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, flagLine) {
			return
		}
	}
	t.Fatalf("usage does not list the flag %q:\n%s", name, stderr)
}

// TestMain removes the scratch directory holding the built binary after the
// package finishes.
func TestMain(m *testing.M) {
	code := m.Run()
	if cliBinaryDir != "" {
		_ = os.RemoveAll(cliBinaryDir)
	}
	os.Exit(code)
}
