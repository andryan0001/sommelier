package index

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// TagDict is the id -> name mapping for the taxonomy. It is cached separately
// from the catalog because tag ids never change and a rebuild should not pay to
// resolve the same id twice. Over a full crawl this turns several thousand
// /tags/ids calls into a few dozen.
type TagDict struct {
	Path string
	mu   sync.RWMutex
	byID map[int]TagRef
}

// TagRef is one cached taxonomy entry.
type TagRef struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

// OpenTags loads the dictionary, or returns an empty one if absent.
func OpenTags(path string) (*TagDict, error) {
	d := &TagDict{Path: path, byID: map[int]TagRef{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return d, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var t TagRef
		if err := json.Unmarshal(sc.Bytes(), &t); err != nil || t.ID <= 0 {
			continue
		}
		d.byID[t.ID] = t
	}
	return d, sc.Err()
}

// Len reports how many tags are cached.
func (d *TagDict) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.byID)
}

// Get looks up one tag.
func (d *TagDict) Get(id int) (TagRef, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	t, ok := d.byID[id]
	return t, ok
}

// Missing returns the ids not yet cached, deduplicated and order-stable.
func (d *TagDict) Missing(ids []int) []int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	seen := make(map[int]bool, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := d.byID[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// PutMany records new tags and appends them to the dictionary file.
func (d *TagDict) PutMany(refs []TagRef) error {
	if len(refs) == 0 {
		return nil
	}
	d.mu.Lock()
	fresh := make([]TagRef, 0, len(refs))
	for _, r := range refs {
		if r.ID <= 0 || r.Name == "" {
			continue
		}
		if _, ok := d.byID[r.ID]; ok {
			continue
		}
		d.byID[r.ID] = r
		fresh = append(fresh, r)
	}
	d.mu.Unlock()

	if len(fresh) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(d.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, r := range fresh {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return w.Flush()
}

// Save rewrites the dictionary sorted by id, removing duplicates left by
// repeated builds.
func (d *TagDict) Save() error {
	d.mu.RLock()
	ids := make([]int, 0, len(d.byID))
	for id := range d.byID {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	refs := make([]TagRef, len(ids))
	for i, id := range ids {
		refs[i] = d.byID[id]
	}
	d.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(d.Path), 0o755); err != nil {
		return err
	}
	tmp := d.Path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	enc := json.NewEncoder(w)
	for _, r := range refs {
		if err := enc.Encode(r); err != nil {
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
	return os.Rename(tmp, d.Path)
}

// Append writes entries to the end of the catalog file without rewriting it.
// Duplicate ids are tolerated: Open keeps the last line for each id.
func (i *Index) Append(entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(i.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(i.Path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	// A crash can leave a partial final line. Start a fresh line so the new
	// entry is not glued onto unparseable bytes.
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], info.Size()-1); err == nil && last[0] != '\n' {
			if _, err := f.Write([]byte("\n")); err != nil {
				return fmt.Errorf("append newline: %w", err)
			}
		}
	}

	w := bufio.NewWriterSize(f, 1<<20)
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("append entry %d: %w", e.ID, err)
		}
	}
	return w.Flush()
}
