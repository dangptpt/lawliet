package command

import (
	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/snapshot"
)

// RegisterPersistence adds SAVE and the INFO persistence section.
// snap is nil when the server runs without --dir.
func RegisterPersistence(srv *server.Server, snap *snapshot.Snapshotter) {
	srv.Register("save", 1, func(c *server.Conn, _ [][]byte) {
		if snap == nil {
			c.W().WriteError("ERR snapshot disabled: start server with --dir")
			return
		}
		if _, err := snap.Save(); err != nil {
			c.W().WriteError("ERR snapshot failed: " + err.Error())
			return
		}
		c.W().WriteOK()
	})
	srv.AddInfo("persistence", func() [][2]string {
		if snap == nil {
			return [][2]string{{"snapshot_enabled", "0"}}
		}
		return append([][2]string{
			{"snapshot_enabled", "1"},
			{"snapshot_path", snap.Path()},
		}, snap.Info()...)
	})
}
