package launcher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestIssueErrorAndNewValidator(t *testing.T) {
	issue := Issue{Code: "x", Message: "y"}
	if issue.Error() != "x: y" {
		t.Fatalf("Issue.Error() = %q", issue.Error())
	}
	v := NewValidator()
	if v.LookPath == nil || v.Stat == nil {
		t.Fatal("NewValidator must provide filesystem functions")
	}
}

func TestValidatorAcceptsCompleteConfiguration(t *testing.T) {
	v := Validator{
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := v.Validate(LaunchConfig{
		Agent:       config.Agent{Command: "claude"},
		UseJail:     true,
		UseMemory:   true,
		Permissions: map[string]bool{"docker": true, "gpu": true},
		Mounts:      []config.Mount{{Path: ""}, {Path: "/work"}},
	})
	if len(issues) != 0 {
		t.Fatalf("complete configuration produced issues: %#v", issues)
	}
}

func TestValidatorReportsGpuWithoutDocker(t *testing.T) {
	v := Validator{
		LookPath: func(command string) (string, error) {
			if command == "claude" || command == "ai-jail" {
				return "/bin/" + command, nil
			}
			return "", errors.New("missing")
		},
		Stat: func(string) (os.FileInfo, error) { return nil, nil },
	}
	issues := v.Validate(LaunchConfig{Agent: config.Agent{Command: "claude"}, UseJail: true, Permissions: map[string]bool{"gpu": true}})
	if len(issues) != 1 {
		t.Fatalf("got issues %#v; want gpu dependency", issues)
	}
	if !strings.Contains(issues[0].Error(), "gpu permission requires docker") {
		t.Fatalf("unexpected dependency issue: %#v", issues)
	}
}

func TestValidatorUsesDefaultDependenciesWhenUnset(t *testing.T) {
	issues := (Validator{}).Validate(LaunchConfig{Agent: config.Agent{Command: "sh"}})
	if len(issues) != 0 {
		t.Fatalf("default validator reported issues for sh: %#v", issues)
	}
}

func TestValidatorUsesConfiguredExecutablePath(t *testing.T) {
	v := Validator{
		LookPath: func(command string) (string, error) {
			if command == "/opt/xpto/bin/xpto" {
				return command, nil
			}
			return "", errors.New("missing")
		},
	}
	issues := v.Validate(LaunchConfig{Agent: config.Agent{Command: "xpto"}, Executable: "/opt/xpto/bin/xpto"})
	if len(issues) != 0 {
		t.Fatalf("configured executable produced issues: %#v", issues)
	}
}

func TestHomeMountPathFailsClosedWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	if _, ok := homeMountPath("", ".config/gh"); ok {
		t.Fatal("homeMountPath without any home must report false, not a relative path")
	}
	if got, ok := homeMountPath("/", ".config/gh"); !ok || got != "/.config/gh" {
		t.Fatalf("homeMountPath(\"/\") = %q, %t; want /.config/gh", got, ok)
	}
	if got, ok := homeMountPath("/home/tester/", ".config/gh"); !ok || got != "/home/tester/.config/gh" {
		t.Fatalf("homeMountPath with trailing slash = %q, %t", got, ok)
	}
}

func TestCoveredByMountsExpandsTilde(t *testing.T) {
	home := t.TempDir()
	original := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = original })
	mounts := []config.Mount{{Path: "~/data", Mode: "rw"}}
	if !coveredByMounts(filepath.Join(home, "data", "cache"), mounts) {
		t.Fatal("a path under ~/data must be covered by the ~-configured mount")
	}
}

