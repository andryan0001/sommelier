package agent

import (
	"testing"

	"github.com/andryan0001/sommelier/internal/retrieve"
)

var vocab = []string{"incest", "illness", "big breasts", "females only", "sole female",
	"sole male", "anal", "romance", "netorare", "lolicon"}

func TestPlanHeuristicMatchesKnownTags(t *testing.T) {
	plan := PlanHeuristic("incest hentai with illness female protagonist", vocab)
	if !contains(plan.Query, `tag:"incest"`) {
		t.Errorf("missing incest filter: %s", plan.Query)
	}
	if !contains(plan.Query, `tag:"illness"`) {
		t.Errorf("missing illness filter: %s", plan.Query)
	}
}

func TestPlanHeuristicPrefersLongestTag(t *testing.T) {
	plan := PlanHeuristic("big breasts please", vocab)
	if !contains(plan.Query, `tag:"big breasts"`) {
		t.Errorf("expected the full tag: %s", plan.Query)
	}
}

func TestPlanHeuristicFallsBackToKeywords(t *testing.T) {
	plan := PlanHeuristic("surreal mecha introspection", vocab)
	if plan.Query == "" {
		t.Fatal("empty query")
	}
	if contains(plan.Query, "tag:") {
		t.Errorf("should not invent tags: %s", plan.Query)
	}
}

func TestPlanHeuristicStripsStopwords(t *testing.T) {
	plan := PlanHeuristic("a story about the thing", vocab)
	if contains(plan.Query, "story") || contains(plan.Query, "about") {
		t.Errorf("stopwords survived: %s", plan.Query)
	}
}

func TestRankHeuristicPrefersLongPopularOverTiny(t *testing.T) {
	strong := retrieve.Candidate{ID: 1, Title: "A", Pages: 80, Favorites: 50000}
	weak := retrieve.Candidate{ID: 2, Title: "B", Pages: 8, Favorites: 50}
	rows := RankHeuristic([]retrieve.Candidate{weak, strong})
	if rows[0].Candidate.ID != 1 {
		t.Errorf("expected the strong candidate first, got %+v", rows)
	}
}

func TestRankHeuristicIsSortedAndBounded(t *testing.T) {
	cands := []retrieve.Candidate{
		{ID: 1, Pages: 60, Favorites: 10},
		{ID: 2, Pages: 60, Favorites: 100000},
		{ID: 3, Pages: 60, Favorites: 1000},
	}
	rows := RankHeuristic(cands)
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Score < rows[i].Score {
			t.Fatalf("not sorted: %+v", rows)
		}
	}
	for _, r := range rows {
		if r.Score > 100 || r.Score < 0 {
			t.Errorf("score out of range: %v", r.Score)
		}
	}
}

func TestRankHeuristicHandlesZeroFavorites(t *testing.T) {
	rows := RankHeuristic([]retrieve.Candidate{{ID: 1, Pages: 20}})
	if len(rows) != 1 || rows[0].Score < 0 {
		t.Errorf("unexpected rows: %+v", rows)
	}
}

func TestDossierOmitsIDs(t *testing.T) {
	c := retrieve.Candidate{
		ID: 42, Title: "T", Pages: 30, Favorites: 100,
		Tags:     []string{"a", "b"},
		TagTypes: map[string][]string{"artist": {"someone"}},
	}
	d := c.Dossier()
	if _, ok := d["media_id"]; ok {
		t.Error("media_id should not be in the dossier")
	}
	if d["artist"] != "someone" {
		t.Errorf("artist = %v", d["artist"])
	}
	if d["id"] != 42 {
		t.Errorf("id = %v", d["id"])
	}
}

func TestDossierTruncatesTags(t *testing.T) {
	c := retrieve.Candidate{ID: 1, Tags: make([]string, 40)}
	d := c.Dossier()
	if got := len(d["tags"].([]string)); got != 24 {
		t.Errorf("tag count = %d, want 24", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
