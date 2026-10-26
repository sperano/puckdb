package projection

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/canonhash"
)

// configHash identifies the settings a snapshot was generated with
// (projection_snapshots.config_hash, projection_evaluations.config_hash).
// It fails on a setting JSON cannot encode (a NaN or infinite float).
func configHash(cfg Config) (string, error) {
	if !supportsLinemateContext(cfg.ModelVersion) {
		historical := struct {
			ModelVersion          string
			LookbackSeasons       int
			SeasonDecay           float64
			SkaterPriorTOISeconds float64
			GoaliePriorShots      float64
			GoalieShutoutMinTOI   int
			MaxGames              float64
			IntervalZ             float64
			MinimumUncertainty    float64
			MaximumUncertainty    float64
			MinimumHistoryGames   int
		}{
			ModelVersion: cfg.ModelVersion, LookbackSeasons: cfg.LookbackSeasons,
			SeasonDecay: cfg.SeasonDecay, SkaterPriorTOISeconds: cfg.SkaterPriorTOISeconds,
			GoaliePriorShots: cfg.GoaliePriorShots, GoalieShutoutMinTOI: cfg.GoalieShutoutMinTOI,
			MaxGames: cfg.MaxGames, IntervalZ: cfg.IntervalZ,
			MinimumUncertainty: cfg.MinimumUncertainty, MaximumUncertainty: cfg.MaximumUncertainty,
			MinimumHistoryGames: cfg.MinimumHistoryGames,
		}
		return hashConfig(historical)
	}
	return hashConfig(cfg)
}

func hashConfig(value any) (string, error) {
	hash, err := canonhash.JSON(value)
	if err != nil {
		return "", fmt.Errorf("hash projection config: %w", err)
	}
	return hash, nil
}

// sourceDataHash identifies the input rows a snapshot was generated from. It
// fails on a row JSON cannot encode (a NaN or infinite float read from the
// database).
func sourceDataHash(cfg Config, input Input) (string, error) {
	lowerSeason := historyFloorSeason(input.TargetSeason, cfg.LookbackSeasons)
	if supportsAgingCurve(cfg.ModelVersion) {
		lowerSeason = projectionLoadFloor(cfg, input.TargetSeason)
	}
	input.Skaters = slices.DeleteFunc(slices.Clone(input.Skaters), func(row SkaterSeason) bool {
		return row.Season < lowerSeason || row.Season >= input.TargetSeason ||
			!supportsAgingCurve(cfg.ModelVersion) && row.GamesPlayed <= 0
	})
	input.Goalies = slices.DeleteFunc(slices.Clone(input.Goalies), func(row GoalieSeason) bool {
		return row.Season < lowerSeason || row.Season >= input.TargetSeason ||
			!supportsAgingCurve(cfg.ModelVersion) && row.GamesPlayed <= 0
	})
	input.TeamSeasons, input.TargetTeams, input.SkaterClubSeasons = teamEnvironmentHashRows(cfg, input, func(season int) bool {
		age, ok := seasonAge(input.TargetSeason, season)
		return !ok || age >= cfg.LookbackSeasons
	})
	input.GoalieStarts = goalieStartHashRows(cfg, input)
	return inputDataHash(input, cfg.ModelVersion)
}

func evaluationDataHash(cfg Config, input Input) (string, error) {
	lowerSeason := historyFloorSeason(input.TargetSeason, cfg.LookbackSeasons)
	if supportsAgingCurve(cfg.ModelVersion) {
		lowerSeason = projectionLoadFloor(cfg, input.TargetSeason)
	}
	input.Skaters = slices.DeleteFunc(slices.Clone(input.Skaters), func(row SkaterSeason) bool {
		return row.Season < lowerSeason || row.Season > input.TargetSeason
	})
	input.Goalies = slices.DeleteFunc(slices.Clone(input.Goalies), func(row GoalieSeason) bool {
		return row.Season < lowerSeason || row.Season > input.TargetSeason
	})
	teamFloor := historyFloorSeason(input.TargetSeason, cfg.LookbackSeasons)
	input.TeamSeasons, input.TargetTeams, input.SkaterClubSeasons = teamEnvironmentHashRows(cfg, input, func(season int) bool {
		return season < teamFloor || season > input.TargetSeason
	})
	input.GoalieStarts = goalieStartHashRows(cfg, input)
	return inputDataHash(input, cfg.ModelVersion)
}

