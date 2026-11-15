package coupon

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MemoryIndex is an exact in-memory set of valid codes.
type MemoryIndex struct{ set map[string]struct{} }

// NewMemoryIndex builds an index from codes.
func NewMemoryIndex(codes []string) *MemoryIndex {
	m := &MemoryIndex{set: make(map[string]struct{}, len(codes))}
	for _, c := range codes {
		m.set[c] = struct{}{}
	}
	return m
}

// Contains implements Index.
func (m *MemoryIndex) Contains(code string) (bool, error) {
	_, ok := m.set[code]
	return ok, nil
}

// IsValid implements Validator.
func (m *MemoryIndex) IsValid(code string) bool { ok, _ := m.Contains(code); return ok }

// Size returns the number of codes.
func (m *MemoryIndex) Size() int { return len(m.set) }

// SaveIndexFile writes codes (one per line) atomically so readers never see a partial file.
func SaveIndexFile(path string, codes []string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, c := range codes {
		_, err := w.WriteString(c)
		if err != nil {
			return err
		}
		err = w.WriteByte('\n')
		if err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		err := f.Close()
		if err != nil {
			return err
		}
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadIndexFile reads an index written by SaveIndexFile from a path or http(s) URL
// (e.g. a pre-signed S3 / CDN link published by the offline job).
func LoadIndexFile(location string) (*MemoryIndex, error) {
	rc, err := open(location)
	if err != nil {
		return nil, err
	}
	defer func(rc io.ReadCloser) {
		err := rc.Close()
		if err != nil {

		}
	}(rc)
	var codes []string
	sc := bufio.NewScanner(rc)
	for sc.Scan() {
		if t := sc.Text(); LengthOK(t) {
			codes = append(codes, t)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read index %s: %w", location, err)
	}
	return NewMemoryIndex(codes), nil
}
