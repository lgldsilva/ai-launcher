package main

import (
	"flag"
	"reflect"
	"strconv"
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

// container_host_gateway is the one security field whose default (on) is the
// less-secure choice, so the two flags are the only CLI path to disable it.
// Pin each arm of the tri-state the flag produces: unset stays nil (resolved
// on at launch), --container-host-gateway pins true, --no-container-host-gateway
// pins false. Setting either also opts into the docker backend, matching every
// other container_* flag.
func TestContainerHostGatewayFlagsApplyTriState(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantSet  bool
		wantTrue bool
	}{
		{name: "no flag leaves the field unset (resolved on at launch)", args: nil, wantSet: false},
		{name: "--container-host-gateway enables explicitly", args: []string{"--container-host-gateway"}, wantSet: true, wantTrue: true},
		{name: "--no-container-host-gateway disables", args: []string{"--no-container-host-gateway"}, wantSet: true, wantTrue: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := cliOptions{}
			flags := flag.NewFlagSet("test", flag.ContinueOnError)
			opts.register(flags)
			if err := flags.Parse(tc.args); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			local := &config.Local{Options: config.Options{}}
			opts.applyOptionFlags(flags, local)

			field := local.Options.ContainerHostGateway
			switch {
			case !tc.wantSet && field != nil:
				t.Errorf("ContainerHostGateway = %v; want nil (unset)", *field)
			case tc.wantSet && field == nil:
				t.Errorf("ContainerHostGateway = nil; want %v", tc.wantTrue)
			case tc.wantSet && *field != tc.wantTrue:
				t.Errorf("ContainerHostGateway = %v; want %v", *field, tc.wantTrue)
			}

			// Every container_* flag opts into the docker backend, and these are
			// no exception: host-gateway is a docker-backend property.
			if tc.args != nil && !local.Options.Docker {
				t.Errorf("Options.Docker = false; setting a container flag must opt into the docker backend")
			}
		})
	}
}

// EffectiveContainerHostGateway resolves nil to true (the historical default),
// so a disabled config must survive the flag round-trip as a real false, not as
// a re-defaulted true. Losing the raw tri-state would silently reintroduce that.
func TestNoContainerHostGatewayFlagResolvesFalse(t *testing.T) {
	opts := cliOptions{}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	opts.register(flags)
	if err := flags.Parse([]string{"--no-container-host-gateway"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	local := &config.Local{Options: config.Options{}}
	opts.applyOptionFlags(flags, local)

	if got := config.EffectiveContainerHostGateway(local.Options.ContainerHostGateway); got {
		t.Errorf("EffectiveContainerHostGateway = true; --no-container-host-gateway must resolve false, got field=%s",
			boolFieldString(local.Options.ContainerHostGateway))
	}
}

func boolFieldString(value *bool) string {
	if value == nil {
		return "nil"
	}
	return strconv.FormatBool(*value)
}
