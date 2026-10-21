package names

import "testing"

func TestUnique(t *testing.T) {
	if len(adjectives) < 50 || len(nouns) < 50 {
		t.Fatalf("lists too short: %d %d", len(adjectives), len(nouns))
	}
	seen := map[string]bool{}
	for range 3000 {
		n := New(func(s string) bool { return seen[s] })
		if seen[n] {
			t.Fatalf("duplicate %q", n)
		}
		seen[n] = true
	}
}

func TestSlug(t *testing.T) {
	if got := Slug("Fuzzy Muffins"); got != "fuzzy-muffins" {
		t.Fatal(got)
	}
	if got := Slug("Fuzzy Muffins 201"); got != "fuzzy-muffins-201" {
		t.Fatal(got)
	}
}
