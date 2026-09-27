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
	players, err := includePlayerPool(players, input.PlayerPool, input.AsOf, cfg.MaximumUncertainty)
	if err != nil {
		return Snapshot{}, err
	}
	for i := range players {
		players[i].SourceAsOf = input.AsOf.UTC()
	}
	players, err = applyOverrides(players, input.Overrides, input.AsOf)
	if err != nil {
		return Snapshot{}, err
	}
	for i := range players {
		players[i].MissingStats = missingStats(cfg.ModelVersion, players[i].Kind, players[i].Values)
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

func includePlayerPool(
	players []PlayerProjection,
	pool []PoolPlayer,
	asOf time.Time,
	maximumUncertainty float64,
) ([]PlayerProjection, error) {
	merger := newPoolMerger(players, len(pool), asOf, maximumUncertainty)
	for _, candidate := range pool {
		if err := merger.add(candidate); err != nil {
			return nil, err
		}
	}
	return merger.players(), nil
}

type poolMerger struct {
	byKey                map[string]PlayerProjection
	seenPool             map[string]PoolPlayer
	historyKeyByPlayerID map[int64]string
	poolKeyByPlayerID    map[int64]string
	asOf                 time.Time
	maximumUncertainty   float64
}

func newPoolMerger(players []PlayerProjection, poolSize int, asOf time.Time, maximumUncertainty float64) *poolMerger {
	merger := &poolMerger{
		byKey: make(map[string]PlayerProjection, len(players)+poolSize), seenPool: make(map[string]PoolPlayer, poolSize),
		historyKeyByPlayerID: make(map[int64]string, len(players)), poolKeyByPlayerID: make(map[int64]string, poolSize),
		asOf: asOf, maximumUncertainty: maximumUncertainty,
	}
	for _, player := range players {
		merger.byKey[player.PlayerKey] = player
		if player.PlayerID != nil {
			merger.historyKeyByPlayerID[*player.PlayerID] = player.PlayerKey
		}
	}
	return merger
}

func (m *poolMerger) add(candidate PoolPlayer) error {
	if err := validatePoolPlayer(candidate); err != nil {
		return err
	}
	if previous, exists := m.seenPool[candidate.PlayerKey]; exists {
		if !samePoolPlayer(previous, candidate) {
			return fmt.Errorf("pool player %q has conflicting entries", candidate.PlayerKey)
		}
		return nil
	}
	m.seenPool[candidate.PlayerKey] = candidate
	if candidate.PlayerID != nil {
		if key, exists := m.poolKeyByPlayerID[*candidate.PlayerID]; exists && key != candidate.PlayerKey {
			return fmt.Errorf("NHL player %d has conflicting pool keys %q and %q", *candidate.PlayerID, key, candidate.PlayerKey)
		}
		m.poolKeyByPlayerID[*candidate.PlayerID] = candidate.PlayerKey
	}
	player, exists := m.find(candidate)
	if exists {
		if player.PlayerID != nil && candidate.PlayerID != nil && *player.PlayerID != *candidate.PlayerID {
			return fmt.Errorf(
				"pool player %q NHL ID %d conflicts with historical NHL ID %d",
				candidate.PlayerKey, *candidate.PlayerID, *player.PlayerID,
			)
		}
		if player.Kind != candidate.Kind {
			return fmt.Errorf("pool player %q kind %q conflicts with historical kind %q", candidate.PlayerKey, candidate.Kind, player.Kind)
		}
		player.PlayerKey = candidate.PlayerKey
		if candidate.PlayerID != nil {
			player.PlayerID = candidate.PlayerID
		}
		if candidate.TeamID != nil {
			player.TeamID = candidate.TeamID
		}
		player.Position = candidate.Position
		m.byKey[candidate.PlayerKey] = player
		return nil
	}
	m.byKey[candidate.PlayerKey] = PlayerProjection{
		PlayerKey: candidate.PlayerKey, PlayerID: candidate.PlayerID, TeamID: candidate.TeamID,
		Kind: candidate.Kind, Position: candidate.Position, Source: SourceInternal,
		SourceAsOf: m.asOf.UTC(), Uncertainty: m.maximumUncertainty, InsufficientHistory: true,
		Values: make(map[Stat]Estimate),
	}
	return nil
}

func (m *poolMerger) find(candidate PoolPlayer) (PlayerProjection, bool) {
	if player, exists := m.byKey[candidate.PlayerKey]; exists {
		return player, true
	}
	if candidate.PlayerID == nil {
		return PlayerProjection{}, false
	}
	historyKey, exists := m.historyKeyByPlayerID[*candidate.PlayerID]
	if !exists {
		return PlayerProjection{}, false
	}
	player := m.byKey[historyKey]
	delete(m.byKey, historyKey)
	return player, true
}

func (m *poolMerger) players() []PlayerProjection {
	out := make([]PlayerProjection, 0, len(m.byKey))
	for _, player := range m.byKey {
		out = append(out, player)
	}
	return out
}

func samePoolPlayer(a, b PoolPlayer) bool {
	return a.PlayerKey == b.PlayerKey && optionalIDsEqual(a.PlayerID, b.PlayerID) &&
		optionalIDsEqual(a.TeamID, b.TeamID) && a.Kind == b.Kind && a.Position == b.Position
}

func optionalIDsEqual(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func validatePoolPlayer(player PoolPlayer) error {
	switch {
	case player.PlayerKey == "":
		return fmt.Errorf("pool player key is required")
	case player.Kind != PlayerKindSkater && player.Kind != PlayerKindGoalie:
		return fmt.Errorf("pool player %q has invalid player kind %q", player.PlayerKey, player.Kind)
	case player.Position == "":
		return fmt.Errorf("pool player %q position is required", player.PlayerKey)
	}
	return nil
}

func applyOverrides(players []PlayerProjection, overrides []Override, snapshotAsOf time.Time) ([]PlayerProjection, error) {
	byKey := make(map[string]PlayerProjection, len(players)+len(overrides))
	seenOverrides := make(map[string]struct{}, len(overrides))
	for _, player := range players {
		byKey[player.PlayerKey] = player
	}
	for _, override := range overrides {
		if _, exists := seenOverrides[override.PlayerKey]; exists {
			return nil, fmt.Errorf("duplicate override for player %q", override.PlayerKey)
		}
		seenOverrides[override.PlayerKey] = struct{}{}
		if err := validateOverride(override, snapshotAsOf); err != nil {
			return nil, err
		}
		if existing, exists := byKey[override.PlayerKey]; exists && existing.Kind != override.Kind {
			return nil, fmt.Errorf("override %q kind %q conflicts with existing kind %q", override.PlayerKey, override.Kind, existing.Kind)
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

func missingStats(modelVersion string, kind PlayerKind, values map[Stat]Estimate) []Stat {
	supported := supportedStats(kind)
	missing := make([]Stat, 0, len(supported))
	for _, stat := range supported {
		if !supportsFaceoffs(modelVersion) && (stat == StatFaceoffsWon || stat == StatFaceoffsLost) {
			continue
		}
		if _, exists := values[stat]; !exists {
			missing = append(missing, stat)
		}
	}
	return missing
}

func supportedStats(kind PlayerKind) []Stat {
	if kind == PlayerKindSkater {
		return []Stat{
			StatGamesPlayed, StatTOISeconds, StatGoals, StatAssists, StatPoints,
			StatPlusMinus, StatPenaltyMinutes, StatPowerPlayPoints, StatShotsOnGoal,
			StatHits, StatBlockedShots, StatFaceoffsWon, StatFaceoffsLost,
		}
	}
	if kind == PlayerKindGoalie {
		return []Stat{
			StatGamesPlayed, StatGamesStarted, StatTOISeconds, StatWins, StatShutouts,
			StatShotsAgainst, StatSaves, StatGoalsAgainst, StatSavePercentage,
			StatGoalsAgainstAvg,
		}
	}
	return nil
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
	return slices.Contains(supportedStats(kind), stat)
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
