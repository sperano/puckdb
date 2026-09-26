package newsadjust

import (
	"fmt"
	"math"
	"slices"

	"github.com/sperano/puckdb/internal/projection"
)

// Effect is what a player's news does to projection inputs under one
// scenario. Factors multiply the baseline; 1 means unchanged.
type Effect struct {
	// MissedGames is the union of absences, in team games.
	MissedGames float64 `json:"missed_games"`
	// Availability is the share of the season the player is available.
	Availability float64 `json:"availability"`
	// GamesFactor scales games played and every per-game total. It equals
	// Availability unless an input override sets games directly.
	GamesFactor float64 `json:"games_factor"`
	// IceTime scales a skater's ice time per game and its counting stats.
	IceTime float64 `json:"ice_time_factor"`
	// PowerPlay scales a skater's power-play production.
	PowerPlay float64 `json:"power_play_factor"`
	// GoalieStarts scales a goalie's starts for a reported role.
	GoalieStarts float64 `json:"goalie_starts_factor"`
	// TeamID is the new team of a reported trade.
	TeamID *int64 `json:"team_id,omitempty"`
}

func neutralEffect() Effect {
	return Effect{Availability: noChange, GamesFactor: noChange, IceTime: noChange, PowerPlay: noChange, GoalieStarts: noChange}
}

// effectInputs is everything computeEffect reads for one player.
type effectInputs struct {
	player    projection.PlayerProjection
	claims    *playerClaims
	overrides []Override
	season    Season
	policy    Policy
}

// effectResult is one scenario's effect with its audit trail.
type effectResult struct {
	effect      Effect
	applied     []AppliedOverride
	assumptions []string
	alerts      []string
}

func computeEffect(in effectInputs, s Scenario) effectResult {
	result := effectResult{effect: neutralEffect()}
	if in.claims != nil {
		result.effect.MissedGames = in.missedGames(s, &result)
		for _, claim := range in.claims.roles {
			in.applyRole(claim, s, &result)
		}
	}
	for _, o := range in.overrides {
		if o.Kind == OverrideMissedGames && o.appliesTo(s) {
			result.applied = append(result.applied, AppliedOverride{Override: o, Scenario: s, Original: result.effect.MissedGames, Value: o.Value})
			result.effect.MissedGames = math.Min(o.Value, in.season.Games)
			break
		}
	}
	result.effect.Availability = math.Max(0, in.season.Games-result.effect.MissedGames) / in.season.Games
	result.effect.GamesFactor = result.effect.Availability
	in.applyInputOverrides(s, &result)
	return result
}

// missedGames unions the absences of every availability claim.
func (in effectInputs) missedGames(s Scenario, result *effectResult) float64 {
	var spans []interval
	for _, claim := range in.claims.availability {
		span, ok := in.claimInterval(claim, s, result)
		if ok {
			spans = append(spans, span)
		}
	}
	return unionLength(spans)
}

// claimInterval is a claim's absence in one scenario. Conservative takes
// the longest member, optimistic the shortest, base the primary report.
func (in effectInputs) claimInterval(claim availabilityClaim, s Scenario, result *effectResult) (interval, bool) {
	var chosen interval
	found := false
	for _, m := range claim.members {
		if !slices.Contains(m.scenarios, s) {
			continue
		}
		span := in.memberInterval(m.event, claim.closed, s, result)
		switch {
		case !found:
			chosen, found = span, true
		case s == ScenarioConservative && span.length() > chosen.length():
			chosen = span
		case s == ScenarioOptimistic && span.length() < chosen.length():
			chosen = span
		}
		if s == ScenarioBase {
			break
		}
	}
	return chosen, found
}

func (in effectInputs) memberInterval(e Event, closed closure, s Scenario, result *effectResult) interval {
	from := in.season.position(e.start())
	var to float64
	switch {
	case closed.by != "":
		to = in.season.position(closed.at)
	case !e.EffectiveUntil.IsZero():
		to = in.season.position(e.EffectiveUntil)
	case e.Duration.Kind == DurationGames:
		to = from + float64(e.Duration.Games)
		if e.start().Before(in.season.Start) {
			result.addAssumption("an attributed game count is served from the season start when it takes effect earlier; games served before the season are not deducted until a report says so")
		}
	case e.Duration.Kind == DurationSeason && in.season.priorSeason(e.start(), in.policy.OffseasonDays):
		to = from
		result.addAssumption(priorSeasonEnded)
	case e.Duration.Kind == DurationSeason:
		to = in.season.Games
	case e.Type != EventSuspension && in.season.priorSeason(e.start(), in.policy.OffseasonDays) && s != ScenarioConservative:
		to = from
		result.addAssumption(priorSeasonStale)
	default:
		r := in.policy.MissedGames[e.Duration.Kind]
		to = from + r.at(s)
		result.addAssumption(fmt.Sprintf("%s absence: %.0f / %.0f / %.0f missed games (conservative / base / optimistic; %s)",
			e.Duration.Kind, r.Conservative, r.Base, r.Optimistic, in.policy.Calibration))
	}
	return interval{From: from, To: math.Min(math.Max(to, from), in.season.Games)}
}

