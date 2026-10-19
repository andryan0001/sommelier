// Package retrieve turns a query into candidates, from the local index and from
// the live API.
//
// Cost model measured against the live API:
//
//	/search returns 25 items carrying only bare tag_ids — no names, no
//	scanlator, no upload date. Resolving those ids costs one batched
//	/tags/ids call per 100 unique ids. Full metadata costs one
//	/galleries/{id} call per candidate and exists nowhere else.
//
// So a cold query is roughly 1 search + tag batches + shortlist details. A warm
// query is zero network calls if the index covers it.
package retrieve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/andryan0001/sommelier/internal/api"
	"github.com/andryan0001/sommelier/internal/index"
	"github.com/andryan0001/sommelier/internal/query"
)

// GalleryURL is the public page for a gallery id.
const GalleryURL = "https://nhentai.net/g/%d/"

// Candidate is one gallery under consideration, with tag names resolved.
type Candidate struct {
	ID            int      `json:"id"`
	Title         string   `json:"title"`
	Tags          []string `json:"tags"`
	TagTypes      map[string][]string `json:"-"`
	Pages         int      `json:"pages"`
	Favorites     int      `json:"favorites"`
	Scanlator     string   `json:"scanlator,omitempty"`
	UploadDate    int64    `json:"upload_date,omitempty"`
	Language      string   `json:"language,omitempty"`

	// Source records where this candidate came from: "index", "live" or both.
	Source string `json:"source"`
}

// URL is the public gallery page.
func (c Candidate) URL() string { return fmt.Sprintf(GalleryURL, c.ID) }

// Dossier is the compact record handed to the model. Every field is something a
// model can reason about; ids and media ids are deliberately excluded.
func (c Candidate) Dossier() map[string]any {
	d := map[string]any{
		"id":        c.ID,
		"title":     c.Title,
		"pages":     c.Pages,
		"favorites": c.Favorites,
		"tags":      c.Tags,
	}
	if len(c.Tags) > 24 {
		d["tags"] = c.Tags[:24]
	}
	if c.Language != "" {
		d["language"] = c.Language
	}
	if c.Scanlator != "" {
		d["scanlator"] = c.Scanlator
	}
	if artist := strings.Join(c.TagTypes["artist"], ", "); artist != "" {
		d["artist"] = artist
	}
	if group := strings.Join(c.TagTypes["group"], ", "); group != "" {
		d["group"] = group
	}
	if parody := strings.Join(c.TagTypes["parody"], ", "); parody != "" {
		d["parody"] = parody
	}
	return d
}

// Result is what a retrieval run produced, plus accounting for the summary line.
type Result struct {
	Query      string
	Candidates []Candidate
	Seen       int
	FromIndex  int
	FromLive   int
	Dropped    map[string]int
}

// Summary renders the one-line corpus report.
func (r Result) Summary() string {
	parts := []string{
		fmt.Sprintf("seen %d", r.Seen),
		fmt.Sprintf("index %d", r.FromIndex),
		fmt.Sprintf("live %d", r.FromLive),
	}
	keys := make([]string, 0, len(r.Dropped))
	for k := range r.Dropped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("dropped %d (%s)", r.Dropped[k], k))
	}
	return strings.Join(parts, ", ")
}

// Options controls one retrieval.
type Options struct {
	Query     string
	Pages     int
	Shortlist int
	Sort      string
	Exclude   []string
	MinPages  int
	MaxPages  int
	Enrich    bool // fetch /galleries/{id} for the shortlist
	Live      bool // query the API in addition to the index
	Index     *index.Index
}

