package coupon

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
)

// maxSources is bounded by the uint16 source index used while reducing.
const maxSources = 65535

// Options tunes BuildIndexOpts. The zero value is valid.
type Options struct {
	TmpDir     string // spill space (created if missing)
	Partitions int    // hash partitions; more = less RAM per reduce, more spill files
	Workers    int    // partitions reduced in parallel

	// SourceConcurrency caps how many sources are downloaded/decompressed at
	// once (default 4). Spill memory scales with it.
	SourceConcurrency int
	// SpillMemory is the total byte budget for spill write buffers (default
	// 256 MiB), split across concurrently streamed sources and partitions.
	SpillMemory int64

	// Key makes the build resumable: the work directory is derived from it and
	// survives a failure, so a re-run skips finished sources and partitions.
	// Include everything that defines the input (locations, content
	// fingerprints, partitions). Empty = ephemeral build, removed on exit.
	Key string
	// Resume: reuse an existing work directory for Key (default behaviour of
	// Build). When false, a leftover directory is discarded first.
	Resume bool
	// Cooperative lets several processes (or machines sharing TmpDir) build the
	// same Key together: work is claimed with leases, every process returns the
	// full result. The work directory is NOT removed, so give each run a fresh
	// Key (Build appends COUPON_BUILD_ID).
	Cooperative bool
}

// BuildIndex returns the exact, sorted list of valid codes: valid length and
// present in >= MinFiles distinct sources. It is the simple, ephemeral form of
// BuildIndexOpts.
func BuildIndex(sources []Source, tmpDir string, partitions, workers int) ([]string, error) {
	return BuildIndexOpts(sources, Options{TmpDir: tmpDir, Partitions: partitions, Workers: workers})
}

