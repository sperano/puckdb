package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// combinedNameCases pins how a combined name splits. Every path that splits
// a player name through ParseCombinedName runs these cases, so the boxscore
// and Yahoo player paths cannot drift apart.
var combinedNameCases = []struct {
	name      string
	fullName  string
	wantFirst string
	wantLast  string
}{
	{name: "first and last", fullName: "Connor McDavid", wantFirst: "Connor", wantLast: "McDavid"},
	{name: "hyphenated first name", fullName: "Pierre-Luc Dubois", wantFirst: "Pierre-Luc", wantLast: "Dubois"},
	{name: "multi-part surname", fullName: "James van Riemsdyk", wantFirst: "James", wantLast: "van Riemsdyk"},
	{name: "single name", fullName: "Sedin", wantFirst: "Sedin", wantLast: ""},
	{name: "empty", fullName: "", wantFirst: "", wantLast: ""},
	{name: "blank", fullName: " \t ", wantFirst: "", wantLast: ""},
	{name: "surrounding whitespace trimmed", fullName: "  Connor McDavid \t", wantFirst: "Connor", wantLast: "McDavid"},
	{name: "inner spaces kept in last name", fullName: "Connor  McDavid", wantFirst: "Connor", wantLast: " McDavid"},
	{name: "tab is not a separator", fullName: "Connor\tMcDavid", wantFirst: "Connor\tMcDavid", wantLast: ""},
}

func TestParseCombinedName(t *testing.T) {
	t.Parallel()

	for _, tc := range combinedNameCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			first, last := ParseCombinedName(tc.fullName)
			assert.Equal(t, tc.wantFirst, first)
			assert.Equal(t, tc.wantLast, last)
		})
	}
}
