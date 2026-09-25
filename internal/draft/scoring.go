package draft

import (
	"errors"
	"fmt"
	"strings"
)

// ScoringFormat is how a league turns stats into standings.
type ScoringFormat string

const (
	// FormatCategories ranks teams per category (roto or head-to-head).
	FormatCategories ScoringFormat = "categories"
	// FormatPoints sums weighted stats.
	FormatPoints ScoringFormat = "points"
)

// Objective is the horizon teams compete over.
type Objective string

const (
	ObjectiveSeasonLong  Objective = "season-long"
	ObjectiveHeadToHead  Objective = "head-to-head"
	scoringTypeRoto                = "roto"
	scoringTypeHead                = "head"
	scoringTypeHeadOne             = "headone"
	scoringTypePoint               = "point"
	scoringTypeHeadPoint           = "headpoint"
	problemListSeparator           = "; "
)

// scoringTypes maps Yahoo's scoring_type to the format and objective it uses.
var scoringTypes = map[string]struct {
	format    ScoringFormat
	objective Objective
}{
	scoringTypeRoto:      {FormatCategories, ObjectiveSeasonLong},
	scoringTypeHead:      {FormatCategories, ObjectiveHeadToHead},
	scoringTypeHeadOne:   {FormatCategories, ObjectiveHeadToHead},
	scoringTypePoint:     {FormatPoints, ObjectiveSeasonLong},
	scoringTypeHeadPoint: {FormatPoints, ObjectiveHeadToHead},
}

// ErrMissingScoringInputs means the league's rules do not say enough to
// score players. Rankings must stop on it rather than assume a default.
var ErrMissingScoringInputs = errors.New("league rules are missing scoring inputs")

// ScoringStat is one stat that counts toward the league's standings.
type ScoringStat struct {
	StatID        int
	Abbr          string
	Name          string
	PositionTypes []string
	Direction     Direction
	// Weight is set for points leagues only.
	Weight float64
}

// Scoring is the complete, validated scoring configuration of one league.
type Scoring struct {
	LeagueKey string
	Format    ScoringFormat
	Objective Objective
	Stats     []ScoringStat
	// Provisional is true when the rules are a temporary stand-in rather
	// than the league's own Yahoo settings.
	Provisional bool
}

// ScoringFor validates a snapshot's scoring inputs. It returns an error
// wrapping ErrMissingScoringInputs that lists every problem when the scoring
// type is unknown, no stat scores, a category has no direction, or a points
// stat has no weight or a bonus the model cannot apply.
func ScoringFor(snapshot Snapshot) (Scoring, error) {
	rules := snapshot.Rules
	kind, known := scoringTypes[rules.ScoringType]
	if !known {
		return Scoring{}, missingInputs(rules, []string{fmt.Sprintf("unsupported scoring type %q", rules.ScoringType)})
	}
	scoring := Scoring{
		LeagueKey: rules.LeagueKey, Format: kind.format, Objective: kind.objective,
		Provisional: snapshot.Source != SourceYahooAPI,
	}
	var problems []string
	for _, category := range rules.Categories {
		if !category.Scores() {
			continue
		}
		stat, statProblems := scoringStat(category, kind.format)
		problems = append(problems, statProblems...)
		scoring.Stats = append(scoring.Stats, stat)
	}
	if len(scoring.Stats) == 0 {
		problems = append(problems, "no enabled, non-display-only stat categories")
	}
	if len(problems) > 0 {
		return Scoring{}, missingInputs(rules, problems)
	}
	return scoring, nil
}

func scoringStat(category StatCategory, format ScoringFormat) (ScoringStat, []string) {
	stat := ScoringStat{
		StatID: category.StatID, Abbr: category.Label(), Name: category.Name,
		PositionTypes: category.PositionTypes, Direction: category.Direction,
	}
	label := fmt.Sprintf("stat %d (%s)", category.StatID, category.Label())
	var problems []string
	switch format {
	case FormatCategories:
		if category.Direction == DirectionUnknown {
			problems = append(problems, label+" has no sort direction")
		}
	case FormatPoints:
		if category.Weight == nil {
			problems = append(problems, label+" has no points weight")
		} else {
			stat.Weight = *category.Weight
		}
		if len(category.Bonuses) > 0 {
			problems = append(problems, label+" has points bonuses, which are not supported")
		}
	}
	return stat, problems
}

func missingInputs(rules Rules, problems []string) error {
	return fmt.Errorf("season %d league %s: %w: %s",
		rules.Season, rules.LeagueKey, ErrMissingScoringInputs, strings.Join(problems, problemListSeparator))
}
