package store

import (
	"errors"
	"fmt"
)

var (
	ErrCollectionExists   = errors.New("collection already exists")
	ErrCollectionNotFound = errors.New("collection not found")
	ErrZeroVector         = errors.New("zero vector is not allowed with cosine metric")
	ErrNonFinite          = errors.New("vector contains NaN or Inf")
)

// DimMismatchError reports a vector whose length differs from the collection dim.
type DimMismatchError struct {
	Expected, Got int
}

func (e *DimMismatchError) Error() string {
	return fmt.Sprintf("dimension mismatch: expected %d, got %d", e.Expected, e.Got)
}

// ConfigError reports an invalid collection configuration.
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string { return e.Msg }
