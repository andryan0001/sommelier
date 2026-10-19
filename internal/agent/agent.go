// Package agent runs the model passes: plan, rank, critique, review.
//
// Every pass has a deterministic fallback, so the CLI stays useful with no model
// configured and never hard-fails because a provider is down.
package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/andryan0001/sommelier/internal/llm"
	"github.com/andryan0001/sommelier/internal/retrieve"
)

// Plan is the planner's output.
type Plan struct {
	Query string `json:"query"`
	Notes string `json:"notes"`
}

// Ranked is one scored candidate.
type Ranked struct {
	Candidate retrieve.Candidate
	Score     float64
	Why       string
	Critique  string
	Review    string
}

const plannerSystem = `You translate natural-language requests into nhentai search queries.

GRAMMAR
  foo bar            both terms required (implicit AND)
  -foo               exclude
  "exact phrase"     phrase match
  tag:"big breasts"  tag-scoped; also artist:, group:, parody:, character:, language:
  pages:>20          numeric filters; also favorites:>=100, uploaded:<30d
  sort is chosen separately; do not put it in the query

RULES
  - Prefer tag: filters built from the vocabulary below. Bare words AND together
    and usually return zero results.
  - Use at most 6 tag filters. Fewer, more precise filters beat many.
  - Map intent, not vocabulary: "sick girl" -> tag:"illness"; "long" -> pages:>60.
  - Never invent tags. If nothing in the vocabulary fits, use a bare keyword.
  - Always add language:english unless the user asks for raw Japanese.

Return ONLY JSON: {"query": "<nhentai query string>", "notes": "<one sentence>"}`

const rankerSystem = `You rank doujinshi candidates by how well they match a request.

Judge story fit, not popularity: premise match, character and relationship match,
tone, and whether the tags support an actual narrative. Favorites are a weak
prior, not a verdict. Penalise tag-stuffed entries with no coherent premise.

Return ONLY a JSON array, best first:
[{"id": <int>, "score": <0-100>, "why": "<12 words max>"}]`

const critiqueSystem = `You are a second reader reviewing a shortlist.

For each candidate, say in one sentence what the tags and title actually promise
the reader, and one sentence on the weakest part of the match. Be concrete about
which tags carry the premise. Do not repeat the title back.

Return ONLY a JSON array:
[{"id": <int>, "verdict": "<two sentences>"}]`

const reviewSystem = `You write short recommendation blurbs for a ranked shortlist.

For each candidate write 60-90 words: what the story is, who it is for, and what
it does well. Ground every claim in the title and tags given. No spoilers past
the premise, no score, no rating, no mention of the API or of being a model.
Second person, present tense, no headings.

Return ONLY a JSON array:
[{"id": <int>, "review": "<60-90 words>"}]`

// PlanQuery asks the model for a query string, falling back to the heuristic on
// any provider or parse failure.
func PlanQuery(client *llm.Client, request string, vocabulary []string) (Plan, bool) {
	if client == nil {
		return PlanHeuristic(request, vocabulary), false
	}
	prompt := fmt.Sprintf("VOCABULARY (real nhentai tags):\n%s\n\nREQUEST: %s\n\nReturn the JSON object.",
		strings.Join(vocabulary, ", "), request)

	value, err := client.CompleteJSON(plannerSystem, prompt, 400)
	if err != nil {
		return PlanHeuristic(request, vocabulary), false
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return PlanHeuristic(request, vocabulary), false
	}
	q, _ := obj["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return PlanHeuristic(request, vocabulary), false
	}
	notes, _ := obj["notes"].(string)
	return Plan{Query: q, Notes: strings.TrimSpace(notes)}, true
}

var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "with": true, "and": true, "or": true,
	"of": true, "in": true, "on": true, "for": true, "to": true, "that": true,
	"this": true, "is": true, "are": true, "about": true, "some": true, "any": true,
	"where": true, "who": true, "has": true, "have": true, "hentai": true,
	"manga": true, "doujin": true, "doujinshi": true, "comic": true,
	"story": true, "stories": true, "one": true,
}

var nonWord = regexp.MustCompile(`[^a-z0-9\s'-]`)

