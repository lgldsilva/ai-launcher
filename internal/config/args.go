package config

import (
	"errors"
	"strings"
)

// SplitArgs parses shell-style arguments used by configuration fields such as
// extra_args. It supports whitespace separation, single and double quotes,
// and backslash escaping outside single quotes.
func SplitArgs(input string) ([]string, error) {
	parser := &argParser{}
	for _, r := range input {
		parser.feed(r)
	}
	return parser.finish()
}

// argParser holds the state of a SplitArgs scan: the accumulated words, the
// word being built, and the quoting/escaping mode.
type argParser struct {
	result    []string
	current   strings.Builder
	quote     rune
	escaped   bool
	haveValue bool
}

// feed consumes one rune in the current scanning mode.
func (p *argParser) feed(r rune) {
	if p.escaped {
		p.write(r)
		p.escaped = false
		return
	}
	if p.quote != 0 {
		p.feedQuoted(r)
		return
	}
	p.feedBare(r)
}

// feedQuoted consumes one rune inside a quoted section. A backslash only
// escapes inside double quotes; inside single quotes it is literal.
func (p *argParser) feedQuoted(r rune) {
	switch {
	case r == p.quote:
		p.quote = 0
	case p.quote != '\'' && r == '\\':
		p.escaped = true
	default:
		p.write(r)
	}
}

// feedBare consumes one rune outside any quoting.
func (p *argParser) feedBare(r rune) {
	switch r {
	case '\'', '"':
		p.quote = r
		p.haveValue = true
	case '\\':
		p.escaped = true
		p.haveValue = true
	case ' ', '\t', '\n', '\r':
		p.flush()
	default:
		p.write(r)
	}
}

// write appends a rune to the word being built.
func (p *argParser) write(r rune) {
	p.current.WriteRune(r)
	p.haveValue = true
}

// flush closes the word being built, if any.
func (p *argParser) flush() {
	if p.haveValue {
		p.result = append(p.result, p.current.String())
		p.current.Reset()
		p.haveValue = false
	}
}

// finish validates the end state and returns the accumulated words.
func (p *argParser) finish() ([]string, error) {
	if p.escaped {
		return nil, errors.New("trailing escape")
	}
	if p.quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	p.flush()
	return p.result, nil
}
