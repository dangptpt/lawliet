// Package filter parses and evaluates metadata filter expressions (DESIGN.md §6).
//
//	(source == "hr.md" or source in ["it.md", "ops.md"]) and page >= 2 and not tags contains "draft"
//
// A filter is parsed once per search and evaluated for every candidate record.
package filter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tranbaohieu/lawliet/internal/meta"
)

const (
	// MaxLength is the longest accepted filter expression in bytes.
	MaxLength = 16 << 10
	// MaxDepth bounds parenthesis / not nesting.
	MaxDepth = 64
	// MaxInList bounds the number of values in an "in" list.
	MaxInList = 1024
)

// Error reports an invalid filter expression.
type Error struct {
	Pos int // byte offset in the expression
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("invalid filter: %s at position %d", e.Msg, e.Pos)
}

func errAt(pos int, format string, a ...any) error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, a...)}
}

// Filter is a parsed expression.
type Filter struct {
	root node
	src  string
}

// Match reports whether a record's metadata satisfies the filter.
func (f *Filter) Match(m meta.Map) bool { return f.root.eval(m) }

// String returns the original expression.
func (f *Filter) String() string { return f.src }

// Parse parses a filter expression.
func Parse(src string) (*Filter, error) {
	if len(src) > MaxLength {
		return nil, errAt(MaxLength, "filter longer than %d bytes", MaxLength)
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	if p.peek().kind == tEOF {
		return nil, errAt(0, "empty filter")
	}
	root, err := p.parseOr(0)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tEOF {
		return nil, errAt(t.pos, "unexpected %s", t.describe())
	}
	return &Filter{root: root, src: src}, nil
}

// --- parser ---------------------------------------------------------------

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }

func (p *parser) next() token {
	t := p.toks[p.i]
	if t.kind != tEOF {
		p.i++
	}
	return t
}

func (p *parser) expect(kind tokenKind, what string) (token, error) {
	t := p.next()
	if t.kind != kind {
		return t, errAt(t.pos, "expected %s, got %s", what, t.describe())
	}
	return t, nil
}

// or_expr = and_expr { "or" and_expr }
func (p *parser) parseOr(depth int) (node, error) {
	left, err := p.parseAnd(depth)
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOr {
		p.next()
		right, err := p.parseAnd(depth)
		if err != nil {
			return nil, err
		}
		left = orNode{left, right}
	}
	return left, nil
}

// and_expr = not_expr { "and" not_expr }
func (p *parser) parseAnd(depth int) (node, error) {
	left, err := p.parseNot(depth)
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tAnd {
		p.next()
		right, err := p.parseNot(depth)
		if err != nil {
			return nil, err
		}
		left = andNode{left, right}
	}
	return left, nil
}

// not_expr = "not" not_expr | primary
func (p *parser) parseNot(depth int) (node, error) {
	if depth > MaxDepth {
		return nil, errAt(p.peek().pos, "nesting deeper than %d", MaxDepth)
	}
	if p.peek().kind == tNot {
		p.next()
		inner, err := p.parseNot(depth + 1)
		if err != nil {
			return nil, err
		}
		return notNode{inner}, nil
	}
	return p.parsePrimary(depth)
}

// primary = "(" expr ")" | comparison
func (p *parser) parsePrimary(depth int) (node, error) {
	if p.peek().kind == tLParen {
		p.next()
		inner, err := p.parseOr(depth + 1)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	return p.parseComparison()
}

// comparison = IDENT op literal | IDENT "in" "[" literal {"," literal} "]" | IDENT "contains" STRING
func (p *parser) parseComparison() (node, error) {
	field, err := p.expect(tIdent, "field name")
	if err != nil {
		return nil, err
	}
	t := p.next()
	switch t.kind {
	case tOp:
		lit, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		if t.text != "==" && t.text != "!=" && lit.Kind != meta.Int && lit.Kind != meta.Float {
			return nil, errAt(t.pos, "operator %s requires a number", t.text)
		}
		return cmpNode{field: field.text, op: t.text, lit: lit}, nil

	case tIn:
		if _, err := p.expect(tLBrack, "'['"); err != nil {
			return nil, err
		}
		var list []meta.Value
		for {
			lit, err := p.parseLiteral()
			if err != nil {
				return nil, err
			}
			list = append(list, lit)
			if len(list) > MaxInList {
				return nil, errAt(t.pos, "'in' list longer than %d", MaxInList)
			}
			sep := p.next()
			if sep.kind == tRBrack {
				break
			}
			if sep.kind != tComma {
				return nil, errAt(sep.pos, "expected ',' or ']', got %s", sep.describe())
			}
		}
		return inNode{field: field.text, list: list}, nil

	case tContains:
		s, err := p.expect(tString, "string")
		if err != nil {
			return nil, err
		}
		return containsNode{field: field.text, s: s.text}, nil
	}
	return nil, errAt(t.pos, "expected operator after field '%s', got %s", field.text, t.describe())
}

// literal = STRING | NUMBER | "true" | "false"
func (p *parser) parseLiteral() (meta.Value, error) {
	t := p.next()
	switch t.kind {
	case tString:
		return meta.Value{Kind: meta.String, S: t.text}, nil
	case tTrue:
		return meta.Value{Kind: meta.Bool, B: true}, nil
	case tFalse:
		return meta.Value{Kind: meta.Bool, B: false}, nil
	case tNumber:
		if !strings.ContainsAny(t.text, ".eE") {
			if i, err := strconv.ParseInt(t.text, 10, 64); err == nil {
				return meta.Value{Kind: meta.Int, I: i}, nil
			}
		}
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return meta.Value{}, errAt(t.pos, "invalid number '%s'", t.text)
		}
		return meta.Value{Kind: meta.Float, F: f}, nil
	}
	return meta.Value{}, errAt(t.pos, "expected a value (string, number, true or false), got %s", t.describe())
}
