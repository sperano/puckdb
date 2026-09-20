package resource_test

import (
	"testing"

	"github.com/sperano/puckdb/internal/resource"
	"github.com/stretchr/testify/require"
)

// Each Edge resource type wraps Format/Parse around json.Marshal/Unmarshal
// for a specific nhl.* type. The 19 pairs share an identical shape:
//
//	func (r R) Format(*T) ([]byte, error) { return json.Marshal(obj) }
//	func (r R) Parse([]byte) (*T, error) {
//	    var v T
//	    if err := json.Unmarshal(data, &v); err != nil { return nil, fmt.Errorf(...) }
//	    return &v, nil
//	}
//
// edgeFormatParseRoundTrip exercises both halves of one such pair: marshal
// a zero value (Format) and round-trip it back (Parse), then verify Parse
// rejects malformed input. Each call covers the two unique branches in
// Parse plus the trivial Format wrapper, for one resource type.
func edgeFormatParseRoundTrip[T any](
	t *testing.T,
	name string,
	format func(*T) ([]byte, error),
	parse func([]byte) (*T, error),
) {
	t.Helper()
	t.Run(name+"_roundTrip", func(t *testing.T) {
		var zero T
		data, err := format(&zero)
		require.NoError(t, err)
		got, err := parse(data)
		require.NoError(t, err)
		require.NotNil(t, got)
	})
	t.Run(name+"_parseError", func(t *testing.T) {
		// Garbage input that's not even syntactically valid JSON exercises
		// the json.Unmarshal error branch and the fmt.Errorf wrapping.
		_, err := parse([]byte("not valid json"))
		require.Error(t, err)
	})
}

func TestEdgeResource_FormatParse(t *testing.T) {
	// Skater per-player resources
	{
		r := resource.EdgeSkaterDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeSkaterDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeSkaterSpeedDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeSkaterSpeedDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeSkaterDistanceDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeSkaterDistanceDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeSkaterShotSpeedDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeSkaterShotSpeedDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeSkaterShotLocationDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeSkaterShotLocationDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeSkaterZoneTime{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeSkaterZoneTime", r.Format, r.Parse)
	}
	{
		r := resource.EdgeSkaterComparison{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeSkaterComparison", r.Format, r.Parse)
	}

	// Goalie per-player resources
	{
		r := resource.EdgeGoalieDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeGoalieDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeGoalie5v5Detail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeGoalie5v5Detail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeGoalieShotLocationDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeGoalieShotLocationDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeGoalieSavePctgDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeGoalieSavePctgDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeGoalieComparison{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeGoalieComparison", r.Format, r.Parse)
	}

	// Team per-team resources
	{
		r := resource.EdgeTeamDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeTeamDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeTeamSpeedDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeTeamSpeedDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeTeamDistanceDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeTeamDistanceDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeTeamShotSpeedDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeTeamShotSpeedDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeTeamShotLocationDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeTeamShotLocationDetail", r.Format, r.Parse)
	}
	{
		r := resource.EdgeTeamZoneTimeDetails{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeTeamZoneTimeDetails", r.Format, r.Parse)
	}
	{
		r := resource.EdgeTeamComparison{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeTeamComparison", r.Format, r.Parse)
	}

	// League-wide landing resources (no player/team ID)
	{
		r := resource.EdgeSkaterLanding{Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeSkaterLanding", r.Format, r.Parse)
	}
	{
		r := resource.EdgeGoalieLanding{Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeGoalieLanding", r.Format, r.Parse)
	}
	{
		r := resource.EdgeTeamLanding{Season: edgeTestSeason, GameType: edgeTestGameType}
		edgeFormatParseRoundTrip(t, "EdgeTeamLanding", r.Format, r.Parse)
	}
}
