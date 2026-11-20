package config

import (
	"reflect"
	"testing"
)

func toolByCommand(tools []Tool, command string) Tool {
	for _, tool := range tools {
		if tool.Command == command {
			return tool
		}
	}
	return Tool{}
}

var memoryHooksDir = []ArchiveDir{{Source: "hooks", Target: ".local/share/ai-memory/hooks"}}

func TestDefaultMemoryReleaseInstallsTheHooksBundle(t *testing.T) {
	memory := toolByCommand(DefaultGlobal().Tools, "ai-memory")
	if memory.Release == nil || !reflect.DeepEqual(memory.Release.Dirs, memoryHooksDir) {
		t.Fatalf("ai-memory release = %#v; want the hooks dir installed beside the binary", memory.Release)
	}
}

// A tools list replaces the defaults wholesale, and configs written before
// release dirs existed carry an ai-memory recipe without them: ai-memory kept
// arriving without its hooks on exactly the machines that had a config.
func TestUserToolsInheritReleaseDirsFromTheMatchingDefault(t *testing.T) {
	user := Global{Tools: []Tool{
		{Name: "ai-memory", Command: "ai-memory", Release: &GitHubRelease{Repository: "akitaonrails/ai-memory", Binary: "ai-memory"}},
		{Name: "fork", Command: "ai-memory-fork", Release: &GitHubRelease{Repository: "akitaonrails/ai-memory"}},
		{Name: "mirror", Command: "ai-jail", Release: &GitHubRelease{Repository: "someone/ai-jail"}},
	}}
	merged := mergeGlobalDefaults(DefaultGlobal(), user)

	if got := toolByCommand(merged.Tools, "ai-memory").Release.Dirs; !reflect.DeepEqual(got, memoryHooksDir) {
		t.Errorf("ai-memory Dirs = %#v; want inherited %#v", got, memoryHooksDir)
	}
	if got := toolByCommand(merged.Tools, "ai-memory-fork").Release.Dirs; got != nil {
		t.Errorf("other command inherited Dirs %#v", got)
	}
	if got := toolByCommand(merged.Tools, "ai-jail").Release.Dirs; got != nil {
		t.Errorf("other repository inherited Dirs %#v", got)
	}
	if user.Tools[0].Release.Dirs != nil {
		t.Error("merge mutated the user's release recipe in place")
	}
}

func TestUserReleaseDirsWinOverTheDefault(t *testing.T) {
	own := []ArchiveDir{{Source: "hooks", Target: "mine/hooks"}}
	user := Global{Tools: []Tool{
		{Command: "ai-memory", Release: &GitHubRelease{Repository: "akitaonrails/ai-memory", Dirs: own}},
		{Command: "no-release"},
	}}
	merged := mergeGlobalDefaults(DefaultGlobal(), user)
	if got := toolByCommand(merged.Tools, "ai-memory").Release.Dirs; !reflect.DeepEqual(got, own) {
		t.Errorf("Dirs = %#v; the user's own value must win", got)
	}
	if toolByCommand(merged.Tools, "no-release").Release != nil {
		t.Error("a tool without a release recipe gained one")
	}
}