// teamEnvironmentHashRows returns the team-environment rows a model reads:
// none before nhl-baseline-v5, so their hashes stay what those versions
// computed, and otherwise the club and split-season rows outside excluded.
func teamEnvironmentHashRows(
	cfg Config, input Input, excluded func(season int) bool,
) ([]TeamSeason, []PlayerTeam, []SkaterClubSeason) {
	if !supportsTeamEnvironment(cfg.ModelVersion) {
		return nil, nil, nil
	}
	teams := slices.DeleteFunc(slices.Clone(input.TeamSeasons), func(row TeamSeason) bool {
		return excluded(row.Season) || row.GamesPlayed <= 0
	})
	splits := slices.DeleteFunc(slices.Clone(input.SkaterClubSeasons), func(row SkaterClubSeason) bool {
		return excluded(row.Season)
	})
	return teams, input.TargetTeams, splits
}

// goalieStartHashRows returns the recent starts a model reads: none before
// nhl-baseline-v7, so earlier hashes stay what those versions computed, and
// otherwise the starts inside the history window (usableGoalieStarts).
func goalieStartHashRows(cfg Config, input Input) []GoalieStart {
	if !supportsGoalieStartShare(cfg.ModelVersion) {
		return nil
	}
	return usableGoalieStarts(cfg, input.TargetSeason, input.GoalieStarts)
}

type legacySkaterSeason struct {
	PlayerID        int64
	TeamID          int64
	Season          int
	Position        string
	GamesPlayed     int
	TOISeconds      int
	Goals           int
	Assists         int
	PlusMinus       int
	PenaltyMinutes  int
	PowerPlayPoints int
	ShotsOnGoal     int
	Hits            int
	BlockedShots    int
}

type faceoffSkaterSeason struct {
	legacySkaterSeason
	FaceoffsWon  int
	FaceoffsLost int
}

type linemateSkaterSeason struct {
	faceoffSkaterSeason
	LinematePointsPer60 float64
	LinemateTOISeconds  int
}

type legacyGoalieSeason struct {
	PlayerID     int64
	TeamID       int64
	Season       int
	GamesPlayed  int
	GamesStarted int
	TOISeconds   int
	Wins         int
	Shutouts     int
	ShotsAgainst int
	Saves        int
	GoalsAgainst int
}

func encodedSkaterHashRows(rows []SkaterSeason, modelVersion string) ([]string, error) {
	if supportsAgingCurve(modelVersion) {
		return encodedValues(rows)
	}
	if modelVersion == LinemateModelVersion {
		linemates := make([]linemateSkaterSeason, len(rows))
		for index, row := range rows {
			linemates[index] = linemateSkaterSeason{
				faceoffSkaterSeason: faceoffSkaterSeason{
					legacySkaterSeason: legacySkaterHashRow(row),
					FaceoffsWon:        row.FaceoffsWon,
					FaceoffsLost:       row.FaceoffsLost,
				},
				LinematePointsPer60: row.LinematePointsPer60,
				LinemateTOISeconds:  row.LinemateTOISeconds,
			}
		}
		return encodedValues(linemates)
	}
	if modelVersion == FaceoffModelVersion {
		faceoffs := make([]faceoffSkaterSeason, len(rows))
		for index, row := range rows {
			faceoffs[index] = faceoffSkaterSeason{
				legacySkaterSeason: legacySkaterHashRow(row),
				FaceoffsWon:        row.FaceoffsWon,
				FaceoffsLost:       row.FaceoffsLost,
			}
		}
		return encodedValues(faceoffs)
	}
	legacy := make([]legacySkaterSeason, len(rows))
	for index, row := range rows {
		legacy[index] = legacySkaterHashRow(row)
	}
	return encodedValues(legacy)
}

func legacySkaterHashRow(row SkaterSeason) legacySkaterSeason {
	return legacySkaterSeason{
		PlayerID: row.PlayerID, TeamID: row.TeamID, Season: row.Season, Position: row.Position,
		GamesPlayed: row.GamesPlayed, TOISeconds: row.TOISeconds, Goals: row.Goals, Assists: row.Assists,
		PlusMinus: row.PlusMinus, PenaltyMinutes: row.PenaltyMinutes, PowerPlayPoints: row.PowerPlayPoints,
		ShotsOnGoal: row.ShotsOnGoal, Hits: row.Hits, BlockedShots: row.BlockedShots,
	}
}

