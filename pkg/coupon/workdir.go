package coupon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Tunables (exported vars so tests and operators can adjust them).
var (
	// ClaimLease: a claim whose heartbeat is older than this is considered
	// abandoned (its owner crashed) and may be taken over.
	ClaimLease = 2 * time.Minute
	// ClaimPoll: how long a cooperating process waits before re-checking
	// work that other processes are doing.
	ClaimPoll = 2 * time.Second
)

func randSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Claim takes an exclusive, leased claim on a unit of work shared through the
// work directory (works across processes and, on a shared volume, machines).
// While held, a heartbeat keeps the claim fresh; if the owner dies the claim
// goes stale after ClaimLease and another process takes it over. Work units are
// idempotent (outputs are committed with atomic renames), so the rare double
// execution caused by a takeover race is harmless.
func Claim(dir, name string) (release func(), ok bool) {
	path := filepath.Join(dir, name+".claim")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			stop := make(chan struct{})
			var once sync.Once
			interval := ClaimLease / 3
			go func() {
				t := time.NewTicker(interval)
				defer t.Stop()
				for {
					select {
					case <-stop:
						return
					case now := <-t.C:
						_ = os.Chtimes(path, now, now)
					}
				}
			}()
			return func() {
				once.Do(func() {
					close(stop)
					_ = os.Remove(path)
				})
			}, true
		}
		if !os.IsExist(err) {
			return nil, false
		}
		st, err := os.Stat(path)
		if err != nil {
			continue // vanished: retry
		}
		if time.Since(st.ModTime()) < ClaimLease {
			return nil, false // held by a live owner
		}
		// Stale: move it aside (atomic, so only one taker wins), then retry.
		aside := path + ".stale-" + randSuffix()
		if os.Rename(path, aside) != nil {
			return nil, false
		}
		_ = os.Remove(aside)
	}
	return nil, false
}

// runPhase executes do(i) for every i in [0,n) that is not done(i), at most
// conc at a time. When coop is set, each item is first claimed so several
// processes can share the phase; the call returns only once every item is done
// (by anyone), waiting for - and taking over from - other processes as needed.
func runPhase(n, conc int, coop bool, dir, kind string, done func(i int) bool, do func(i int) error) error {
	if conc < 1 {
		conc = 1
	}
	for {
		var pending []int
		for i := 0; i < n; i++ {
			if !done(i) {
				pending = append(pending, i)
			}
		}
		if len(pending) == 0 {
			return nil
		}

		var (
			mu      sync.Mutex
			first   error
			skipped int
			wg      sync.WaitGroup
			sem     = make(chan struct{}, conc)
		)
		for _, i := range pending {
			mu.Lock()
			failed := first != nil
			mu.Unlock()
			if failed {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(i int) {
				defer func() { <-sem; wg.Done() }()
				release := func() {}
				if coop {
					r, ok := Claim(dir, fmt.Sprintf("%s%05d", kind, i))
					if !ok {
						mu.Lock()
						skipped++
						mu.Unlock()
						return
					}
					release = r
				}
				defer release()
				if coop && done(i) { // finished by someone else while we queued
					return
				}
				if err := do(i); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		if first != nil {
			return first
		}
		if !coop {
			return nil
		}
		if skipped > 0 {
			time.Sleep(ClaimPoll) // others are working; wait, then re-check / take over stale claims
		}
	}
}
