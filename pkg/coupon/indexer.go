package coupon

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math/bits"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// BuildIndex returns the exact, sorted list of valid codes: valid length and
// present in >= MinFiles distinct sources.
//
// It is an external (disk-spilling) hash-partition job, so memory is bounded
// no matter how large the sources are, and the result is exact (no hash
// collisions):
//
//  1. Spill: every source is streamed ONCE (concurrently). Each code of valid
//     length goes to spill file <tmp>/p<partition>-s<source> where partition =
//     FNMA(code) % partitions. Equal codes always land in the same partition.
//  2. Reduce: partitions are processed one at a time; within a partition a map
//     code -> bitmask-of-sources is built and codes with >= MinFiles bits are
//     kept. Peak memory ~ (total codes / partitions) entries.
//
// For the real ~340M-code input: ~3.4 GB temporary disk and, with 64
// partitions, a few hundred MB RAM. Raise `partitions` to lower memory further.
func BuildIndex(sources []Source, tmpDir string, partitions int) ([]string, error) {
	if len(sources) > 32 {
		return nil, fmt.Errorf("at most 32 sources supported")
	}
	if partitions < 1 {
		partitions = 1
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(tmpDir, "couponbuild-")
	if err != nil {
		return nil, err
	}
	defer func(path string) {
		err := os.RemoveAll(path)
		if err != nil {

		}
	}(work)

	// 1. spill
	errs := make([]error, len(sources))
	var wg sync.WaitGroup
	for i, open := range sources {
		wg.Go(func() {
			n, err := spill(open, work, i, partitions)
			if err != nil {
				errs[i] = fmt.Errorf("source %d: %w", i, err)
				return
			}
			log.Printf("coupon indexer: source %d spilled (%d codes of valid length)", i, n)
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	// 2. reduce, one partition at a time
	var valid []string
	for p := 0; p < partitions; p++ {
		got, err := reduce(work, p, len(sources))
		if err != nil {
			return nil, err
		}
		valid = append(valid, got...)
		if (p+1)%16 == 0 || p == partitions-1 {
			log.Printf("coupon indexer: reduced %d/%d partitions, %d valid so far", p+1, partitions, len(valid))
		}
	}
	slices.Sort(valid)
	return valid, nil
}

func spillPath(dir string, p, s int) string {
	return filepath.Join(dir, fmt.Sprintf("p%04d-s%02d", p, s))
}

func spill(open Source, dir string, srcIdx, partitions int) (int64, error) {
	rc, err := open()
	if err != nil {
		return 0, err
	}
	defer func(rc io.ReadCloser) {
		err := rc.Close()
		if err != nil {

		}
	}(rc)

	files := make([]*os.File, partitions)
	writers := make([]*bufio.Writer, partitions)
	defer func() {
		for _, f := range files {
			if f != nil {
				err := f.Close()
				if err != nil {
					return
				}
			}
		}
	}()
	for p := range files {
		f, err := os.Create(spillPath(dir, p, srcIdx))
		if err != nil {
			return 0, err
		}
		files[p] = f
		writers[p] = bufio.NewWriterSize(f, 64*1024)
	}

	var n int64
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		b := sc.Bytes()
		if l := len(b); l < MinLen || l > MaxLen {
			continue
		}
		w := writers[fnv1a(b)%uint64(partitions)]
		_, err := w.Write(b)
		if err != nil {
			return 0, err
		}
		err = w.WriteByte('\n')
		if err != nil {
			return 0, err
		}
		n++
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	for _, w := range writers {
		if err := w.Flush(); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func reduce(dir string, p, nSources int) ([]string, error) {
	seen := make(map[string]uint32)
	for s := range nSources {
		path := spillPath(dir, p, s)
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		bit := uint32(1) << s
		for sc.Scan() {
			b := sc.Bytes()
			if m, ok := seen[string(b)]; ok { // lookup: no allocation
				seen[string(b)] = m | bit
			} else {
				seen[string(b)] = bit
			}
		}
		scErr := sc.Err()
		if scErr != nil {
			return nil, scErr
		}
		err = f.Close()
		if err != nil {
			return nil, err
		}
		osErr := os.Remove(path)
		if osErr != nil {
			return nil, osErr
		} // free disk as we go
	}
	var out []string
	for code, mask := range seen {
		if bits.OnesCount32(mask) >= MinFiles {
			out = append(out, code)
		}
	}
	return out, nil
}

func fnv1a(b []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}
