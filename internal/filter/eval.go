package filter

import "github.com/tranbaohieu/lawliet/internal/meta"

// node is one expression in the parsed filter tree.
//
// Semantics (DESIGN.md §6.2): a comparison on a missing field, or between
// incompatible types, is false. Only "not" can turn that into true.
type node interface {
	eval(m meta.Map) bool
}

type andNode struct{ l, r node }
type orNode struct{ l, r node }
type notNode struct{ n node }

func (n andNode) eval(m meta.Map) bool { return n.l.eval(m) && n.r.eval(m) }
func (n orNode) eval(m meta.Map) bool  { return n.l.eval(m) || n.r.eval(m) }
func (n notNode) eval(m meta.Map) bool { return !n.n.eval(m) }

// cmpNode: field <op> literal
type cmpNode struct {
	field string
	op    string
	lit   meta.Value
}

func (n cmpNode) eval(m meta.Map) bool {
	v, ok := m[n.field]
	if !ok {
		return false
	}
	switch n.op {
	case "==":
		eq, comparable := equal(v, n.lit)
		return comparable && eq
	case "!=":
		eq, comparable := equal(v, n.lit)
		return comparable && !eq
	}
	a, ok1 := v.Number()
	b, ok2 := n.lit.Number()
	if !ok1 || !ok2 {
		return false
	}
	switch n.op {
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case ">=":
		return a >= b
	}
	return false
}

// equal compares two scalar values. comparable is false for type mismatches
// (e.g. string vs number) and for string lists.
func equal(a, b meta.Value) (eq, comparable bool) {
	if x, ok := a.Number(); ok {
		y, ok := b.Number()
		return x == y, ok
	}
	if a.Kind != b.Kind {
		return false, false
	}
	switch a.Kind {
	case meta.String:
		return a.S == b.S, true
	case meta.Bool:
		return a.B == b.B, true
	}
	return false, false
}

// inNode: field in [v1, v2, ...]
type inNode struct {
	field string
	list  []meta.Value
}

func (n inNode) eval(m meta.Map) bool {
	v, ok := m[n.field]
	if !ok {
		return false
	}
	for _, lit := range n.list {
		if eq, comparable := equal(v, lit); comparable && eq {
			return true
		}
	}
	return false
}

// containsNode: field contains "s" (field must be a string list)
type containsNode struct {
	field string
	s     string
}

func (n containsNode) eval(m meta.Map) bool {
	v, ok := m[n.field]
	if !ok || v.Kind != meta.StringList {
		return false
	}
	for _, s := range v.L {
		if s == n.s {
			return true
		}
	}
	return false
}
