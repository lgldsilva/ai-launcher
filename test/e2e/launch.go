//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type launchSpec struct {
	agent     string
	jailBin   string
	memory    bool
	noNetwork bool
	docker    bool
	dryRun    bool
	profile   string
	timeout   time.Duration
	wantCode  int
	wantText  string
	wantArgs  []string
	forbids   []string
}

type launchResult struct {
	code   int
	stdout string
	stderr string
}

func (s launchSpec) args(global, local string) []string {
	args := []string{"--config", global, "--local-config", local, "--agent", s.agent}
	if s.profile != "" {
		args = append(args, "--profile", s.profile)
	}
	args = append(args, memoryFlag(s.memory)...)
	if s.noNetwork {
		args = append(args, "--no-network")
	}
	if s.docker {
		args = append(args, "--docker-backend", "--stack", "python")
	}
	if s.dryRun {
		args = append(args, "--dry-run")
	}
	return append(args, "--", "--version")
}

func memoryFlag(on bool) []string {
	if on {
		return []string{"--memory", "--new", "e2e"}
	}
	return []string{"--no-memory"}
}

func runLaunch(ctx context.Context, bin string, spec launchSpec, env []string, dir, global, local string) launchResult {
	if spec.timeout == 0 {
		spec.timeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, spec.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, spec.args(global, local)...) // #nosec G204 -- argv is the e2e matrix, built in this package
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return launchResult{code: exitCode(err), stdout: stdout.String(), stderr: stderr.String()}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errorsAsExit(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func errorsAsExit(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*target = exitErr
	}
	return ok
}

func checkLaunch(spec launchSpec, got launchResult) error {
	if got.code != spec.wantCode {
		return fmt.Errorf("exit %d, want %d\nstderr:\n%s", got.code, spec.wantCode, tail(got.stderr))
	}
	text := got.stdout + got.stderr
	if spec.wantText != "" && !strings.Contains(text, spec.wantText) {
		return fmt.Errorf("missing %q\nstdout:\n%s\nstderr:\n%s", spec.wantText, tail(got.stdout), tail(got.stderr))
	}
	if err := requireArgs(text, spec.wantArgs); err != nil {
		return fmt.Errorf("%w\nstderr:\n%s", err, tail(got.stderr))
	}
	if err := rejectArgs(text, spec.forbids); err != nil {
		return fmt.Errorf("%w\nstderr:\n%s", err, tail(got.stderr))
	}
	return nil
}

func requireArgs(text string, args []string) error {
	for _, arg := range args {
		if !strings.Contains(text, arg) {
			return fmt.Errorf("missing arg %q", arg)
		}
	}
	return nil
}

func rejectArgs(text string, args []string) error {
	for _, arg := range args {
		if strings.Contains(text, arg) {
			return fmt.Errorf("forbidden arg %q", arg)
		}
	}
	return nil
}

func tail(text string) string {
	if len(text) <= 2000 {
		return text
	}
	return text[len(text)-2000:]
}

func pathWith(dir, jailName, jailBin, memoryBin, stubs string) ([]string, error) {
	binDir := filepath.Join(dir, "bin-"+jailName)
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return nil, err
	}
	if err := link(jailBin, filepath.Join(binDir, "ai-jail")); err != nil {
		return nil, err
	}
	if err := link(memoryBin, filepath.Join(binDir, "ai-memory")); err != nil {
		return nil, err
	}
	base := os.Getenv("PATH")
	return []string{"PATH=" + stubs + string(os.PathListSeparator) + binDir + string(os.PathListSeparator) + base}, nil
}

func link(src, dest string) error {
	_ = os.Remove(dest)
	return os.Symlink(src, dest)
}

func keepWritable(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		_ = exec.Command("chmod", "-R", "u+w", root).Run()
	})
}

func baseEnv(home, project string) []string {
	env := []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"AI_MEMORY_RUN_AUTOWIRE=false",
		"TERM=dumb",
	}
	for _, key := range []string{"USER", "LOGNAME", "LANG", "LC_ALL", "TMPDIR", "PATH"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	_ = project
	return env
}
