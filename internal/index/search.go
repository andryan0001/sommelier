package index

import (
	"sort"
	"strings"

	"github.com/andryan0001/sommelier/internal/query"
)

// Search scores indexed entries against a parsed query.
//
// The score is a cheap lexical prior, not a ranking: it decides which
// candidates are worth spending API calls on. Tag hits weigh more than title
// hits because tags are curated, and a tag that appears in few indexed galleries
// counts for more than a ubiquitous one — matching "illness" says more about a
// gallery than matching "big breasts".
func (i *Index) Search(q query.Query, limit int) []Entry {
	if i.Len() == 0 {
		return nil
	}
	terms := make([]string, 0, len(q.Terms))
	for _, t := range q.Terms {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			terms = append(terms, t)
		}
	}

	type scored struct {
		entry Entry
		score float64
	}
	matches := make([]scored, 0, 64)
	total := float64(i.Len())

	for _, e := range i.Entries {
		if q.MinPages > 0 && e.Pages < q.MinPages {
			continue
		}
		if q.MaxPages > 0 && e.Pages > q.MaxPages {
			continue
		}
		if q.Language != "" && !strings.EqualFold(e.Language, q.Language) {
			continue
		}
		if excluded(e, q.Excludes) {
			continue
		}
		if excluded(e, q.Excludes) {
			continue
		}

		tagSet := make(map[string]bool, len(e.Tags))
		for _, t := range e.Tags {
			tagSet[strings.ToLower(t)] = true
		}

		// Every explicit tag filter is required, mirroring the server's AND.
		allTags := true
		score := 0.0
		for _, want := range q.Tags {
			if !tagSet[want] {
				allTags = false
				break
			}
			score += 10 * idf(i.TagCounts[want], total)
		}
		if !allTags {
			continue
		}

		title := strings.ToLower(e.Title + " " + e.JapaneseTitle)
		for _, term := range terms {
			if strings.Contains(title, term) {
				score += 4
			} else if containsAny(e.Tags, term) {
				score += 2
			} else {
				score += 0 // unmatched bare terms are tolerated, not required
			}
		}

		if q.Empty() {
			score = 1
		}
		// A query made only of filters (language:, pages:) has no positive
		// signals to score, so the filters alone decide membership.
		if len(q.Tags) == 0 && len(terms) == 0 {
			score = 1
		}
		if score <= 0 {
			continue
		}
		matches = append(matches, scored{entry: e, score: score})
	}

	sort.Slice(matches, func(a, b int) bool {
		if matches[a].score != matches[b].score {
			return matches[a].score > matches[b].score
		}
		return matches[a].entry.Favorites > matches[b].entry.Favorites
	})

	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]Entry, len(matches))
	for n, m := range matches {
		out[n] = m.entry
	}
	return out
}

func excluded(e Entry, excludes []string) bool {
	if len(excludes) == 0 {
		return false
	}
	for _, tag := range e.Tags {
		tag = strings.ToLower(tag)
		for _, ex := range excludes {
			if tag == ex {
				return true
			}
		}
	}
	return false
}

func containsAny(tags []string, term string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), term) {
			return true
		}
	}
	return false
}

// idf weights a tag by how rare it is in the local corpus, normalised to ~1.
func idf(count int, total float64) float64 {
	if count <= 0 {
		return 1.5
	}
	ratio := float64(count) / total
	if ratio <= 0 {
		return 1.5
	}
	if ratio >= 1 {
		return 0.2
	}
	// 0.2 (ubiquitous) .. 1.5 (rare)
	return 0.2 + 1.3*(1-ratio)
}
