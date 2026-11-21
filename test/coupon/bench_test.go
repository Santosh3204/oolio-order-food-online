package coupon_test

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
)

func benchService(idx coupon.Index) *coupon.Service {
	s := &coupon.Service{}
	s.SetIndex(idx)
	return s
}

func BenchmarkCheckMemory(b *testing.B) {
	s := benchService(coupon.NewMemoryIndex([]string{"HAPPYHRS", "FIFTYOFF", "SIXTYOFF", "BIRTHDAY"}))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := s.Check("HAPPYHRS")
			if err != nil {
				return
			}
			_, err = s.Check("NOTACODE1")
			if err != nil {
				return
			}
		}
	})
}

func BenchmarkCheckRedis(b *testing.B) {
	r := coupon.NewRedisIndex("localhost:6379", "", "coupons:valid")
	if ok, err := r.Exists(); err != nil || !ok {
		b.Skip("redis with index not reachable on localhost:6379")
	}
	s := benchService(r)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := s.Check("HAPPYHRS")
			if err != nil {
				return
			}
		}
	})
}

// genSource streams n deterministic 8-char codes starting at id `from` without
// holding them in memory, so the benchmark measures the indexer, not the input.
func genSource(from, n int) coupon.Source {
	return func() (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		go func() {
			w := bufio.NewWriterSize(pw, 64<<10)
			buf := make([]byte, 0, 16)
			for i := from; i < from+n; i++ {
				buf = strconv.AppendInt(buf[:0], int64(i)+36*36*36*36*36*36*36, 36) // 8 chars
				buf = append(buf, '\n')
				_, _ = w.Write(buf)
			}
			_ = pw.CloseWithError(w.Flush())
		}()
		return pr, nil
	}
}

// peakHeap samples heap-in-use until stop is closed and returns the maximum.
func peakHeap(stop <-chan struct{}, out chan<- uint64) {
	var peak uint64
	var m runtime.MemStats
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			out <- peak
			return
		case <-t.C:
			runtime.ReadMemStats(&m)
			peak = max(peak, m.HeapInuse)
		}
	}
}

// BenchmarkBuildIndex measures the offline build: time, throughput and peak
// heap for 3 sources of N codes each, where consecutive sources overlap by half
// (so N codes are valid). Run with:
//
//	go test ./test/coupon -run xxx -bench BuildIndex -benchtime 1x
//
// BENCH_CODES overrides N per source (default 1,000,000).
func BenchmarkBuildIndex(b *testing.B) {
	n := 1_000_000
	if v, err := strconv.Atoi(os.Getenv("BENCH_CODES")); err == nil && v > 0 {
		n = v
	}
	for _, partitions := range []int{16, 64} {
		b.Run(fmt.Sprintf("N=%d/partitions=%d", n, partitions), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				srcs := []coupon.Source{genSource(0, n), genSource(n/2, n), genSource(n, n)}
				runtime.GC()
				stop, peak := make(chan struct{}), make(chan uint64, 1)
				go peakHeap(stop, peak)
				start := time.Now()
				codes, err := coupon.BuildIndex(srcs, b.TempDir(), partitions, 4)
				el := time.Since(start)
				close(stop)
				if err != nil {
					b.Fatal(err)
				}
				// sources cover [0,n) [n/2,1.5n) [n,2n): ids [n/2,1.5n) are in two of them
				if want := n; len(codes) != want {
					b.Fatalf("got %d valid codes, want %d", len(codes), want)
				}
				b.ReportMetric(float64(<-peak)/(1<<20), "peak-heap-MB")
				b.ReportMetric(float64(3*n)/el.Seconds()/1e6, "Mcodes/s")
			}
		})
	}
}