// applyRole folds one role claim into the effect. Each field's change is
// weighted by the share of the season its segment covers and added to the
// factor, so consecutive segments of one field blend instead of stacking.
func (in effectInputs) applyRole(claim roleClaim, s Scenario, result *effectResult) {
	for _, field := range claim.fields {
		coverage := in.roleCoverage(claim, field)
		switch field {
		case fieldTeam:
			if coverage > 0 {
				result.effect.TeamID = claim.event.Role.TeamID
				result.addAssumption("a team change keeps per-game rates; team context beyond the reported role is not modeled")
			}
		case fieldIceTime:
			in.applyMultiplier(&result.effect.IceTime, directionRange(in.policy.IceTimeUp, in.policy.IceTimeDown, claim.event.Role.IceTime), s, coverage, "ice time", result)
		case fieldPowerPlay:
			in.applyMultiplier(&result.effect.PowerPlay, directionRange(in.policy.PowerPlayUp, in.policy.PowerPlayDown, claim.event.Role.PowerPlay), s, coverage, "power-play", result)
		case fieldGoalieRole:
			in.applyGoalieRole(claim.event.Role.GoalieRole, s, coverage, result)
		}
	}
}

func directionRange(up, down Range, direction Direction) Range {
	if direction == DirectionUp {
		return up
	}
	return down
}

func (in effectInputs) applyMultiplier(factor *float64, r Range, s Scenario, coverage float64, label string, result *effectResult) {
	if in.player.Kind != projection.PlayerKindSkater {
		result.addAlert(fmt.Sprintf("%s role reported for a goalie is not modeled", label))
		return
	}
	*factor += (r.at(s) - noChange) * coverage
	result.addAssumption(fmt.Sprintf("%s role change: x%.2f / x%.2f / x%.2f per game (conservative / base / optimistic; %s)",
		label, r.Conservative, r.Base, r.Optimistic, in.policy.Calibration))
}

func (in effectInputs) applyGoalieRole(role GoalieRole, s Scenario, coverage float64, result *effectResult) {
	starts := in.player.Values[projection.StatGamesStarted].Mean
	switch {
	case in.player.Kind != projection.PlayerKindGoalie:
		result.addAlert("goalie role reported for a skater is ignored")
		return
	case starts <= 0:
		result.addAlert("goalie role change needs baseline starts; set a games_started override instead")
		return
	}
	r := in.policy.GoalieStartShare[role]
	target := r.at(s) * in.season.Games
	result.effect.GoalieStarts += (target/starts - noChange) * coverage
	result.addAssumption(fmt.Sprintf("%s goalie starts %.0f%% / %.0f%% / %.0f%% of team games (conservative / base / optimistic; %s)",
		role, r.Conservative*percent, r.Base*percent, r.Optimistic*percent, in.policy.Calibration))
}

const (
	percent = 100

	priorSeasonEnded = "a season-long absence reported in the previous season ends with that season"
	priorSeasonStale = "an injury or absence of unknown length reported in the previous season, with no newer report, " +
		"counts in the conservative scenario only"
)

// roleCoverage is the share of the season a role claim is in force.
func (in effectInputs) roleCoverage(claim roleClaim, field roleField) float64 {
	from := in.season.position(claim.event.start())
	to := in.season.Games
	switch {
	case claim.closed.by != "":
		to = in.season.position(claim.closed.at)
	case !claim.event.EffectiveUntil.IsZero():
		to = in.season.position(claim.event.EffectiveUntil)
	}
	if next := claim.ends[field]; !next.IsZero() {
		to = math.Min(to, in.season.position(next))
	}
	return math.Max(0, to-from) / in.season.Games
}

func (r *effectResult) addAssumption(text string) {
	if !slices.Contains(r.assumptions, text) {
		r.assumptions = append(r.assumptions, text)
	}
}

func (r *effectResult) addAlert(text string) {
	if !slices.Contains(r.alerts, text) {
		r.alerts = append(r.alerts, text)
	}
}