// BuildIndexOpts is an external (disk-spilling) hash-partition job, so memory
// is bounded no matter how large the sources are, and the result is exact (no
// hash collisions):
//
//  1. Spill: every source is streamed ONCE (at most SourceConcurrency at a
//     time). Each code of valid length is appended to
//     <work>/s<source>/p<partition>, partition = FNV(code) % partitions. Equal
//     codes always land in the same partition. Write buffers are small and
//     flushed with open-append-close, so no file descriptors stay open and
//     buffer memory is bounded by SpillMemory regardless of partitions x sources.
//     A source is committed by atomically renaming its directory, so a crash
//     never leaves a half-spilled source that looks complete.
//  2. Reduce: partitions are processed `Workers` at a time; within a partition
//     the sources are read in order and a code -> (last source, distinct count)
//     map is built (no bitmask, so any number of sources works). Codes with
//     count >= MinFiles are kept and written atomically to r<partition>.
//  3. Merge the (tiny) per-partition results and sort.
//
// With Options.Key set, finished sources/partitions survive failures and are
// skipped on the next run; with Options.Cooperative, several processes share
// the work. Peak RAM ~ Workers x (total codes / partitions) entries.
func BuildIndexOpts(sources []Source, o Options) ([]string, error) {
	if len(sources) > maxSources {
		return nil, fmt.Errorf("at most %d sources supported", maxSources)
	}
	if o.Partitions < 1 {
		o.Partitions = 1
	}
	if o.Workers < 1 {
		o.Workers = 1
	}
	if o.SourceConcurrency < 1 {
		o.SourceConcurrency = 4
	}
	o.SourceConcurrency = min(o.SourceConcurrency, max(len(sources), 1))
	if o.SpillMemory <= 0 {
		o.SpillMemory = 256 << 20
	}
	if o.Cooperative && o.Key == "" {
		return nil, fmt.Errorf("cooperative build needs a Key")
	}
	if err := os.MkdirAll(o.TmpDir, 0o755); err != nil {
		return nil, err
	}

	work, ephemeral, err := workDir(o)
	if err != nil {
		return nil, err
	}
	if !o.Cooperative {
		// One build per work directory; also guards against two local processes
		// (e.g. API autobuild + indexer) racing on the same Key.
		release, ok := Claim(work, "build")
		if !ok {
			return nil, fmt.Errorf("another build is using %s (lock held; stale locks expire after %s)", work, ClaimLease)
		}
		defer release()
		removeLeftovers(work)
	}

	n := len(sources)
	// per-source buffer so that (partitions x concurrent sources x buf) fits the budget
	bufSize := int(min(max(o.SpillMemory/int64(o.Partitions*o.SourceConcurrency), 4<<10), 256<<10))

	// 1. spill
	srcDone := func(i int) bool { _, err := os.Stat(sourceDir(work, i)); return err == nil }
	err = runPhase(n, o.SourceConcurrency, o.Cooperative, work, "s", srcDone, func(i int) error {
		cnt, err := spillSource(sources[i], work, i, o.Partitions, bufSize)
		if err != nil {
			return fmt.Errorf("source %d: %w", i, err)
		}
		if cnt >= 0 {
			log.Printf("coupon indexer: source %d spilled (%d codes of valid length)", i, cnt)
		}
		return nil
	})
	if err != nil {
		return nil, keepOrClean(work, ephemeral, err)
	}

	// 2. reduce: `Workers` partitions at a time (peak RAM ~ workers x partition size)
	var finished atomic.Int64
	resDone := func(p int) bool { _, err := os.Stat(resultPath(work, p)); return err == nil }
	err = runPhase(o.Partitions, o.Workers, o.Cooperative, work, "p", resDone, func(p int) error {
		codes, err := reduce(work, p, n)
		if err != nil {
			return fmt.Errorf("partition %d: %w", p, err)
		}
		if resDone(p) { // someone else committed it first; our read may have raced their cleanup
			return nil
		}
		if err := writeResult(work, p, codes); err != nil {
			return err
		}
		// result is durable: free this partition's spill files
		for s := 0; s < n; s++ {
			_ = os.Remove(spillPath(work, s, p))
		}
		if d := finished.Add(1); d%16 == 0 {
			log.Printf("coupon indexer: reduced %d/%d partitions", d, o.Partitions)
		}
		return nil
	})
	if err != nil {
		return nil, keepOrClean(work, ephemeral, err)
	}

	// 3. merge
	var valid []string
	for p := 0; p < o.Partitions; p++ {
		codes, err := readResult(work, p)
		if err != nil {
			return nil, keepOrClean(work, ephemeral, err)
		}
		valid = append(valid, codes...)
	}
	slices.Sort(valid)

	if ephemeral || !o.Cooperative {
		if err := os.RemoveAll(work); err != nil {
			log.Printf("coupon indexer: cleanup %s: %v", work, err)
		}
	}
	return valid, nil
}

// workDir returns the directory for this build. Keyed builds get a stable
// directory (resumable); keyless builds a private temp dir.
func workDir(o Options) (dir string, ephemeral bool, err error) {
	if o.Key == "" {
		dir, err = os.MkdirTemp(o.TmpDir, "couponbuild-")
		return dir, true, err
	}
	sum := sha256.Sum256([]byte(o.Key))
	dir = filepath.Join(o.TmpDir, "couponbuild-"+hex.EncodeToString(sum[:8]))
	if !o.Resume && !o.Cooperative {
		if err := os.RemoveAll(dir); err != nil {
			return "", false, err
		}
	}
	return dir, false, os.MkdirAll(dir, 0o755)
}

// keepOrClean returns err; ephemeral builds also drop their dir, keyed builds
// keep it so the next run resumes.
func keepOrClean(work string, ephemeral bool, err error) error {
	if ephemeral {
		_ = os.RemoveAll(work)
	} else {
		log.Printf("coupon indexer: build failed; progress kept in %s for resume", work)
	}
	return err
}

// removeLeftovers deletes half-written artifacts of a crashed previous run
// (single-process mode only: in cooperative mode they may belong to a live peer).
func removeLeftovers(work string) {
	for _, pat := range []string{"s*.partial-*", "r*.tmp-*"} {
		m, _ := filepath.Glob(filepath.Join(work, pat))
		for _, p := range m {
			_ = os.RemoveAll(p)
		}
	}
}

func sourceDir(work string, s int) string { return filepath.Join(work, fmt.Sprintf("s%05d", s)) }
func spillPath(work string, s, p int) string {
	return filepath.Join(sourceDir(work, s), fmt.Sprintf("p%05d", p))
}
func resultPath(work string, p int) string { return filepath.Join(work, fmt.Sprintf("r%05d", p)) }