// encodedGoalieHashRows encodes the goalie rows a model version reads.
// nhl-baseline-v4 and v5 hashed the whole GoalieSeason before GamesAppeared
// existed, so their rows are encoded with it cleared (omitempty drops it and
// the JSON is byte-identical to theirs); v6 reads it and hashes it.
func encodedGoalieHashRows(rows []GoalieSeason, modelVersion string) ([]string, error) {
	if countsGoalieAppearances(modelVersion) {
		return encodedValues(rows)
	}
	if supportsAgingCurve(modelVersion) {
		published := slices.Clone(rows)
		for index := range published {
			published[index].GamesAppeared = 0
		}
		return encodedValues(published)
	}
	legacy := make([]legacyGoalieSeason, len(rows))
	for index, row := range rows {
		legacy[index] = legacyGoalieSeason{
			PlayerID: row.PlayerID, TeamID: row.TeamID, Season: row.Season, GamesPlayed: row.GamesPlayed,
			GamesStarted: row.GamesStarted, TOISeconds: row.TOISeconds, Wins: row.Wins, Shutouts: row.Shutouts,
			ShotsAgainst: row.ShotsAgainst, Saves: row.Saves, GoalsAgainst: row.GoalsAgainst,
		}
	}
	return encodedValues(legacy)
}

func inputDataHash(input Input, modelVersion string) (string, error) {
	skaters, err := encodedSkaterHashRows(input.Skaters, modelVersion)
	if err != nil {
		return "", err
	}
	goalies, err := encodedGoalieHashRows(input.Goalies, modelVersion)
	if err != nil {
		return "", err
	}
	return hashInputParts(input, skaters, goalies)
}

func hashInputParts(input Input, skaters, goalies []string) (string, error) {
	sources, err := encodeSourceRows(input)
	if err != nil {
		return "", err
	}
	canonical := struct {
		TargetSeason      int
		AsOf              time.Time
		SourceMaxGameDate time.Time
		Skaters           []string
		Goalies           []string
		PlayerPool        []string
		Overrides         []string
		// omitempty keeps the hash of an input without team environment
		// data identical to the one earlier versions computed.
		TeamSeasons       []string `json:",omitempty"`
		TargetTeams       []string `json:",omitempty"`
		SkaterClubSeasons []string `json:",omitempty"`
		GoalieStarts      []string `json:",omitempty"`
	}{
		TargetSeason: input.TargetSeason, AsOf: input.AsOf.UTC(),
		SourceMaxGameDate: input.SourceMaxGameDate.UTC(), Skaters: skaters, Goalies: goalies,
		PlayerPool: sources.playerPool, Overrides: sources.overrides,
		TeamSeasons:       sources.teamSeasons,
		TargetTeams:       sources.targetTeams,
		SkaterClubSeasons: sources.skaterClubSeasons,
		GoalieStarts:      sources.goalieStarts,
	}
	hash, err := canonhash.JSON(canonical)
	if err != nil {
		return "", fmt.Errorf("hash projection source data: %w", err)
	}
	return hash, nil
}

// encodedSourceRows are the sorted encodings of an input's non-history rows.
type encodedSourceRows struct {
	playerPool, overrides, teamSeasons, targetTeams, skaterClubSeasons, goalieStarts []string
}

func encodeSourceRows(input Input) (encodedSourceRows, error) {
	var rows encodedSourceRows
	var err error
	if rows.playerPool, err = encodedValues(input.PlayerPool); err != nil {
		return rows, err
	}
	if rows.overrides, err = encodedValues(input.Overrides); err != nil {
		return rows, err
	}
	if rows.teamSeasons, err = encodedValues(input.TeamSeasons); err != nil {
		return rows, err
	}
	if rows.targetTeams, err = encodedValues(input.TargetTeams); err != nil {
		return rows, err
	}
	if rows.skaterClubSeasons, err = encodedValues(input.SkaterClubSeasons); err != nil {
		return rows, err
	}
	rows.goalieStarts, err = encodedValues(input.GoalieStarts)
	return rows, err
}

// encodedValues encodes each value as JSON and sorts the encodings, so the
// hash does not depend on row order.
func encodedValues[T any](values []T) ([]string, error) {
	encoded := make([]string, 0, len(values))
	for _, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode projection source row %T: %w", value, err)
		}
		encoded = append(encoded, string(data))
	}
	slices.Sort(encoded)
	return encoded, nil
}
