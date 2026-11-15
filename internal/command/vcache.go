package command

import (
	"math"
	"strconv"

	"github.com/tranbaohieu/lawliet/internal/filter"
	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/store"
)

// Semantic cache commands (DESIGN.md §16.1). A cache is an ordinary collection;
// these commands add threshold lookups, default TTL, statistics and invalidation.

func registerVCache(srv *server.Server, h *handlers) {
	srv.Register("vcache.get", -4, h.vcacheGet)              // VCACHE.GET col <vector> ...
	srv.Register("vcache.set", -5, h.vcacheSet)              // VCACHE.SET col id <vector> TEXT s ...
	srv.Register("vcache.stats", 2, h.vcacheStats)           // VCACHE.STATS col
	srv.Register("vcache.invalidate", 4, h.vcacheInvalidate) // VCACHE.INVALIDATE col FILTER expr
}

const errNoThreshold = cmdError("no threshold: pass THRESHOLD or set CACHE_THRESHOLD on the collection")

// parseFloat parses a finite float64.
func parseFloat(arg []byte, what string) (float64, error) {
	f, err := strconv.ParseFloat(string(arg), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, cmdError(what + " must be a finite number")
	}
	return f, nil
}

// VCACHE.GET <col> (FP32 <blob> | VALUES <n> v...) [THRESHOLD <s>] [FILTER <expr>] [WITHMETA]
//
// Reply on a hit (best record with score >= threshold): [id, score, text, (meta_json)].
// Reply on a miss: null. Only a hit counts as an access for LRU.
func (h *handlers) vcacheGet(c *server.Conn, args [][]byte) {
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
	cfg := col.Config()
	threshold, hasThreshold := cfg.CacheThreshold, cfg.HasCacheThreshold
	opt := store.SearchOptions{K: 1, WithText: true, NoTouch: true}
	for ; i < len(args); i++ {
		switch {
		case isKeyword(args[i], "THRESHOLD") && i+1 < len(args):
			if threshold, err = parseFloat(args[i+1], "THRESHOLD"); err != nil {
				writeErr(c, err)
				return
			}
			hasThreshold = true
			i++
		case isKeyword(args[i], "FILTER") && i+1 < len(args):
			f, err := filter.Parse(string(args[i+1]))
			if err != nil {
				writeErr(c, err)
				return
			}
			opt.Filter = f.Match
			i++
		case isKeyword(args[i], "WITHMETA"):
			opt.WithMeta = true
		default:
			writeErr(c, errSyntax)
			return
		}
	}
	if !hasThreshold {
		writeErr(c, errNoThreshold)
		return
	}

	results, err := col.Search(query, opt)
	if err != nil {
		writeErr(c, err)
		return
	}
	hit := len(results) == 1 && float64(results[0].Score) >= threshold
	var score float32
	if hit {
		score = results[0].Score
		col.Touch(results[0].ID)
	}
	col.RecordCacheLookup(hit, score)

	w := c.W()
	if !hit {
		w.WriteNull()
		return
	}
	r := results[0]
	if opt.WithMeta {
		w.WriteArrayHeader(4)
	} else {
		w.WriteArrayHeader(3)
	}
	w.WriteBulkString(r.ID)
	w.WriteFloat32(r.Score)
	writeText(c, r.Text)
	if opt.WithMeta {
		writeMeta(c, r.Meta)
	}
}

// VCACHE.SET <col> <id> (FP32 <blob> | VALUES <n> v...) TEXT <answer>
//
//	[META <json>] [EX <sec> | PX <ms>] [NX | XX]
//
// Same as VADD, but TEXT is required and the collection's CACHE_TTL applies
// when no EX/PX is given. Reply: 1 inserted, 0 updated, null if NX/XX not met.
func (h *handlers) vcacheSet(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	w, err := parseWrite(args, 3)
	if err != nil {
		writeErr(c, err)
		return
	}
	if w.rec.Text == "" {
		writeErr(c, cmdError("VCACHE.SET requires a non-empty TEXT (the cached answer)"))
		return
	}
	if w.ttlMs == 0 {
		w.ttlMs = col.Config().CacheTTL * 1000
	}
	h.applyWrite(c, col, string(args[2]), w)
}

// VCACHE.STATS <col>
//
// Reply map: hits, misses, hit_rate, avg_hit_score, entries. Counters live in
// memory and reset when the server restarts.
func (h *handlers) vcacheStats(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	st := col.CacheStats()
	var rate, avg float64
	if total := st.Hits + st.Misses; total > 0 {
		rate = float64(st.Hits) / float64(total)
	}
	if st.Hits > 0 {
		avg = st.HitScoreSum / float64(st.Hits)
	}
	w := c.W()
	w.WriteMapHeader(5)
	w.WriteBulkString("hits")
	w.WriteInt(st.Hits)
	w.WriteBulkString("misses")
	w.WriteInt(st.Misses)
	w.WriteBulkString("hit_rate")
	w.WriteDouble(rate)
	w.WriteBulkString("avg_hit_score")
	w.WriteDouble(avg)
	w.WriteBulkString("entries")
	w.WriteInt(int64(col.Count()))
}

// VCACHE.INVALIDATE <col> FILTER <expr>
//
// Removes every entry whose metadata matches the filter, e.g.
// `model == "old-model"`. Reply: number of entries removed.
func (h *handlers) vcacheInvalidate(c *server.Conn, args [][]byte) {
	col, err := h.st.Get(string(args[1]))
	if err != nil {
		writeErr(c, err)
		return
	}
	if !isKeyword(args[2], "FILTER") {
		writeErr(c, errSyntax)
		return
	}
	f, err := filter.Parse(string(args[3]))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.W().WriteInt(int64(col.DeleteWhere(f.Match)))
}
