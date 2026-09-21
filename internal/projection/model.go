package projection

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"
)

const (
	secondsPerMinute        = 60
	secondsPerHour          = 3_600
	typicalSkaterTOIPerGame = 15 * secondsPerMinute
)

func Generate(cfg Config, input Input) (Snapshot, error) {
	if err := cfg.Validate(); err != nil {
		return Snapshot{}, err
	}
	if input.TargetSeason <= 0 {
		return Snapshot{}, fmt.Errorf("target season must be positive")
	}
	if input.AsOf.IsZero() {
		return Snapshot{}, fmt.Errorf("as-of time is required")
	}

	players := projectSkaters(cfg, input.TargetSeason, input.Skaters)
	players = append(players, projectGoalies(cfg, input.TargetSeason, input.Goalies)...)
	for i := range players {
		players[i].SourceAsOf = input.AsOf.UTC()
	}
	players, err := applyOverrides(players, input.Overrides, input.AsOf)
	if err != nil {
		return Snapshot{}, err
	}
	slices.SortFunc(players, func(a, b PlayerProjection) int {
		return cmp.Compare(a.PlayerKey, b.PlayerKey)
	})

	return Snapshot{
		TargetSeason:      input.TargetSeason,
		AsOf:              input.AsOf.UTC(),
		SourceMaxGameDate: input.SourceMaxGameDate,
		SourceDataHash:    sourceDataHash(cfg, input),
		Config:            cfg,
		Players:           players,
	}, nil
}

func applyOverrides(players []PlayerProjection, overrides []Override, snapshotAsOf time.Time) ([]PlayerProjection, error) {
	byKey := make(map[string]PlayerProjection, len(players)+len(overrides))
	for _, player := range players {
		byKey[player.PlayerKey] = player
	}
	for _, override := range overrides {
		if err := validateOverride(override, snapshotAsOf); err != nil {
			return nil, err
		}
		values := make(map[Stat]Estimate, len(override.Values))
		for stat, estimate := range override.Values {
			values[stat] = normalizeEstimate(estimate, stat != StatPlusMinus)
		}
		sourceAsOf := override.SourceAsOf
		if sourceAsOf.IsZero() {
			sourceAsOf = snapshotAsOf
		}
		byKey[override.PlayerKey] = PlayerProjection{
			PlayerKey:               override.PlayerKey,
			PlayerID:                override.PlayerID,
			TeamID:                  override.TeamID,
			Kind:                    override.Kind,
			Position:                override.Position,
			Source:                  override.Source,
			Provider:                override.Provider,
			ProviderVersion:         override.ProviderVersion,
			SourceAsOf:              sourceAsOf.UTC(),
			IncorporatesNewsThrough: override.IncorporatesNewsThrough.UTC(),
			InsufficientHistory:     false,
			Values:                  values,
		}
	}
	out := make([]PlayerProjection, 0, len(byKey))
	for _, player := range byKey {
		out = append(out, player)
	}
	return out, nil
}

func validateOverride(override Override, snapshotAsOf time.Time) error {
	switch {
	case override.PlayerKey == "":
		return fmt.Errorf("override player key is required")
	case override.Kind != PlayerKindSkater && override.Kind != PlayerKindGoalie:
		return fmt.Errorf("override %q has invalid player kind %q", override.PlayerKey, override.Kind)
	case override.Source != SourceImported && override.Source != SourceManual:
		return fmt.Errorf("override %q must be imported or manual", override.PlayerKey)
	case len(override.Values) == 0:
		return fmt.Errorf("override %q has no projected values", override.PlayerKey)
	case override.Source == SourceImported && override.Provider == "":
		return fmt.Errorf("imported override %q requires a provider", override.PlayerKey)
	case override.Source == SourceImported && override.ProviderVersion == "":
		return fmt.Errorf("imported override %q requires a provider version", override.PlayerKey)
	case override.Source == SourceImported && override.SourceAsOf.IsZero():
		return fmt.Errorf("imported override %q requires an as-of time", override.PlayerKey)
	case override.Position == "":
		return fmt.Errorf("override %q position is required", override.PlayerKey)
	case override.SourceAsOf.After(snapshotAsOf):
		return fmt.Errorf("override %q source as-of is after the snapshot cutoff", override.PlayerKey)
	case override.IncorporatesNewsThrough.After(snapshotAsOf):
		return fmt.Errorf("override %q incorporates news after the snapshot cutoff", override.PlayerKey)
	}
	for stat, value := range override.Values {
		if !supportsStat(override.Kind, stat) {
			return fmt.Errorf("override %q has unsupported %s stat %q", override.PlayerKey, override.Kind, stat)
		}
		if !finite(value.Mean) || !finite(value.Low) || !finite(value.High) {
			return fmt.Errorf("override %q stat %q has a non-finite estimate", override.PlayerKey, stat)
		}
	}
	return nil
}

