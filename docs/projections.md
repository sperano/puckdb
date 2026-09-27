# Baseline player projections

`internal/projection` produces versioned, league-neutral NHL projection
snapshots. League scoring is deliberately downstream: one projection snapshot
can be scored differently for each Yahoo league without changing its inputs.

## Model

The `nhl-baseline-v3` model uses completed NHL regular-season games from the
three seasons before the target season. Live, postponed, preseason and playoff
games, and games from the target season or later, are excluded from model inputs.
Each season is weighted by `season_decay ^ age`, where the immediately prior
season has age zero. Traded-player rows are summed before workload is
calculated, so two club rows in one season still count as one season.

Skater counting rates use weighted time on ice and regress toward a
forward/defense peer rate. Version 2 added faceoffs won and lost, which regress
toward a centre or non-centre peer rate instead, since faceoff usage is far more
position-specific than the other counting stats: lumping centres in with
wingers under the forward/defense split would badly misstate both groups' peer
rate. Expected games and time on ice per game come from weighted historical
workload. Goalie save and goals-against rates regress by shots faced; wins and
shutouts regress by starts. Goalie GAA and save
percentage are derived from projected goals against, saves, shots, and time on
ice rather than averaging historical ratios. The most recent historical team
and position are retained as context. TOI and power-play production represent
observed role; the baseline does not guess at unobserved offseason role or team
changes.

Version 3 neutralizes historical even-strength linemate context. Shift
boundaries form half-open on-ice segments. A segment counts only when both
teams have the same number of active skaters, from three through five; goalies,
power plays, penalty kills, empty-net advantages and line-change 6v6 artifacts
are excluded. Each teammate's even-strength points-per-60 rate is weighted by
shared seconds. Forward and defense contexts use separate exposure-weighted
averages. The model scales the non-power-play share of projected goals and
assists toward average context; points remain their sum, power-play production
is preserved, and no other statistic changes. The correction is
reliability-weighted against four times the skater TOI prior, so sparse shift
coverage stays close to neutral. This is strictly a correction for past
context and makes no assumption about the target season's lines.

The default parameters are:

| Parameter | Value |
| --- | ---: |
| Lookback | 3 seasons |
| Season decay | 0.40 |
| Skater regression prior | 9,000 seconds of TOI |
| Linemate regression strength | 0.25 |
| Goalie regression prior | 500 shots faced |
| Goalie shutout minimum TOI | 3,540 seconds |
| Maximum games | 82 |
| Interval z-score | 1.28 |
| Uncertainty bounds | 10%–100% |
| Insufficient-history threshold | 10 games |

The decay and skater prior were selected from a small grid of 15 combinations
(`decay` 0.40–1.00, prior 9,000–36,000 seconds). Across the nine skater
scoring statistics and three held-out seasons below, the selected combination
had a mean relative MAE of 0.935 against the previous-season baseline. The
grid and sample are small, so these defaults are versioned parameters rather
than claims of a final calibrated model.

## Historical evaluation

The evaluation used production game-level regular-season aggregates available
on 2026-09-20. For each target season, the model received only earlier seasons;
the target season supplied outcomes. Both models were scored on players with a
target-season row and a previous-season row. Each cell is `model MAE / previous
season MAE`. Stored evaluations include the configuration hash, source-data
hash, projection cutoff, and observation time.

This table records the version 1 external evaluation. Its production-derived
source rows are not committed, so the exact numbers cannot be reproduced from
this repository alone. Given equivalent season aggregates,
`projection.Evaluate` reproduces the calculation and rejects overrides so
external projections cannot leak into held-out results.

