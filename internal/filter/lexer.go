package filter

import (
	"strings"
	"unicode/utf8"
)

type tokenKind uint8

const (
	tEOF tokenKind = iota
	tIdent
	tString
	tNumber
	tTrue
	tFalse
	tAnd
	tOr
	tNot
	tIn
	tContains
	tOp // == != < <= > >=
	tLParen
	tRParen
	tLBrack
	tRBrack
	tComma
)

type token struct {
	kind tokenKind
	text string // identifier name, decoded string, number text, or operator
	pos  int    // byte offset in the input (for error messages)
}

func (t token) describe() string {
	switch t.kind {
	case tEOF:
		return "end of filter"
	case tString:
		return "string \"" + t.text + "\""
	}
	return "'" + t.text + "'"
}

var keywords = map[string]tokenKind{
	"and":      tAnd,
	"or":       tOr,
	"not":      tNot,
	"in":       tIn,
	"contains": tContains,
	"true":     tTrue,
	"false":    tFalse,
}

// lex splits the input into tokens.
func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++

		case c == '(' || c == ')' || c == '[' || c == ']' || c == ',':
			kind := map[byte]tokenKind{'(': tLParen, ')': tRParen, '[': tLBrack, ']': tRBrack, ',': tComma}[c]
			toks = append(toks, token{kind: kind, text: string(c), pos: i})
			i++

		case c == '=' || c == '!' || c == '<' || c == '>':
			op := string(c)
			if i+1 < len(src) && src[i+1] == '=' {
				op += "="
			}
			if op == "=" || op == "!" {
				return nil, errAt(i, "unexpected '%s' (use == or !=)", op)
			}
			toks = append(toks, token{kind: tOp, text: op, pos: i})
			i += len(op)

		case c == '"' || c == '\'':
			s, n, err := lexString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tString, text: s, pos: i})
			i += n

		case c == '-' || c == '+' || c == '.' || isDigit(c):
			n := lexNumber(src[i:])
			if n == 0 {
				return nil, errAt(i, "invalid number")
			}
			toks = append(toks, token{kind: tNumber, text: src[i : i+n], pos: i})
			i += n

		case isIdentStart(c):
			j := i + 1
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			word := src[i:j]
			kind, ok := keywords[strings.ToLower(word)]
			if !ok {
				kind = tIdent
			}
			toks = append(toks, token{kind: kind, text: word, pos: i})
			i = j

		default:
			r, _ := utf8.DecodeRuneInString(src[i:])
			return nil, errAt(i, "unexpected character '%c'", r)
		}
	}
	return append(toks, token{kind: tEOF, pos: len(src)}), nil
}

// lexString reads a quoted string starting at src[start] (the quote).
// Supported escapes: \" \' \\ \n \t \r. Returns the decoded string and bytes consumed.
func lexString(src string, start int) (string, int, error) {
	quote := src[start]
	var b strings.Builder
	for i := start + 1; i < len(src); i++ {
		c := src[i]
		switch {
		case c == quote:
			return b.String(), i - start + 1, nil
		case c == '\\':
			if i+1 >= len(src) {
				return "", 0, errAt(i, "unterminated escape")
			}
			i++
			switch src[i] {
			case '"', '\'', '\\':
				b.WriteByte(src[i])
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				return "", 0, errAt(i-1, "unknown escape '\\%c'", src[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, errAt(start, "unterminated string")
}

// lexNumber returns the length of a number literal: [+-]digits[.digits][e[+-]digits].
// Returns 0 if no digits were found.
func lexNumber(s string) int {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && isDigit(s[i]) {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isDigit(s[i]) {
			i++
			digits++
		}
	}
	if digits == 0 {
		return 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i = j
		}
	}
	return i
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') }
func isIdentPart(c byte) bool  { return isIdentStart(c) || isDigit(c) || c == '.' }
