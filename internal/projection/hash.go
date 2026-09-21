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
	return hashValue(cfg)
}

func sourceDataHash(cfg Config, input Input) string {
	input.Skaters = slices.DeleteFunc(slices.Clone(input.Skaters), func(row SkaterSeason) bool {
		age, ok := seasonAge(input.TargetSeason, row.Season)
		return !ok || age >= cfg.LookbackSeasons || row.GamesPlayed <= 0 || row.TOISeconds <= 0
	})
	input.Goalies = slices.DeleteFunc(slices.Clone(input.Goalies), func(row GoalieSeason) bool {
		age, ok := seasonAge(input.TargetSeason, row.Season)
		return !ok || age >= cfg.LookbackSeasons || row.GamesPlayed <= 0
	})
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
	return inputDataHash(input)
}

func inputDataHash(input Input) string {
	canonical := struct {
		TargetSeason      int
		AsOf              time.Time
		SourceMaxGameDate time.Time
		Skaters           []string
		Goalies           []string
		Overrides         []string
	}{
		TargetSeason: input.TargetSeason, AsOf: input.AsOf.UTC(),
		SourceMaxGameDate: input.SourceMaxGameDate.UTC(),
		Skaters:           encodedValues(input.Skaters),
		Goalies:           encodedValues(input.Goalies),
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
