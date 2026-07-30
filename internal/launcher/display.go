package launcher

import (
	"fmt"
	"strings"
	"unicode"
)

// SanitizeDisplay makes untrusted text safe to write to a terminal: C0/C1
// control runes (except tab and newline) become visible escapes so ESC/CSI/OSC
// sequences from repository config or filenames cannot reprogram the TTY.
func SanitizeDisplay(value string) string {
	if value == "" {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch {
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r == unicode.ReplacementChar:
			b.WriteString(`\uFFFD`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r >= 0x80 && r <= 0x9f:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
