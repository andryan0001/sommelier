package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// diskCache stores raw API responses so repeated runs never touch the network.
// It is an optimisation only: every failure path degrades to a live request.
type diskCache struct {
	dir string

	mu   sync.Mutex
	hits int
}

func newDiskCache(dir string) *diskCache {
	return &diskCache{dir: dir}
}

func cacheKey(path string, params url.Values) string {
	if len(params) == 0 {
		return path
	}
	return path + "?" + params.Encode()
}

func (d *diskCache) file(kind, key string) string {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:])[:24] + ".json"
	return filepath.Join(d.dir, kind, name)
}

type cacheEnvelope struct {
	FetchedAt int64           `json:"fetched_at"`
	Payload   json.RawMessage `json:"payload"`
}

func (d *diskCache) load(kind, key string, ttl time.Duration, out *json.RawMessage) bool {
	if d == nil || d.dir == "" || ttl <= 0 {
		return false
	}
	blob, err := os.ReadFile(d.file(kind, key))
	if err != nil {
		return false
	}
	var env cacheEnvelope
	if err := json.Unmarshal(blob, &env); err != nil {
		return false
	}
	if time.Since(time.Unix(env.FetchedAt, 0)) > ttl {
		return false
	}
	if len(env.Payload) == 0 {
		return false
	}
	*out = env.Payload
	d.mu.Lock()
	d.hits++
	d.mu.Unlock()
	return true
}

func (d *diskCache) store(kind, key string, payload []byte) {
	if d == nil || d.dir == "" || len(payload) == 0 {
		return
	}
	blob, err := json.Marshal(cacheEnvelope{FetchedAt: time.Now().Unix(), Payload: payload})
	if err != nil {
		return
	}
	path := d.file(kind, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
	}
}

func (d *diskCache) hitCount() int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hits
}
