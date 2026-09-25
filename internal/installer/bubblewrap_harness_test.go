package installer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// harnessDir holds one compose scenario per package manager row. Each
// check.sh runs the row's root command in a real image, as text, so this
// test keeps that text equal to the table: a row changed here fails until
// the harness runs the new command too.
var harnessDir = filepath.Join("..", "..", "test", "bubblewrap-distros")

func TestBubblewrapHarnessRunsTheTableArgv(t *testing.T) {
	for _, manager := range bubblewrapManagers() {
		path := filepath.Join(harnessDir, manager.Command, "check.sh")
		data, err := os.ReadFile(path) // #nosec G304 -- fixed repo path built from the table
		if err != nil {
			t.Fatalf("%s has no harness scenario: %v", manager.Command, err)
		}
		want := manager.display(false) + "\n"
		if !strings.Contains(string(data), want) {
			t.Errorf("%s: check.sh does not run %q", path, strings.TrimSpace(want))
		}
	}
}

func TestBubblewrapHarnessHasNoScenarioOutsideTheTable(t *testing.T) {
	entries, err := os.ReadDir(harnessDir)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]bool{}
	for _, manager := range bubblewrapManagers() {
		rows[manager.Command] = true
	}
	var stray []string
	for _, entry := range entries {
		if entry.IsDir() && !rows[entry.Name()] {
			stray = append(stray, entry.Name())
		}
	}
	sort.Strings(stray)
	if len(stray) > 0 {
		t.Fatalf("harness scenarios without a table row: %v", stray)
	}
}
