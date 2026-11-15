package command

import (
	"strings"

	"github.com/tranbaohieu/lawliet/internal/index"
	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/store"
)

// VCREATE <col> DIM <n> [METRIC cosine|l2|ip] [INDEX flat]
//
//	[MAX_VECTORS <n>] [POLICY noeviction|allkeys-lru|volatile-ttl]
//
// POLICY defaults to the server's --maxmemory-policy.
func (h *handlers) vcreate(c *server.Conn, args [][]byte) {
	cfg := store.Config{Metric: index.Cosine, Index: "flat", Policy: h.st.Limits().DefaultPolicy}
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
		case isKeyword(args[i], "MAX_VECTORS"):
			n, err := parseInt(val, "MAX_VECTORS", 0, store.MaxVectorsLimit)
			if err != nil {
				writeErr(c, err)
				return
			}
			cfg.MaxVectors = n
		case isKeyword(args[i], "POLICY"):
			p, err := store.ParsePolicy(string(val))
			if err != nil {
				writeErr(c, cmdError(err.Error()))
				return
			}
			cfg.Policy = p
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
	w.WriteMapHeader(10)
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
	w.WriteBulkString("expires")
	w.WriteInt(int64(info.Expires))
	w.WriteBulkString("max_vectors")
	w.WriteInt(int64(info.Config.MaxVectors))
	w.WriteBulkString("policy")
	w.WriteBulkString(info.Config.Policy.String())
	w.WriteBulkString("memory_bytes")
	w.WriteInt(info.MemoryBytes)
	w.WriteBulkString("vector_bytes")
	w.WriteInt(info.VectorBytes)
}

// VCONFIG <col> GET
// VCONFIG <col> SET <param> <value> [<param> <value> ...]
//
// Mutable parameters: POLICY, MAX_VECTORS. All values are validated before any
// change is applied. Lowering MAX_VECTORS below the current count only blocks
// new inserts.
func (h *handlers) vconfig(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	switch {
	case isKeyword(args[2], "GET") && len(args) == 3:
		cfg := col.Config()
		w := c.W()
		w.WriteMapHeader(2)
		w.WriteBulkString("max_vectors")
		w.WriteInt(int64(cfg.MaxVectors))
		w.WriteBulkString("policy")
		w.WriteBulkString(cfg.Policy.String())

	case isKeyword(args[2], "SET") && len(args) >= 5 && len(args)%2 == 1:
		var policy *store.Policy
		maxVectors := -1
		for i := 3; i < len(args); i += 2 {
			val := args[i+1]
			switch {
			case isKeyword(args[i], "POLICY"):
				p, err := store.ParsePolicy(string(val))
				if err != nil {
					writeErr(c, cmdError(err.Error()))
					return
				}
				policy = &p
			case isKeyword(args[i], "MAX_VECTORS"):
				if maxVectors, err = parseInt(val, "MAX_VECTORS", 0, store.MaxVectorsLimit); err != nil {
					writeErr(c, err)
					return
				}
			default:
				writeErr(c, cmdError("unknown or immutable parameter '"+string(args[i])+"' (mutable: POLICY, MAX_VECTORS)"))
				return
			}
		}
		if policy != nil {
			col.SetPolicy(*policy)
		}
		if maxVectors >= 0 {
			if err := col.SetMaxVectors(maxVectors); err != nil {
				writeErr(c, err)
				return
			}
		}
		c.W().WriteOK()

	default:
		writeErr(c, errSyntax)
	}
}
