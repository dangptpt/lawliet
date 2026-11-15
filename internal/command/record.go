package command

import (
	"github.com/tranbaohieu/lawliet/internal/meta"
	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/store"
)

// VADD <col> <id> (FP32 <blob> | VALUES <n> v...)
//
//	[TEXT <str>] [META <json>] [EX <sec> | PX <ms>] [NX | XX]
//
// Reply: 1 inserted, 0 updated, null if the NX/XX condition was not met.
// An update replaces the whole record (text, meta and TTL not given are cleared).
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
	rec := store.Record{Vector: vec}
	mode := store.Upsert
	var ttlMs int64 // 0 = no TTL
	for ; i < len(args); i++ {
		switch {
		case (isKeyword(args[i], "EX") || isKeyword(args[i], "PX")) && i+1 < len(args) && ttlMs == 0:
			unit := int64(1000)
			if isKeyword(args[i], "PX") {
				unit = 1
			}
			if ttlMs, err = parseExpire(args[i+1], unit); err != nil {
				writeErr(c, err)
				return
			}
			i++
		case isKeyword(args[i], "TEXT") && i+1 < len(args):
			rec.Text = string(args[i+1])
			i++
		case isKeyword(args[i], "META") && i+1 < len(args):
			if rec.Meta, err = meta.ParseJSON(args[i+1]); err != nil {
				writeErr(c, err)
				return
			}
			i++
		case isKeyword(args[i], "NX") && mode != store.OnlyExisting:
			mode = store.OnlyNew
		case isKeyword(args[i], "XX") && mode != store.OnlyNew:
			mode = store.OnlyExisting
		default:
			writeErr(c, errSyntax)
			return
		}
	}

	if ttlMs > 0 {
		rec.ExpireAt = h.st.Now() + ttlMs
	}
	res, err := col.Add(string(args[2]), rec, mode)
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
// Reply: [text, meta_json, (vector)] or null if the record does not exist.
// Missing text / metadata are null.
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
	rec, ok := col.Get(string(args[2]))
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
	writeText(c, rec.Text)
	writeMeta(c, rec.Meta)
	if withVec {
		w.WriteBulk(encodeFP32(rec.Vector))
	}
}

// writeText writes the record text, or null if empty.
func writeText(c *server.Conn, s string) {
	if s == "" {
		c.W().WriteNull()
		return
	}
	c.W().WriteBulkString(s)
}

// writeMeta writes metadata as a JSON object string, or null if none.
func writeMeta(c *server.Conn, m meta.Map) {
	if len(m) == 0 {
		c.W().WriteNull()
		return
	}
	c.W().WriteBulk(m.JSON())
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