// spillSource spills one source into a private directory and commits it with an
// atomic rename. Returns -1 if another process committed the source first.
func spillSource(open Source, work string, s, partitions, bufSize int) (int64, error) {
	tmp, err := os.MkdirTemp(work, fmt.Sprintf("s%05d.partial-", s))
	if err != nil {
		return 0, err
	}
	n, err := spill(open, tmp, partitions, bufSize)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, sourceDir(work, s)); err != nil {
		_ = os.RemoveAll(tmp)
		if _, serr := os.Stat(sourceDir(work, s)); serr == nil {
			return -1, nil // lost the race to an identical result
		}
		return 0, err
	}
	return n, nil
}

// spiller buffers codes per partition in small slices and appends them to the
// partition file with open-append-close. No file stays open between flushes, so
// the descriptor count does not depend on partitions x sources.
type spiller struct {
	dir     string
	bufSize int
	bufs    [][]byte
}

func (sp *spiller) path(p int) string { return filepath.Join(sp.dir, fmt.Sprintf("p%05d", p)) }

func (sp *spiller) add(p int, code []byte) error {
	buf := sp.bufs[p]
	if len(buf) > 0 && len(buf)+len(code)+1 > sp.bufSize {
		if err := sp.flush(p); err != nil {
			return err
		}
		buf = sp.bufs[p]
	}
	if buf == nil {
		buf = make([]byte, 0, sp.bufSize)
	}
	buf = append(append(buf, code...), '\n')
	sp.bufs[p] = buf
	return nil
}

func (sp *spiller) flush(p int) error {
	buf := sp.bufs[p]
	if len(buf) == 0 {
		return nil
	}
	f, err := os.OpenFile(sp.path(p), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(buf)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	sp.bufs[p] = buf[:0]
	return nil
}

func spill(open Source, dir string, partitions, bufSize int) (int64, error) {
	rc, err := open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = rc.Close() }()

	sp := &spiller{dir: dir, bufSize: bufSize, bufs: make([][]byte, partitions)}
	var n int64
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		b := sc.Bytes()
		if l := len(b); l < MinLen || l > MaxLen {
			continue
		}
		if err := sp.add(int(fnv1a(b)%uint64(partitions)), b); err != nil {
			return 0, err
		}
		n++
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	for p := range sp.bufs {
		if err := sp.flush(p); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// seenEntry tracks, per code, the last source that contained it and in how many
// distinct sources it appeared. Sources are read in order, so "last" is enough
// to count each source once - no per-source bitmask, hence no 32-source cap.
type seenEntry struct{ last, count uint16 }

func reduce(work string, p, nSources int) ([]string, error) {
	seen := make(map[string]seenEntry)
	for s := 0; s < nSources; s++ {
		f, err := os.Open(spillPath(work, s, p))
		if os.IsNotExist(err) {
			continue // no code of this source hashed to this partition
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			b := sc.Bytes()
			if e, ok := seen[string(b)]; !ok { // lookup: no allocation
				seen[string(b)] = seenEntry{last: uint16(s), count: 1}
			} else if e.last != uint16(s) {
				seen[string(b)] = seenEntry{last: uint16(s), count: e.count + 1}
			}
		}
		scErr := sc.Err()
		clErr := f.Close()
		if scErr != nil {
			return nil, scErr
		}
		if clErr != nil {
			return nil, clErr
		}
	}
	var out []string
	for code, e := range seen {
		if int(e.count) >= MinFiles {
			out = append(out, code)
		}
	}
	return out, nil
}

// writeResult commits a partition's valid codes atomically.
func writeResult(work string, p int, codes []string) error {
	tmp := resultPath(work, p) + ".tmp-" + randSuffix()
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, c := range codes {
		w.WriteString(c)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, resultPath(work, p))
}

func readResult(work string, p int) ([]string, error) {
	f, err := os.Open(resultPath(work, p))
	if err != nil {
		return nil, err
	}
	defer func(f io.Closer) { _ = f.Close() }(f)
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out, sc.Err()
}

func fnv1a(b []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}
