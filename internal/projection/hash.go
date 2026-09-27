package projection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

func configHash(cfg Config) string {
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
		return hashValue(historical)
	}
	return hashValue(cfg)
}

func sourceDataHash(cfg Config, input Input) string {
	input.Skaters = slices.DeleteFunc(slices.Clone(input.Skaters), func(row SkaterSeason) bool {
		age, ok := seasonAge(input.TargetSeason, row.Season)
		return !ok || age >= cfg.LookbackSeasons || row.GamesPlayed <= 0
	})
	input.Goalies = slices.DeleteFunc(slices.Clone(input.Goalies), func(row GoalieSeason) bool {
		age, ok := seasonAge(input.TargetSeason, row.Season)
		return !ok || age >= cfg.LookbackSeasons || row.GamesPlayed <= 0
	})
	switch cfg.ModelVersion {
	case LegacyModelVersion:
		return legacyInputDataHash(input)
	case FaceoffModelVersion:
		return faceoffInputDataHash(input)
	}
	return inputDataHash(input)
}

func evaluationDataHash(cfg Config, input Input) string {
	lowerSeason := historyFloorSeason(input.TargetSeason, cfg.LookbackSeasons)
	input.Skaters = slices.DeleteFunc(slices.Clone(input.Skaters), func(row SkaterSeason) bool {
		return row.Season < lowerSeason || row.Season > input.TargetSeason
	})
	input.Goalies = slices.DeleteFunc(slices.Clone(input.Goalies), func(row GoalieSeason) bool {
		return row.Season < lowerSeason || row.Season > input.TargetSeason
	})
	switch cfg.ModelVersion {
	case LegacyModelVersion:
		return legacyInputDataHash(input)
	case FaceoffModelVersion:
		return faceoffInputDataHash(input)
	}
	return inputDataHash(input)
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
	FaceoffsWon     int
	FaceoffsLost    int
}

func legacyInputDataHash(input Input) string {
	legacySkaters := make([]legacySkaterSeason, 0, len(input.Skaters))
	for _, row := range input.Skaters {
		legacySkaters = append(legacySkaters, legacySkaterSeason{
			PlayerID: row.PlayerID, TeamID: row.TeamID, Season: row.Season, Position: row.Position,
			GamesPlayed: row.GamesPlayed, TOISeconds: row.TOISeconds, Goals: row.Goals, Assists: row.Assists,
			PlusMinus: row.PlusMinus, PenaltyMinutes: row.PenaltyMinutes,
			PowerPlayPoints: row.PowerPlayPoints, ShotsOnGoal: row.ShotsOnGoal,
			Hits: row.Hits, BlockedShots: row.BlockedShots,
		})
	}
	return hashInputParts(input, encodedValues(legacySkaters))
}

func faceoffInputDataHash(input Input) string {
	faceoffSkaters := make([]faceoffSkaterSeason, 0, len(input.Skaters))
	for _, row := range input.Skaters {
		faceoffSkaters = append(faceoffSkaters, faceoffSkaterSeason{
			PlayerID: row.PlayerID, TeamID: row.TeamID, Season: row.Season, Position: row.Position,
			GamesPlayed: row.GamesPlayed, TOISeconds: row.TOISeconds, Goals: row.Goals, Assists: row.Assists,
			PlusMinus: row.PlusMinus, PenaltyMinutes: row.PenaltyMinutes,
			PowerPlayPoints: row.PowerPlayPoints, ShotsOnGoal: row.ShotsOnGoal,
			Hits: row.Hits, BlockedShots: row.BlockedShots,
			FaceoffsWon: row.FaceoffsWon, FaceoffsLost: row.FaceoffsLost,
		})
	}
	return hashInputParts(input, encodedValues(faceoffSkaters))
}

func inputDataHash(input Input) string {
	return hashInputParts(input, encodedValues(input.Skaters))
}

func hashInputParts(input Input, skaters []string) string {
	canonical := struct {
		TargetSeason      int
		AsOf              time.Time
		SourceMaxGameDate time.Time
		Skaters           []string
		Goalies           []string
		PlayerPool        []string
		Overrides         []string
	}{
		TargetSeason: input.TargetSeason, AsOf: input.AsOf.UTC(),
		SourceMaxGameDate: input.SourceMaxGameDate.UTC(),
		Skaters:           skaters,
		Goalies:           encodedValues(input.Goalies),
		PlayerPool:        encodedValues(input.PlayerPool),
		Overrides:         encodedValues(input.Overrides),
	}
	return hashValue(canonical)
}

func encodedValues[T any](values []T) []string {
	encoded := make([]string, 0, len(values))
	for _, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			panic(fmt.Sprintf("marshal projection source value: %v", err))
		}
		encoded = append(encoded, string(data))
	}
	slices.Sort(encoded)
	return encoded
}

func hashValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal projection hash input: %v", err))
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