func TestValidatorWarnsOnCatalogBooleanFlagInjection(t *testing.T) {
	v := Validator{
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	agent := config.Agent{Command: "kimi", Params: []config.Param{
		{Name: "danger", Flag: "--allow-everything", TakesValue: false},
		{Name: "model", Flag: "--model", TakesValue: true},
	}}
	issues := v.Validate(LaunchConfig{
		Agent:       agent,
		ParamValues: map[string]string{"danger": "true", "model": "k2"},
	})
	if len(issues) != 1 || issues[0].Code != "catalog-flag-param" || !issues[0].Warning {
		t.Fatalf("issues = %#v; want one catalog-flag-param warning", issues)
	}
	if !strings.Contains(issues[0].Message, "--allow-everything") {
		t.Fatalf("warning must name the injected flag: %q", issues[0].Message)
	}
}

func TestValidatorWarnsOnUndeclaredParamsInSortedOrder(t *testing.T) {
	v := Validator{
		LookPath: func(command string) (string, error) { return "/bin/" + command, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
	agent := config.Agent{Command: "kimi", Params: []config.Param{{Name: "model", Flag: "--model", TakesValue: true}}}
	issues := v.Validate(LaunchConfig{
		Agent:       agent,
		ParamValues: map[string]string{"zeta": "1", "model": "k2", "alpha": "2"},
	})
	if len(issues) != 2 {
		t.Fatalf("issues = %#v; want two param-not-declared warnings", issues)
	}
	for _, issue := range issues {
		if issue.Code != "param-not-declared" {
			t.Fatalf("issue code = %q; want param-not-declared", issue.Code)
		}
	}
	if !strings.Contains(issues[0].Message, `"alpha"`) || !strings.Contains(issues[1].Message, `"zeta"`) {
		t.Fatalf("undeclared params not sorted: %#v", issues)
	}
}

func TestPTYExecutorRunsCommand(t *testing.T) {
	var output bytes.Buffer
	err := (PTYExecutor{}).RunWithEnv(context.Background(), []string{"sh", "-c", "printf hi"}, nil, strings.NewReader(""), &output, nil)
	if err != nil {
		t.Fatalf("PTYExecutor.RunWithEnv() error = %v", err)
	}
	if !strings.Contains(output.String(), "hi") {
		t.Fatalf("PTY output = %q; want hi", output.String())
	}
}

func TestPTYExecutorDrainsOutputWhenWriterIsNil(t *testing.T) {
	// A nil out must not deadlock: nothing drains the PTY, the child blocks
	// on a full pipe buffer, and cmd.Wait never returns. Write well past the
	// PTY buffer size to prove it.
	done := make(chan error, 1)
	go func() {
		done <- (PTYExecutor{}).RunWithEnv(context.Background(),
			[]string{"sh", "-c", "head -c 1048576 /dev/zero | tr '\\000' x"}, nil,
			strings.NewReader(""), nil, nil)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PTYExecutor.RunWithEnv() error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run blocked on a full PTY buffer with out == nil")
	}
}

func TestPTYExecutorRejectsEmptyCommand(t *testing.T) {
	if err := (PTYExecutor{}).RunWithEnv(context.Background(), nil, nil, nil, nil, nil); err == nil {
		t.Fatal("empty command should fail")
	}
}

func TestPTYExecutorReportsStartError(t *testing.T) {
	err := (PTYExecutor{}).RunWithEnv(context.Background(), []string{"definitely-not-an-ai-launcher-command"}, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("start error = %v", err)
	}
}

// TestPTYExecutorCapturesChildOutputInError is the regression test for the
// ai-memory 409 recovery: the PTY streams the child's output to the caller's
// out, but the launcher also needs that text (the underlying error) in the
// returned error so it can pattern-match a recovery hint and arm --new. The
// executor tees into a rolling tail and appends it on failure.
func TestPTYExecutorCapturesChildOutputInError(t *testing.T) {
	var out bytes.Buffer
	err := (PTYExecutor{}).RunWithEnv(context.Background(),
		[]string{"sh", "-c", `printf '%s\n' 'server returned 409 Conflict: workstream is already active: owned by host:1' >/dev/tty 2>/dev/null || printf '%s\n' 'server returned 409 Conflict: workstream is already active: owned by host:1'; exit 1`},
		nil, strings.NewReader(""), &out, io.Discard)
	if err == nil {
		t.Fatal("expected a non-nil error from the failing child")
	}
	if !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "workstream is already active") {
		t.Fatalf("error = %q; want it to carry the child's 409 output via the tail", err.Error())
	}
	// The live stream still reached the caller's out — the capture is additive.
	if !strings.Contains(out.String(), "workstream is already active") {
		t.Fatalf("live output lost: out = %q", out.String())
	}
}

func TestPTYExecutorReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (PTYExecutor{}).RunWithEnv(ctx, []string{"sh", "-c", "sleep 1"}, nil, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context error = %v; want context canceled", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failure") }

func TestPTYExecutorReportsOutputError(t *testing.T) {
	err := (PTYExecutor{}).RunWithEnv(context.Background(), []string{"sh", "-c", "printf hi"}, nil, nil, failingWriter{}, nil)
	if err == nil || !strings.Contains(err.Error(), "read command output") {
		t.Fatalf("output error = %v", err)
	}
}

func TestPrepareHostTTYNoopsForPipes(t *testing.T) {
	// Non-file readers and non-TTY files must not panic; restore is a no-op.
	restore := prepareHostTTY(strings.NewReader("x"), nil)
	restore()
	// A regular file is not a terminal → no raw mode.
	f, err := os.CreateTemp(t.TempDir(), "notty")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	restore = prepareHostTTY(f, f)
	restore()
}
