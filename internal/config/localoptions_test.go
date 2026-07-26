package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLocal(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".ai-launch.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The safety defaults for jail and memory must come from the parsed document.
// Deciding presence by substring search matches "jail:" anywhere in the file —
// inside a permissions block, a comment, or a mount path — which skips the safe
// default and leaves the Go zero value false. Writing the config that looks like
// it enables the sandbox is what turns it off.
func TestLoadLocalIgnoresOptionKeysOutsideTheOptionsBlock(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "nested in a permissions block",
			body: "version: \"2.0\"\nagent: claude\npermissions:\n  jail: true\n  memory: true\noptions:\n  yolo: true\n",
		},
		{
			name: "inside a comment",
			body: "version: \"2.0\"\nagent: claude\n# jail: false\n# memory: false\noptions:\n  yolo: true\n",
		},
		{
			name: "inside a mount path",
			body: "version: \"2.0\"\nagent: claude\nmounts:\n  - path: /srv/jail:/opt/memory:ro\noptions:\n  yolo: true\n",
		},
		{
			name: "nested under a sibling mapping",
			body: "version: \"2.0\"\nagent: claude\nparam_values:\n  jail: yes\n  memory: yes\noptions:\n  yolo: true\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := LoadLocal(writeLocal(t, testCase.body))
			if err != nil {
				t.Fatalf("LoadLocal() error = %v", err)
			}
			if !got.Options.Jail {
				t.Error("Options.Jail = false; an options block that omits jail must keep the sandbox on")
			}
			if !got.Options.Memory {
				t.Error("Options.Memory = false; an options block that omits memory must keep it on")
			}
		})
	}
}

// The safe default must not override an explicit opt-out inside the options
// block itself.
func TestLoadLocalHonorsExplicitOptionValues(t *testing.T) {
	got, err := LoadLocal(writeLocal(t,
		"version: \"2.0\"\nagent: claude\npermissions:\n  jail: true\noptions:\n  jail: false\n  memory: false\n"))
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if got.Options.Jail {
		t.Error("Options.Jail = true; an explicit false inside options must be honored")
	}
	if got.Options.Memory {
		t.Error("Options.Memory = true; an explicit false inside options must be honored")
	}
}
