package newsfeed

import (
	"testing"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
)

func ruleSnapshot(source draft.Source) sqlcdb.YahooLeagueRuleSnapshot {
	return sqlcdb.YahooLeagueRuleSnapshot{Source: string(source)}
}

func TestStandInSkipReason_AllStandInLeaguesSkip(t *testing.T) {
	leagues := []sqlcdb.YahooLeagueRuleSnapshot{
		ruleSnapshot(draft.SourceTemporaryStandIn), ruleSnapshot(draft.SourceTemporaryStandIn),
	}

	reason := standInSkipReason(leagues)

	assert.Contains(t, reason, "all 2 leagues")
	assert.Contains(t, reason, "stand-in")
}

func TestStandInSkipReason_FailsWhenALeagueShouldHaveAPool(t *testing.T) {
	tests := []struct {
		name    string
		leagues []sqlcdb.YahooLeagueRuleSnapshot
	}{
		{"no_leagues", nil},
		{"one_real_league", []sqlcdb.YahooLeagueRuleSnapshot{
			ruleSnapshot(draft.SourceTemporaryStandIn), ruleSnapshot(draft.SourceYahooAPI),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Empty(t, standInSkipReason(tt.leagues),
				"a season with no league or with a real Yahoo league must keep reporting the missing pool")
		})
	}
}
