package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// filterState is the log pane's substring filter.
type filterState struct {
	editing bool
	input   textinput.Model
	query   string
	matches int
}

func newFilter() filterState {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 120
	return filterState{input: ti}
}

// smartContains matches case-insensitively while the query is all lowercase,
// and exactly once the query carries an uppercase rune.
//
// ponytail: substring, not regex. No pattern-error state, no per-line regex
// cost. Add regex when a filter people actually want cannot be expressed.
func smartContains(line, query string) bool {
	if strings.ToLower(query) == query {
		return strings.Contains(strings.ToLower(line), query)
	}
	return strings.Contains(line, query)
}
