package main

import (
	"fmt"
	"strings"
)

// tokenKind is the kind of a KDL token.
type tokenKind int

const (
	tokIdent tokenKind = iota
	tokString
	tokLBrace
	tokRBrace
	tokEOF
)

// token is one KDL token with the line it started on.
type token struct {
	kind  tokenKind
	value string
	line  int
}

// scanKDL splits src into tokens. It accepts comments and blank lines. It is
// the minimal scanner for the frozen keymap shape.
func scanKDL(src string) ([]token, error) {
	var toks []token
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
			toks = append(toks, token{kind: tokLBrace, value: "{", line: line})
			i++
		case c == '}':
			toks = append(toks, token{kind: tokRBrace, value: "}", line: line})
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
			toks = append(toks, token{kind: tokString, value: b.String(), line: start})
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
			toks = append(toks, token{kind: tokIdent, value: src[start:i], line: line})
		}
	}
	toks = append(toks, token{kind: tokEOF, line: line})
	return toks, nil
}

// findPrefix scans the top-level tokens for the prefix node. It returns the
// default ctrl+b when no prefix node is present. It rejects a duplicate prefix
// node and a prefix key that is not one sequence.
func findPrefix(toks []token) (string, []byte, error) {
	depth := 0
	found := false
	name := ""
	var keyBytes []byte
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case tokLBrace:
			depth++
		case tokRBrace:
			depth--
		case tokIdent:
			if depth != 0 || t.value != "prefix" {
				continue
			}
			if found {
				return "", nil, fmt.Errorf("line %d: duplicate prefix node", t.line)
			}
			found = true
			if i+1 >= len(toks) || toks[i+1].kind != tokString {
				return "", nil, fmt.Errorf("line %d: expected quoted key after prefix", t.line)
			}
			name = toks[i+1].value
			if name == "" {
				return "", nil, fmt.Errorf("line %d: empty prefix key", toks[i+1].line)
			}
			seqs, err := keySequences(name, nil)
			if err != nil {
				return "", nil, fmt.Errorf("line %d: prefix: %w", toks[i+1].line, err)
			}
			if len(seqs) != 1 {
				return "", nil, fmt.Errorf("line %d: prefix key must be one sequence", toks[i+1].line)
			}
			keyBytes = seqs[0]
			i++
		}
	}
	if !found {
		seqs, err := keySequences("ctrl+b", nil)
		if err != nil {
			return "", nil, err
		}
		return "ctrl+b", seqs[0], nil
	}
	return name, keyBytes, nil
}
