package config

import (
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestDependencySettingsRoundTrip(t *testing.T) {
	input := []byte(`policy: safe
overrides:
  node.nvm:
    enabled: true
    sources:
      darwin: ~/toolchains/node
      linux: /srv/node
    target: /home/ai-launcher/.nvm
    mode: ro
    allow_incompatible: true
`)
	var got DependencySettings
	if err := yaml.Unmarshal(input, &got); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if got.Policy != "safe" || got.Overrides["node.nvm"].Sources["darwin"] != "~/toolchains/node" {
		t.Fatalf("decoded dependency settings = %#v", got)
	}

	encoded, err := yaml.Marshal(got)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	var decoded DependencySettings
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round-trip unmarshal error = %v", err)
	}
	if !reflect.DeepEqual(decoded, got) {
		t.Fatalf("round-trip = %#v; want %#v", decoded, got)
	}
}

func TestMergeDependencySettingsPreservesGlobalAndOverlaysLocal(t *testing.T) {
	globalEnabled := true
	localEnabled := false
	global := DependencySettings{
		Policy: "safe",
		Overrides: map[string]DependencyOverride{
			"node.nvm": {Enabled: &globalEnabled, Source: "/global/node", Mode: MountReadWrite},
		},
	}
	local := DependencySettings{
		Overrides: map[string]DependencyOverride{
			"node.nvm":         {Enabled: &localEnabled, Target: "/home/ai-launcher/.custom-nvm"},
			"python.pip-cache": {Source: "/srv/pip", Mode: MountReadOnly},
		},
	}

	merged := MergeDependencySettings(global, local)
	nvm := merged.Overrides["node.nvm"]
	if nvm.Enabled == nil || *nvm.Enabled || nvm.Source != "/global/node" || nvm.Target != "/home/ai-launcher/.custom-nvm" || nvm.Mode != MountReadWrite {
		t.Fatalf("merged nvm override = %#v", nvm)
	}
	if merged.Overrides["python.pip-cache"].Source != "/srv/pip" || merged.Policy != "safe" {
		t.Fatalf("merged settings lost local/global values = %#v", merged)
	}

	localNVM := local.Overrides["node.nvm"]
	localNVM.Sources = map[string]string{"linux": "/local/node"}
	local.Overrides["node.nvm"] = localNVM
	merged = MergeDependencySettings(global, local)
	merged.Overrides["node.nvm"].Sources["linux"] = "/mutated"
	if global.Overrides["node.nvm"].Enabled == nil || *global.Overrides["node.nvm"].Enabled != true {
		t.Fatal("merge mutated global Enabled pointer")
	}
	if len(global.Overrides["node.nvm"].Sources) != 0 {
		t.Fatal("merge unexpectedly attached local source map to global")
	}
}

func TestDependencySettingsCloneAndZero(t *testing.T) {
	if !(DependencySettings{}).IsZero() {
		t.Fatal("empty dependency settings should be zero")
	}
	enabled := true
	original := DependencySettings{Policy: "none", Overrides: map[string]DependencyOverride{
		"go.build-cache": {Enabled: &enabled, Sources: map[string]string{"darwin": "~/go-cache"}},
	}}
	clone := original.Clone()
	*clone.Overrides["go.build-cache"].Enabled = false
	clone.Overrides["go.build-cache"].Sources["darwin"] = "/changed"
	if *original.Overrides["go.build-cache"].Enabled != true || original.Overrides["go.build-cache"].Sources["darwin"] != "~/go-cache" {
		t.Fatal("Clone shares mutable override state")
	}
	if clone.IsZero() {
		t.Fatal("configured dependency settings should not be zero")
	}
}
