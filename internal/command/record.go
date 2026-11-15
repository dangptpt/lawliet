package command

import (
	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/store"
)

// VADD <col> <id> (FP32 <blob> | VALUES <n> v...) [NX | XX]
//
// Reply: 1 inserted, 0 updated, null if the NX/XX condition was not met.
// TEXT/META (M2) and EX/PX (M3) are not supported yet.
func (h *handlers) vadd(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	vec, i, err := parseVector(args, 3)
	if err != nil {
		writeErr(c, err)
		return
	}
	mode := store.Upsert
	for ; i < len(args); i++ {
		switch {
		case isKeyword(args[i], "NX") && mode != store.OnlyExisting:
			mode = store.OnlyNew
		case isKeyword(args[i], "XX") && mode != store.OnlyNew:
			mode = store.OnlyExisting
		default:
			writeErr(c, errSyntax)
			return
		}
	}

	res, err := col.Add(string(args[2]), vec, mode)
	if err != nil {
		writeErr(c, err)
		return
	}
	switch res {
	case store.Inserted:
		c.W().WriteInt(1)
	case store.Updated:
		c.W().WriteInt(0)
	default:
		c.W().WriteNull()
	}
}

// VGET <col> <id> [WITHVEC]
//
// Reply: [text, meta_json, (vector)] or null. Text and metadata arrive in M2
// and are null until then.
func (h *handlers) vget(c *server.Conn, args [][]byte) {
	withVec := false
	switch {
	case len(args) == 4 && isKeyword(args[3], "WITHVEC"):
		withVec = true
	case len(args) != 3:
		writeErr(c, errSyntax)
		return
	}
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	vec, ok := col.Get(string(args[2]))
	if !ok {
		c.W().WriteNull()
		return
	}
	w := c.W()
	if withVec {
		w.WriteArrayHeader(3)
	} else {
		w.WriteArrayHeader(2)
	}
	w.WriteNull() // text (M2)
	w.WriteNull() // meta (M2)
	if withVec {
		w.WriteBulk(encodeFP32(vec))
	}
}

// VDEL <col> <id> [<id> ...]
func (h *handlers) vdel(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	ids := make([]string, len(args)-2)
	for i, a := range args[2:] {
		ids[i] = string(a)
	}
	c.W().WriteInt(int64(col.Delete(ids...)))
}

// VEXISTS <col> <id>
func (h *handlers) vexists(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	if col.Exists(string(args[2])) {
		c.W().WriteInt(1)
	} else {
		c.W().WriteInt(0)
	}
}

// VCOUNT <col>
func (h *handlers) vcount(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.W().WriteInt(int64(col.Count()))
}
