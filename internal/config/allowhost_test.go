package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAllowHostsDropsBlanksAndRejectsNonHostnames(t *testing.T) {
	got, err := NormalizeAllowHosts([]string{" api.anthropic.com ", "", "github.com"})
	if err != nil {
		t.Fatalf("NormalizeAllowHosts() error = %v", err)
	}
	want := []string{"api.anthropic.com", "github.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeAllowHosts() = %#v; want %#v", got, want)
	}
	if _, err := NormalizeAllowHosts([]string{"https://api.anthropic.com"}); err == nil {
		t.Fatal("NormalizeAllowHosts(url) = nil; a URL is not a host")
	}
	if got, err := NormalizeAllowHosts([]string{"  ", ""}); err != nil || got != nil {
		t.Fatalf("blank list = %#v, %v; want nil, nil", got, err)
	}
}

func TestParseAllowHostListSplitsCommasAndSpaces(t *testing.T) {
	got, err := ParseAllowHostList("api.anthropic.com, github.com localhost")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api.anthropic.com", "github.com", "localhost"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseAllowHostList() = %#v; want %#v", got, want)
	}
	if got, err := ParseAllowHostList("   "); err != nil || got != nil {
		t.Fatalf("blank entry = %#v, %v; want a cleared list", got, err)
	}
}

// Hostname limits are inclusive: 253 characters overall and 63 per label, and
// the accepted alphabet includes both ends of each range (a/z, A/Z, 0/9) plus
// an interior hyphen. One step past a limit, an empty label, or a hyphen on
// either end is a different token and must be refused.
func TestNormalizeAllowHostsHonorsHostnameBoundaries(t *testing.T) {
	atLimit := strings.Join([]string{
		strings.Repeat("a", 63),
		strings.Repeat("a", 63),
		strings.Repeat("a", 63),
		strings.Repeat("b", 61),
	}, ".")
	if len(atLimit) != 253 {
		t.Fatalf("fixture length = %d; want the 253-character DNS maximum", len(atLimit))
	}
	pastLimit := strings.Join([]string{
		strings.Repeat("a", 63),
		strings.Repeat("a", 63),
		strings.Repeat("a", 63),
		strings.Repeat("b", 62),
	}, ".")

	accepted := []string{
		atLimit,
		strings.Repeat("a", 63),
		"zA-Z0.9",
	}
	for _, host := range accepted {
		got, err := NormalizeAllowHosts([]string{host})
		if err != nil || !reflect.DeepEqual(got, []string{host}) {
			t.Errorf("NormalizeAllowHosts(%q) = %#v, %v; want the host accepted", host, got, err)
		}
	}

	rejected := []string{
		pastLimit,
		strings.Repeat("a", 64),
		"bad..host",
		".example",
		"example.",
		"-example",
		"example-",
	}
	for _, host := range rejected {
		if _, err := NormalizeAllowHosts([]string{host}); err == nil {
			t.Errorf("NormalizeAllowHosts(%q) = nil; want a hostname rejection", host)
		}
	}
}
