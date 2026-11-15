// Package store holds collections and their records in memory.
package store

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Limits are server-wide limits (DESIGN.md §8.1).
type Limits struct {
	MaxMemory      int64  // bytes, 0 = unlimited
	MaxCollections int    // 0 = unlimited
	DefaultPolicy  Policy // policy for VCREATE without POLICY
}

// Store is the set of collections on a server.
type Store struct {
	mu          sync.RWMutex
	collections map[string]*Collection
	now         func() int64 // unix ms

	limits  atomic.Pointer[Limits]
	used    atomic.Int64 // estimated bytes of all records
	evicted atomic.Int64 // records removed by eviction
}

// New returns an empty store using the system clock and no limits.
func New() *Store {
	s := &Store{
		collections: make(map[string]*Collection),
		now:         func() int64 { return time.Now().UnixMilli() },
	}
	s.limits.Store(&Limits{})
	return s
}

// SetLimits replaces the server-wide limits.
func (s *Store) SetLimits(l Limits) { s.limits.Store(&l) }

// Limits returns the server-wide limits.
func (s *Store) Limits() Limits { return *s.limits.Load() }

// Evicted returns the number of records removed by eviction.
func (s *Store) Evicted() int64 { return s.evicted.Load() }

// collectionList returns a snapshot of the collections.
func (s *Store) collectionList() []*Collection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cols := make([]*Collection, 0, len(s.collections))
	for _, c := range s.collections {
		cols = append(cols, c)
	}
	return cols
}

// SetClock replaces the clock (unix ms). Intended for tests; call before
// creating collections.
func (s *Store) SetClock(now func() int64) { s.now = now }

// Now returns the store clock in unix ms.
func (s *Store) Now() int64 { return s.now() }

// Create adds a new collection.
func (s *Store) Create(name string, cfg Config) (*Collection, error) {
	if name == "" {
		return nil, &ConfigError{Msg: "collection name must not be empty"}
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.collections[name]; ok {
		return nil, ErrCollectionExists
	}
	if max := s.Limits().MaxCollections; max > 0 && len(s.collections) >= max {
		return nil, ErrMaxCollections
	}
	c := newCollection(name, cfg, s)
	s.collections[name] = c
	return c, nil
}

// Get returns the named collection.
func (s *Store) Get(name string) (*Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.collections[name]
	if !ok {
		return nil, ErrCollectionNotFound
	}
	return c, nil
}

// Drop removes the named collection. Returns false if it did not exist.
// Requests already holding the collection finish normally; its memory is
// released by the GC afterwards.
func (s *Store) Drop(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.collections[name]
	if !ok {
		return false
	}
	delete(s.collections, name)
	c.mu.Lock()
	s.used.Add(-c.memBytes)
	c.memBytes = 0
	c.mu.Unlock()
	return true
}

// List returns collection names in sorted order.
func (s *Store) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.collections))
	for n := range s.collections {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}
