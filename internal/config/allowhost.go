package config

import (
	"fmt"
	"strings"
	"unicode"
)

// ParseAllowHostList splits a TUI or CLI entry on commas and whitespace and
// validates each host. A blank entry clears the list.
func ParseAllowHostList(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	return NormalizeAllowHosts(fields)
}

// NormalizeAllowHosts trims hosts, drops blanks, and rejects anything that is
// not a hostname. ai-jail matches the host and its subdomains; a URL, a path,
// or a port is a different flag and must not be forwarded as one.
func NormalizeAllowHosts(hosts []string) ([]string, error) {
	if len(hosts) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if !validAllowHost(host) {
			return nil, fmt.Errorf("allow host %q is not a hostname", host)
		}
		out = append(out, host)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func validAllowHost(host string) bool {
	if len(host) > 253 || strings.Contains(host, "..") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !validAllowLabel(label) {
			return false
		}
	}
	return true
}

func validAllowLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if !allowHostRune(r) {
			return false
		}
	}
	return true
}

func allowHostRune(r rune) bool {
	if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
		return false
	}
	return true
}
