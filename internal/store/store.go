// Package store holds collections and their records in memory.
package store

import (
	"slices"
	"sync"
	"time"
)

// Store is the set of collections on a server.
type Store struct {
	mu          sync.RWMutex
	collections map[string]*Collection
	now         func() int64 // unix ms
}

// New returns an empty store using the system clock.
func New() *Store {
	return &Store{
		collections: make(map[string]*Collection),
		now:         func() int64 { return time.Now().UnixMilli() },
	}
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
	c := newCollection(name, cfg, s.now)
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
	if _, ok := s.collections[name]; !ok {
		return false
	}
	delete(s.collections, name)
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
