package command

import (
	"github.com/tranbaohieu/lawliet/internal/server"
)

// VSEARCH <col> (FP32 <blob> | VALUES <n> v...) [K <n>] [WITHVEC]
//
// Reply: array of hits, best first; each hit is [id, score, (vector)].
// FILTER, WITHTEXT and WITHMETA arrive in M2.
func (h *handlers) vsearch(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	query, i, err := parseVector(args, 2)
	if err != nil {
		writeErr(c, err)
		return
	}
	k, withVec := 10, false
	for ; i < len(args); i++ {
		switch {
		case isKeyword(args[i], "K") && i+1 < len(args):
			if k, err = parseInt(args[i+1], "K", 1, MaxK); err != nil {
				writeErr(c, err)
				return
			}
			i++
		case isKeyword(args[i], "WITHVEC"):
			withVec = true
		default:
			writeErr(c, errSyntax)
			return
		}
	}

	results, err := col.Search(query, k, withVec)
	if err != nil {
		writeErr(c, err)
		return
	}
	w := c.W()
	w.WriteArrayHeader(len(results))
	for _, r := range results {
		if withVec {
			w.WriteArrayHeader(3)
		} else {
			w.WriteArrayHeader(2)
		}
		w.WriteBulkString(r.ID)
		w.WriteFloat32(r.Score)
		if withVec {
			w.WriteBulk(encodeFP32(r.Vector))
		}
	}
}
