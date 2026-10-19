package retrieve

import (
	"testing"

	"github.com/andryan0001/sommelier/internal/index"
)

func TestPriorPrefersPopularAndSubstantial(t *testing.T) {
	strong := prior(Candidate{Pages: 80, Favorites: 50000})
	weak := prior(Candidate{Pages: 8, Favorites: 50})
	if strong <= weak {
		t.Errorf("strong=%v weak=%v", strong, weak)
	}
}

func TestPriorPenalisesExtremes(t *testing.T) {
	sweet := prior(Candidate{Pages: 80, Favorites: 1000})
	tiny := prior(Candidate{Pages: 5, Favorites: 1000})
	huge := prior(Candidate{Pages: 400, Favorites: 1000})
	if tiny >= sweet {
		t.Errorf("tiny=%v should rank below sweet=%v", tiny, sweet)
	}
	if huge >= sweet {
		t.Errorf("huge=%v should rank below sweet=%v", huge, sweet)
	}
}

func TestSummaryListsDrops(t *testing.T) {
	r := Result{Seen: 10, FromIndex: 4, FromLive: 6, Dropped: map[string]int{"too short": 3}}
	got := r.Summary()
	for _, want := range []string{"seen 10", "index 4", "live 6", "dropped 3 (too short)"} {
		if !contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}
}

func TestURL(t *testing.T) {
	c := Candidate{ID: 123}
	if got := c.URL(); got != "https://nhentai.net/g/123/" {
		t.Errorf("url = %q", got)
	}
}

func TestDossierIncludesTagTypes(t *testing.T) {
	c := Candidate{
		ID: 7, Title: "T", Pages: 40, Favorites: 900, Language: "english",
		Tags:     []string{"romance"},
		TagTypes: map[string][]string{"artist": {"someone"}, "parody": {"x"}},
	}
	d := c.Dossier()
	if d["language"] != "english" {
		t.Errorf("language = %v", d["language"])
	}
	if d["artist"] != "someone" || d["parody"] != "x" {
		t.Errorf("typed tags missing: %v", d)
	}
}

func TestRunFiltersExcludedTags(t *testing.T) {
	idx := &index.Index{
		Path:      t.TempDir() + "/i.jsonl",
		Entries:   map[int]index.Entry{},
		TagCounts: map[string]int{},
	}
	idx.Add(index.Entry{ID: 1, Title: "keep", Pages: 50, Tags: []string{"romance"}})
	idx.Add(index.Entry{ID: 2, Title: "drop", Pages: 50, Tags: []string{"guro"}})

	result, err := Run(nil, Options{
		Query:   "", // matches everything, so only the exclude can drop a row
		Exclude: []string{"guro"},
		Live:    false,
		Index:   idx,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, c := range result.Candidates {
		if c.ID == 2 {
			t.Error("excluded gallery survived")
		}
	}
	if result.Dropped["excluded tag"] != 1 {
		t.Errorf("dropped = %v", result.Dropped)
	}
}

func TestRunAppliesPageBounds(t *testing.T) {
	idx := &index.Index{
		Path:      t.TempDir() + "/i.jsonl",
		Entries:   map[int]index.Entry{},
		TagCounts: map[string]int{},
	}
	idx.Add(index.Entry{ID: 1, Title: "short", Pages: 5, Tags: []string{"romance"}})
	idx.Add(index.Entry{ID: 2, Title: "long", Pages: 200, Tags: []string{"romance"}})
	idx.Add(index.Entry{ID: 3, Title: "just right", Pages: 60, Tags: []string{"romance"}})

	result, err := Run(nil, Options{Query: "romance", MinPages: 20, MaxPages: 120, Index: idx})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].ID != 3 {
		t.Fatalf("unexpected candidates: %+v", result.Candidates)
	}
	if result.Dropped["too short"] != 1 || result.Dropped["too long"] != 1 {
		t.Errorf("dropped = %v", result.Dropped)
	}
}

func TestRunShortlistIsCapped(t *testing.T) {
	idx := &index.Index{
		Path:      t.TempDir() + "/i.jsonl",
		Entries:   map[int]index.Entry{},
		TagCounts: map[string]int{},
	}
	for i := 1; i <= 30; i++ {
		idx.Add(index.Entry{ID: i, Title: "t", Pages: 50, Tags: []string{"romance"}})
	}
	result, err := Run(nil, Options{Query: "romance", Shortlist: 5, Index: idx})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Candidates) != 5 {
		t.Errorf("shortlist = %d, want 5", len(result.Candidates))
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
