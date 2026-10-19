package api

import (
	"sync"
	"time"
)

// bucket is a token bucket. nhentai publishes its per-minute ceilings in prose
// and returns no X-RateLimit-* headers, so the client can only throttle itself
// and treat 429 as the sole feedback signal.
type bucket struct {
	mu       sync.Mutex
	rate     float64 // tokens per second
	capacity float64
	tokens   float64
	last     time.Time
	waited   time.Duration
	consumed int
}

func newBucket(perMinute, capacity float64) *bucket {
	if perMinute <= 0 {
		perMinute = 1
	}
	if capacity < 1 {
		capacity = 1
	}
	return &bucket{
		rate:     perMinute / 60,
		capacity: capacity,
		tokens:   capacity,
		last:     time.Now(),
	}
}

// take blocks until a token is available and reports how long it waited.
func (b *bucket) take() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	var waited time.Duration
	for {
		now := time.Now()
		if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
			b.tokens += elapsed * b.rate
			if b.tokens > b.capacity {
				b.tokens = b.capacity
			}
			b.last = now
		}
		if b.tokens >= 1 {
			b.tokens--
			b.consumed++
			b.waited += waited
			return waited
		}
		need := (1 - b.tokens) / b.rate
		d := time.Duration(need * float64(time.Second))
		if d < time.Millisecond {
			d = time.Millisecond
		}
		time.Sleep(d)
		waited += d
	}
}
