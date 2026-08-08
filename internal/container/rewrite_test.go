package container

import (
	"testing"
)

func TestRewriteLocalhost(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		changed bool
	}{
		{"http://localhost:8080/mcp", "http://host.docker.internal:8080/mcp", true},
		{"http://localhost/mcp", "http://host.docker.internal/mcp", true},
		{"http://127.0.0.1:3000", "http://host.docker.internal:3000", true},
		{"ws://localhost:9000", "ws://host.docker.internal:9000", true},
		{"https://host.docker.internal:8080/x", "https://host.docker.internal:8080/x", false},
		{"https://example.com/mcp", "https://example.com/mcp", false},
		{"no url here", "no url here", false},
		{"http://LOCALHOST:1", "http://host.docker.internal:1", true},
	}
	for _, tt := range tests {
		got, changed := RewriteLocalhost(tt.in, "host.docker.internal")
		if got != tt.want || changed != tt.changed {
			t.Errorf("RewriteLocalhost(%q) = (%q, %v); want (%q, %v)", tt.in, got, changed, tt.want, tt.changed)
		}
	}
}

func TestContainsLoopbackURL(t *testing.T) {
	yes := []string{
		`{"mcpServers": {"x": {"url": "http://localhost:8080"}}}`,
		"server = http://127.0.0.1:3000",
	}
	for _, text := range yes {
		if !ContainsLoopbackURL(text) {
			t.Errorf("ContainsLoopbackURL(%q) = false; want true", text)
		}
	}
	no := []string{
		`{"url": "https://example.com"}`,
		"no loopback references here",
		"",
	}
	for _, text := range no {
		if ContainsLoopbackURL(text) {
			t.Errorf("ContainsLoopbackURL(%q) = true; want false", text)
		}
	}
}

func TestRewriteDisabled(t *testing.T) {
	if RewriteDisabled(nil) {
		t.Fatal("RewriteDisabled(nil) = true; want false")
	}
	if RewriteDisabled([]string{"PATH=/bin", "FOO=1"}) {
		t.Fatal("RewriteDisabled with unrelated env = true; want false")
	}
	if !RewriteDisabled([]string{"AI_LAUNCHER_NO_REWRITE=1"}) {
		t.Fatal("RewriteDisabled with the flag = false; want true")
	}
	if !RewriteDisabled([]string{"PATH=/bin", "AI_LAUNCHER_NO_REWRITE="}) {
		t.Fatal("RewriteDisabled with empty value should still disable")
	}
}

func TestNoRewriteEnvConstant(t *testing.T) {
	if NoRewriteEnv != "AI_LAUNCHER_NO_REWRITE" {
		t.Fatalf("NoRewriteEnv = %q; the constant must match the flag spelling", NoRewriteEnv)
	}
}

func TestRewriteLocalhostRegexStability(t *testing.T) {
	// The replacement must not mangle URLs that embed paths or query strings.
	in := "http://localhost:8080/api?x=1"
	want := "http://host.docker.internal:8080/api?x=1"
	got, changed := RewriteLocalhost(in, "host.docker.internal")
	if !changed || got != want {
		t.Fatalf("RewriteLocalhost(%q) = (%q, %v); want (%q, true)", in, got, changed, want)
	}
}

func TestRewriteLocalhostUsesRuntimeGateway(t *testing.T) {
	got, changed := RewriteLocalhost("http://localhost:8080/mcp", "host.containers.internal")
	if !changed || got != "http://host.containers.internal:8080/mcp" {
		t.Fatalf("RewriteLocalhost() = (%q, %v); want Podman gateway", got, changed)
	}
}