// Run executes the retrieval.
func Run(client *api.Client, opts Options) (*Result, error) {
	result := &Result{Query: opts.Query, Dropped: map[string]int{}}
	shortlist := opts.Shortlist
	if shortlist <= 0 {
		shortlist = 12
	}
	excluded := map[string]bool{}
	for _, e := range opts.Exclude {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			excluded[e] = true
		}
	}

	merged := map[int]Candidate{}
	fromIndex := map[int]bool{}
	fromLive := map[int]bool{}

	// The index is free: check it before spending any API budget.
	if opts.Index != nil && opts.Index.Len() > 0 {
		parsed := query.Parse(opts.Query)
		for _, entry := range opts.Index.Search(parsed, shortlist*4) {
			merged[entry.ID] = Candidate{
				ID:            entry.ID,
				Title:         entry.Title,
				Tags:          entry.Tags,
				Pages:         entry.Pages,
				Favorites:     entry.Favorites,
				Scanlator:     entry.Scanlator,
				UploadDate:    entry.UploadDate,
				Language:      entry.Language,
				Source:        "index",
			}
			fromIndex[entry.ID] = true
		}
	}

	if opts.Live {
		if client == nil {
			if len(merged) == 0 {
				return nil, fmt.Errorf("no live client and nothing in the index matches %q", opts.Query)
			}
		} else if err := collectLive(client, opts, merged, fromLive); err != nil && len(merged) == 0 {
			return nil, err
		}
	} else if len(merged) == 0 {
		return nil, fmt.Errorf("no index matches for %q; run `sommelier index` or drop --offline", opts.Query)
	}

	// Filter, then order by a cheap prior so the expensive calls land on
	// plausible winners.
	staged := make([]Candidate, 0, len(merged))
	for id, cand := range merged {
		lowered := make([]string, len(cand.Tags))
		for i, t := range cand.Tags {
			lowered[i] = strings.ToLower(t)
		}
		hit := false
		for _, t := range lowered {
			if excluded[t] {
				hit = true
				break
			}
		}
		if hit {
			result.Dropped["excluded tag"]++
			continue
		}
		if opts.MinPages > 0 && cand.Pages < opts.MinPages {
			result.Dropped["too short"]++
			continue
		}
		if opts.MaxPages > 0 && cand.Pages > opts.MaxPages {
			result.Dropped["too long"]++
			continue
		}
		switch {
		case fromIndex[id] && fromLive[id]:
			cand.Source = "index+live"
		case fromIndex[id]:
			cand.Source = "index"
		default:
			cand.Source = "live"
		}
		staged = append(staged, cand)
	}

	sort.SliceStable(staged, func(a, b int) bool {
		return prior(staged[a]) > prior(staged[b])
	})

	if len(staged) > shortlist {
		staged = staged[:shortlist]
	}

	if opts.Enrich && client != nil {
		for i := range staged {
			if staged[i].Source == "index" {
				continue // index entries already carry full metadata
			}
			detail, err := client.Gallery(staged[i].ID, "")
			if err != nil {
				continue // a missing detail is not fatal: keep the thin record
			}
			enrich(&staged[i], detail)
		}
	}

	result.Candidates = staged
	result.Seen = len(merged)
	result.FromIndex = len(fromIndex)
	result.FromLive = len(fromLive)
	return result, nil
}

func collectLive(client *api.Client, opts Options, into map[int]Candidate, fromLive map[int]bool) error {
	pages := opts.Pages
	if pages < 1 {
		pages = 1
	}
	for page := 1; page <= pages; page++ {
		list, err := client.Search(opts.Query, opts.Sort, page)
		if err != nil {
			return err
		}
		ids := make([]int, 0, len(list.Result)*8)
		for _, item := range list.Result {
			ids = append(ids, item.TagIDs...)
		}
		tags, _ := client.TagsByIDs(ids)
		byID := map[int]api.Tag{}
		for _, t := range tags {
			byID[t.ID] = t
		}

		for _, item := range list.Result {
			if item.Blacklisted {
				continue
			}
			if _, exists := into[item.ID]; exists {
				fromLive[item.ID] = true
				continue
			}
			cand := Candidate{
				ID:        item.ID,
				Title:     firstNonEmpty(item.EnglishTitle, deref(item.JapaneseTitle)),
				Pages:     item.NumPages,
				Favorites: item.NumFavorites,
			}
			cand.Tags, cand.TagTypes, cand.Language = resolve(item.TagIDs, byID)
			into[item.ID] = cand
			fromLive[item.ID] = true
		}
		if page >= list.NumPages || len(list.Result) == 0 {
			break
		}
	}
	return nil
}

func resolve(ids []int, byID map[int]api.Tag) ([]string, map[string][]string, string) {
	tags := make([]string, 0, len(ids))
	byType := map[string][]string{}
	language := ""
	for _, id := range ids {
		t, ok := byID[id]
		if !ok || t.Name == "" {
			continue
		}
		tags = append(tags, t.Name)
		byType[t.Type] = append(byType[t.Type], t.Name)
		if t.Type == "language" {
			language = t.Name
		}
	}
	return tags, byType, language
}

func enrich(c *Candidate, g *api.Gallery) {
	if g.Title.Pretty != "" {
		c.Title = g.Title.Pretty
	} else if g.Title.English != "" {
		c.Title = g.Title.English
	}
	if g.NumPages > 0 {
		c.Pages = g.NumPages
	}
	if g.NumFavorites > 0 {
		c.Favorites = g.NumFavorites
	}
	c.Scanlator = g.Scanlator
	c.UploadDate = g.UploadDate

	byType := map[string][]string{}
	names := make([]string, 0, len(g.Tags))
	language := ""
	for _, t := range g.Tags {
		if t.Name == "" {
			continue
		}
		names = append(names, t.Name)
		byType[t.Type] = append(byType[t.Type], t.Name)
		if t.Type == "language" {
			language = t.Name
		}
	}
	c.Tags = names
	c.TagTypes = byType
	c.Language = language
}

// prior is the cheap pre-rank: popularity, tempered by whether there is enough
// page count to hold a plot. It only decides which candidates get detail calls,
// never the final order.
func prior(c Candidate) float64 {
	fav := 0.0
	if c.Favorites > 0 {
		fav = float64(c.Favorites)
	}
	shape := 0.75
	switch {
	case c.Pages >= 40 && c.Pages <= 140:
		shape = 1.0
	case c.Pages < 15:
		shape = 0.2
	case c.Pages > 250:
		shape = 0.5
	}
	return fav*shape
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
