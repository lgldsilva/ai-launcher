package catalog

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"pgregory.net/rapid"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestNormalizePermissionsAddsDependencies(t *testing.T) {
	catalog := New(config.DefaultGlobal())
	got := catalog.NormalizePermissions(map[string]bool{"gpu": true})
	for _, id := range []string{"jail", "docker", "gpu"} {
		if !got[id] {
			t.Fatalf("permission %q should be enabled: %#v", id, got)
		}
	}
}

func TestNormalizePermissionsDoesNotEnableDependents(t *testing.T) {
	catalog := New(config.DefaultGlobal())
	got := catalog.NormalizePermissions(map[string]bool{"docker": false})
	if got["docker"] || got["gpu"] {
		t.Fatalf("docker and gpu should be disabled: %#v", got)
	}
	if !got["jail"] {
		t.Fatal("locked jail should remain enabled")
	}
}

func TestAgentsReportsInstalledStatus(t *testing.T) {
	global := config.Global{Agents: []config.Agent{{Name: "Available", Command: "available"}, {Name: "Missing", Command: "missing"}}}
	catalog := Catalog{Global: global, LookPath: func(command string) (string, error) {
		if command == "available" {
			return "/bin/available", nil
		}
		return "", errors.New("not found")
	}}
	got := catalog.Agents()
	if !got[0].Installed || got[0].Path != "/bin/available" || got[1].Installed {
		t.Fatalf("unexpected statuses: %#v", got)
	}
}

func TestAgentsUsesConfiguredExecutablePath(t *testing.T) {
	global := config.Global{Agents: []config.Agent{{Name: "Xpto", Command: "xpto", Path: "/opt/xpto/bin/xpto"}}}
	catalog := Catalog{Global: global, LookPath: func(command string) (string, error) {
		if command == "/opt/xpto/bin/xpto" {
			return command, nil
		}
		return "", errors.New("not found")
	}}
	got := catalog.Agents()
	if len(got) != 1 || !got[0].Installed || got[0].Path != "/opt/xpto/bin/xpto" {
		t.Fatalf("configured path status = %#v", got)
	}
}

func TestAgentsResolvesInstalledAliasAndResolveAcceptsIt(t *testing.T) {
	global := config.Global{Agents: []config.Agent{{Name: "Kilo Code", Command: "kilo", Aliases: []string{"kilocode", "kilo-code"}}}}
	catalog := Catalog{Global: global, LookPath: func(command string) (string, error) {
		if command == "kilocode" {
			return "/home/test/.local/bin/kilocode", nil
		}
		return "", errors.New("not found")
	}}
	got := catalog.Agents()
	if len(got) != 1 || !got[0].Installed || got[0].ResolvedCommand != "kilocode" || got[0].Path != "/home/test/.local/bin/kilocode" {
		t.Fatalf("alias status = %#v", got)
	}
	resolved, err := catalog.Resolve("kilocode")
	if err != nil || resolved.ResolvedCommand != "kilocode" {
		t.Fatalf("Resolve(alias) = %#v, %v", resolved, err)
	}
}

func TestResolveByNameAndRejectsUnknown(t *testing.T) {
	catalog := New(config.DefaultGlobal())
	status, err := catalog.Resolve("Claude Code")
	if err != nil || status.Agent.Command != "claude" {
		t.Fatalf("resolve by display name: %#v, %v", status, err)
	}
	if _, err := catalog.Resolve("unknown"); err == nil {
		t.Fatal("unknown agent should return an error")
	}
}

// TestPropertyNormalizePermissionsIsIdempotent generates permission catalogs
// with arbitrary dependency graphs (including cycles and references to
// unknown IDs) and asserts that normalization is a fixpoint: applying it to
// an already-normalized selection changes nothing.
func TestPropertyNormalizePermissionsIsIdempotent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		count := rapid.IntRange(1, 8).Draw(rt, "permissionCount")
		permissions := make([]config.Permission, count)
		for i := range permissions {
			requires := rapid.SliceOf(rapid.IntRange(0, count)).Draw(rt, "requires"+strconv.Itoa(i))
			dependencies := make([]string, 0, len(requires))
			for _, index := range requires {
				// Index == count references an unknown permission on purpose.
				dependencies = append(dependencies, "p"+strconv.Itoa(index))
			}
			permissions[i] = config.Permission{
				ID:       "p" + strconv.Itoa(i),
				Name:     "Permission " + strconv.Itoa(i),
				Default:  rapid.Bool().Draw(rt, "default"+strconv.Itoa(i)),
				Locked:   rapid.Bool().Draw(rt, "locked"+strconv.Itoa(i)),
				Requires: dependencies,
			}
		}
		selected := make(map[string]bool)
		selections := rapid.SliceOf(rapid.IntRange(0, count)).Draw(rt, "selections")
		for _, index := range selections {
			selected["p"+strconv.Itoa(index)] = rapid.Bool().Draw(rt, "selected"+strconv.Itoa(index))
		}

		catalog := New(config.Global{Permissions: permissions})
		once := catalog.NormalizePermissions(selected)
		twice := catalog.NormalizePermissions(once)
		if !reflect.DeepEqual(once, twice) {
			rt.Fatalf("NormalizePermissions is not idempotent:\nselected=%#v\nonce=%#v\ntwice=%#v", selected, once, twice)
		}
	})
}
