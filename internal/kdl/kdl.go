// Package kdl is the minimal KDL tokenizer shared by the keymap and theme
// loaders.
package kdl

import (
	"fmt"
	"strings"
)

// Kind is the kind of a KDL token.
type Kind int

const (
	Ident Kind = iota
	String
	LBrace
	RBrace
	EOF
)

// Token is one KDL token with the line it started on.
type Token struct {
	Kind  Kind
	Value string
	Line  int
}

// Scan splits src into tokens. It accepts comments and blank lines. It is
// the minimal scanner for the frozen keymap shape.
func Scan(src string) ([]Token, error) {
	var toks []Token
	i := 0
	line := 1
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			start := line
			i += 2
			closed := false
			for i < len(src) {
				if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated block comment", start)
			}
		case c == '{':
			toks = append(toks, Token{Kind: LBrace, Value: "{", Line: line})
			i++
		case c == '}':
			toks = append(toks, Token{Kind: RBrace, Value: "}", Line: line})
			i++
		case c == '"':
			start := line
			i++
			var b strings.Builder
			closed := false
			for i < len(src) {
				ch := src[i]
				if ch == '\\' && i+1 < len(src) {
					i++
					switch src[i] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case 'r':
						b.WriteByte('\r')
					case '"':
						b.WriteByte('"')
					case '\\':
						b.WriteByte('\\')
					default:
						b.WriteByte(src[i])
					}
					i++
					continue
				}
				if ch == '"' {
					i++
					closed = true
					break
				}
				if ch == '\n' {
					line++
				}
				b.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated string", start)
			}
			toks = append(toks, Token{Kind: String, Value: b.String(), Line: start})
		default:
			start := i
			for i < len(src) {
				ch := src[i]
				if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '{' || ch == '}' || ch == '"' {
					break
				}
				if ch == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*') {
					break
				}
				i++
			}
			if i == start {
				return nil, fmt.Errorf("line %d: unexpected character %q", line, c)
			}
			toks = append(toks, Token{Kind: Ident, Value: src[start:i], Line: line})
		}
	}
	toks = append(toks, Token{Kind: EOF, Line: line})
	return toks, nil
}
