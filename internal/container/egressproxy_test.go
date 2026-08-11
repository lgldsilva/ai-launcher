package container

import (
	"strings"
	"testing"
)

func TestEgressProxyConfigPath(t *testing.T) {
	got := EgressProxyConfigPath("/home/user/.ai-launcher/data")
	want := "/home/user/.ai-launcher/data/egress-proxy/squid.conf"
	if got != want {
		t.Fatalf("EgressProxyConfigPath() = %q; want %q", got, want)
	}
}

func TestGenerateEgressProxyConfig(t *testing.T) {
	tests := []struct {
		name    string
		domains []string
		wantErr bool
		want    []string // lines/substrings that must appear
	}{
		{
			name:    "single domain",
			domains: []string{"api.anthropic.com"},
			want: []string{
				"acl allowed_dst dstdomain .api.anthropic.com",
				"http_access allow allowed_dst",
				"http_access deny all",
				"cache deny all",
			},
		},
		{
			name:    "multiple domains",
			domains: []string{"api.anthropic.com", "github.com"},
			want: []string{
				"acl allowed_dst dstdomain .api.anthropic.com",
				"acl allowed_dst dstdomain .github.com",
			},
		},
		{
			name:    "already wildcarded domain is not double-prefixed",
			domains: []string{".npmjs.org"},
			want: []string{
				"acl allowed_dst dstdomain .npmjs.org",
			},
		},
		{
			name:    "empty domain list is an error",
			domains: nil,
			wantErr: true,
		},
		{
			name:    "domain with space is rejected",
			domains: []string{"api anthropic.com"},
			wantErr: true,
		},
		{
			name:    "domain with semicolon is rejected",
			domains: []string{"api.anthropic.com;evil.com"},
			wantErr: true,
		},
		{
			name:    "domain with quote is rejected",
			domains: []string{`api.anthropic.com"`},
			wantErr: true,
		},
		// Regression cases for a finding from independent review: a
		// character-denylist validator let non-hostname entries through.
		// The one with real blast radius is a bare TLD — "com" as a squid
		// dstdomain ACL matches (and thus opens egress to) every ".com"
		// domain on the internet, silently far broader than what an
		// operator typing "allowed domain: com" would expect.
		{
			name:    "bare TLD is rejected",
			domains: []string{"com"},
			wantErr: true,
		},
		{
			name:    "single label is rejected",
			domains: []string{"localhost"},
			wantErr: true,
		},
		{
			name:    "wildcard character is rejected",
			domains: []string{"*"},
			wantErr: true,
		},
		{
			name:    "double dot is rejected",
			domains: []string{"a..com"},
			wantErr: true,
		},
		{
			name:    "domain with port suffix is rejected",
			domains: []string{"api.anthropic.com:80"},
			wantErr: true,
		},
		{
			name:    "domain with path suffix is rejected",
			domains: []string{"api.anthropic.com/evil"},
			wantErr: true,
		},
		{
			name:    "non-ASCII domain is rejected",
			domains: []string{"évil.com"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateEgressProxyConfig(tt.domains)
			if tt.wantErr {
				assertGenerateEgressProxyConfigRejected(t, tt.domains, got, err)
				return
			}
			if err != nil {
				t.Fatalf("GenerateEgressProxyConfig(%v) error = %v", tt.domains, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("GenerateEgressProxyConfig(%v) missing %q:\n%s", tt.domains, want, got)
				}
			}
		})
	}
}

func assertGenerateEgressProxyConfigRejected(t *testing.T, domains []string, got string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("GenerateEgressProxyConfig(%v) error = nil; want error", domains)
	}
	if got != "" {
		t.Fatalf("GenerateEgressProxyConfig(%v) = %q; want empty output on error", domains, got)
	}
	for _, domain := range domains {
		if strings.Contains(got, domain) {
			t.Fatalf("rejected domain %q leaked into output: %q", domain, got)
		}
	}
}

