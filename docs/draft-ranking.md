# Draft helper ranking model

`draft.BuildRanking` scores one complete Yahoo player pool using one immutable
league rules snapshot and one projection snapshot. Its result stores a method
version, canonical rule hash, projection source hash and model version, scoring
format and objective, strategy options, and an order-independent hash of Yahoo
base-position eligibility. Rebuilding with the same inputs gives the same
scores and key-based tie order. A changed category preference, rule, projection,
or eligibility makes a distinct ranking identity.

## League scoring and replacement

Points leagues apply the imported Yahoo weight to each projected stat. For
category leagues, each enabled stat contributes a standardized deviation from
the full draftable pool of its own player type. A lower-is-better category
reverses that deviation. The contribution is divided by the square root of
the number of categories for that player type, putting skater and goalie
category totals on a similar scale before replacement. A category's center
and spread are computed once on the complete pool; position filters never
recompute them.

Goalie save percentage uses shots against as its opportunity denominator;
goals-against average uses time on ice. The ratio center and spread weight
each player's ratio by that denominator. An individual's influence is further
multiplied by `min(1, sqrt(opportunities / mean opportunities))`. A zero or
missing denominator stops the ranking. This is a deterministic approximation
of how much a goalie can move a team's season ratio, not a forecast of the
final roster's exact ratio. The ratio and count contribution formulas are
part of `league-replacement-v1`.

The season-long category objective uses the full-season standardized values.
For head-to-head categories, the model subtracts
`uncertainty / (1 + uncertainty)` times the contribution's absolute value as
an explicit weekly downside-risk assumption. This lowers uncertain positive
and negative projections instead of rewarding an uncertain below-average
estimate. The same Yahoo categories and directions apply in either format.
Points leagues keep their exact imported point weights in both objectives.

Roster demand expands each active position and flex slot by the league team
count. Each candidate can fill one unit, with augmenting paths moving a
multi-position player when that permits a better-scoring set of players to
fit. Ordinary bench slots are included by default and can be excluded with
the explicit `BenchExcluded` policy. IR, IR+ and NA do not create ordinary
draft demand. An unsupported roster slot stops the ranking. A position's
undrafted frontier is its best undrafted eligible player. For a drafted player,
the baseline is the best undrafted candidate that can legally enter after the
player is removed and all remaining drafted players are reassigned. Thus
removing a C/LW can move the existing LW into that slot and admit a center;
the model cannot invent an empty LW replacement. An undrafted player's baseline
is the lowest frontier among their eligible base positions. A player already on
that frontier contributes to it and has zero or negative value above
replacement.

The only automatically interpreted workload settings in this version are
`max_games_played` and `max_goalie_starts`. Because the live 2026 Yahoo rule
text has not been verified, either setting fails closed unless the caller
explicitly selects `WorkloadCapsPerPlayer` after confirming that interpretation.
That policy scales projected count stats and goalie ratio exposure using,
respectively, a per-player games cap and a per-goalie starts cap, and is
surfaced in the ranking's `Options` and `Assumptions`. Another setting whose
name indicates a game, start or appearance limit—including a nested unmodeled
setting such as a goalie minimum—fails until an adapter defines its semantics.

Each row reports its official score, replacement baseline, official value
above replacement, optional strategy-adjusted value, projection uncertainty,
category contributions, explanatory text, overall rank, eligible-position
ranks and a value-gap tier. The strategy can set a category multiplier from
zero (an explicit punt) to ten and a nonnegative uncertainty penalty. These
change adjusted value and recommendation order; official Yahoo weights,
directions, score and baseline remain visible. Ties resolve by adjusted value,
then official value, then stable player key. Filtering is a view over the fixed
result, so it does not renumber overall or position ranks and emits a
multi-position player once even when several requested positions match.

## Evaluation and sensitivity

`draft.EvaluateOrdering` accepts later realized values and basic point totals
for the same players and compares pairwise ordering accuracy, with half credit
for predicted ties. It requires every outcome and baseline, and it cannot
train or rescore the frozen ranking. A synthetic scarcity fixture in
`ranking_evaluation_test.go` scores 3/3 ordered pairs for the ranking and 2/3
for basic points. This only validates the evaluation and scarcity effect;
it is **not** evidence of improvement in historical NHL drafts. The repository
does not contain a held-out fantasy ranking outcome set or verified rules for
the target 2026 leagues. The projection MAE comparison in `projections.md`
measures stat forecasts, not draft rank quality. A historical fantasy backtest
remains required before claiming improvement over point totals.

The deterministic tests also vary goalie workload (a 40-start projection
under a 10-start cap contributes one quarter of its projected wins) and
uncertainty. With equal raw scores, a high-uncertainty goalie or rookie has
the same official value as a stable peer. An explicit uncertainty penalty
reduces their adjusted value and recommendation rank. For category leagues,
the head-to-head reliability assumption additionally shrinks uncertain
contributions. These checks show direction and sensitivity, not calibrated
outcome probabilities.

A complete ranking also requires an estimate for every scoring stat of every
draftable player. A rookie or unmatched player with missing values stops the
build with an explicit error; callers must add a versioned imported or manual
projection override rather than letting the ranking invent a zero estimate or
drop the player.

News adjustments do not change this model. `internal/newsadjust` produces an
adjusted projection snapshot per scenario, and each is ranked with
`BuildRanking` like any other snapshot (see `draft-news-adjustments.md`).
`internal/draftrank` stores those rankings as league snapshots and serves them
to GraphQL, the CLI and exports (see `draft-rankings-api.md`).