// PlanHeuristic matches the request against known tag names. Longest match
// first, so "big breasts" wins over "breasts".
func PlanHeuristic(request string, vocabulary []string) Plan {
	text := " " + strings.Join(strings.Fields(nonWord.ReplaceAllString(strings.ToLower(request), " ")), " ") + " "

	sorted := make([]string, 0, len(vocabulary))
	seen := map[string]bool{}
	for _, tag := range vocabulary {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if len(tag) < 3 || seen[tag] {
			continue
		}
		seen[tag] = true
		sorted = append(sorted, tag)
	}
	sort.Slice(sorted, func(a, b int) bool { return len(sorted[a]) > len(sorted[b]) })

	hits := make([]string, 0, 5)
	for _, tag := range sorted {
		// Longest first: a tag nested inside an accepted one (breasts inside
		// big breasts) would over-constrain the query, so skip it.
		nested := false
		for _, hit := range hits {
			if strings.Contains(hit, tag) {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		if strings.Contains(text, " "+tag+" ") || strings.Contains(text, " "+tag+"s ") {
			hits = append(hits, tag)
			if len(hits) == 5 {
				break
			}
		}
	}

	if len(hits) > 0 {
		quoted := make([]string, len(hits))
		for i, h := range hits {
			quoted[i] = fmt.Sprintf("tag:%q", h)
		}
		return Plan{
			Query: strings.Join(quoted, " ") + " language:english",
			Notes: fmt.Sprintf("heuristic: matched %d known tags", len(hits)),
		}
	}

	words := make([]string, 0, 4)
	for _, w := range strings.Fields(text) {
		if stopwords[w] || len(w) <= 2 {
			continue
		}
		words = append(words, w)
		if len(words) == 4 {
			break
		}
	}
	return Plan{Query: strings.Join(append(words, "language:english"), " "), Notes: "heuristic: no tag match, raw keywords"}
}

// Rank scores candidates against the request.
func Rank(client *llm.Client, request string, candidates []retrieve.Candidate) ([]Ranked, bool) {
	if client == nil || len(candidates) == 0 {
		return RankHeuristic(candidates), false
	}

	dossiers := make([]map[string]any, len(candidates))
	for i, c := range candidates {
		dossiers[i] = c.Dossier()
	}
	blob, err := json.Marshal(dossiers)
	if err != nil {
		return RankHeuristic(candidates), false
	}
	prompt := fmt.Sprintf("REQUEST: %s\n\nCANDIDATES:\n%s\n\nReturn the JSON array.", request, blob)

	value, err := client.CompleteJSON(rankerSystem, prompt, 1500)
	if err != nil {
		return RankHeuristic(candidates), false
	}
	rows, ok := value.([]any)
	if !ok {
		return RankHeuristic(candidates), false
	}

	byID := make(map[int]retrieve.Candidate, len(candidates))
	for _, c := range candidates {
		byID[c.ID] = c
	}

	out := make([]Ranked, 0, len(rows))
	seen := map[int]bool{}
	for _, row := range rows {
		obj, ok := row.(map[string]any)
		if !ok {
			continue
		}
		id, ok := asInt(obj["id"])
		if !ok || seen[id] {
			continue
		}
		cand, ok := byID[id]
		if !ok {
			continue
		}
		seen[id] = true
		score, _ := asFloat(obj["score"])
		why, _ := obj["why"].(string)
		out = append(out, Ranked{Candidate: cand, Score: score, Why: strings.TrimSpace(why)})
	}
	if len(out) == 0 {
		return RankHeuristic(candidates), false
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	return out, true
}

// Critique adds a second-reader verdict to the top of the shortlist.
func Critique(client *llm.Client, request string, ranked []Ranked, limit int) bool {
	if client == nil || len(ranked) == 0 {
		return false
	}
	if limit <= 0 || limit > len(ranked) {
		limit = len(ranked)
	}
	top := ranked[:limit]

	dossiers := make([]map[string]any, len(top))
	for i, r := range top {
		dossiers[i] = r.Candidate.Dossier()
	}
	blob, err := json.Marshal(dossiers)
	if err != nil {
		return false
	}
	prompt := fmt.Sprintf("REQUEST: %s\n\nSHORTLIST:\n%s\n\nReturn the JSON array.", request, blob)

	value, err := client.CompleteJSON(critiqueSystem, prompt, 900)
	if err != nil {
		return false
	}
	rows, ok := value.([]any)
	if !ok {
		return false
	}
	verdicts := map[int]string{}
	for _, row := range rows {
		obj, ok := row.(map[string]any)
		if !ok {
			continue
		}
		id, ok := asInt(obj["id"])
		if !ok {
			continue
		}
		verdict, _ := obj["verdict"].(string)
		if verdict = strings.TrimSpace(verdict); verdict != "" {
			verdicts[id] = verdict
		}
	}
	applied := false
	for i := range ranked {
		if v, ok := verdicts[ranked[i].Candidate.ID]; ok {
			ranked[i].Critique = v
			applied = true
		}
	}
	return applied
}

// Review writes the blurb for each finalist.
func Review(client *llm.Client, request string, ranked []Ranked, limit int) bool {
	if client == nil || len(ranked) == 0 {
		return false
	}
	if limit <= 0 || limit > len(ranked) {
		limit = len(ranked)
	}
	top := ranked[:limit]

	dossiers := make([]map[string]any, len(top))
	for i, r := range top {
		dossiers[i] = r.Candidate.Dossier()
	}
	blob, err := json.Marshal(dossiers)
	if err != nil {
		return false
	}
	prompt := fmt.Sprintf("REQUEST: %s\n\nFINALISTS:\n%s\n\nReturn the JSON array.", request, blob)

	value, err := client.CompleteJSON(reviewSystem, prompt, 2000)
	if err != nil {
		return false
	}
	rows, ok := value.([]any)
	if !ok {
		return false
	}
	reviews := map[int]string{}
	for _, row := range rows {
		obj, ok := row.(map[string]any)
		if !ok {
			continue
		}
		id, ok := asInt(obj["id"])
		if !ok {
			continue
		}
		text, _ := obj["review"].(string)
		if text = strings.TrimSpace(text); text != "" {
			reviews[id] = text
		}
	}
	applied := false
	for i := range ranked {
		if v, ok := reviews[ranked[i].Candidate.ID]; ok {
			ranked[i].Review = v
			applied = true
		}
	}
	return applied
}

// RankHeuristic is the no-model ranking: a popularity prior shaped by page
// count. It is a proxy for quality, not a judgement of it.
func RankHeuristic(candidates []retrieve.Candidate) []Ranked {
	out := make([]Ranked, 0, len(candidates))
	for _, c := range candidates {
		fav := math.Log10(math.Max(1, float64(c.Favorites))) / 5.0
		if fav > 1 {
			fav = 1
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
		score := math.Round((fav*0.65+shape*0.35)*1000) / 10
		out = append(out, Ranked{
			Candidate: c,
			Score:     score,
			Why:       fmt.Sprintf("%dp, %d favs (heuristic)", c.Pages, c.Favorites),
		})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	return out
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}
