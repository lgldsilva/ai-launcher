package launcher

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

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

func TestFilepathOrEmptySupportsMissingHome(t *testing.T) {
	if got := filepathOrEmpty("", ".config/gh"); got != ".config/gh" {
		t.Fatalf("filepathOrEmpty() = %q", got)
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
	err := (PTYExecutor{}).Run(context.Background(), []string{"sh", "-c", "printf hi"}, strings.NewReader(""), &output, nil)
	if err != nil {
		t.Fatalf("PTYExecutor.Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "hi") {
		t.Fatalf("PTY output = %q; want hi", output.String())
	}
}

func TestPTYExecutorRejectsEmptyCommand(t *testing.T) {
	if err := (PTYExecutor{}).Run(context.Background(), nil, nil, nil, nil); err == nil {
		t.Fatal("empty command should fail")
	}
}

func TestPTYExecutorReportsStartError(t *testing.T) {
	err := (PTYExecutor{}).Run(context.Background(), []string{"definitely-not-an-ai-launcher-command"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("start error = %v", err)
	}
}

func TestPTYExecutorReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (PTYExecutor{}).Run(ctx, []string{"sh", "-c", "sleep 1"}, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context error = %v; want context canceled", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failure") }

func TestPTYExecutorReportsOutputError(t *testing.T) {
	err := (PTYExecutor{}).Run(context.Background(), []string{"sh", "-c", "printf hi"}, nil, failingWriter{}, nil)
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
