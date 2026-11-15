// Package coupon validates promo codes.
//
// A code is valid when its length is within [MinLen, MaxLen] and it appears in
// at least MinFiles of the coupon source files.
//
// Architecture (see README):
//
//	offline:  Sources --BuildIndex (partitioned external build)--> valid codes --> Index store
//	online:   API --Service.Check--> Index store (memory file, or shared Redis)
//
// The multi-GB source files are only ever touched by the offline indexer; API
// replicas just load / query the tiny resulting index.
package coupon

import (
	"errors"
	"io"
	"strings"
)

const (
	MinLen   = 8
	MaxLen   = 10
	MinFiles = 2
)

// ErrNotReady is returned while no index is available yet.
var ErrNotReady = errors.New("coupon index not ready")

// Validator checks promo codes in-process (used by tests / memory index).
type Validator interface {
	IsValid(code string) bool
}

// Checker validates a code and can report that it cannot answer (not ready,
// store unreachable).
type Checker interface {
	Check(code string) (bool, error)
}

// Index is a queryable set of valid codes. Implementations: MemoryIndex,
// RedisIndex. Add another (SQL, DynamoDB...) by implementing this interface.
type Index interface {
	Contains(code string) (bool, error)
}

// Source opens one coupon file (plain text, one code per line).
type Source func() (io.ReadCloser, error)

// LengthOK reports whether code has a valid length (cheap pre-filter).
func LengthOK(code string) bool { return len(code) >= MinLen && len(code) <= MaxLen }

func isURL(s string) bool { return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") }

// Static adapts a Validator to Checker (useful for tests).
type Static struct{ Validator }

func (s Static) Check(code string) (bool, error) { return s.IsValid(code), nil }
