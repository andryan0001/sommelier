package index

import (
	"fmt"
	"time"

	"github.com/andryan0001/sommelier/internal/api"
)

// BuildOptions controls a catalog build.
type BuildOptions struct {
	// Query is the search expression used to walk the catalog, e.g.
	// "language:english". Empty walks /galleries newest-first.
	Query string
	// Language is stamped on every entry when the query fixes it, so the index
	// can answer language filters without a detail call.
	Language string
	// Sort is the search ordering; "date" walks the catalog stably.
	Sort string
	// MaxPages caps the walk. 0 means until the last page.
	MaxPages int
	// PerPage is the /galleries page size, capped upstream at 100.
	PerPage int
	// Resume continues from the saved cursor instead of page 1.
	Resume bool
	// Restart clears the cursor and rebuilds from page 1.
	Restart bool
	// Scope names the catalog being built ("english", "all", or a language),
	// so a later --resume repeats the same walk instead of the default one.
	Scope string
	// Progress is called after each page. It may be nil.
	Progress func(Progress)
}

// Progress is one line of build status.
type Progress struct {
	Page      int
	Pages     int
	Entries   int
	Tags      int
	Calls     int
	Elapsed   time.Duration
	Remaining time.Duration
}

// Build walks the catalog and appends entries to the index.
//
// The walk is resumable: a cursor file records the last completed page, so an
// interrupted build continues where it stopped instead of paying for the same
// pages again.
func Build(client *api.Client, idx *Index, tags *TagDict, opts BuildOptions) (Progress, error) {
	var progress Progress
	if opts.Restart {
		if err := ClearCursor(idx.Path); err != nil {
			return progress, err
		}
	} else if !opts.Resume {
		// A fresh walk must not inherit a stale cursor from an earlier run.
		if err := ClearCursor(idx.Path); err != nil {
			return progress, err
		}
	}

	start := 1
	if opts.Resume {
		cursor := LoadCursor(idx.Path)
		if cursor.Page > 0 {
			start = cursor.Page + 1
		}
	}

	if opts.Sort == "" {
		opts.Sort = "date"
	}
	if opts.PerPage < 1 || opts.PerPage > 100 {
		opts.PerPage = 100
	}

	started := time.Now()
	page := start
	completed := false

	for {
		if opts.MaxPages > 0 && page-start >= opts.MaxPages {
			break
		}

		var (
			items    []api.GalleryListItem
			numPages int
		)
		if opts.Query != "" {
			list, err := client.Search(opts.Query, opts.Sort, page)
			if err != nil {
				return progress, err
			}
			items, numPages = list.Result, list.NumPages
		} else {
			list, err := client.Galleries(page, opts.PerPage)
			if err != nil {
				return progress, err
			}
			items, numPages = list.Result, list.NumPages
		}
		if len(items) == 0 {
			completed = true
			break
		}

		// Resolve only tag ids the dictionary has never seen. After the first
		// few hundred galleries this is usually zero calls per page.
		wanted := make([]int, 0, len(items)*8)
		for _, item := range items {
			wanted = append(wanted, item.TagIDs...)
		}
		missing := tags.Missing(wanted)
		if len(missing) > 0 {
			resolved, rerr := client.TagsByIDs(missing)
			if rerr != nil {
				return progress, rerr
			}
			refs := make([]TagRef, 0, len(resolved))
			for _, t := range resolved {
				refs = append(refs, TagRef{ID: t.ID, Type: t.Type, Name: t.Name})
			}
			if err := tags.PutMany(refs); err != nil {
				return progress, err
			}
		}

		batch := make([]Entry, 0, len(items))
		for _, item := range items {
			if item.Blacklisted {
				continue
			}
			names := make([]string, 0, len(item.TagIDs))
			byType := map[string][]string{}
			for _, id := range item.TagIDs {
				ref, ok := tags.Get(id)
				if !ok || ref.Name == "" {
					continue
				}
				names = append(names, ref.Name)
				byType[ref.Type] = append(byType[ref.Type], ref.Name)
			}
			language := opts.Language
			if language == "" {
				if langs := byType["language"]; len(langs) > 0 {
					language = langs[0]
				}
			}
			batch = append(batch, Entry{
				ID:            item.ID,
				Title:         firstNonEmpty(item.EnglishTitle, deref(item.JapaneseTitle)),
				JapaneseTitle: deref(item.JapaneseTitle),
				Pages:         item.NumPages,
				Favorites:     item.NumFavorites,
				TagIDs:        item.TagIDs,
				Tags:          names,
				Language:      language,
			})
		}

		if err := idx.Append(batch); err != nil {
			return progress, err
		}
		for _, e := range batch {
			idx.Add(e)
		}

		stats := client.Stats()
		progress = Progress{
			Page:      page,
			Pages:     numPages,
			Entries:   idx.Len(),
			Tags:      tags.Len(),
			Calls:     stats.Calls,
			Elapsed:   time.Since(started),
			Remaining: estimate(page-start+1, numPages-page, stats.Calls, time.Since(started)),
		}
		if opts.Progress != nil {
			opts.Progress(progress)
		}

		if err := SaveCursor(idx.Path, Cursor{Page: page, Entries: idx.Len(), Scope: opts.Scope}); err != nil {
			return progress, err
		}
		if page >= numPages {
			completed = true
			break
		}
		page++
	}

	// Compact once at the end so a resumed build leaves no duplicate lines.
	if err := idx.Save(); err != nil {
		return progress, err
	}
	if err := tags.Save(); err != nil {
		return progress, err
	}
	// A bounded run is not a finished catalog: keep the cursor so --resume
	// continues where this walk stopped.
	if completed {
		if err := ClearCursor(idx.Path); err != nil {
			return progress, err
		}
	}
	return progress, nil
}

func estimate(donePages, remainingPages, calls int, elapsed time.Duration) time.Duration {
	if donePages <= 0 || remainingPages <= 0 || elapsed <= 0 {
		return 0
	}
	perPage := elapsed / time.Duration(donePages)
	return perPage * time.Duration(remainingPages)
}

// FormatDuration renders a duration as "1h04m" or "12m30s".
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
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
