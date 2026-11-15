package coupon

import (
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// Config selects where the index lives and how it is built.
type Config struct {
	Store         string        // "file" (default) or "redis"
	IndexPath     string        // file store: path or http(s) URL of the index file
	RedisAddr     string        // Redis store
	RedisPassword string        // Redis store
	RedisKey      string        // Redis store: SET key
	Sources       []string      // coupon source files (paths / URLs, .gz ok)
	TmpDir        string        // spill space for the offline build
	Partitions    int           // build partitions (more = less RAM)
	Workers       int           // partitions reduced in parallel (more = faster, more RAM)
	Refresh       time.Duration // how often instances reload the index (0 = never)
	AutoBuild     bool          // API builds the index itself if none exists (local dev); disable in prod
}

// RetryDelay is the wait between attempts to find/build the index.
var RetryDelay = 15 * time.Second

// Build runs the offline job: build the exact valid set from Sources and publish it.
func Build(cfg Config) (int, error) {
	if len(cfg.Sources) < MinFiles {
		return 0, fmt.Errorf("need at least %d coupon sources, got %d", MinFiles, len(cfg.Sources))
	}
	sources := make([]Source, len(cfg.Sources))
	for i, loc := range cfg.Sources {
		sources[i] = FileSource(loc)
	}
	start := time.Now()
	codes, err := BuildIndex(sources, cfg.TmpDir, cfg.Partitions, cfg.Workers)
	if err != nil {
		return 0, err
	}
	log.Printf("coupon indexer: %d valid codes built in %s", len(codes), time.Since(start).Round(time.Second))
	return len(codes), publish(cfg, codes)
}

func publish(cfg Config, codes []string) error {
	switch cfg.Store {
	case "redis":
		r := NewRedisIndex(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisKey)
		defer func(r *RedisIndex) {
			err := r.Close()
			if err != nil {

			}
		}(r)
		return r.Save(codes)
	case "", "file":
		return SaveIndexFile(cfg.IndexPath, codes)
	}
	return fmt.Errorf("unknown coupon store %q", cfg.Store)
}

// Open returns the published index, or ErrNotReady if there is none yet.
//
// The result is an in-memory snapshot whenever possible, so the request path
// is a map lookup with no network I/O and keeps working if Redis goes away. Only
// a Redis set larger than MaxSnapshot is queried live.
func Open(cfg Config) (Index, error) {
	switch cfg.Store {
	case "redis":
		r := NewRedisIndex(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisKey)
		snap, ok, err := r.Snapshot(MaxSnapshot)
		if err != nil || !ok {
			if err != nil {
				r.Close()
				return nil, err
			}
			log.Printf("coupon set larger than %d: querying redis directly", MaxSnapshot)
			return r, nil // too big to copy: keep the client for live lookups
		}
		r.Close()
		return snap, nil
	case "", "file":
		return LoadIndexFile(cfg.IndexPath)
	}
	return nil, fmt.Errorf("unknown coupon store %q", cfg.Store)
}

// Service implements Checker on top of an Index. It starts serving immediately,
// picks the index up in the background, then keeps refreshing it so a re-run of
// the indexer is picked up without restarting. A failed refresh keeps serving
// the last good index.
type Service struct {
	idx atomic.Pointer[Index]
}

// NewService starts the background loader/refresher.
func NewService(cfg Config) *Service {
	s := &Service{}
	go s.run(cfg)
	return s
}

func (s *Service) run(cfg Config) {
	for { // initial load (optionally building it)
		idx, err := Open(cfg)
		if err == nil {
			s.idx.Store(&idx)
			log.Printf("coupon index ready (store=%s)", orDefault(cfg.Store))
			break
		}
		log.Printf("coupon index not available yet: %v", err)
		if cfg.AutoBuild && len(cfg.Sources) >= MinFiles {
			log.Printf("coupon index missing: building from %d sources (takes minutes; prefer `make index` offline)", len(cfg.Sources))
			if _, err := Build(cfg); err != nil {
				log.Printf("coupon build failed (retrying in %s): %v", RetryDelay, err)
			} else {
				continue // re-open immediately
			}
		}
		time.Sleep(RetryDelay)
	}
	if cfg.Refresh <= 0 {
		return
	}
	for { // periodic refresh; keep last good index on failure
		time.Sleep(cfg.Refresh)
		if _, live := (*s.idx.Load()).(*RedisIndex); live {
			continue // live Redis lookups are always current
		}
		idx, err := Open(cfg)
		if err != nil {
			log.Printf("coupon index refresh failed, keeping previous: %v", err)
			continue
		}
		s.idx.Store(&idx)
	}
}

func orDefault(s string) string {
	if s == "" {
		return "file"
	}
	return s
}

// Ready reports whether the index is available.
func (s *Service) Ready() bool { return s.idx.Load() != nil }

// Check implements Checker.
func (s *Service) Check(code string) (bool, error) {
	if !LengthOK(code) {
		return false, nil
	}
	idx := s.idx.Load()
	if idx == nil {
		return false, ErrNotReady
	}
	return (*idx).Contains(code)
}
