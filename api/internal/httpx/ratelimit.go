package httpx

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type LimitResult struct {
	Allowed      bool
	Remaining    int
	ResetSeconds int
}

// Limiter enforces a number of requests per window for a key.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (LimitResult, error)
}

// MemoryLimiter is a fixed-window limiter for single-instance installs.
type MemoryLimiter struct {
	mu      sync.Mutex
	windows map[string]*memWindow
	now     func() time.Time
}

type memWindow struct {
	start time.Time
	count int
}

func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{windows: map[string]*memWindow{}, now: time.Now}
}

func (m *MemoryLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (LimitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	w := m.windows[key]
	if w == nil || now.Sub(w.start) >= window {
		w = &memWindow{start: now.Truncate(window)}
		m.windows[key] = w
		if len(m.windows) > 100_000 { // drop stale windows
			for k, v := range m.windows {
				if now.Sub(v.start) >= window {
					delete(m.windows, k)
				}
			}
		}
	}
	w.count++
	reset := int(w.start.Add(window).Sub(now).Seconds()) + 1
	return LimitResult{Allowed: w.count <= limit, Remaining: max(0, limit-w.count), ResetSeconds: reset}, nil
}

// RedisLimiter shares limits across several API instances.
type RedisLimiter struct{ client *redis.Client }

func NewRedisLimiter(client *redis.Client) *RedisLimiter { return &RedisLimiter{client: client} }

func (r *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (LimitResult, error) {
	now := time.Now()
	start := now.Truncate(window)
	rk := "purros:rl:" + key + ":" + start.Format("200601021504")
	pipe := r.client.TxPipeline()
	incr := pipe.Incr(ctx, rk)
	pipe.Expire(ctx, rk, window+time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return LimitResult{}, err
	}
	n := int(incr.Val())
	reset := int(start.Add(window).Sub(now).Seconds()) + 1
	return LimitResult{Allowed: n <= limit, Remaining: max(0, limit-n), ResetSeconds: reset}, nil
}
