// Command loadtest is a tiny dependency-free HTTP load generator: N workers
// POST /api/order for a fixed duration and it prints throughput and latency
// percentiles. Used for the numbers in the README.
//
//	go run ./cmd/loadtest -url http://localhost:8080 -c 64 -d 10s -coupon HAPPYHRS
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"
)

func main() {
	base := flag.String("url", "http://localhost:8080", "API base URL")
	key := flag.String("key", "apitest", "api_key header")
	coupon := flag.String("coupon", "HAPPYHRS", "coupon code (empty = none)")
	conc := flag.Int("c", 64, "concurrent workers")
	dur := flag.Duration("d", 10*time.Second, "test duration")
	flag.Parse()

	body := `{"items":[{"productId":"1","quantity":2}]}`
	if *coupon != "" {
		body = fmt.Sprintf(`{"couponCode":%q,"items":[{"productId":"1","quantity":2}]}`, *coupon)
	}
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *conc}}

	var (
		mu     sync.Mutex
		all    []time.Duration
		status = map[int]int{}
		wg     sync.WaitGroup
	)
	deadline := time.Now().Add(*dur)
	start := time.Now()
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lat []time.Duration
			codes := map[int]int{}
			for time.Now().Before(deadline) {
				req, _ := http.NewRequest(http.MethodPost, *base+"/api/order", bytes.NewReader([]byte(body)))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("api_key", *key)
				t := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					codes[0]++
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				lat = append(lat, time.Since(t))
				codes[resp.StatusCode]++
			}
			mu.Lock()
			all = append(all, lat...)
			for c, n := range codes {
				status[c] += n
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(all) == 0 {
		fmt.Fprintln(os.Stderr, "no successful requests (is the API up?)")
		os.Exit(1)
	}
	slices.Sort(all)
	p := func(q float64) time.Duration { return all[int(float64(len(all)-1)*q)] }
	fmt.Printf("requests=%d  duration=%s  rps=%.0f  workers=%d\n", len(all), time.Since(start).Round(time.Millisecond), float64(len(all))/time.Since(start).Seconds(), *conc)
	fmt.Printf("latency  p50=%s  p90=%s  p99=%s  max=%s\n", p(.5), p(.9), p(.99), all[len(all)-1])
	fmt.Printf("status   %v\n", status)
}
