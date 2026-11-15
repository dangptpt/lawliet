// Package store holds collections and their records in memory.
package store

import (
	"slices"
	"sync"
)

// Store is the set of collections on a server.
type Store struct {
	mu          sync.RWMutex
	collections map[string]*Collection
}

// New returns an empty store.
func New() *Store {
	return &Store{collections: make(map[string]*Collection)}
}

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
	c := newCollection(name, cfg)
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
