package command

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/server/servertest"
	"github.com/tranbaohieu/lawliet/internal/snapshot"
	"github.com/tranbaohieu/lawliet/internal/store"
)

func TestSaveDisabled(t *testing.T) {
	addr := servertest.Start(t, func(s *server.Server) {
		Register(s, store.New())
		RegisterPersistence(s, nil)
	})
	c := servertest.Dial(t, addr)
	expect(t, describe(c.Do("SAVE")), `-"ERR snapshot disabled: start server with --dir"`)
	if info := c.Do("INFO", "persistence").Str; !strings.Contains(info, "snapshot_enabled:0") {
		t.Fatalf("INFO persistence:\n%s", info)
	}
}

func TestSaveAndInfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.lwdb")
	st := store.New()
	snap := snapshot.New(st, path, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	addr := servertest.Start(t, func(s *server.Server) {
		Register(s, st)
		RegisterPersistence(s, snap)
	})
	c := servertest.Dial(t, addr)
	c.Do("VCREATE", "v", "DIM", 1)
	c.Do("VADD", "v", "a", "VALUES", 1, "1", "TEXT", "hello")

	if info := c.Do("INFO", "persistence").Str; !strings.Contains(info, "rdb_changes_since_last_save:2") {
		t.Fatalf("before SAVE:\n%s", info)
	}
	expect(t, describe(c.Do("SAVE")), `+"OK"`)
	info := c.Do("INFO", "persistence").Str
	for _, want := range []string{"snapshot_enabled:1", "snapshot_path:" + path, "rdb_changes_since_last_save:0", "rdb_last_save_status:ok"} {
		if !strings.Contains(info, want) {
			t.Errorf("INFO missing %q:\n%s", want, info)
		}
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("snapshot file: %v", err)
	}

	// The file loads into a fresh store.
	st2 := store.New()
	if _, found, err := snapshot.New(st2, path, nil, nil).Load(); err != nil || !found {
		t.Fatalf("load: %v %v", found, err)
	}
	col, _ := st2.Get("v")
	if rec, ok := col.Get("a"); !ok || rec.Text != "hello" {
		t.Fatalf("loaded record: %+v %v", rec, ok)
	}
}
