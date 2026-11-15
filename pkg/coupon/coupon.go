// Package coupon validates promo codes.
//
// A code is valid when its length is within [MinLen, MaxLen] and it appears in
// at least MinFiles of the configured coupon sources.
package coupon

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"hash/maphash"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	MinLen   = 8
	MaxLen   = 10
	MinFiles = 2
)

// Validator checks promo codes. Implementations can be swapped (e.g. Redis,
// a database, a bloom filter) without touching the order flow.
type Validator interface {
	IsValid(code string) bool
}

// Source opens one coupon file (plain text, one code per line).
type Source func() (io.ReadCloser, error)

// FileSource reads a local path or an http(s) URL; ".gz" is transparently decompressed.
func FileSource(location string) Source {
	return func() (io.ReadCloser, error) {
		var rc io.ReadCloser
		if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
			resp, err := http.Get(location)
			if err != nil {
				return nil, err
			}
			if resp.StatusCode != http.StatusOK {
				err := resp.Body.Close()
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("fetch %s: status %d", location, resp.StatusCode)
			}
			rc = resp.Body
		} else {
			f, err := os.Open(location)
			if err != nil {
				return nil, err
			}
			rc = f
		}
		if !strings.HasSuffix(location, ".gz") {
			return rc, nil
		}
		gz, err := gzip.NewReader(rc)
		if err != nil {
			err := rc.Close()
			if err != nil {
				return nil, err
			}
			return nil, err
		}
		return &gzReadCloser{gz: gz, under: rc}, nil
	}
}

type gzReadCloser struct {
	gz    *gzip.Reader
	under io.Closer
}

func (g *gzReadCloser) Read(p []byte) (int, error) { return g.gz.Read(p) }
func (g *gzReadCloser) Close() error {
	err := g.gz.Close()
	if err != nil {
		return err
	}
	return g.under.Close()
}

// SetValidator is an in-memory Validator built by streaming the sources once.
//
// Memory: only codes of valid length are kept, as a 64-bit hash mapped to a
// bitmask of the files they appeared in; entries present in fewer than
// MinFiles files are dropped after loading, so steady-state memory is
// ~16 bytes per valid code. A 64-bit hash makes false positives negligible
// (~n²/2⁶⁵) but not impossible; swap in an exact store if that matters.
type SetValidator struct {
	seed  maphash.Seed
	valid map[uint64]struct{}
}

// NewSetValidator streams all sources and builds the validator.
func NewSetValidator(sources ...Source) (*SetValidator, error) {
	v := &SetValidator{seed: maphash.MakeSeed()}
	seen := make(map[uint64]uint32)
	for i, open := range sources {
		if err := v.load(open, uint32(1)<<i, seen); err != nil {
			return nil, fmt.Errorf("coupon source %d: %w", i, err)
		}
	}
	v.valid = make(map[uint64]struct{})
	for h, mask := range seen {
		if popcount(mask) >= MinFiles {
			v.valid[h] = struct{}{}
		}
	}
	return v, nil
}

func (v *SetValidator) load(open Source, bit uint32, seen map[uint64]uint32) error {
	rc, err := open()
	if err != nil {
		return err
	}
	defer func(rc io.ReadCloser) {
		err := rc.Close()
		if err != nil {

		}
	}(rc)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if len(line) < MinLen || len(line) > MaxLen {
			continue
		}
		seen[maphash.String(v.seed, line)] |= bit
	}
	return sc.Err()
}

func popcount(x uint32) int {
	n := 0
	for ; x != 0; x &= x - 1 {
		n++
	}
	return n
}

// Size returns the number of valid codes known.
func (v *SetValidator) Size() int { return len(v.valid) }

// IsValid reports whether code satisfies the promo rules.
func (v *SetValidator) IsValid(code string) bool {
	if len(code) < MinLen || len(code) > MaxLen {
		return false
	}
	_, ok := v.valid[maphash.String(v.seed, code)]
	return ok
}
