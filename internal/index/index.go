// Package index stores a local catalog of galleries so repeat queries do not
// pay the API's retrieval cost.
//
// The on-disk format is JSONL, one gallery per line. That keeps the file
// appendable, greppable and recoverable: a truncated final line is skipped on
// load instead of losing the whole index. It also keeps the binary free of any
// SQLite dependency, so `go build` works on a bare toolchain.
package index

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one indexed gallery, already carrying resolved tag names.
type Entry struct {
	ID            int      `json:"id"`
	Title         string   `json:"title"`
	JapaneseTitle string   `json:"japanese_title,omitempty"`
	Pages         int      `json:"pages"`
	Favorites     int      `json:"favorites"`
	TagIDs        []int    `json:"tag_ids,omitempty"`
	Tags          []string `json:"tags"`
	Language      string   `json:"language,omitempty"`
	Scanlator     string   `json:"scanlator,omitempty"`
	UploadDate    int64    `json:"upload_date,omitempty"`
}

// Index is the in-memory catalog plus its backing file.
type Index struct {
	Path    string
	Entries map[int]Entry

	// TagCounts is built on load and used for vocabulary grounding and for
	// weighting rare tags above ubiquitous ones.
	TagCounts map[string]int
}

// Open loads an index from disk. A missing file yields an empty index.
func Open(path string) (*Index, error) {
	idx := &Index{Path: path, Entries: map[int]Entry{}, TagCounts: map[string]int{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return idx, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // truncated or hand-edited line: skip, never abort the load
		}
		if e.ID <= 0 {
			continue
		}
		idx.Add(e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return idx, nil
}

// Len reports how many galleries are indexed.
func (i *Index) Len() int { return len(i.Entries) }

// Add inserts or replaces an entry, keeping TagCounts in sync.
func (i *Index) Add(e Entry) {
	if old, ok := i.Entries[e.ID]; ok {
		for _, tag := range old.Tags {
			i.TagCounts[tag]--
			if i.TagCounts[tag] <= 0 {
				delete(i.TagCounts, tag)
			}
		}
	}
	i.Entries[e.ID] = e
	for _, tag := range e.Tags {
		i.TagCounts[tag]++
	}
}

// Vocabulary returns the most common tags, most frequent first.
func (i *Index) Vocabulary(limit int) []string {
	type kv struct {
		tag string
		n   int
	}
	all := make([]kv, 0, len(i.TagCounts))
	for tag, n := range i.TagCounts {
		all = append(all, kv{tag, n})
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].n != all[b].n {
			return all[a].n > all[b].n
		}
		return all[a].tag < all[b].tag
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	out := make([]string, len(all))
	for n, item := range all {
		out[n] = item.tag
	}
	return out
}

// Save writes the whole index atomically: a crash mid-write leaves the previous
// file intact rather than a half-written one.
func (i *Index) Save() error {
	if err := os.MkdirAll(filepath.Dir(i.Path), 0o755); err != nil {
		return err
	}
	tmp := i.Path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)

	ids := make([]int, 0, len(i.Entries))
	for id := range i.Entries {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	enc := json.NewEncoder(w)
	for _, id := range ids {
		if err := enc.Encode(i.Entries[id]); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, i.Path)
}

// Cursor records how far a build got, so `sommelier index` can resume instead
// of re-walking pages it already paid for.
type Cursor struct {
	Page    int    `json:"page"`
	Entries int    `json:"entries"`
	Scope   string `json:"scope,omitempty"`
}

func cursorPath(indexPath string) string { return indexPath + ".cursor" }

// LoadCursor returns the saved cursor, or a zero cursor if none exists.
func LoadCursor(indexPath string) Cursor {
	var c Cursor
	data, err := os.ReadFile(cursorPath(indexPath))
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c)
	return c
}

// SaveCursor persists build progress.
func SaveCursor(indexPath string, c Cursor) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cursorPath(indexPath), data, 0o644)
}

// ClearCursor removes build progress, forcing the next build to start at page 1.
func ClearCursor(indexPath string) error {
	err := os.Remove(cursorPath(indexPath))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear cursor: %w", err)
	}
	return nil
}
