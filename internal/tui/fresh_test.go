package tui

import (
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

// --fresh is an ai-memory run wrapper flag; offering it with the memory layer
// off would toggle a setting that silently does nothing.
func TestFreshToggleOnlyAppearsWithMemory(t *testing.T) {
	stubWindows(t, false)
	model := NewModel(config.DefaultGlobal(), launcher.LaunchConfig{UseMemory: false, Permissions: map[string]bool{}})
	for _, row := range model.optionRows() {
		if row.name == "--fresh" {
			t.Fatal("--fresh toggle offered with ai-memory disabled")
		}
	}
	model = NewModel(config.DefaultGlobal(), launcher.LaunchConfig{UseMemory: true, Permissions: map[string]bool{}})
	found := false
	for _, row := range model.optionRows() {
		if row.name == "--fresh" {
			found = true
		}
	}
	if !found {
		t.Fatal("--fresh toggle missing with ai-memory enabled")
	}
}
