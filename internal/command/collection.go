package command

import (
	"strings"

	"github.com/tranbaohieu/lawliet/internal/index"
	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/store"
)

// VCREATE <col> DIM <n> [METRIC cosine|l2|ip] [INDEX flat]
func (h *handlers) vcreate(c *server.Conn, args [][]byte) {
	cfg := store.Config{Metric: index.Cosine, Index: "flat"}
	for i := 2; i < len(args); i += 2 {
		if i+1 >= len(args) {
			writeErr(c, errSyntax)
			return
		}
		val := args[i+1]
		switch {
		case isKeyword(args[i], "DIM"):
			n, err := parseInt(val, "DIM", 1, store.MaxDim)
			if err != nil {
				writeErr(c, err)
				return
			}
			cfg.Dim = n
		case isKeyword(args[i], "METRIC"):
			m, err := index.ParseMetric(string(val))
			if err != nil {
				writeErr(c, cmdError(err.Error()))
				return
			}
			cfg.Metric = m
		case isKeyword(args[i], "INDEX"):
			cfg.Index = strings.ToLower(string(val))
		default:
			writeErr(c, errSyntax)
			return
		}
	}
	if cfg.Dim == 0 {
		writeErr(c, cmdError("DIM is required"))
		return
	}
	if _, err := h.st.Create(string(args[1]), cfg); err != nil {
		writeErr(c, err)
		return
	}
	c.W().WriteOK()
}

// VDROP <col>
func (h *handlers) vdrop(c *server.Conn, args [][]byte) {
	if h.st.Drop(string(args[1])) {
		c.W().WriteInt(1)
	} else {
		c.W().WriteInt(0)
	}
}

// VLIST
func (h *handlers) vlist(c *server.Conn, _ [][]byte) {
	names := h.st.List()
	c.W().WriteArrayHeader(len(names))
	for _, n := range names {
		c.W().WriteBulkString(n)
	}
}

// VINFO <col>
func (h *handlers) vinfo(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	info := col.Info()
	w := c.W()
	w.WriteMapHeader(6)
	w.WriteBulkString("name")
	w.WriteBulkString(info.Name)
	w.WriteBulkString("dim")
	w.WriteInt(int64(info.Config.Dim))
	w.WriteBulkString("metric")
	w.WriteBulkString(info.Config.Metric.String())
	w.WriteBulkString("index")
	w.WriteBulkString(info.Config.Index)
	w.WriteBulkString("count")
	w.WriteInt(int64(info.Count))
	w.WriteBulkString("vector_bytes")
	w.WriteInt(info.VectorBytes)
}
