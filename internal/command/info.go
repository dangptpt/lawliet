package command

import (
	"strconv"

	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/store"
)

// RegisterInfo adds store fields to INFO (memory, stats, keyspace sections).
func RegisterInfo(srv *server.Server, st *store.Store) {
	i64 := func(n int64) string { return strconv.FormatInt(n, 10) }
	srv.AddInfo("memory", func() [][2]string {
		l := st.Limits()
		return [][2]string{
			{"used_memory_estimated", i64(st.UsedMemory())},
			{"used_memory_vector_slab", i64(st.SlabBytes())},
			{"maxmemory", i64(l.MaxMemory)},
			{"maxmemory_policy", l.DefaultPolicy.String()},
		}
	})
	srv.AddInfo("stats", func() [][2]string {
		return [][2]string{{"evicted_keys", i64(st.Evicted())}}
	})
	srv.AddInfo("keyspace", func() [][2]string {
		return [][2]string{
			{"collections", strconv.Itoa(len(st.List()))},
			{"max_collections", strconv.Itoa(st.Limits().MaxCollections)},
		}
	})
}
