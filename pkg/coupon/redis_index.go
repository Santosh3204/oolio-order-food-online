package coupon

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisIndex keeps the valid codes in a shared Redis SET so every API replica
// sees the same index and no replica holds it in its own memory.
type RedisIndex struct {
	client *redis.Client
	key    string
}

// NewRedisIndex connects (lazily) to addr and uses `key` for the set.
func NewRedisIndex(addr, password, key string) *RedisIndex {
	return &RedisIndex{client: redis.NewClient(&redis.Options{Addr: addr, Password: password}), key: key}
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Second)
}

// Contains implements Index (O(1) SISMEMBER).
func (r *RedisIndex) Contains(code string) (bool, error) {
	c, cancel := ctx()
	defer cancel()
	return r.client.SIsMember(c, r.key, code).Result()
}

// Exists reports whether an index has been published.
func (r *RedisIndex) Exists() (bool, error) {
	c, cancel := ctx()
	defer cancel()
	n, err := r.client.Exists(c, r.key).Result()
	return n > 0, err
}

// Save publishes codes with an atomic swap: build into a temp key, then RENAME,
// so concurrent readers never see a partial index.
func (r *RedisIndex) Save(codes []string) error {
	if len(codes) == 0 {
		return fmt.Errorf("refusing to publish an empty index")
	}
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tmp := r.key + ":building"
	pipe := r.client.Pipeline()
	pipe.Del(c, tmp)
	for i := 0; i < len(codes); i += 1000 {
		end := min(i+1000, len(codes))
		args := make([]any, 0, end-i)
		for _, code := range codes[i:end] {
			args = append(args, code)
		}
		pipe.SAdd(c, tmp, args...)
	}
	pipe.Rename(c, tmp, r.key)
	_, err := pipe.Exec(c)
	return err
}

// Close releases the connection.
func (r *RedisIndex) Close() error { return r.client.Close() }
