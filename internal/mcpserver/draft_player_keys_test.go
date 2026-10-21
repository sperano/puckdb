package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateDraftPlayerKeySupportsYahooAndStandInKeys(t *testing.T) {
	tests := map[string]struct {
		playerKey string
		leagueKey string
		wantError string
	}{
		"Yahoo":             {"465.p.2", "465.l.1001", ""},
		"foreign game":      {"464.p.2", "465.l.1001", "does not belong to game 465"},
		"stand-in":          {"nhl.p.8478402", "temporary-stand-in.l.1001", ""},
		"Yahoo in stand-in": {"465.p.2", "temporary-stand-in.l.1001", "want a stand-in player key"},
		"stand-in in Yahoo": {"nhl.p.8478402", "465.l.1001", "does not belong to game 465"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateDraftPlayerKey(test.playerKey, test.leagueKey)
			if test.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantError)
		})
	}
}
