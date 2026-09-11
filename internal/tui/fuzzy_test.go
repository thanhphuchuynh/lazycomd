package tui

import (
	"slices"
	"testing"
)

func TestMatchSubsequence(t *testing.T) {
	if _, _, ok := match("api", "app:api"); !ok {
		t.Fatal("api should match app:api")
	}
	if _, _, ok := match("xyz", "app:api"); ok {
		t.Fatal("xyz should not match app:api")
	}
	if _, _, ok := match("ipa", "app:api"); ok {
		t.Fatal("out-of-order runes should not match")
	}
}

func TestMatchPositions(t *testing.T) {
	_, pos, ok := match("app", "app:api")
	if !ok {
		t.Fatal("no match")
	}
	if !slices.Equal(pos, []int{0, 1, 2}) {
		t.Fatalf("positions = %v, want [0 1 2]", pos)
	}
}

func TestMatchSmartCase(t *testing.T) {
	if _, _, ok := match("api", "APP:API"); !ok {
		t.Fatal("lowercase query should match case-insensitively")
	}
	if _, _, ok := match("API", "app:api"); ok {
		t.Fatal("uppercase query should be case-sensitive")
	}
}

func TestMatchEmptyQuery(t *testing.T) {
	score, pos, ok := match("", "anything")
	if !ok || score != 0 || len(pos) != 0 {
		t.Fatalf("empty query = %d, %v, %v; want 0, [], true", score, pos, ok)
	}
}

func TestRankOrdersBestFirst(t *testing.T) {
	got := rank("api", []string{"scraper:api", "app:api", "api", "proxy"})

	var names []string
	for _, c := range got {
		names = append(names, c.name)
	}
	want := []string{"api", "app:api", "scraper:api"}
	if !slices.Equal(names, want) {
		t.Fatalf("rank = %v, want %v", names, want)
	}
}

func TestRankEmptyQueryKeepsInputOrder(t *testing.T) {
	got := rank("", []string{"b", "a"})
	if len(got) != 2 || got[0].name != "b" || got[1].name != "a" {
		t.Fatalf("rank = %+v, want input order", got)
	}
}

func TestRankTiesBreakByName(t *testing.T) {
	got := rank("x", []string{"bx", "ax"})
	if len(got) != 2 {
		t.Fatalf("rank = %+v, want 2 matches", got)
	}
	if got[0].score == got[1].score && got[0].name != "ax" {
		t.Fatalf("equal scores should sort by name, got %v then %v", got[0].name, got[1].name)
	}
}
