package nhl

import (
	"testing"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
)

func TestParseLocalizedName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         nhlapi.LocalizedString
		expectedFirst string
		expectedLast  string
	}{
		{
			name:          "two-part name",
			input:         nhlapi.LocalizedString{Default: "Connor McDavid"},
			expectedFirst: "Connor",
			expectedLast:  "McDavid",
		},
		{
			name:          "hyphenated first name",
			input:         nhlapi.LocalizedString{Default: "Pierre-Luc Dubois"},
			expectedFirst: "Pierre-Luc",
			expectedLast:  "Dubois",
		},
		{
			name:          "single name",
			input:         nhlapi.LocalizedString{Default: "Connor"},
			expectedFirst: "Connor",
			expectedLast:  "",
		},
		{
			name:          "empty string",
			input:         nhlapi.LocalizedString{Default: ""},
			expectedFirst: "",
			expectedLast:  "",
		},
		{
			name:          "more than two words keeps remainder as last",
			input:         nhlapi.LocalizedString{Default: "Jean Claude Van Damme"},
			expectedFirst: "Jean",
			expectedLast:  "Claude Van Damme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first, last := parseLocalizedName(tt.input)
			assert.Equal(t, tt.expectedFirst, first)
			assert.Equal(t, tt.expectedLast, last)
		})
	}
}
