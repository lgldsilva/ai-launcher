package main

import (
	"errors"
	"flag"
	"os"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/launcher"
	"github.com/lgldsilva/ai-launcher/internal/tui"
)

// classifyTUIError maps a TUI result to a process outcome. Only a deliberate
// cancellation is a quiet exit; anything else (a terminal that cannot be
// initialized, for example) is a real failure and must not exit 0 in silence.
func classifyTUIError(err error) error {
	if err == nil || errors.Is(err, tui.ErrCancelled) {
		return nil
	}
	return err
}

func applyBoolFlag(flags *flag.FlagSet, name string, permissions map[string]bool, id string, value bool) {
	if flagsWasSet(flags, name) {
		permissions[id] = value
	}
}

func flagsWasSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, value := range argv {
		// Sanitize after quoting so ESC/CSI from repo config cannot reprogram
		// the terminal when dry-run or the launch banner print the argv.
		parts[i] = launcher.SanitizeDisplay(shellQuote(redactSensitiveDisplayArg(value)))
	}
	return strings.Join(parts, " ")
}

func redactSensitiveDisplayArg(value string) string {
	key, _, found := strings.Cut(value, "=")
	if !found {
		return value
	}
	normalized := strings.ToUpper(strings.TrimLeft(strings.TrimSpace(key), "-"))
	if !strings.Contains(normalized, "TOKEN") &&
		!strings.Contains(normalized, "SECRET") &&
		!strings.Contains(normalized, "PASSWORD") &&
		!strings.Contains(normalized, "API_KEY") &&
		!strings.HasSuffix(normalized, "_KEY") {
		return value
	}
	return key + "=<redacted>"
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-", r)
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func splitArgs(input string) ([]string, error) {
	return launcher.SplitArgs(input)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