func TestGenerateEgressProxyConfigIsOrderDeterministic(t *testing.T) {
	domains := []string{"one.com", "two.com", "three.com"}
	first, err := GenerateEgressProxyConfig(domains)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateEgressProxyConfig(domains)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("GenerateEgressProxyConfig is not deterministic for the same input order:\n%s\n---\n%s", first, second)
	}
	firstIndex := strings.Index(first, "one.com")
	secondIndex := strings.Index(first, "two.com")
	thirdIndex := strings.Index(first, "three.com")
	if firstIndex >= secondIndex || secondIndex >= thirdIndex {
		t.Fatalf("domains not emitted in input order:\n%s", first)
	}
}

func TestEgressProxyComposeService(t *testing.T) {
	t.Run("valid inputs", func(t *testing.T) {
		svc, err := EgressProxyComposeService("/data/egress-proxy/squid.conf", "ai-launcher", "ai-launcher-egress")
		if err != nil {
			t.Fatalf("EgressProxyComposeService() error = %v", err)
		}
		if svc.Image != EgressProxyImage {
			t.Fatalf("Image = %q; want %q", svc.Image, EgressProxyImage)
		}
		wantNetworks := []string{"ai-launcher", "ai-launcher-egress"}
		if len(svc.Networks) != len(wantNetworks) {
			t.Fatalf("Networks = %v; want %v", svc.Networks, wantNetworks)
		}
		for i, want := range wantNetworks {
			if svc.Networks[i] != want {
				t.Fatalf("Networks[%d] = %q; want %q", i, svc.Networks[i], want)
			}
		}
		if len(svc.Volumes) != 1 {
			t.Fatalf("Volumes = %v; want exactly one entry", svc.Volumes)
		}
		if !strings.HasSuffix(svc.Volumes[0], ":ro") {
			t.Fatalf("Volumes[0] = %q; want a :ro (read-only) mount", svc.Volumes[0])
		}
		if !strings.HasPrefix(svc.Volumes[0], "/data/egress-proxy/squid.conf:") {
			t.Fatalf("Volumes[0] = %q; want it to mount the given config path", svc.Volumes[0])
		}
		if err := validateComposeHealthcheck(EgressProxyServiceID, svc.Healthcheck); err != nil {
			t.Fatalf("generated healthcheck fails ComposeFile validation: %v", err)
		}
	})

	t.Run("empty arguments are rejected", func(t *testing.T) {
		cases := []struct {
			name                                       string
			configPath, internalNetwork, egressNetwork string
		}{
			{"empty config path", "", "ai-launcher", "ai-launcher-egress"},
			{"empty internal network", "/data/squid.conf", "", "ai-launcher-egress"},
			{"empty egress network", "/data/squid.conf", "ai-launcher", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := EgressProxyComposeService(tc.configPath, tc.internalNetwork, tc.egressNetwork); err == nil {
					t.Fatalf("EgressProxyComposeService(%q, %q, %q) error = nil; want error", tc.configPath, tc.internalNetwork, tc.egressNetwork)
				}
			})
		}
	})
}

func TestParseEgressDomains(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    []string
		wantErr bool
	}{
		{name: "two domains", value: "a.com,b.com", want: []string{"a.com", "b.com"}},
		{
			name:  "whitespace and empty pieces are cleaned up",
			value: "a.com, b.com ,,c.com",
			want:  []string{"a.com", "b.com", "c.com"},
		},
		{name: "empty value clears the field", value: "", want: nil},
		{name: "whitespace-only value clears the field", value: "   ", want: nil},
		{name: "domain with space is rejected", value: "bad domain", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEgressDomains(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseEgressDomains(%q) error = nil; want error", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEgressDomains(%q) error = %v", tt.value, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseEgressDomains(%q) = %v; want %v", tt.value, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ParseEgressDomains(%q) = %v; want %v", tt.value, got, tt.want)
				}
			}
		})
	}
}
