package coupon_test

import (
	"errors"
	"fmt"
	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// counted wraps a source and counts how many times it was opened.
func counted(s string, opens *atomic.Int32) coupon.Source {
	return func() (io.ReadCloser, error) {
		opens.Add(1)
		return io.NopCloser(strings.NewReader(s)), nil
	}
}

// More than 32 sources used to be rejected (uint32 bitmask).
func TestManySources(t *testing.T) {
	var srcs []coupon.Source
	for i := 0; i < 100; i++ {
		body := fmt.Sprintf("UNIQUE%04d\n", i) // unique per source: never valid
		if i == 3 || i == 77 {
			body += "SHAREDCODE\n" // in exactly two of 100 sources: valid
		}
		if i == 5 {
			body += "ONLYONCE1\nONLYONCE1\n"
		}
		srcs = append(srcs, src(body))
	}
	got, err := coupon.BuildIndex(srcs, t.TempDir(), 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"SHAREDCODE"}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Tiny buffer budget + many partitions must still be exact and must not keep
// files open (would hit the descriptor limit with 2000 partitions x sources).
func TestTinyBudgetManyPartitions(t *testing.T) {
	var lines strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&lines, "CODE%06d\n", i)
	}
	a := lines.String()
	b := "CODE000001\nCODE004999\nNOPE000001\n"
	got, err := coupon.BuildIndexOpts([]coupon.Source{src(a), src(b), src("CODE004999\n")}, coupon.Options{
		TmpDir: t.TempDir(), Partitions: 2000, Workers: 4, SpillMemory: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"CODE000001", "CODE004999"}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSourceConcurrencyIsCapped(t *testing.T) {
	var cur, peak atomic.Int32
	mk := func() coupon.Source {
		return func() (io.ReadCloser, error) {
			n := cur.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			cur.Add(-1)
			return io.NopCloser(strings.NewReader("SAMECODE1\n")), nil
		}
	}
	var srcs []coupon.Source
	for i := 0; i < 8; i++ {
		srcs = append(srcs, mk())
	}
	got, err := coupon.BuildIndexOpts(srcs, coupon.Options{TmpDir: t.TempDir(), Partitions: 4, Workers: 2, SourceConcurrency: 2})
	if err != nil || !slices.Equal(got, []string{"SAMECODE1"}) {
		t.Fatalf("got %v err %v", got, err)
	}
	if p := peak.Load(); p > 2 {
		t.Fatalf("peak concurrent sources %d, cap 2", p)
	}
}

// A failed build keeps finished sources; the next run does not re-download them.
func TestResumeSkipsFinishedSources(t *testing.T) {
	tmp := t.TempDir()
	var o0, o1, o2 atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	flaky := func() (io.ReadCloser, error) {
		o2.Add(1)
		if fail.Load() {
			return nil, errors.New("network down")
		}
		return io.NopCloser(strings.NewReader("HAPPYHRS\nSUPER100\n")), nil
	}
	srcs := []coupon.Source{
		counted("HAPPYHRS\nFIFTYOFF\n", &o0),
		counted("HAPPYHRS\nFIFTYOFF\nSUPER100\n", &o1),
		flaky,
	}
	opts := coupon.Options{TmpDir: tmp, Partitions: 4, Workers: 2, SourceConcurrency: 1, Key: "k1", Resume: true}

	if _, err := coupon.BuildIndexOpts(srcs, opts); err == nil {
		t.Fatal("expected failure")
	}
	fail.Store(false)
	got, err := coupon.BuildIndexOpts(srcs, opts)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"FIFTYOFF", "HAPPYHRS", "SUPER100"}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if o0.Load() != 1 || o1.Load() != 1 || o2.Load() != 2 {
		t.Fatalf("opens = %d,%d,%d; want 1,1,2 (finished sources must not be re-read)", o0.Load(), o1.Load(), o2.Load())
	}
	if m, _ := filepath.Glob(filepath.Join(tmp, "couponbuild-*")); len(m) != 0 {
		t.Fatalf("work dir not cleaned after success: %v", m)
	}
}

// Resume=false discards leftovers; a different Key never reuses another build's data.
func TestNoResumeStartsFresh(t *testing.T) {
	tmp := t.TempDir()
	var opens atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	bad := func() (io.ReadCloser, error) {
		if fail.Load() {
			return nil, errors.New("down")
		}
		return io.NopCloser(strings.NewReader("HAPPYHRS\n")), nil
	}
	srcs := []coupon.Source{counted("HAPPYHRS\n", &opens), bad}
	o := coupon.Options{TmpDir: tmp, Partitions: 2, Workers: 1, SourceConcurrency: 1, Key: "k", Resume: false}
	_, _ = coupon.BuildIndexOpts(srcs, o)
	fail.Store(false)
	if _, err := coupon.BuildIndexOpts(srcs, o); err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 2 {
		t.Fatalf("source opened %d times; Resume=false must re-read", opens.Load())
	}
}

// Two builds on the same work dir (non-cooperative) must not trample each other.
func TestConcurrentBuildSameKeyIsRejected(t *testing.T) {
	tmp := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	blocker := func() (io.ReadCloser, error) {
		close(started)
		<-release
		return io.NopCloser(strings.NewReader("HAPPYHRS\n")), nil
	}
	o := coupon.Options{TmpDir: tmp, Partitions: 2, Workers: 1, Key: "same", Resume: true}
	done := make(chan error, 1)
	go func() { _, err := coupon.BuildIndexOpts([]coupon.Source{blocker, src("HAPPYHRS\n")}, o); done <- err }()
	<-started
	if _, err := coupon.BuildIndexOpts([]coupon.Source{src("HAPPYHRS\n"), src("HAPPYHRS\n")}, o); err == nil {
		t.Fatal("second build on the same key should be rejected")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Several "machines" sharing one work dir split the work and all return the full result.
func TestCooperativeBuild(t *testing.T) {
	oldPoll := coupon.ClaimPoll
	coupon.ClaimPoll = 10 * time.Millisecond
	defer func() { coupon.ClaimPoll = oldPoll }()

	tmp := t.TempDir()
	var opens [6]atomic.Int32
	var srcs []coupon.Source
	for i := range opens {
		body := fmt.Sprintf("UNIQUE%04d\nSHARED%04d\n", i, i/2) // pairs (0,1)(2,3)(4,5) share a code
		srcs = append(srcs, counted(body, &opens[i]))
	}
	o := coupon.Options{TmpDir: tmp, Partitions: 8, Workers: 2, SourceConcurrency: 2, Key: "run1", Cooperative: true}

	var wg sync.WaitGroup
	results := make([][]string, 3)
	errs := make([]error, 3)
	for i := range results {
		wg.Add(1)
		go func() { defer wg.Done(); results[i], errs[i] = coupon.BuildIndexOpts(srcs, o) }()
	}
	wg.Wait()
	want := []string{"SHARED0000", "SHARED0001", "SHARED0002"}
	for i := range results {
		if errs[i] != nil || !slices.Equal(results[i], want) {
			t.Fatalf("worker %d: %v err %v", i, results[i], errs[i])
		}
	}
	var total int32
	for i := range opens {
		total += opens[i].Load()
	}
	if total != 6 {
		t.Fatalf("sources opened %d times in total, want 6 (work must be split, not repeated)", total)
	}
}

// A crashed worker's stale claim is taken over.
func TestStaleClaimIsTakenOver(t *testing.T) {
	oldLease := coupon.ClaimLease
	coupon.ClaimLease = 50 * time.Millisecond
	defer func() { coupon.ClaimLease = oldLease }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.claim"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := coupon.Claim(dir, "x"); ok {
		t.Fatal("fresh foreign claim must not be taken")
	}
	time.Sleep(120 * time.Millisecond)
	rel, ok := coupon.Claim(dir, "x")
	if !ok {
		t.Fatal("stale claim should be taken over")
	}
	rel()
}

func TestCooperativeNeedsKey(t *testing.T) {
	if _, err := coupon.BuildIndexOpts([]coupon.Source{src("a"), src("b")}, coupon.Options{TmpDir: t.TempDir(), Cooperative: true}); err == nil {
		t.Fatal("expected error")
	}
}
