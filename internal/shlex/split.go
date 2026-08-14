// Package shlex parses shell-style argument strings with quoting and escaping.
package shlex

import (
	"errors"
	"strings"
)

// Split parses a shell-style argument string. It supports whitespace
// separation, single and double quotes, and backslash escaping outside single
// quotes.
func Split(input string) ([]string, error) {
	parser := &parser{}
	for _, r := range input {
		parser.feed(r)
	}
	return parser.finish()
}

// parser holds the state of a Split scan: the accumulated words, the word
// being built, and the quoting/escaping mode.
type parser struct {
	result    []string
	current   strings.Builder
	quote     rune
	escaped   bool
	haveValue bool
}

// feed consumes one rune in the current scanning mode.
func (p *parser) feed(r rune) {
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
func (p *parser) feedQuoted(r rune) {
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
func (p *parser) feedBare(r rune) {
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
func (p *parser) write(r rune) {
	p.current.WriteRune(r)
	p.haveValue = true
}

// flush closes the word being built, if any.
func (p *parser) flush() {
	if p.haveValue {
		p.result = append(p.result, p.current.String())
		p.current.Reset()
		p.haveValue = false
	}
}

// finish validates the end state and returns the accumulated words.
func (p *parser) finish() ([]string, error) {
	if p.escaped {
		return nil, errors.New("trailing escape")
	}
	if p.quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	p.flush()
	return p.result, nil
}