func supportsStat(kind PlayerKind, stat Stat) bool {
	skater := stat == StatGamesPlayed || stat == StatTOISeconds || stat == StatGoals ||
		stat == StatAssists || stat == StatPoints || stat == StatPlusMinus ||
		stat == StatPenaltyMinutes || stat == StatPowerPlayPoints || stat == StatShotsOnGoal ||
		stat == StatHits || stat == StatBlockedShots
	goalie := stat == StatGamesPlayed || stat == StatGamesStarted || stat == StatTOISeconds ||
		stat == StatWins || stat == StatShutouts || stat == StatShotsAgainst || stat == StatSaves ||
		stat == StatGoalsAgainst || stat == StatSavePercentage || stat == StatGoalsAgainstAvg
	return kind == PlayerKindSkater && skater || kind == PlayerKindGoalie && goalie
}

func seasonAge(targetSeason, historySeason int) (int, bool) {
	targetStart := seasonStartYear(targetSeason)
	historyStart := seasonStartYear(historySeason)
	age := targetStart - historyStart - 1
	return age, age >= 0
}

func seasonStartYear(season int) int {
	if season >= 10_000 {
		return season / 10_000
	}
	return season
}

func seasonWeight(decay float64, age int) float64 {
	return math.Pow(decay, float64(age))
}

func clamp(value, low, high float64) float64 {
	return math.Min(math.Max(value, low), high)
}

func uncertainty(cfg Config, effectiveSamples float64) float64 {
	if effectiveSamples <= 0 {
		return cfg.MaximumUncertainty
	}
	return clamp(cfg.IntervalZ/math.Sqrt(effectiveSamples), cfg.MinimumUncertainty, cfg.MaximumUncertainty)
}

func estimate(mean, fraction float64, nonNegative bool) Estimate {
	spread := math.Abs(mean) * fraction
	if mean == 0 {
		spread = fraction
	}
	low := mean - spread
	if nonNegative {
		low = math.Max(0, low)
	}
	return normalizeEstimate(Estimate{Mean: mean, Low: low, High: mean + spread}, nonNegative)
}

func cappedEstimate(mean, fraction, maximum float64) Estimate {
	value := estimate(mean, fraction, true)
	value.High = math.Min(value.High, maximum)
	return normalizeEstimate(value, true)
}

func normalizeEstimate(value Estimate, nonNegative bool) Estimate {
	if value.Low > value.High {
		value.Low, value.High = value.High, value.Low
	}
	value.Mean = clamp(value.Mean, value.Low, value.High)
	if nonNegative {
		value.Low = math.Max(0, value.Low)
		value.Mean = math.Max(0, value.Mean)
		value.High = math.Max(0, value.High)
	}
	return value
}

func playerKey(playerID int64) string {
	return fmt.Sprintf("nhl:%d", playerID)
}

func playerIDPointer(playerID int64) *int64 {
	value := playerID
	return &value
}

func optionalIDPointer(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return playerIDPointer(id)
}
