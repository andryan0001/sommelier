// Package api is a read-only client for nhentai API v2.
//
// Every read endpoint answers unauthenticated; a key only raises the per-minute
// ceilings (search 10->20, gallery detail 20->45). No X-RateLimit-* or
// Retry-After headers are published, so the client throttles itself with a
// token bucket per endpoint class and treats 429 as the only feedback signal.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BaseURL is the documented API root.
const BaseURL = "https://nhentai.net/api/v2"

// limit is the published per-minute ceiling for an endpoint class.
type limit struct {
	Anonymous float64
	Keyed     float64
}

// Measured against the live docs: search is the tightest endpoint, gallery
// detail is the one an agent hammers most.
var limits = map[string]limit{
	"search":    {10, 20},
	"galleries": {15, 30},
	"gallery":   {20, 45},
	"tags":      {15, 30},
	"default":   {15, 30},
}

// Small bursts keep the traffic shape flat instead of scraper-like.
var bursts = map[string]float64{
	"search": 2, "galleries": 2, "gallery": 3, "tags": 2, "default": 2,
}

// Gallery metadata is effectively immutable; searches are not.
var ttls = map[string]time.Duration{
	"gallery":   7 * 24 * time.Hour,
	"search":    time.Hour,
	"galleries": 10 * time.Minute,
	"tags":      30 * 24 * time.Hour,
	"default":   5 * time.Minute,
}

// Error is a non-retryable API failure.
type Error struct {
	Status int
	Path   string
	Body   string
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("%s: %s", e.Path, e.Body)
	}
	return fmt.Sprintf("HTTP %d on %s: %s", e.Status, e.Path, e.Body)
}

// Options configures a Client. Zero values pick sensible defaults.
type Options struct {
	BaseURL      string
	UserAgent    string
	APIKey       string
	CacheDir     string
	Safety       float64
	Timeout      time.Duration
	Retries      int
	DisableCache bool
}

// Stats reports what a run actually cost.
type Stats struct {
	Calls      int           `json:"calls"`
	CacheHits  int           `json:"cache_hits"`
	Throttle   time.Duration `json:"-"`
	ThrottleMS int64         `json:"throttle_ms"`
	Keyed      bool          `json:"keyed"`
}

// Client is safe for concurrent use.
type Client struct {
	base    string
	http    *http.Client
	ua      string
	key     string
	cache   *diskCache
	safety  float64
	retries int

	mu      sync.Mutex
	buckets map[string]*bucket
	calls   int
	waited  time.Duration
}

// New builds a client. It never fails: bad options degrade to defaults.
func New(opts Options) *Client {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = BaseURL
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = "sommelier/0.1.0 (+https://github.com/andryan0001/sommelier)"
	}
	safety := opts.Safety
	if safety <= 0 || safety > 1 {
		safety = 0.9
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	retries := opts.Retries
	if retries <= 0 {
		retries = 4
	}
	c := &Client{
		base:    base,
		ua:      ua,
		key:     opts.APIKey,
		safety:  safety,
		retries: retries,
		buckets: map[string]*bucket{},
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          16,
				MaxIdleConnsPerHost:   8,
				IdleConnTimeout:       60 * time.Second,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
	}
	if !opts.DisableCache {
		c.cache = newDiskCache(opts.CacheDir)
	}
	return c
}

func (c *Client) bucketFor(kind string) *bucket {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.buckets[kind]; ok {
		return b
	}
	l, ok := limits[kind]
	if !ok {
		l = limits["default"]
	}
	perMin := l.Anonymous
	if c.key != "" {
		perMin = l.Keyed
	}
	b := newBucket(perMin*c.safety, bursts[kind])
	c.buckets[kind] = b
	return b
}

func (c *Client) ttl(kind string) time.Duration {
	if t, ok := ttls[kind]; ok {
		return t
	}
	return ttls["default"]
}

