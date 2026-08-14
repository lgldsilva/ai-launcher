package launcher

import "github.com/lgldsilva/ai-launcher/internal/shlex"

// SplitArgs parses shell-style arguments used by configuration fields such as
// extra_args. It supports whitespace separation, single and double quotes,
// and backslash escaping outside single quotes.
func SplitArgs(input string) ([]string, error) {
	return shlex.Split(input)
}
