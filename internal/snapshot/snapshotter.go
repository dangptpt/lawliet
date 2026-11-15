package snapshot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tranbaohieu/lawliet/internal/store"
)

// Rule triggers a snapshot when at least Changes mutations happened and
// Seconds elapsed since the last save (Redis "save <seconds> <changes>").
type Rule struct {
	Seconds int
	Changes int64
}

// DefaultRules are used when --dir is set without --save.
var DefaultRules = []Rule{{3600, 1}, {300, 100}, {60, 10000}}

// ParseRule parses "<seconds> <changes>".
func ParseRule(s string) (Rule, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return Rule{}, fmt.Errorf("save rule %q: expected \"<seconds> <changes>\"", s)
	}
	sec, err1 := strconv.Atoi(f[0])
	ch, err2 := strconv.ParseInt(f[1], 10, 64)
	if err1 != nil || err2 != nil || sec <= 0 || ch <= 0 {
		return Rule{}, fmt.Errorf("save rule %q: seconds and changes must be positive integers", s)
	}
	return Rule{Seconds: sec, Changes: ch}, nil
}

// Snapshotter saves the store to one file: on rules, on SAVE, and on shutdown.
type Snapshotter struct {
	st    *store.Store
	path  string
	rules []Rule
	log   *slog.Logger

	mu           sync.Mutex // one save at a time; guards the fields below
	lastSave     time.Time
	lastOK       bool
	lastDuration time.Duration
	lastErr      error
}

// New returns a snapshotter writing to path.
func New(st *store.Store, path string, rules []Rule, logger *slog.Logger) *Snapshotter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Snapshotter{st: st, path: path, rules: rules, log: logger, lastSave: time.Now(), lastOK: true}
}

// Path returns the snapshot file path.
func (s *Snapshotter) Path() string { return s.path }

// Load reads the snapshot file if it exists. It returns (zero, false, nil)
// when there is no file.
func (s *Snapshotter) Load() (Stats, bool, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Stats{}, false, nil
	}
	if err != nil {
		return Stats{}, false, err
	}
	defer f.Close()
	stats, err := Read(f, s.st)
	if err != nil {
		return stats, true, fmt.Errorf("%s: %w", s.path, err)
	}
	return stats, true, nil
}

// Save writes a snapshot: temp file, fsync, atomic rename, fsync directory.
func (s *Snapshotter) Save() (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := time.Now()
	dirtyAtStart := s.st.Dirty()
	stats, err := s.write()
	s.lastDuration = time.Since(start)
	s.lastOK = err == nil
	s.lastErr = err
	if err != nil {
		s.log.Error("snapshot failed", "path", s.path, "err", err)
		return stats, err
	}
	s.st.ConsumeDirty(dirtyAtStart) // keep changes made while writing
	s.lastSave = time.Now()
	s.log.Info("snapshot saved", "path", s.path, "collections", stats.Collections,
		"records", stats.Records, "duration", s.lastDuration.Round(time.Millisecond))
	return stats, nil
}

func (s *Snapshotter) write() (Stats, error) {
	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return Stats{}, err
	}
	stats, err := Write(f, s.st)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, s.path)
	}
	if err != nil {
		os.Remove(tmp)
		return stats, err
	}
	// Persist the rename itself (best effort; not supported on every OS).
	if d, err := os.Open(filepath.Dir(s.path)); err == nil {
		d.Sync()
		d.Close()
	}
	return stats, nil
}

// due reports whether a save rule matches.
func (s *Snapshotter) due(now time.Time) bool {
	dirty := s.st.Dirty()
	if dirty == 0 {
		return false
	}
	s.mu.Lock()
	elapsed := now.Sub(s.lastSave)
	failed := !s.lastOK
	s.mu.Unlock()
	if failed && elapsed < 5*time.Second {
		return false // after a failure, retry at most every 5s (like Redis)
	}
	for _, r := range s.rules {
		if dirty >= r.Changes && elapsed >= time.Duration(r.Seconds)*time.Second {
			return true
		}
	}
	return false
}

// Run checks the save rules every second until ctx is cancelled.
// It does not save on exit: the caller saves once the server has stopped,
// so that no write can land after the final snapshot.
func (s *Snapshotter) Run(ctx context.Context) {
	if len(s.rules) == 0 {
		return
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if s.due(now) {
				s.Save()
			}
		}
	}
}

// Info returns fields for the INFO persistence section (Redis names).
func (s *Snapshotter) Info() [][2]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "ok"
	if !s.lastOK {
		status = "err"
	}
	return [][2]string{
		{"rdb_changes_since_last_save", strconv.FormatInt(s.st.Dirty(), 10)},
		{"rdb_last_save_time", strconv.FormatInt(s.lastSave.Unix(), 10)},
		{"rdb_last_save_status", status},
		{"rdb_last_save_duration_ms", strconv.FormatInt(s.lastDuration.Milliseconds(), 10)},
	}
}
