package container

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateRunResources validates the opt-in resource settings that are
// translated directly to container-runtime flags.
func ValidateRunResources(cfg RunConfig) error {
	if value := strings.TrimSpace(cfg.MemoryLimit); value != "" {
		if err := validateMemoryLimit(value); err != nil {
			return err
		}
	}
	if value := strings.TrimSpace(cfg.CPULimit); value != "" {
		if err := validateCPULimit(value); err != nil {
			return err
		}
	}
	if cfg.PIDsLimit < 0 {
		return fmt.Errorf("invalid pids limit %d: expected zero or a positive integer", cfg.PIDsLimit)
	}
	for index, mapping := range cfg.ExposedPorts {
		if err := ValidatePortMapping(mapping); err != nil {
			return fmt.Errorf("invalid published port %d: %w", index+1, err)
		}
	}
	if value := strings.TrimSpace(cfg.NetworkName); value != "" && strings.IndexFunc(value, func(r rune) bool {
		return r == '/' || r == '\\' || r == ':' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) >= 0 {
		return fmt.Errorf("invalid container network %q: expected a network name without separators or whitespace", cfg.NetworkName)
	}
	if strings.EqualFold(strings.TrimSpace(cfg.NetworkName), "host") && len(cfg.ExposedPorts) > 0 {
		return fmt.Errorf("container network %q cannot be combined with published ports", cfg.NetworkName)
	}
	return nil
}

func validateMemoryLimit(value string) error {
	if len(value) < 2 {
		return invalidMemoryLimit(value)
	}
	numberText, suffix := value[:len(value)-1], strings.ToLower(value[len(value)-1:])
	if suffix != "b" && suffix != "k" && suffix != "m" && suffix != "g" {
		return invalidMemoryLimit(value)
	}
	if numberText == "" {
		return invalidMemoryLimit(value)
	}
	for _, character := range numberText {
		if character < '0' || character > '9' {
			return invalidMemoryLimit(value)
		}
	}
	number, err := strconv.ParseUint(numberText, 10, 64)
	if err != nil || number == 0 {
		return invalidMemoryLimit(value)
	}
	multiplier := uint64(1)
	switch suffix {
	case "k":
		multiplier = 1024
	case "m":
		multiplier = 1024 * 1024
	case "g":
		multiplier = 1024 * 1024 * 1024
	}
	const maxInt64 = uint64(1<<63 - 1)
	if number > maxInt64/multiplier || number*multiplier < 6*1024*1024 {
		return fmt.Errorf("invalid memory limit %q: Docker requires at least 6m and the value must fit in a signed byte count", value)
	}
	return nil
}

func invalidMemoryLimit(value string) error {
	return fmt.Errorf("invalid memory limit %q: expected a positive integer followed by b, k, m, or g (minimum 6m)", value)
}

func validateCPULimit(value string) error {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return invalidCPULimit(value)
	}
	for _, character := range parts[0] {
		if character < '0' || character > '9' {
			return invalidCPULimit(value)
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > 9 {
			return invalidCPULimit(value)
		}
		for _, character := range fraction {
			if character < '0' || character > '9' {
				return invalidCPULimit(value)
			}
		}
	}
	whole, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return invalidCPULimit(value)
	}
	for len(fraction) < 9 {
		fraction += "0"
	}
	nanos, err := strconv.ParseUint(fraction, 10, 64)
	if err != nil || whole > uint64(1<<63-1)/1_000_000_000 || (whole == uint64(1<<63-1)/1_000_000_000 && nanos > uint64(1<<63-1)%1_000_000_000) || whole*1_000_000_000+nanos == 0 {
		return invalidCPULimit(value)
	}
	return nil
}

func invalidCPULimit(value string) error {
	return fmt.Errorf("invalid CPU limit %q: expected a positive decimal with at most 9 fractional digits", value)
}

// ValidatePortMapping checks the host and container sides of one published
// port. Host=0 is the YAML/CLI shorthand for host=internal.
func ValidatePortMapping(mapping PortMapping) error {
	internal := mapping.Internal
	host := mapping.HostPort()
	if internal < 1 || internal > 65535 {
		return fmt.Errorf("internal port %d is outside 1..65535", internal)
	}
	if host < 1 || host > 65535 {
		return fmt.Errorf("host port %d is outside 1..65535", host)
	}
	protocol := strings.ToLower(strings.TrimSpace(mapping.Protocol))
	if protocol != "" && protocol != "tcp" && protocol != "udp" {
		return fmt.Errorf("protocol %q is unsupported (expected tcp or udp)", mapping.Protocol)
	}
	return nil
}

// ParsePortMapping parses host:internal[/protocol]. A bare port maps the
// same host and container port. TCP is the default and is omitted in Docker
// argv because it is the runtime default.
func ParsePortMapping(value string) (PortMapping, error) {
	original := value
	value = strings.TrimSpace(value)
	if value == "" {
		return PortMapping{}, fmt.Errorf("empty port mapping")
	}
	protocol := ""
	if slash := strings.LastIndexByte(value, '/'); slash >= 0 {
		protocol = strings.TrimSpace(value[slash+1:])
		value = value[:slash]
		if protocol == "" {
			return PortMapping{}, fmt.Errorf("invalid port mapping %q: protocol is empty", original)
		}
	}
	parts := strings.Split(value, ":")
	if len(parts) > 2 || len(parts) == 0 {
		return PortMapping{}, fmt.Errorf("invalid port mapping %q: expected port or host:internal[/protocol]", original)
	}
	internalText := parts[0]
	hostText := internalText
	if len(parts) == 2 {
		hostText, internalText = parts[0], parts[1]
	}
	host, err := strconv.Atoi(hostText)
	if err != nil {
		return PortMapping{}, fmt.Errorf("invalid port mapping %q: host port %q is not an integer", original, hostText)
	}
	internal, err := strconv.Atoi(internalText)
	if err != nil {
		return PortMapping{}, fmt.Errorf("invalid port mapping %q: internal port %q is not an integer", original, internalText)
	}
	mapping := PortMapping{Host: host, Internal: internal, Protocol: strings.ToLower(protocol)}
	if err := ValidatePortMapping(mapping); err != nil {
		return PortMapping{}, fmt.Errorf("invalid port mapping %q: %w", original, err)
	}
	return mapping, nil
}

// ParsePortMappings parses repeatable --publish values in declaration order.
func ParsePortMappings(values []string) ([]PortMapping, error) {
	mappings := make([]PortMapping, 0, len(values))
	for _, value := range values {
		mapping, err := ParsePortMapping(value)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, mapping)
	}
	return mappings, nil
}
