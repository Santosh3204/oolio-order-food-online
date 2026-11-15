package coupon

import (
	"errors"
	"log"
	"sync/atomic"
)

// ErrNotReady is returned while the coupon index is still loading (or failed to load).
var ErrNotReady = errors.New("coupon index not ready")

// Checker validates a code and can report that it cannot answer yet.
type Checker interface {
	Check(code string) (bool, error)
}

// Async builds a SetValidator in the background so the API starts serving
// immediately; coupon checks return ErrNotReady until loading completes.
type Async struct {
	v atomic.Pointer[SetValidator]
}

// NewAsync starts loading the sources in a goroutine.
func NewAsync(sources ...Source) *Async {
	a := &Async{}
	go func() {
		v, err := NewSetValidator(sources...)
		if err != nil {
			log.Printf("coupon load failed: %v", err)
			return
		}
		a.v.Store(v)
		log.Printf("coupon index ready: %d valid codes", v.Size())
	}()
	return a
}

// Ready reports whether the index is loaded.
func (a *Async) Ready() bool { return a.v.Load() != nil }

// Check implements Checker.
func (a *Async) Check(code string) (bool, error) {
	v := a.v.Load()
	if v == nil {
		return false, ErrNotReady
	}
	return v.IsValid(code), nil
}

// Static adapts a Validator to Checker (useful for tests).
type Static struct{ Validator }

func (s Static) Check(code string) (bool, error) { return s.IsValid(code), nil }
