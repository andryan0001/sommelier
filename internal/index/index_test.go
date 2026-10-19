package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andryan0001/sommelier/internal/query"
)

func testIndex(t *testing.T) *Index {
	t.Helper()
	idx := &Index{
		Path:      filepath.Join(t.TempDir(), "index.jsonl"),
		Entries:   map[int]Entry{},
		TagCounts: map[string]int{},
	}
	idx.Add(Entry{ID: 1, Title: "Sick Girl", Pages: 80, Favorites: 9000,
		Tags: []string{"incest", "illness", "sole female"}, Language: "english"})
	idx.Add(Entry{ID: 2, Title: "Big Breasts Adventure", Pages: 30, Favorites: 50000,
		Tags: []string{"big breasts", "sole female"}, Language: "english"})
	idx.Add(Entry{ID: 3, Title: "Raw Only", Pages: 20, Favorites: 10,
		Tags: []string{"incest"}, Language: "japanese"})
	return idx
}

func TestSearchRequiresEveryTag(t *testing.T) {
	idx := testIndex(t)
	got := idx.Search(query.Parse(`tag:"incest" tag:"illness"`), 10)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("expected only gallery 1, got %+v", got)
	}
}

func TestSearchRespectsLanguage(t *testing.T) {
	idx := testIndex(t)
	got := idx.Search(query.Parse(`language:english`), 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 english entries, got %d", len(got))
	}
	for _, e := range got {
		if e.Language != "english" {
			t.Errorf("leaked %q", e.Language)
		}
	}
}

func TestSearchRespectsPageRange(t *testing.T) {
	idx := testIndex(t)
	got := idx.Search(query.Parse(`pages:>50`), 10)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("expected gallery 1, got %+v", got)
	}
}

func TestSearchExcludesTags(t *testing.T) {
	idx := testIndex(t)
	got := idx.Search(query.Parse(`-incest`), 10)
	for _, e := range got {
		for _, tag := range e.Tags {
			if tag == "incest" {
				t.Fatalf("excluded tag leaked: %+v", e)
			}
		}
	}
}

func TestRareTagOutweighsCommonTag(t *testing.T) {
	idx := &Index{Path: filepath.Join(t.TempDir(), "i.jsonl"), Entries: map[int]Entry{}, TagCounts: map[string]int{}}
	for i := 1; i <= 20; i++ {
		idx.Add(Entry{ID: i, Title: "common", Tags: []string{"big breasts"}})
	}
	idx.Add(Entry{ID: 99, Title: "rare", Tags: []string{"illness"}})
	idx.Add(Entry{ID: 100, Title: "both", Tags: []string{"big breasts", "illness"}})

	got := idx.Search(query.Parse(`tag:"illness"`), 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 illness entries, got %d", len(got))
	}
	if got[0].ID != 100 && got[0].ID != 99 {
		t.Errorf("unexpected order: %+v", got)
	}
}

func TestSaveAndOpenRoundTrip(t *testing.T) {
	idx := testIndex(t)
	if err := idx.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := Open(idx.Path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if reopened.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", reopened.Len())
	}
	if reopened.Entries[1].Title != "Sick Girl" {
		t.Errorf("entry lost: %+v", reopened.Entries[1])
	}
	if reopened.TagCounts["incest"] != 2 {
		t.Errorf("tag counts not rebuilt: %v", reopened.TagCounts)
	}
}

func TestOpenSkipsTruncatedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.jsonl")
	body := `{"id":1,"title":"ok","pages":10,"tags":["a"]}` + "\n" + `{"id":2,"title":"trunc`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Open(path)
	if err != nil {
		t.Fatalf("open should tolerate a truncated tail: %v", err)
	}
	if idx.Len() != 1 {
		t.Errorf("expected the complete line only, got %d", idx.Len())
	}
}

func TestOpenMissingFileIsEmpty(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if idx.Len() != 0 {
		t.Error("expected an empty index")
	}
}

func TestVocabularyOrderedByFrequency(t *testing.T) {
	idx := testIndex(t)
	vocab := idx.Vocabulary(2)
	if len(vocab) != 2 {
		t.Fatalf("expected 2 tags, got %v", vocab)
	}
	if vocab[0] != "incest" && vocab[0] != "sole female" {
		t.Errorf("most frequent tag not first: %v", vocab)
	}
}

func TestTagDictRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tags.jsonl")
	dict, err := OpenTags(path)
	if err != nil {
		t.Fatal(err)
	}
	if missing := dict.Missing([]int{1, 2, 2, 3}); len(missing) != 3 {
		t.Errorf("missing = %v", missing)
	}
	if err := dict.PutMany([]TagRef{{ID: 1, Type: "tag", Name: "romance"}}); err != nil {
		t.Fatal(err)
	}
	if missing := dict.Missing([]int{1, 2}); len(missing) != 1 || missing[0] != 2 {
		t.Errorf("after PutMany missing = %v", missing)
	}
	ref, ok := dict.Get(1)
	if !ok || ref.Name != "romance" {
		t.Errorf("Get(1) = %+v, %t", ref, ok)
	}

	reopened, err := OpenTags(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Len() != 1 {
		t.Errorf("reopened dict has %d tags", reopened.Len())
	}
}

func TestTagDictDeduplicatesOnPut(t *testing.T) {
	dict, err := OpenTags(filepath.Join(t.TempDir(), "tags.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := dict.PutMany([]TagRef{{ID: 1, Name: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := dict.PutMany([]TagRef{{ID: 1, Name: "a"}}); err != nil {
		t.Fatal(err)
	}
	if dict.Len() != 1 {
		t.Errorf("expected 1 tag, got %d", dict.Len())
	}
}