| Statistic | 2023-24 (n=782) | 2024-25 (n=778) | 2025-26 (n=791) |
| --- | ---: | ---: | ---: |
| Games played | 16.01 / 16.64 | 15.78 / 16.03 | 16.04 / 16.16 |
| TOI seconds | 16,863 / 17,745 | 16,497 / 17,004 | 16,675 / 16,689 |
| Goals | 4.43 / 5.02 | 4.21 / 4.51 | 4.23 / 4.41 |
| Assists | 6.43 / 7.21 | 6.51 / 6.83 | 6.41 / 6.66 |
| Points | 9.80 / 10.80 | 9.50 / 9.99 | 9.62 / 9.75 |
| Plus/minus | 9.39 / 11.15 | 9.68 / 11.14 | 9.19 / 10.42 |
| Penalty minutes | 12.67 / 13.76 | 11.75 / 13.41 | 12.02 / 12.95 |
| Power-play points | 3.08 / 3.38 | 3.08 / 3.25 | 2.78 / 2.89 |
| Shots on goal | 31.21 / 33.80 | 29.66 / 31.33 | 29.85 / 29.13 |
| Hits | 27.96 / 29.02 | 28.77 / 29.53 | 26.22 / 25.80 |
| Blocked shots | 18.70 / 19.44 | 17.39 / 18.72 | 15.79 / 16.88 |

The model improves most skater MAEs, but the 2025-26 previous-season baseline
is better for shots and hits. This is a baseline for later ranking and
sensitivity work, not evidence that every category has improved. Faceoffs won
and lost were added in `nhl-baseline-v2` after this evaluation was recorded and
are not yet reflected in the table above.

### Version 3 linemate holdout

`projection.EvaluateLinemateAdjustment` compares version 3 against the same
configuration with only `LinemateRegressionStrength` set to zero. It uses the
same player/stat sample on both sides and returns MAE and RMSE per skater stat.
For a 2025-26 holdout, `Repository.LoadEvaluationInput` reads 2022-23 through
2024-25 as model history and 2025-26 only as outcomes.

The numeric 2025-26 result is not recorded here. This repository contains no
production-derived shift or game aggregate export, and database-backed tests
skip when `PUCKDB_TEST_PG_URL` is unset. The holdout must therefore be run
against an approved 2022-26 database before the coefficient can be considered
empirically validated. No error change is inferred from synthetic fixtures.

Goalie component and ratio behavior is covered by deterministic tests. A live
goalie backtest was not recorded in this change because approval to export the
production-derived goalie aggregate was denied. It should be run before goalie
rankings are used for the draft helper.

## Snapshots and coverage

Migration `000004` stores immutable snapshot identity, a source-data hash, and
model parameters in typed columns. Player metadata is separate from normalized
stat rows, where each value stores its mean and interval. Exact rebuilds replace
their player rows atomically, which makes retrying a workflow safe; a historical
backfill produces a distinct source hash and snapshot.

Internal projections use `nhl:<player_id>` keys. Imported or manual overrides
can add rookies and unresolved players that have no NHL history. These rows
record their source and provider rather than silently fabricating history.
Internal rows with fewer than ten historical games remain present and carry an
`insufficient_history` flag. Callers pass the current draftable pool in
`Input.PlayerPool`; pool players without usable NHL history remain in the
snapshot with empty values and a complete `missing_stats` list. Historical
skaters whose game rows have no TOI are handled the same way instead of being
discarded.

`ValidateCoverage` maps enabled Yahoo stat IDs to the versioned projection
statistics and returns a deterministic `CoverageError` for every unsupported
category or missing player value. Display-only categories are ignored only
when the caller marks them as such. The currently supported Yahoo scoring IDs
are 1–5, 8, 14, 16, 17, 19, 22–27, 31 and 32; other IDs fail explicitly. Complete live
coverage requires both the verified league categories and the complete player
pool from the Yahoo import. `Repository.BuildSnapshot` accepts both in its
`BuildRequest` and refuses to persist a selected snapshot that fails coverage.

Yahoo approval is still required to validate that every player in the live
2026 draftable pool has either an internal projection or an explicit
imported/manual row. Missing Yahoo eligibility cannot be inferred from NHL
history.

When overrides are supplied, `BuildSnapshot` first persists the unchanged
internal baseline, then stores the override snapshot under its own source-data
hash. This keeps imported or later news-adjusted values separate from the
baseline they replace.
