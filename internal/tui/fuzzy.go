package tui

import (
	"sort"
	"strings"
	"unicode"
)

// candidate is one palette entry that matched the query.
type candidate struct {
	name      string
	score     int
	positions []int
}

// Scoring weights. Consecutive runs and segment starts are what make a
// match feel right; everything else is a tiebreak.
const (
	consecutiveBonus = 15
	segmentBonus     = 10
)

// match reports whether query is a subsequence of target and scores the
// match. Smart-case: an all-lowercase query matches case-insensitively.
//
// ponytail: greedy left-to-right, not an optimal alignment. For command
// names a few dozen runes long nobody can tell the difference.
func match(query, target string) (int, []int, bool) {
	if query == "" {
		return 0, nil, true
	}
	hay := target
	if strings.ToLower(query) == query {
		hay = strings.ToLower(target)
	}

	q := []rune(query)
	h := []rune(hay)
	positions := make([]int, 0, len(q))

	score, qi := 0, 0
	for hi := 0; hi < len(h) && qi < len(q); hi++ {
		if h[hi] != q[qi] {
			continue
		}
		if len(positions) > 0 && positions[len(positions)-1] == hi-1 {
			score += consecutiveBonus
		}
		if hi == 0 || isSegmentBreak(h[hi-1]) {
			score += segmentBonus
		}
		positions = append(positions, hi)
		qi++
	}
	if qi < len(q) {
		return 0, nil, false
	}

	// Prefer an earlier first match and a shorter target.
	score -= positions[0]
	score -= len(h) - len(q)
	return score, positions, true
}

func isSegmentBreak(r rune) bool {
	switch r {
	case ':', '-', '_', '.', '/', ' ':
		return true
	}
	return unicode.IsDigit(r)
}

// rank returns the matching names, best first. An empty query keeps the
// input order so the palette opens as a plain list.
func rank(query string, names []string) []candidate {
	out := make([]candidate, 0, len(names))
	for _, n := range names {
		score, pos, ok := match(query, n)
		if !ok {
			continue
		}
		out = append(out, candidate{name: n, score: score, positions: pos})
	}
	if query == "" {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].name < out[j].name
	})
	return out
}
