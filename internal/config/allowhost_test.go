package config

import (
	"reflect"
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
