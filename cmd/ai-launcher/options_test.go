package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestParseServicePortOverrides(t *testing.T) {
	got, err := parseServicePortOverrides([]string{
		"wiremock=18080:8080",
		"neo4j=17474:7474",
		"neo4j=17687:7687",
	})
	if err != nil {
		t.Fatalf("parseServicePortOverrides() error = %v", err)
	}
	want := map[string][]config.PortMapping{
		"wiremock": {{Host: 18080, Internal: 8080}},
		"neo4j":    {{Host: 17474, Internal: 7474}, {Host: 17687, Internal: 7687}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %#v; want %#v", got, want)
	}
}

func TestParseServicePortOverridesRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{"wiremock", "unknown=18080:8080", "wiremock="} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseServicePortOverrides([]string{input}); err == nil {
				t.Fatal("parseServicePortOverrides() error = nil")
			} else if !strings.Contains(err.Error(), "service") && !strings.Contains(err.Error(), "compose service") {
				t.Fatalf("error = %v; want service-specific diagnostic", err)
			}
		})
	}
}
