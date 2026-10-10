package newsadjust

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// OverrideKind is what a manager override replaces.
type OverrideKind string

const (
	// OverrideMissedGames replaces the player's total missed games.
	OverrideMissedGames OverrideKind = "missed_games"
	// OverrideInput replaces one projection input (see Input).
	OverrideInput OverrideKind = "input"
	// OverrideExcludeEvent keeps one event from changing any projection.
	OverrideExcludeEvent OverrideKind = "exclude_event"
)

// Input is a projection input an override can set.
type Input string

const (
	// InputGamesPlayed is the expected games played for the season.
	InputGamesPlayed Input = "games_played"
	// InputGamesStarted is a goalie's expected starts for the season.
	InputGamesStarted Input = "games_started"
	// InputTOIPerGame is a skater's expected ice time per game, in seconds.
	InputTOIPerGame Input = "toi_per_game"
	// InputPowerPlayFactor multiplies a skater's power-play production.
	InputPowerPlayFactor Input = "power_play_factor"
)

var (
	overrideKinds = []OverrideKind{OverrideMissedGames, OverrideInput, OverrideExcludeEvent}
	inputs        = []Input{InputGamesPlayed, InputGamesStarted, InputTOIPerGame, InputPowerPlayFactor}
)

// Override is a manager's replacement of an assumption. It is never
// deleted: a reset or expiry ends it while keeping the record, and the
// adjustment keeps the value it replaced. An empty LeagueKey applies to
// every league and an empty Scenario to every scenario.
type Override struct {
	ID          string       `json:"id"`
	PlayerKey   string       `json:"player_key"`
	LeagueKey   string       `json:"league_key,omitempty"`
	Kind        OverrideKind `json:"kind"`
	EventID     string       `json:"event_id,omitempty"`
	Scenario    Scenario     `json:"scenario,omitempty"`
	Input       Input        `json:"input,omitempty"`
	Value       float64      `json:"value"`
	Reason      string       `json:"reason"`
	CreatedBy   string       `json:"created_by,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	ExpiresAt   time.Time    `json:"expires_at,omitzero"`
	ResetAt     time.Time    `json:"reset_at,omitzero"`
	ResetReason string       `json:"reset_reason,omitempty"`
}

// Validate checks the override's shape; player-kind checks happen when it
// is applied to a projection.
func (o Override) Validate() error {
	switch {
	case o.ID == "" || o.PlayerKey == "":
		return fmt.Errorf("override needs an ID and a player key")
	case strings.TrimSpace(o.Reason) == "":
		return fmt.Errorf("override %s needs a reason", o.ID)
	case o.CreatedAt.IsZero():
		return fmt.Errorf("override %s needs a creation time", o.ID)
	case !slices.Contains(overrideKinds, o.Kind):
		return fmt.Errorf("override %s has unknown kind %q", o.ID, o.Kind)
	case o.Scenario != "" && !slices.Contains(Scenarios, o.Scenario):
		return fmt.Errorf("override %s has unknown scenario %q", o.ID, o.Scenario)
	case !o.ExpiresAt.IsZero() && !o.ExpiresAt.After(o.CreatedAt):
		return fmt.Errorf("override %s expires before it starts", o.ID)
	case !o.ResetAt.IsZero() && o.ResetAt.Before(o.CreatedAt):
		return fmt.Errorf("override %s is reset before it starts", o.ID)
	case math.IsNaN(o.Value) || math.IsInf(o.Value, 0) || o.Value < 0:
		return fmt.Errorf("override %s value must be finite and nonnegative", o.ID)
	}
	return o.validateTarget()
}

func (o Override) validateTarget() error {
	switch o.Kind {
	case OverrideExcludeEvent:
		if o.EventID == "" || o.Input != "" || o.Value != 0 {
			return fmt.Errorf("exclusion %s names an event and nothing else", o.ID)
		}
	case OverrideMissedGames:
		if o.EventID != "" || o.Input != "" {
			return fmt.Errorf("missed-games override %s applies to the player's total", o.ID)
		}
	case OverrideInput:
		if o.EventID != "" || !slices.Contains(inputs, o.Input) {
			return fmt.Errorf("input override %s needs a known input", o.ID)
		}
	}
	return nil
}

// activeAt reports whether the override applies to a league at a time.
func (o Override) activeAt(asOf time.Time, leagueKey string) bool {
	switch {
	case o.CreatedAt.After(asOf):
		return false
	case !o.ExpiresAt.IsZero() && !asOf.Before(o.ExpiresAt):
		return false
	case !o.ResetAt.IsZero() && !asOf.Before(o.ResetAt):
		return false
	}
	return o.LeagueKey == "" || o.LeagueKey == leagueKey
}

func (o Override) appliesTo(s Scenario) bool {
	return o.Scenario == "" || o.Scenario == s
}

// target is what an override replaces; two active overrides with the same
// target compete and only the most specific, then newest, applies.
func (o Override) target() string {
	return overrideTarget(o.PlayerKey, o.Kind, o.EventID, o.Input)
}

func overrideTarget(playerKey string, kind OverrideKind, eventID string, input Input) string {
	return strings.Join([]string{playerKey, string(kind), eventID, string(input)}, "|")
}

// matchOverride returns the override that decides a target in one
// scenario: the first match in overrides, which activeOverrides orders most
// specific first. Every override kind is resolved through it, so league,
// player, event and scenario scope mean the same thing for each.
func matchOverride(overrides []Override, target string, s Scenario) (Override, bool) {
	for _, o := range overrides {
		if o.target() == target && o.appliesTo(s) {
			return o, true
		}
	}
	return Override{}, false
}

// activeOverrides returns the overrides in force, most specific first, and
// the ones another override with the same target and scenario shadows.
func activeOverrides(overrides []Override, asOf time.Time, leagueKey string) (active, shadowed []Override) {
	candidates := make([]Override, 0, len(overrides))
	for _, o := range overrides {
		if o.activeAt(asOf, leagueKey) {
			candidates = append(candidates, o.knownAt())
		}
	}
	slices.SortFunc(candidates, compareOverrideSpecificity)
	seen := make(map[string]bool, len(candidates))
	for _, o := range candidates {
		if fullyShadowed(o, seen) {
			shadowed = append(shadowed, o)
			continue
		}
		for _, s := range Scenarios {
			if o.appliesTo(s) {
				seen[o.target()+"|"+string(s)] = true
			}
		}
		active = append(active, o)
	}
	return active, shadowed
}

// fullyShadowed reports whether every scenario the override applies to is
// already taken by a more specific override.
func fullyShadowed(o Override, seen map[string]bool) bool {
	for _, s := range Scenarios {
		if o.appliesTo(s) && !seen[o.target()+"|"+string(s)] {
			return false
		}
	}
	return true
}

// compareOverrideSpecificity orders league-scoped before global,
// scenario-scoped before all-scenario, then newest first, then by ID.
func compareOverrideSpecificity(a, b Override) int {
	if c := cmp.Compare(boolRank(a.LeagueKey == ""), boolRank(b.LeagueKey == "")); c != 0 {
		return c
	}
	if c := cmp.Compare(boolRank(a.Scenario == ""), boolRank(b.Scenario == "")); c != 0 {
		return c
	}
	if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func boolRank(value bool) int {
	if value {
		return 1
	}
	return 0
}

// knownAt returns an active override as it stood at the as-of time: times
// in UTC and without a reset that only happened later, so resetting an
// override does not change the identity of earlier adjustments.
func (o Override) knownAt() Override {
	o.CreatedAt, o.ExpiresAt = utc(o.CreatedAt), utc(o.ExpiresAt)
	o.ResetAt, o.ResetReason = time.Time{}, ""
	return o
}