// do performs one GET, honouring cache, bucket and retry policy.
func (c *Client) do(kind, path string, params url.Values, out any) error {
	key := cacheKey(path, params)
	if c.cache != nil {
		var raw json.RawMessage
		if c.cache.load(kind, key, c.ttl(kind), &raw) {
			if err := json.Unmarshal(raw, out); err == nil {
				return nil
			}
		}
	}

	var last error
	delay := time.Second
	for attempt := 0; attempt < c.retries; attempt++ {
		waited := c.bucketFor(kind).take()
		c.mu.Lock()
		c.calls++
		c.waited += waited
		c.mu.Unlock()

		body, status, header, err := c.fetch(path, params)
		if err != nil {
			last = err
		} else if status == http.StatusOK {
			if err := json.Unmarshal(body, out); err != nil {
				return &Error{Status: status, Path: path, Body: "malformed JSON: " + err.Error()}
			}
			// Cache only after a successful decode: a malformed body must never
			// be served from disk on the next run.
			if c.cache != nil {
				c.cache.store(kind, key, body)
			}
			return nil
		} else if status == http.StatusTooManyRequests || status >= 500 {
			last = &Error{Status: status, Path: path, Body: snippet(body)}
			if after := header.Get("Retry-After"); after != "" {
				if secs, perr := strconv.ParseFloat(after, 64); perr == nil && secs >= 0 && secs < 300 {
					time.Sleep(time.Duration(secs * float64(time.Second)))
					if d := time.Duration(secs*2*float64(time.Second)); d > delay {
						delay = d
					}
					continue
				}
			}
		} else {
			return &Error{Status: status, Path: path, Body: snippet(body)}
		}

		if attempt < c.retries-1 {
			// Full jitter: spread retries instead of synchronising them.
			sleep := delay/2 + time.Duration(rand.Int63n(int64(delay/2)+1))
			time.Sleep(sleep)
			delay *= 2
		}
	}
	if last == nil {
		last = errors.New("unknown failure")
	}
	return fmt.Errorf("gave up on %s after %d attempts: %w", path, c.retries, last)
}

func (c *Client) fetch(path string, params url.Values) ([]byte, int, http.Header, error) {
	u := c.base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.http.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Key "+c.key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, err
	}
	return body, resp.StatusCode, resp.Header, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// ---- endpoints ------------------------------------------------------------

// Search queries /search. The endpoint has no per_page parameter: it always
// returns 25 results per page.
func (c *Client) Search(query, sort string, page int) (*GalleryList, error) {
	if sort == "" {
		sort = "popular"
	}
	if page < 1 {
		page = 1
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("sort", sort)
	params.Set("page", strconv.Itoa(page))
	var out GalleryList
	if err := c.do("search", "/search", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Galleries lists /galleries newest-first. per_page is capped at 100 upstream.
func (c *Client) Galleries(page, perPage int) (*GalleryList, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 100
	}
	if perPage > 100 {
		perPage = 100
	}
	params := url.Values{}
	params.Set("page", strconv.Itoa(page))
	params.Set("per_page", strconv.Itoa(perPage))
	var out GalleryList
	if err := c.do("galleries", "/galleries", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Gallery fetches full metadata. include may be empty.
func (c *Client) Gallery(id int, include string) (*Gallery, error) {
	params := url.Values{}
	if include != "" {
		params.Set("include", include)
	}
	var out Gallery
	if err := c.do("gallery", "/galleries/"+strconv.Itoa(id), params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TagsByIDs batch-resolves tag ids, 100 per request.
func (c *Client) TagsByIDs(ids []int) ([]Tag, error) {
	unique := make([]int, 0, len(ids))
	seen := map[int]bool{}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	var out []Tag
	for start := 0; start < len(unique); start += 100 {
		end := start + 100
		if end > len(unique) {
			end = len(unique)
		}
		chunk := unique[start:end]
		parts := make([]string, len(chunk))
		for i, id := range chunk {
			parts[i] = strconv.Itoa(id)
		}
		params := url.Values{}
		params.Set("ids", strings.Join(parts, ","))
		var batch []Tag
		if err := c.do("tags", "/tags/ids", params, &batch); err != nil {
			return out, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

// TagsByType lists a taxonomy type, popular or alphabetical. It walks pages
// until the reported total is reached or maxPages is hit.
func (c *Client) TagsByType(tagType, sort string, perPage, maxPages int) ([]Tag, error) {
	if sort == "" {
		sort = "popular"
	}
	if perPage < 1 || perPage > 100 {
		perPage = 100
	}
	var out []Tag
	for page := 1; ; page++ {
		if maxPages > 0 && page > maxPages {
			break
		}
		params := url.Values{}
		params.Set("sort", sort)
		params.Set("per_page", strconv.Itoa(perPage))
		params.Set("page", strconv.Itoa(page))
		var env struct {
			Result   []Tag `json:"result"`
			Total    int   `json:"total"`
			NumPages int   `json:"num_pages"`
		}
		if err := c.do("tags", "/tags/"+url.PathEscape(tagType), params, &env); err != nil {
			return out, err
		}
		out = append(out, env.Result...)
		if len(env.Result) == 0 || page >= env.NumPages {
			break
		}
	}
	return out, nil
}

// Stats snapshots usage counters.
func (c *Client) Stats() Stats {
	hits := 0
	if c.cache != nil {
		hits = c.cache.hitCount()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		Calls:      c.calls,
		CacheHits:  hits,
		Throttle:   c.waited,
		ThrottleMS: c.waited.Milliseconds(),
		Keyed:      c.key != "",
	}
}

// Close releases idle connections.
func (c *Client) Close() {
	c.http.CloseIdleConnections()
}
