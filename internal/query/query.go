// Package query parses nhentai's search syntax so the same query string can be
// run against the live API and evaluated locally against the index.
//
// Supported, matching the published grammar:
//
//	foo bar              both terms required
//	-foo                 exclude
//	"exact phrase"       phrase match
//	tag:"big breasts"    namespaced filter (tag, artist, group, parody, character,
//	                     language, category)
//	pages:>20            numeric filter, also favorites:, uploaded:
//	pages:20             exact value
package query

import (
	"strconv"
	"strings"
)

// Query is a parsed search expression.
type Query struct {
	Raw      string
	Tags     []string          // namespaced filters, lowercased values
	ByType   map[string][]string
	Terms    []string          // bare words and phrases that must appear
	Excludes []string          // -prefixed terms, lowercased
	MinPages int
	MaxPages int
	Language string
}

// Empty reports whether the query carries no constraints at all.
func (q Query) Empty() bool {
	return len(q.Tags) == 0 && len(q.Terms) == 0 && len(q.Excludes) == 0 &&
		q.MinPages == 0 && q.MaxPages == 0 && q.Language == ""
}

// Parse reads the nhentai grammar. It never fails: anything unrecognised is
// treated as a bare term, which is exactly how the server treats it.
func Parse(raw string) Query {
	q := Query{Raw: raw, ByType: map[string][]string{}}
	for _, token := range tokenize(raw) {
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "-") {
			value := strings.TrimSpace(token[1:])
			// -tag:"big breasts" carries the same namespace as the positive
			// form; exclusions match bare tag names in the local index.
			if _, v, ok := splitNamespace(value); ok {
				value = v
			}
			value = strings.Trim(strings.TrimSpace(value), `"`)
			if value != "" {
				q.Excludes = append(q.Excludes, strings.ToLower(value))
			}
			continue
		}

		key, value, ok := splitNamespace(token)
		if !ok {
			q.Terms = append(q.Terms, strings.Trim(token, `"`))
			continue
		}
		value = strings.Trim(value, `"`)
		if value == "" {
			continue
		}

		switch key {
		case "pages":
			lo, hi := parseRange(value)
			if lo > 0 {
				q.MinPages = lo
			}
			if hi > 0 {
				q.MaxPages = hi
			}
		case "language":
			q.Language = strings.ToLower(value)
			q.ByType[key] = append(q.ByType[key], strings.ToLower(value))
		case "favorites", "uploaded":
			// Not reproducible from the local index; leave these to the live
			// query instead of requiring a tag with a numeric name.
			q.ByType[key] = append(q.ByType[key], strings.ToLower(value))
		default:
			lowered := strings.ToLower(value)
			q.Tags = append(q.Tags, lowered)
			q.ByType[key] = append(q.ByType[key], lowered)
		}
	}
	return q
}

// tokenize splits on spaces while keeping quoted runs intact, including the
// namespace:value form with a quoted value.
func tokenize(s string) []string {
	var out []string
	var current strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			current.WriteRune(r)
		case (r == ' ' || r == '\t') && !inQuote:
			if current.Len() > 0 {
				out = append(out, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

// splitNamespace recognises `name:value` where name is a known namespace.
func splitNamespace(token string) (string, string, bool) {
	idx := strings.Index(token, ":")
	if idx <= 0 {
		return "", "", false
	}
	key := strings.ToLower(token[:idx])
	switch key {
	case "tag", "artist", "group", "parody", "character", "language", "category",
		"pages", "favorites", "uploaded":
		return key, token[idx+1:], true
	}
	return "", "", false
}

// parseRange reads "20", ">20", ">=20", "<30", "<=30" and "20..30".
func parseRange(v string) (int, int) {
	switch {
	case strings.HasPrefix(v, ">="):
		return atoi(v[2:]), 0
	case strings.HasPrefix(v, "<="):
		return 0, atoi(v[2:])
	case strings.HasPrefix(v, ">"):
		return atoi(v[1:]) + 1, 0
	case strings.HasPrefix(v, "<"):
		n := atoi(v[1:])
		if n > 0 {
			return 0, n - 1
		}
		return 0, 0
	case strings.Contains(v, ".."):
		parts := strings.SplitN(v, "..", 2)
		return atoi(parts[0]), atoi(parts[1])
	default:
		n := atoi(v)
		if n > 0 {
			return n, n
		}
		return 0, 0
	}
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
