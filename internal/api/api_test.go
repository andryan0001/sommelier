package api

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

func TestBucketBurstIsImmediate(t *testing.T) {
	b := newBucket(60, 3)
	start := time.Now()
	for i := 0; i < 3; i++ {
		b.take()
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("burst took %s", elapsed)
	}
}

func TestBucketThrottlesPastCapacity(t *testing.T) {
	// 1200/min = 20/s, so a second token on a capacity-1 bucket costs ~50ms.
	b := newBucket(1200, 1)
	b.take()
	waited := b.take()
	if waited < 20*time.Millisecond {
		t.Errorf("expected a wait, got %s", waited)
	}
}

func TestBucketNonPositiveRateStillWorks(t *testing.T) {
	b := newBucket(0, 1)
	done := make(chan struct{})
	go func() {
		b.take()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("bucket with a zero rate deadlocked")
	}
}

func TestCacheKeyIsStable(t *testing.T) {
	first := cacheKey("/search", url.Values{"query": {"a b"}, "page": {"1"}})
	second := cacheKey("/search", url.Values{"query": {"a b"}, "page": {"1"}})
	if first != second {
		t.Errorf("keys differ: %q vs %q", first, second)
	}
	if first == cacheKey("/search", nil) {
		t.Error("params should change the key")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	c := newDiskCache(t.TempDir())
	key := "/search?x=1"
	c.store("search", key, []byte(`{"total":1}`))

	var raw json.RawMessage
	if !c.load("search", key, time.Hour, &raw) {
		t.Fatal("expected a cache hit")
	}
	if string(raw) != `{"total":1}` {
		t.Errorf("payload = %s", raw)
	}
	if c.hitCount() != 1 {
		t.Errorf("hit count = %d", c.hitCount())
	}
}

func TestCacheExpiry(t *testing.T) {
	c := newDiskCache(t.TempDir())
	c.store("search", "/x", []byte(`{}`))

	var raw json.RawMessage
	if c.load("search", "/x", -time.Second, &raw) {
		t.Error("expired entry should not load")
	}
}

func TestCacheMissingEntry(t *testing.T) {
	c := newDiskCache(t.TempDir())
	var raw json.RawMessage
	if c.load("search", "/absent", time.Hour, &raw) {
		t.Error("missing entry should miss")
	}
}

func TestCacheDisabledWhenDirEmpty(t *testing.T) {
	c := newDiskCache("")
	c.store("search", "/x", []byte(`{}`))
	var raw json.RawMessage
	if c.load("search", "/x", time.Hour, &raw) {
		t.Error("a cache with no directory must never hit")
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	c := New(Options{})
	if c.base != BaseURL {
		t.Errorf("base = %q", c.base)
	}
	if c.safety != 0.9 {
		t.Errorf("safety = %v", c.safety)
	}
	if c.retries != 4 {
		t.Errorf("retries = %d", c.retries)
	}
}

func TestNewRejectsUnsafeSafetyFactor(t *testing.T) {
	for _, safety := range []float64{-1, 0, 1.5} {
		if c := New(Options{Safety: safety}); c.safety != 0.9 {
			t.Errorf("safety %v accepted as %v", safety, c.safety)
		}
	}
}

func TestBucketUsesKeyedLimits(t *testing.T) {
	anon := New(Options{DisableCache: true})
	keyed := New(Options{APIKey: "k", DisableCache: true})
	if anon.bucketFor("search").rate >= keyed.bucketFor("search").rate {
		t.Error("a key should raise the search rate")
	}
}

func TestBucketForUnknownKindUsesDefault(t *testing.T) {
	c := New(Options{DisableCache: true})
	if c.bucketFor("mystery") == nil {
		t.Fatal("expected a bucket")
	}
	if c.ttl("mystery") != ttls["default"] {
		t.Errorf("ttl = %s", c.ttl("mystery"))
	}
}

func TestStatsCountsCacheHits(t *testing.T) {
	c := New(Options{CacheDir: t.TempDir()})
	c.cache.store("search", "/x", []byte(`{}`))
	var raw json.RawMessage
	c.cache.load("search", "/x", time.Hour, &raw)
	if got := c.Stats().CacheHits; got != 1 {
		t.Errorf("cache hits = %d", got)
	}
}

func TestSnippetTruncates(t *testing.T) {
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	if got := snippet(long); len(got) > 210 {
		t.Errorf("snippet not truncated: %d chars", len(got))
	}
}
