// Package command implements the V* commands (DESIGN.md §5).
package command

import (
	"errors"
	"strconv"
	"strings"

	"github.com/tranbaohieu/lawliet/internal/filter"
	"github.com/tranbaohieu/lawliet/internal/meta"
	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/store"
)

// MaxK is the largest K accepted by VSEARCH.
const MaxK = 10000

type handlers struct {
	st *store.Store
}

// Register adds all V* commands to srv, backed by st.
func Register(srv *server.Server, st *store.Store) {
	h := &handlers{st: st}
	// Arity counts the command name; negative means "at least".
	srv.Register("vcreate", -4, h.vcreate) // VCREATE col DIM n ...
	srv.Register("vdrop", 2, h.vdrop)
	srv.Register("vlist", 1, h.vlist)
	srv.Register("vinfo", 2, h.vinfo)
	srv.Register("vadd", -5, h.vadd) // VADD col id FP32 blob ...
	srv.Register("vget", -3, h.vget)
	srv.Register("vdel", -3, h.vdel)
	srv.Register("vexists", 3, h.vexists)
	srv.Register("vcount", 2, h.vcount)
	srv.Register("vsearch", -4, h.vsearch) // VSEARCH col FP32 blob ...
}

// cmdError is an error whose text is sent to the client as is (with "ERR " prefix).
type cmdError string

func (e cmdError) Error() string { return string(e) }

const errSyntax = cmdError("syntax error")

// writeErr maps an error to a RESP error reply.
func writeErr(c *server.Conn, err error) {
	var ce cmdError
	var cfg *store.ConfigError
	var dim *store.DimMismatchError
	var me *meta.Error
	var fe *filter.Error
	switch {
	case errors.As(err, &ce),
		errors.As(err, &cfg),
		errors.As(err, &dim),
		errors.As(err, &me),
		errors.As(err, &fe),
		errors.Is(err, store.ErrCollectionNotFound),
		errors.Is(err, store.ErrCollectionExists),
		errors.Is(err, store.ErrZeroVector),
		errors.Is(err, store.ErrNonFinite):
		c.W().WriteError("ERR " + err.Error())
	default:
		c.W().WriteError("ERR internal error: " + err.Error())
	}
}

func isKeyword(arg []byte, kw string) bool { return strings.EqualFold(string(arg), kw) }

func parseInt(arg []byte, what string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(string(arg))
	if err != nil || n < lo || n > hi {
		return 0, cmdError(what + " must be an integer between " + strconv.Itoa(lo) + " and " + strconv.Itoa(hi))
	}
	return n, nil
}
