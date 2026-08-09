package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRewrite replaces localhost with host.docker.internal, mirroring the
// production rule without depending on its regex.
func fakeRewrite(value string) (string, bool) {
	if !strings.Contains(value, "localhost") {
		return value, false
	}
	return strings.ReplaceAll(value, "localhost", "host.docker.internal"), true
}

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { // #nosec G301 -- test fixture dir
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlanOverlayRewritesLoopback(t *testing.T) {
	hostDir := t.TempDir()
	tmpDir := t.TempDir()
	host := writeTempFile(t, hostDir, ".claude.json", `{"mcpServers":{"x":{"url":"http://localhost:8080"}}}`)

	overlay := PlanOverlay(host, tmpDir, fakeRewrite, false)
	if overlay == nil {
		t.Fatal("PlanOverlay() = nil; want an overlay for a loopback config")
	}
	if overlay.HostPath != host {
		t.Fatalf("HostPath = %q; want %q", overlay.HostPath, host)
	}
	if _, err := os.Stat(overlay.RewrittenPath); err != nil {
		t.Fatalf("rewritten copy missing: %v", err)
	}
	data, err := os.ReadFile(overlay.RewrittenPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "localhost") {
		t.Fatalf("rewritten copy still references localhost: %s", data)
	}
	if !strings.Contains(string(data), "host.docker.internal") {
		t.Fatalf("rewritten copy missing host gateway: %s", data)
	}
	// The host file must be untouched (R7 item 31).
	hostData, _ := os.ReadFile(host) // #nosec G304 -- test fixture path
	if !strings.Contains(string(hostData), "localhost") {
		t.Fatal("host file was modified by the overlay")
	}

	spec := overlay.OverlayMountSpec()
	want := overlay.RewrittenPath + ":" + host + ":ro"
	if spec != want {
		t.Fatalf("OverlayMountSpec() = %q; want %q", spec, want)
	}

	overlay.Cleanup()
	if _, err := os.Stat(overlay.RewrittenPath); !os.IsNotExist(err) {
		t.Fatal("Cleanup() did not remove the rewritten copy")
	}
}

func TestPlanOverlaySkipsCleanConfig(t *testing.T) {
	hostDir := t.TempDir()
	tmpDir := t.TempDir()
	host := writeTempFile(t, hostDir, ".claude.json", `{"mcpServers":{"x":{"url":"https://example.com"}}}`)

	if overlay := PlanOverlay(host, tmpDir, fakeRewrite, false); overlay != nil {
		t.Fatalf("PlanOverlay() on a clean config = %#v; want nil", overlay)
	}
}

func TestPlanOverlaySkipsMissingFile(t *testing.T) {
	if overlay := PlanOverlay("/nonexistent/file.json", t.TempDir(), fakeRewrite, false); overlay != nil {
		t.Fatalf("PlanOverlay() on a missing file = %#v; want nil", overlay)
	}
}

func TestPlanOverlayHonorsNoRewrite(t *testing.T) {
	hostDir := t.TempDir()
	host := writeTempFile(t, hostDir, ".claude.json", `{"url":"http://localhost:8080"}`)
	if overlay := PlanOverlay(host, t.TempDir(), fakeRewrite, true); overlay != nil {
		t.Fatalf("PlanOverlay() with disableRewrite = %#v; want nil", overlay)
	}
}

func TestPlanOverlaySkipsUnchangedRewrite(t *testing.T) {
	// A config that contains "localhost" as plain text (not a URL) is still
	// scanned, but a rewrite that reports "unchanged" must produce no overlay.
	hostDir := t.TempDir()
	host := writeTempFile(t, hostDir, ".claude.json", `{"note":"localhost"}`)
	noopRewrite := func(value string) (string, bool) { return value, false }
	if overlay := PlanOverlay(host, t.TempDir(), noopRewrite, false); overlay != nil {
		t.Fatalf("PlanOverlay() with no-op rewrite = %#v; want nil", overlay)
	}
}

func TestOverlayCandidate(t *testing.T) {
	yes := []string{".claude.json", ".mcp.json", "/proj/.mcp.json", "/proj/.mcp.local.json", "opencode.json", "settings.json"}
	for _, path := range yes {
		if !OverlayCandidate(path) {
			t.Errorf("OverlayCandidate(%q) = false; want true", path)
		}
	}
	no := []string{"main.go", "go.mod", ".gitignore", "README.md"}
	for _, path := range no {
		if OverlayCandidate(path) {
			t.Errorf("OverlayCandidate(%q) = true; want false", path)
		}
	}
}

func TestPlanOverlayCreatesPerFileCopy(t *testing.T) {
	// Two configs in the same host dir must get distinct rewritten copies
	// (same basename would collide otherwise).
	hostDir := t.TempDir()
	tmpDir := t.TempDir()
	a := writeTempFile(t, hostDir, ".claude.json", `{"url":"http://localhost:1"}`)
	b := writeTempFile(t, hostDir, ".mcp.json", `{"url":"http://localhost:2"}`)
	oa := PlanOverlay(a, tmpDir, fakeRewrite, false)
	ob := PlanOverlay(b, tmpDir, fakeRewrite, false)
	if oa == nil || ob == nil {
		t.Fatalf("overlays = %#v, %#v; want both", oa, ob)
	}
	if oa.RewrittenPath == ob.RewrittenPath {
		t.Fatal("two configs got the same rewritten path")
	}
}
