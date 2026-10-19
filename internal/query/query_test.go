package query

import "testing"

func TestParseTagFilters(t *testing.T) {
	q := Parse(`tag:"big breasts" tag:"illness" language:english`)
	if len(q.Tags) != 2 {
		t.Fatalf("expected 2 tag filters, got %d: %v", len(q.Tags), q.Tags)
	}
	if q.Tags[0] != "big breasts" || q.Tags[1] != "illness" {
		t.Errorf("unexpected tags: %v", q.Tags)
	}
	if q.Language != "english" {
		t.Errorf("language = %q, want english", q.Language)
	}
	if len(q.ByType["language"]) != 1 {
		t.Errorf("language should be tracked by type: %v", q.ByType)
	}
}

func TestParseBareTermsAndExcludes(t *testing.T) {
	q := Parse(`incest -lolicon "slow burn"`)
	if len(q.Terms) != 2 {
		t.Fatalf("expected 2 terms, got %v", q.Terms)
	}
	if q.Terms[0] != "incest" || q.Terms[1] != "slow burn" {
		t.Errorf("unexpected terms: %v", q.Terms)
	}
	if len(q.Excludes) != 1 || q.Excludes[0] != "lolicon" {
		t.Errorf("unexpected excludes: %v", q.Excludes)
	}
}

func TestParsePageRange(t *testing.T) {
	cases := []struct {
		in       string
		min, max int
	}{
		{"pages:>20", 21, 0},
		{"pages:>=20", 20, 0},
		{"pages:<30", 0, 29},
		{"pages:<=30", 0, 30},
		{"pages:20", 20, 20},
		{"pages:20..40", 20, 40},
	}
	for _, tc := range cases {
		q := Parse(tc.in)
		if q.MinPages != tc.min || q.MaxPages != tc.max {
			t.Errorf("%s -> min=%d max=%d, want %d/%d", tc.in, q.MinPages, q.MaxPages, tc.min, tc.max)
		}
	}
}

func TestParseIgnoresUnknownNamespace(t *testing.T) {
	q := Parse("http://example.com tag:romance")
	if len(q.Terms) != 1 || q.Terms[0] != "http://example.com" {
		t.Errorf("unknown namespace should stay a bare term: %v", q.Terms)
	}
	if len(q.Tags) != 1 || q.Tags[0] != "romance" {
		t.Errorf("tag filter lost: %v", q.Tags)
	}
}

func TestParseNamespacedExclude(t *testing.T) {
	q := Parse(`-tag:"big breasts"`)
	if len(q.Excludes) != 1 || q.Excludes[0] != "big breasts" {
		t.Errorf("excludes = %v", q.Excludes)
	}
}

func TestParseNumericFiltersAreNotTags(t *testing.T) {
	q := Parse("favorites:>=100 uploaded:<30d")
	if len(q.Tags) != 0 {
		t.Errorf("numeric filters leaked into tags: %v", q.Tags)
	}
}

func TestEmpty(t *testing.T) {
	if !Parse("").Empty() {
		t.Error("empty string should parse to an empty query")
	}
	if Parse("tag:romance").Empty() {
		t.Error("tag filter should make the query non-empty")
	}
}
