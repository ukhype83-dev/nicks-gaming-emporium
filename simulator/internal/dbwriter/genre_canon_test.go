package dbwriter

import "testing"

// TestGenreCanonCore verifies the embedded genre_map.tsv resolves to the
// ~39 core-genre taxonomy and that a few representative raws map correctly.
func TestGenreCanonCore(t *testing.T) {
	vals := map[string]bool{}
	for _, v := range genreCanon {
		if v != "" {
			vals[v] = true
		}
	}
	if len(vals) != 39 {
		t.Fatalf("expected 39 core genres in embedded map, got %d", len(vals))
	}
	cases := map[string]any{
		"racing video game": "Racing",
		"platform game":     "Platformer",
		"action game":       "Action",
	}
	for raw, want := range cases {
		if got := canonicalGenre(raw); got != want {
			t.Errorf("canonicalGenre(%q) = %v, want %v", raw, got, want)
		}
	}
}
